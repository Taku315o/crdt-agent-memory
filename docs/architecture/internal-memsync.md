# internal/memsync パッケージ解説

Status: Draft v0.1  
Date: 2026-03-13

---

## 1. 概要

`internal/memsync` は、ピア間（peer-to-peer）の **CRDT ベース増分同期**を実装するサービスです。  
同期は「接続確立 → ハンドシェイク → バッチ抽出 → バッチ適用」の順で行われ、双方向の増分レプリケーションを実現します。

---

## 2. ファイル構成

| ファイル | 役割 |
|---|---|
| `types.go` | 入出力型の定義 |
| `service.go` | `Service` の実装（Handshake / ExtractBatch / ApplyBatch / SyncPair / Diagnostics） |
| `service_test.go` | インテグレーションテスト |

---

## 3. 型定義（types.go）

### HandshakeRequest / HandshakeResponse

```go
type HandshakeRequest struct {
    ProtocolVersion              string   // 送信側のプロトコルバージョン
    MinCompatibleProtocolVersion string   // 送信側が受け入れる最低バージョン
    PeerID                       string   // 送信側のピア ID
    SchemaHash                   string   // 全マイグレーション SQL を結合した SHA-256 ハッシュ
    CRRManifestHash              string   // 同期対象テーブル名のリストの SHA-256 ハッシュ
    Namespaces                   []string // 同期するネームスペース（最低 1 つ必須）
    InviteTicket                 string   // 招待チケット（省略可）
}

type HandshakeResponse struct {
    PeerID             string
    SchemaHash         string
    CRRManifestHash    string
    Namespaces         []string
    NegotiatedProtocol string   // 合意したプロトコルバージョン
}
```

---

### Change / Batch

```go
// Change は crsql_changes の 1 行に対応する
type Change struct {
    DBVersion   int64
    TableName   string  // "memory_nodes" | "memory_edges" | "memory_signals" | "artifact_refs"
    PK          string  // 主キー値
    Op          string  // "upsert"
    RowJSON     string  // 行データ全体の JSON
    MemoryID    string  // インデックスキュー enqueue 用
    Namespace   string
    ChangedAtMS int64
}

type Batch struct {
    BatchID         string   // UUID
    FromPeerID      string
    Namespace       string
    SchemaHash      string   // 送信元の SchemaHash（受信側が検証する）
    CRRManifestHash string   // 送信元の CRRManifestHash（受信側が検証する）
    MaxVersion      int64    // バッチ内で最大の db_version（カーソル更新に使う）
    Changes         []Change
}
```

---

### Diagnostics / TrackedPeer

```go
type Diagnostics struct {
    SchemaHash      string
    CRRManifestHash string
    TrackedPeers    []TrackedPeer
    QuarantineCount int
}

type TrackedPeer struct {
    PeerID      string
    Namespace   string
    Version     int64    // 最後にこのピアから適用した db_version
    UpdatedAtMS int64
}
```

---

## 4. Service の初期化

```go
func NewService(db *sql.DB, meta storage.Metadata, policies *policy.Repository, selfPeer string) *Service
```

| 引数 | 説明 |
|---|---|
| `db` | SQLite 接続 |
| `meta` | RunMigrations が返す SchemaHash / CRRManifestHash / プロトコルバージョン |
| `policies` | `internal/policy.Repository`（ピアのアクセス制御） |
| `selfPeer` | 自ノードのピア ID（ExtractBatch の `FromPeerID` に使われる） |

---

## 5. Service メソッド

### 5-1. Handshake — 同期可否を検証する

```
Handshake(ctx, HandshakeRequest) (HandshakeResponse, error)
```

#### 検証ステップ（順番に実施）

| ステップ | 失敗時の動作 |
|---|---|
| `peer_policies` にアローリスト登録があるか | `"peer is not allowlisted"` を返す |
| `SchemaHash` が自ノードと一致するか | `peer_sync_state` にエラーを記録して `"schema hash mismatch"` を返す |
| `CRRManifestHash` が一致するか | `peer_sync_state` にエラーを記録して `"crr manifest hash mismatch"` を返す |
| プロトコルバージョンが互いに互換か | `peer_sync_state` にエラーを記録して `"protocol mismatch"` を返す |
| `Namespaces` が 1 つ以上あるか | `"at least one namespace is required"` を返す |

すべてのチェックを通過すると `HandshakeResponse` を返します。

---

### 5-2. ExtractBatch — 増分変更バッチを抽出する

```
ExtractBatch(ctx, peerID, namespace string, limit int) (Batch, error)
```

#### 動作フロー

```
crsql_tracked_peers から <peerID, namespace> のカーソル（db_version）を取得
  ↓
SELECT FROM crsql_changes
  WHERE namespace = ? AND db_version > <cursor>
  ORDER BY db_version, table_name, pk
  LIMIT <limit>
  ↓
Changes をソートして Batch を構築
Batch.MaxVersion = 取得した中で最大の db_version
```

