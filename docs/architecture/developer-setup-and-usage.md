# Developer Setup And Usage

Status: Current implementation guide
Date: 2026-03-17

## 1. What Actually Runs

このリポジトリで今ローカル起動するプロセスは次の 4 つ。

- `memoryd`
  - migrate / diag / local HTTP control API
- `syncd`
  - peer registry ベースの whole sync daemon
- `indexd`
  - `index_queue` を処理して embedding state を更新する worker
- SQLite
  - `cr-sqlite` を shared family に load した canonical store

現在の dev transport は `http-dev` であり、Iroh はまだ入っていない。

## 2. Prerequisites

- Go toolchain
  - macOS の現行導線は `/opt/homebrew/bin/go`
- `sqlite3`
- `cr-sqlite` loadable extension
- `sqlite-vec` loadable extension

この repo では次で bootstrap できる。

```bash
make bootstrap-dev
```

これにより次へ展開される。

- `.tools/crsqlite/crsqlite.dylib`
- `.tools/sqlite-vec/vec0.dylib`

## 3. Sample Configs

tracked sample:

- `configs/peer-a.yaml.example`
- `configs/peer-b.yaml.example`

ローカル作業用コピー:

```bash
make clean-dev
make setup-dev-configs
```

コピー先:

```text
/tmp/crdt-agent-memory-dev/
  peer-a/config.yaml
  peer-b/config.yaml
```

注意:

- 既存の fake-CRR DB を使っていた場合、再利用は不可
- `legacy fake-crr database detected` が出たら DB を作り直す

## 4. Commands

### Bootstrap and tests

```bash
make bootstrap-dev
make test
```

### Migrate and inspect

```bash
make clean-dev
make setup-dev-configs
make migrate-peer-a
make migrate-peer-b
make diag-peer-a
make diag-peer-b
```

### Start services

`serve-*` / `index-*` / `sync-*` は cleanup ラッパー経由で起動する。
Ctrl-C や終了シグナルで止めても、子プロセスをまとめて後始末する。

peer A:

```bash
make serve-peer-a
make index-peer-a
make sync-peer-a
```

peer B:

```bash
make serve-peer-b
make index-peer-b
make sync-peer-b
```

### Smoke Checks

```bash
make smoke-sync-confirm
make smoke-recall-confirm
make smoke-e2e-manual
make clean-dev
make smoke-sync
```

## 5. Manual Smoke Flow

最小確認フローは次。

1. `make clean-dev setup-dev-configs migrate-peer-a migrate-peer-b`
2. peer A/B の `memoryd` を起動
3. peer B の `syncd` を起動
4. peer A に shared memory を保存
5. peer A に private memory を保存
6. peer A の `syncd --once` を実行
7. peer B の DB で shared row と private 非存在を確認する
8. peer A の API recall で検索結果を確認する
9. peer B で sync status を確認する

個別実行したい場合は以下を使う。

1. `make smoke-sync-confirm`
1. `make smoke-recall-confirm`
1. `make smoke-e2e-manual`

shared write の例:

```bash
curl -X POST http://127.0.0.1:3101/v1/memory/store \
  -H 'Content-Type: application/json' \
  -d '{"visibility":"shared","namespace":"team/dev","subject":"shared","body":"shared fact from peer a","origin_peer_id":"peer-a","author_agent_id":"agent-a"}'
```

private write の例:

```bash
curl -X POST http://127.0.0.1:3101/v1/memory/store \
  -H 'Content-Type: application/json' \
  -d '{"visibility":"private","namespace":"local/dev","subject":"private","body":"private fact from peer a","origin_peer_id":"peer-a","author_agent_id":"agent-a"}'
```

peer A one-shot sync:

```bash
PATH=/opt/homebrew/bin:$PATH /opt/homebrew/bin/go run -tags sqlite_fts5 ./cmd/syncd \
  --config /tmp/crdt-agent-memory-dev/peer-a/config.yaml \
  --once
```

peer B recall:

```bash
curl -X POST http://127.0.0.1:3102/v1/memory/recall \
  -H 'Content-Type: application/json' \
  -d '{"query":"peer","include_private":true,"limit":10}'
```

期待結果:

- peer B に shared fact が見える
- peer B に private fact は見えない

peer B sync status:

```bash
curl 'http://127.0.0.1:3102/v1/sync/status?namespace=team/dev'
```

期待結果:

- `state=healthy`
- `schema_fenced=false`
- peer A の `last_success_at_ms` が入る

index backlog を直接見るには `indexd` の診断出力を使う。

```bash
PATH=/opt/homebrew/bin:$PATH /opt/homebrew/bin/go run -tags sqlite_fts5 ./cmd/indexd \
  --config /tmp/crdt-agent-memory-dev/peer-a/config.yaml \
  --diag
```

返る JSON には次が入る。

