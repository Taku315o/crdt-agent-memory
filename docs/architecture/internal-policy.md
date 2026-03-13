# internal/policy パッケージ解説

Status: Draft v0.1  
Date: 2026-03-13

---

## 1. 概要

`internal/policy` は、**同期を許可するピアのアクセス制御**（アローリスト）を管理するパッケージです。  
`memsync.Service` がハンドシェイク時に呼び出し、未登録ピアからの同期要求を拒否します。

---

## 2. ファイル構成

| ファイル | 役割 |
|---|---|
| `repository.go` | `Repository` の実装（AllowPeer / IsAllowed） |

---

## 3. 型

### Repository

```go
type Repository struct {
    db *sql.DB
}

func NewRepository(db *sql.DB) *Repository
```

---

## 4. メソッド

### 4-1. AllowPeer — ピアをアローリストに登録する

```go
func (r *Repository) AllowPeer(ctx context.Context, peerID, displayName string) error
```

`peer_policies` テーブルに以下の値で UPSERT します。

| カラム | セットされる値 |
|---|---|
| `peer_id` | 引数 `peerID` |
| `display_name` | 引数 `displayName` |
| `trust_state` | `"allow"` |
| `trust_weight` | `1.0` |
| `updated_at_ms` | 現在時刻（UnixMilli） |

既存行がある場合は `display_name`、`trust_state`、`updated_at_ms` のみ更新されます。

---

### 4-2. IsAllowed — ピアが許可されているか確認する

```go
func (r *Repository) IsAllowed(ctx context.Context, peerID string) (bool, error)
```

`peer_policies` を検索し、該当行の `trust_state = 'allow'` であれば `true` を返します。

| 条件 | 返り値 |
|---|---|
| 行が存在しない（`sql.ErrNoRows`） | `false, nil` |
| `trust_state = 'allow'` | `true, nil` |
| `trust_state` がそれ以外の値 | `false, nil` |
| DB エラー | `false, error` |

---

## 5. 対応する DB テーブル

```sql
CREATE TABLE IF NOT EXISTS peer_policies (
    peer_id          TEXT PRIMARY KEY,
    display_name     TEXT NOT NULL DEFAULT '',
    trust_state      TEXT NOT NULL DEFAULT 'allow',
    trust_weight     REAL NOT NULL DEFAULT 1.0,
    discovery_profile TEXT NOT NULL DEFAULT '',
    relay_profile    TEXT NOT NULL DEFAULT '',
    notes            TEXT NOT NULL DEFAULT '',
    updated_at_ms    INTEGER NOT NULL
);
```

現在実装されている `trust_state` の値：

| 値 | 意味 |
|---|---|
| `"allow"` | 同期を許可 |
| （その他） | 同期を拒否（例: `"deny"` などを将来追加可能） |

---

## 6. 拡張ポイント

現時点では `AllowPeer` と `IsAllowed` のみが実装されています。  
以下の機能が将来の拡張として想定されます。

- `DenyPeer(ctx, peerID) error` — 特定ピアの同期を明示的に拒否
- `SetTrustWeight(ctx, peerID string, weight float64) error` — Recall ランキング用のトラストスコア変更
- `ListPeers(ctx) ([]PeerPolicy, error)` — ポリシー一覧の取得
- `discovery_profile` / `relay_profile` — ピア発見・中継プロファイルの設定

---

## 7. 依存関係

```
internal/policy
  └── database/sql（標準ライブラリ）
```

他パッケージからの利用：

```
internal/memsync
  └── internal/policy（Handshake 時のピア検証）
```