- `limit` のデフォルトは `1000`。
- `crsql_tracked_peers` に行がない場合はカーソル = 0（全件対象）。
- `crsql_changes` には shared テーブル（`memory_nodes` / `memory_edges` / `memory_signals` / `artifact_refs`）の変更のみが記録される。private テーブルは記録されないため、自動的に抽出対象外となる。

---

### 5-3. ApplyBatch — バッチを適用する

```
ApplyBatch(ctx, fromPeerID string, batch Batch) error
```

#### 動作フロー

```
SchemaHash / CRRManifestHash を検証
  → 不一致 → sync_quarantine に記録して error を返す（以降の処理中断）
  ↓
BEGIN TRANSACTION
  ↓
capture_control.suppress = 1（変更ログの再キャプチャを抑制）
  ↓
各 Change に対して:
  change.Namespace ≠ batch.Namespace → 混在バッチとして quarantine
  テーブルごとに UPSERT:
    memory_nodes    → ON CONFLICT(memory_id) DO UPDATE SET ...
    memory_edges    → ON CONFLICT(edge_id) DO UPDATE SET ...
    memory_signals  → ON CONFLICT(signal_id) DO UPDATE SET ...
    artifact_refs   → ON CONFLICT(artifact_id) DO UPDATE SET ...
    未知テーブル    → quarantine
  change.MemoryID != "" → index_queue に enqueue
  ↓
capture_control.suppress = 0
  ↓
crsql_tracked_peers のカーソルを batch.MaxVersion に更新
peer_sync_state の last_success_at_ms を更新
  ↓
COMMIT
```

**重要**: `suppress = 1` にすることで、他ピアからの変更を取り込む際に `crsql_changes` への二重書き込みが発生しない（AFTER INSERT/UPDATE トリガが `capture_control` を参照して動作をスキップする）。

---

### 5-4. SyncPair — 双方向同期のヘルパー関数

```go
func SyncPair(ctx context.Context, left, right *Service, namespace string, limit int) error
```

テストや単純なデプロイ構成で 2 ノードを同期するためのユーティリティ関数。

#### 動作順序

```
right.Handshake(leftReq)   ← left の互換性を right が確認
left.Handshake(rightReq)   ← right の互換性を left が確認
  ↓
left.ExtractBatch(right のカーソル) → right.ApplyBatch
  ↓
right.ExtractBatch(left のカーソル) → left.ApplyBatch
```

---

### 5-5. Diagnostics — 同期状態を診断する

```
Diagnostics(ctx) (Diagnostics, error)
```

| フィールド | 内容 |
|---|---|
| `SchemaHash` | 自ノードの SchemaHash |
| `CRRManifestHash` | 自ノードの CRRManifestHash |
| `TrackedPeers` | `crsql_tracked_peers` の全行（ピアごとのカーソル一覧） |
| `QuarantineCount` | `sync_quarantine` に隔離されたバッチ数 |

---

## 6. クォランティン（隔離）機構

以下の条件でバッチを適用せずに `sync_quarantine` に保存します。

| 条件 | `reason` の値 |
|---|---|
| SchemaHash または CRRManifestHash の不一致 | `"incompatible batch metadata"` |
| `change.Namespace ≠ batch.Namespace` | `"mixed namespace batch"` |
| JSON のパースエラー | テーブル名に応じたメッセージ（例: `"invalid memory_nodes row"`） |
| 未知のテーブル名 | `"unsupported table <name>"` |

クォランティンされたバッチは `sync_quarantine` テーブルに JSON 全体が保存され、あとから手動または自動で検査・再試行できます。

---

## 7. カーソル管理（crsql_tracked_peers）

| カラム | 説明 |
|---|---|
| `peer_id` | 相手ピアの ID |
| `namespace` | 対象ネームスペース |
| `version` | 最後にこのピアから適用した `db_version` |
| `updated_at_ms` | カーソルを更新した時刻 |

`ExtractBatch` はこのテーブルを参照して「どこまで送ったか」を判断します。`ApplyBatch` はバッチ適用後にここを更新します。

---

## 8. テスト

| テスト名 | 検証内容 |
|---|---|
| `TestHandshakeRejectsSchemaMismatch` | SchemaHash が不一致の場合にハンドシェイクがエラーを返すこと |
| `TestExtractBatchExcludesPrivateTables` | private メモリが ExtractBatch に含まれないこと、namespace フィルタが正しく機能すること |
| `TestReplayApplyIsSafeAndTracksCursor` | 同じバッチを 2 回 ApplyBatch しても冪等であること、カーソルが更新されること |

---

## 9. 依存関係

```
internal/memsync
  ├── internal/policy（ピアのアクセス制御）
  ├── internal/storage（Metadata）
  ├── internal/memory（テストのみ）
  ├── github.com/google/uuid
  └── encoding/json
```