- `processed_count`
- `pending_count`
- `embedding_count`
- `oldest_pending_enqueued_at_ms`
- `oldest_pending_age_ms`

## 6. HTTP Contract

`memoryd` の下記 4 エンドポイントは、成功時も失敗時も同じ envelope を返す。

```json
{
  "ok": true,
  "data": {},
  "warnings": [],
  "request_id": "req_..."
}
```

error 時:

```json
{
  "ok": false,
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "body is required",
    "retryable": false,
    "details": null
  },
  "warnings": [],
  "request_id": "req_..."
}
```

### `POST /v1/memory/store`

request:

```json
{
  "visibility": "shared",
  "namespace": "team/dev",
  "body": "shared fact",
  "subject": "shared"
}
```

response `data`:

```json
{
  "memory_ref": {
    "memory_space": "shared",
    "memory_id": "01H..."
  },
  "indexed": false,
  "sync_eligible": true
}
```

### `POST /v1/memory/recall`

request:

```json
{
  "query": "shared",
  "namespaces": ["team/dev"],
  "include_private": false,
  "limit": 10
}
```

response `data`:

```json
{
  "items": [
    {
      "memory_ref": {
        "memory_space": "shared",
        "memory_id": "01H..."
      },
      "namespace": "team/dev",
      "memory_type": "fact",
      "subject": "shared",
      "body": "shared fact",
      "lifecycle_state": "active",
      "authored_at_ms": 1741600000000,
      "source_uri": "",
      "source_hash": "",
      "origin_peer_id": "peer-a"
    }
  ]
}
```

### `POST /v1/memory/supersede`

request:

```json
{
  "old_memory_id": "01H...",
  "request": {
    "visibility": "shared",
    "namespace": "team/dev",
    "body": "updated fact"
  }
}
```

response `data`:

```json
{
  "old_memory_ref": {
    "memory_space": "shared",
    "memory_id": "01H..."
  },
  "new_memory_ref": {
    "memory_space": "shared",
    "memory_id": "01J..."
  },
  "lifecycle_state": "superseded"
}
```

### `GET /v1/sync/status?namespace=...`

response `data`:

```json
{
  "namespace": "team/dev",
  "state": "healthy",
  "schema_fenced": false,
  "peers": [
    {
      "peer_id": "peer-b",
      "namespace": "team/dev",
      "last_seen_at_ms": 1741600000000,
      "last_transport": "http-dev",
      "last_path_type": "direct",
      "last_error": null,
      "last_success_at_ms": 1741600000000,
      "schema_fenced": false
    }
  ]
}
```

Notes:

- `request_id` is now part of the contract for future MCP bridge reuse
- `warnings` is always returned, even when empty
- `/v1/sync/status` and `memory.sync_status` now share the same `data` shape
- `last_error` is `null` when unset

## 7. Diagnostics Checklist

問題が起きたら、まず次を見る。

- config の `peer_id`
- `peer_registry.sync_url`
- local `namespaces` と `namespace_allowlist`
- `schema_hash`
- `crr_manifest_hash`
- `sync_cursors`
- `peer_sync_state.last_error`
- `sync_quarantine`
- `index_queue.processed_at_ms`
- `indexd --diag` の `pending_count` と `oldest_pending_age_ms`

便利な SQL:

```sql
select count(*) from memory_nodes;
select count(*) from private_memory_nodes;
select * from sync_cursors;
select * from peer_sync_state;
select count(*) from sync_quarantine;
select count(*) from index_queue where processed_at_ms = 0;
```

## 8. MCP Verification

`memory-mcp` は現時点で `memory.store` / `memory.recall` / `memory.supersede` / `memory.signal` / `memory.explain` / `memory.sync_status` を実装済み。
bridge は必ず `memoryd` の HTTP API を叩き、`/v1/memory/store` / `/v1/memory/recall` / `/v1/memory/supersede` / `/v1/memory/signal` / `/v1/memory/explain` / `/v1/sync/status` の `data` / `warnings` / `request_id` をそのまま tool response に流す。
手動 smoke は MCP だけで store → recall → supersede / signal / explain まで完結できる。

手動で呼ぶ場合は、MCP stdio framing を使う。

結果として返る `structuredContent.data` には次が入る。

- `namespace`
- `state`
- `schema_fenced`
- `peers[*].last_success_at_ms`
- `peers[*].last_error`

## 8. Known Gaps

- transport は Iroh ではなく `http-dev`
- MCP surface は `memory.store` / `memory.recall` / `memory.supersede` / `memory.signal` / `memory.explain` / `memory.sync_status`
- `memory.trace_decision` は未実装
- embedding state のデフォルトは deterministic local embedding だが、`EMBEDDING_PROVIDER=openai` と `OPENAI_API_KEY` を設定すると OpenAI embeddings backend を使える
