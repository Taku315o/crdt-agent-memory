# internal/storage パッケージ解説

Status: Draft v0.1  
Date: 2026-03-13

---

## 1. 概要

`internal/storage` は、SQLite データベースの**接続管理**と**マイグレーション実行**を担うパッケージです。  
他のすべての internal パッケージがこのパッケージを通じて DB を取得し、起動時の初期化を行います。

---

## 2. ファイル構成

| ファイル | 役割 |
|---|---|
| `sqlite.go` | SQLite 接続のオープン（`OpenSQLite`） |
| `migrator.go` | マイグレーション実行（`RunMigrations`）、メタデータ読み込み（`LoadMetadata`）、`Metadata` 型 |

---

## 3. 関数・型

### 3-1. OpenSQLite — SQLite 接続を開く

```go
func OpenSQLite(ctx context.Context, path string) (*sql.DB, error)
```

ファイルパスを受け取り、以下の PRAGMA を設定した DSN で SQLite を開きます。

| PRAGMA | 値 | 意図 |
|---|---|---|
| `busy_timeout` | `5000`（ms） | ロック競合時の待機時間。書き込み競合を吸収する。 |
| `foreign_keys` | `OFF` | CRDT 適用時に参照整合性エラーが発生しないよう無効化。 |
| `journal_mode` | `WAL` | 読み書き並列実行のために WAL モードを使用。 |
| `trusted_schema` | `ON` | スキーマのトリガ・ビューを信頼済みとして扱う。 |

`sql.Open` 後に `PingContext` を呼び出して接続を確認します。失敗した場合は `db.Close()` を呼んで接続を閉じ、エラーを返します。

使用ドライバは [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite)（Pure-Go ドライバ、CGO 不要）です。

---

### 3-2. Metadata 型

```go
type Metadata struct {
    SchemaHash                   string // 全マイグレーション SQL の SHA-256
    CRRManifestHash              string // 同期対象テーブル名リストの SHA-256
    ProtocolVersion              string // 現在のプロトコルバージョン（"1"）
    MinCompatibleProtocolVersion string // 互換最低バージョン（"1"）
}
```

`Metadata` は `memsync.Service` が Handshake / Batch 検証に使用します。

---

### 3-3. RunMigrations — マイグレーションを実行してメタデータを返す

```go
func RunMigrations(ctx context.Context, db *sql.DB) (Metadata, error)
```

#### 動作フロー

```
migrations/ ディレクトリのファイルをアルファベット順に取得
  ↓
BEGIN TRANSACTION
  ↓
schema_migrations テーブルと app_metadata テーブルを作成（IF NOT EXISTS）
  ↓
各 .sql ファイルに対して:
  schema_migrations に適用済み → SQL を combined に蓄積してスキップ
  未適用               → ExecContext で SQL を実行
                         schema_migrations に INSERT して適用済みとしてマーク
                         SQL を combined に蓄積
  ↓
Metadata を計算:
  SchemaHash      = SHA-256( 全マイグレーション SQL を結合した文字列 )
  CRRManifestHash = SHA-256( "artifact_refs,memory_edges,memory_nodes,memory_signals" )
  ProtocolVersion = "1"
  MinCompatibleProtocolVersion = "1"
  ↓
app_metadata テーブルに UPSERT
  ↓
COMMIT
  ↓
return Metadata
```

#### ハッシュの用途

| ハッシュ | 計算対象 | 使われる場所 |
|---|---|---|
| `SchemaHash` | 全 `.sql` ファイルの内容を結合した文字列 | Handshake でスキーマ互換性を検証 |
| `CRRManifestHash` | 同期対象テーブル名のカンマ区切りリスト | Handshake で同期テーブルセットの互換性を検証 |

**同期対象テーブル（CRR テーブル）**:

```go
var sharedCRRTables = []string{
    "artifact_refs",
    "memory_edges",
    "memory_nodes",
    "memory_signals",
}
```

- private テーブル（`private_memory_nodes` など）はこのリストに含まれないため、CRDT 同期の対象外。

#### マイグレーションディレクトリの解決

```go
func migrationDir() string {
    _, filename, _, _ := runtime.Caller(0)
    return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}
```

`migrator.go` のソースファイル位置から相対パスで `migrations/` を解決します。  
テスト実行時もソースツリーを参照するため、バイナリ配布時は埋め込み（`embed.FS`）への変更を検討する必要があります。

---

### 3-4. LoadMetadata — 保存済みメタデータを読み込む

```go
func LoadMetadata(ctx context.Context, db *sql.DB) (Metadata, error)
```

`app_metadata` テーブルから以下のキーを読み込み、`Metadata` を組み立てて返します。

| キー | 対応フィールド |
|---|---|
| `"schema_hash"` | `SchemaHash` |
| `"crr_manifest_hash"` | `CRRManifestHash` |
| `"protocol_version"` | `ProtocolVersion` |
| `"min_compatible_protocol_version"` | `MinCompatibleProtocolVersion` |

`RunMigrations` を呼ばずに起動済み DB からメタデータだけ取得したい場合に使用します（デーモン再起動時など）。

---

## 4. 対応する DB テーブル

```sql
-- マイグレーション適用履歴
CREATE TABLE IF NOT EXISTS schema_migrations (
    version      TEXT PRIMARY KEY,
    applied_at_ms INTEGER NOT NULL
);

-- アプリケーションメタデータ（ハッシュ・バージョン）
CREATE TABLE IF NOT EXISTS app_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

---

## 5. エラーハンドリング

| ケース | 動作 |
|---|---|
| `migrations/` ディレクトリが存在しない | `os.ReadDir` のエラーとして返る |
| .sql ファイルの実行エラー | `fmt.Errorf("apply migration %s: %w", version, err)` でラップして返る |
| 途中でエラー → `defer tx.Rollback()` | それまでの変更はすべてロールバックされる |

---

## 6. 依存関係

```
internal/storage
  ├── modernc.org/sqlite（Pure-Go SQLite ドライバ）
  ├── crypto/sha256（SchemaHash / CRRManifestHash 計算）
  ├── database/sql（標準ライブラリ）
  └── runtime（ソースファイル位置からマイグレーションパスを解決）
```

他パッケージからの利用：

```
internal/memory   → OpenSQLite, RunMigrations
internal/memsync  → Metadata, RunMigrations
internal/policy   → （直接インポートなし、db を受け取るのみ）
cmd/memoryd       → OpenSQLite, RunMigrations / LoadMetadata
cmd/syncd         → OpenSQLite, RunMigrations / LoadMetadata
cmd/indexd        → OpenSQLite, RunMigrations / LoadMetadata
```
