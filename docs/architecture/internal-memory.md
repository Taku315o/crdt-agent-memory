# internal/memory パッケージ解説

Status: Draft v0.1  
Date: 2026-03-13

---

## 1. 概要

`internal/memory` は、エージェントがメモリ（知識・事実・観察など）を**保存・検索・上書き**するためのコアサービスです。  
shared（他 peer と同期される）と private（ローカル専用）の 2 つの可視性を持ち、どちらに書くかを呼び出し側が指定します。

---

## 2. ファイル構成

| ファイル | 役割 |
|---|---|
| `types.go` | 入出力型の定義 |
| `service.go` | `Service` の実装（Store / Recall / Supersede） |
| `service_test.go` | インテグレーションテスト |

---

## 3. 型定義（types.go）

### Visibility

```go
type Visibility string

const (
    VisibilityShared  Visibility = "shared"
    VisibilityPrivate Visibility = "private"
)
```

- `shared` → `memory_nodes` テーブルへ INSERT。CRDT 変更ログ（`crsql_changes`）に記録され、他 peer との同期対象になる。
- `private` → `private_memory_nodes` テーブルへ INSERT。ローカル専用で同期対象外。

---

### StoreRequest

```go
type StoreRequest struct {
    MemoryID      string     // 省略時は UUID 自動生成
    Visibility    Visibility // "shared" or "private"（必須）
    Namespace     string     // 対象ネームスペース（必須）
    MemoryType    string     // デフォルト: "fact"
    Scope         string     // デフォルト: "team"
    Subject       string     // メモリの件名
    Body          string     // メモリ本文（必須・空白不可）
    SourceURI     string     // 由来 URI（任意）
    SourceHash    string     // 由来コンテンツのハッシュ（任意）
    AuthorAgentID string     // デフォルト: "agent/default"
    OriginPeerID  string     // デフォルト: "peer/local"
    AuthoredAtMS  int64      // 省略時は現在時刻（UnixMilli）
}
```

---

### RecallRequest

```go
type RecallRequest struct {
    Query          string   // FTS 検索クエリ（必須）
    Namespaces     []string // 絞り込むネームスペース（空なら全件対象）
    IncludePrivate bool     // true にすると private も検索対象
    Limit          int      // デフォルト: 10
}
```

---

### RecallResult

```go
type RecallResult struct {
    MemorySpace    string // "shared" or "private"
    MemoryID       string
    Namespace      string
    MemoryType     string
    Subject        string
    Body           string
    LifecycleState string // "active" or "superseded"
    AuthoredAtMS   int64
    SourceURI      string
    SourceHash     string
    OriginPeerID   string
}
```

---

## 4. Service メソッド

### 4-1. Store — メモリを保存する

```
Store(ctx, StoreRequest) (memoryID string, error)
```

#### バリデーション

| 条件 | エラー |
|---|---|
| `Body` が空 | `"body is required"` |
| `Visibility` が不正値 | `"visibility must be shared or private"` |
| `Namespace` が空 | `"namespace is required"` |

#### 動作フロー

```
バリデーション
  ↓
デフォルト値補完（MemoryID / AuthoredAtMS / Scope / MemoryType / AuthorAgentID / OriginPeerID）
  ↓
BEGIN TRANSACTION
  ↓
  [shared]  → INSERT INTO memory_nodes          …lifecycle_state='active', schema_version=1
  [private] → INSERT INTO private_memory_nodes  …lifecycle_state='active', schema_version=1
  ↓
  [shared]  → INSERT INTO memory_signals (signal_type='store', value=1.0)
  [private] → INSERT INTO private_memory_signals (signal_type='store', value=1.0)
  ↓
COMMIT
  ↓
return memoryID
```

- shared メモリの INSERT に対しては `trg_memory_nodes_capture_insert` トリガが発火し、`crsql_changes` に変更が記録されます（→ 同期対象）。
- private メモリにはトリガがないため `crsql_changes` に記録されず、同期対象外です。

---

### 4-2. Recall — メモリを検索する

```
Recall(ctx, RecallRequest) ([]RecallResult, error)
```

#### 動作フロー

```
sqlite-vec が使えるとき
  memory_embedding_vectors（vec0）
  WHERE embedding MATCH vec_f32(<query embedding>)
  [AND memory_space = 'shared']     ← IncludePrivate=false のとき
  JOIN recall_memory_view
  [AND namespace IN (...)]          ← Namespaces 指定のとき
  ORDER BY ranking_bucket, trust_weight DESC, distance, authored_at_ms DESC
  LIMIT <limit>

sqlite-vec が使えないとき
  recall_memory_view（shared + private の UNION ALL ビュー）
    JOIN memory_fts（FTS5 仮想テーブル）
    WHERE memory_fts MATCH <query>
    [AND memory_space = 'shared']   ← IncludePrivate=false のとき
    [AND namespace IN (...)]        ← Namespaces 指定のとき
    ORDER BY bm25(memory_fts), authored_at_ms DESC
    LIMIT <limit>
```

- sqlite-vec が使える場合は semantic retrieval を優先し、結果が空なら FTS5 にフォールバックする。
- `recall_memory_view` は `memory_nodes` と `private_memory_nodes` を `UNION ALL` したビュー。
- `IncludePrivate=false`（デフォルト）の場合、shared のみが返ります。

---

### 4-3. Supersede — メモリを上書き更新する

```
Supersede(ctx, oldMemoryID string, StoreRequest) (newMemoryID string, error)
```

#### 動作フロー

```
Store(req)  → 新メモリを挿入（always shared）
  ↓
BEGIN TRANSACTION
  ↓
UPDATE memory_nodes SET lifecycle_state = 'superseded' WHERE memory_id = <old>
  ↓
INSERT INTO memory_edges (relation_type='supersedes', from=new, to=old)
  ↓
COMMIT
  ↓
return newMemoryID
```

- 旧メモリは **削除されない**。`lifecycle_state` が `superseded` に変更される。
- `memory_edges` に `supersedes` エッジが張られ、グラフから更新履歴を追跡できる。
- `Supersede` は常に `VisibilityShared` を強制する（private の上書きには非対応）。

---

## 5. lifecycle_state の値

| 値 | 意味 | セットされるタイミング |
|---|---|---|
| `active` | 現役のメモリ | `Store` による INSERT 時 |
| `superseded` | 古くなったメモリ | `Supersede` による UPDATE 時 |

- `lifecycle_state` は mutable であり、作者署名（`author_signature`）の対象外。
- Recall の結果に `lifecycle_state` が含まれるため、呼び出し側が古いメモリを判別できる。

---

## 6. テスト

| テスト名 | 検証内容 |
|---|---|
| `TestStoreRoutesSharedAndPrivateSeparately` | shared → `memory_nodes`、private → `private_memory_nodes` に正しくルーティングされること。shared には `crsql_changes` が記録されること。 |
| `TestRecallUnionView` | shared と private 双方が `recall_memory_view` 経由で FTS 検索されること。 |

---

## 7. 依存関係

```
internal/memory
  └── internal/storage（OpenSQLite / RunMigrations）
  └── github.com/google/uuid
```
