# Implementation Plan

Status: Updated after transcript / promote / publish / context implementation
Date: 2026-03-25

## 1. Purpose

この文書は、現在の実装状態を前提に `CRDT-Agent-Memory` の進捗と残タスクを固定する。

## 2. Current State Summary

### Phase 0: Local Memory Core

完了:

- shared/private の write routing
- FTS5 recall
- supersede
- local HTTP control surface

追加完了:

- unified retrieval units
- transcript/private/shared fused recall
- `memory.promote`
- `memory.publish`
- `memory.trace_decision`
- `context.build`

### Phase 1: Shared Sync Core

完了:

- real `cr-sqlite` extension loading
- shared table family の CRR enablement
- fake CRR schema の migration fence
- long-running `syncd`
- `--once` one-shot sync
- peer registry と allowlist の実運用
- schema/protocol/manifest handshake checks
- replay-safe apply
- quarantine path
- apply 後の reindex queue / worker
- sync status HTTP surface
- minimal `memory.sync_status` MCP tool

### Phase 2: Transcript / Promote / Publish

完了:

- `transcript_sessions`, `transcript_messages`, `transcript_chunks`
- deterministic transcript ingest
- transcript artifact extraction
- transcript/private/shared unified retrieval
- publish-time redaction
- transcript provenance in trace
- MCP/HTTP surface for promote/publish/context build

部分完了:

- relation promotion は heuristic ベースの初期版
- context bundle は初期 section 構成

未実装:

- transcript chunk の複数世代共存運用
- `transcript.search` の独立 API
- promotion candidate レイヤの明示化

現在の transport:

- 開発用の実装は `http-dev` で peer-to-peer whole sync を行う
- `discovery_profile` / `relay_profile` は config 上に残すが、今のコードでは HTTP dev transport を使う
- Iroh transport は未実装で、次フェーズの transport replacement として扱う

## 3. Phase 1 Delivery Details

### PR6: CRR enablement + local 2-DB sync

完了:

- `cr-sqlite` を `go-sqlite3` driver 経由で load
- shared family のみ CRR 化
- private family は regular table のまま維持
- old fake-CRR DB は `ErrLegacyDB` で fail closed

### PR7: Sync daemon + handshake

完了:

- `syncd` が daemon / `--once` の両方を持つ
- peer registry が authoritative
- allowlist 未登録 peer は handshake 不可
- namespace intersection が sync 単位になる

補足:

- 実 transport は Iroh ではなく `http-dev`

### PR8: Transport E2E apply / replay / reindex

完了:

- `crsql_changes` ベース extract/apply
- replay-safe apply
- `sync_cursors` に cursor truth を保持
- `peer_sync_state` に success/error/schema fence を保持
- `indexd` が queue を消化して embedding state を更新
- `memoryd` が sync status を返す
- `memory-mcp` が `memory.sync_status` を公開

## 4. Public Interfaces

### `memoryd`

- `--cmd migrate`
- `--cmd diag`
- default `serve`

HTTP:

- `GET /healthz`
- `GET /v1/diag`
- `POST /v1/memory/store`
- `POST /v1/memory/recall`
- `POST /v1/context/build`
- `POST /v1/memory/promote`
- `POST /v1/memory/publish`
- `POST /v1/memory/supersede`
- `POST /v1/memory/signal`
- `POST /v1/memory/explain`
- `POST /v1/memory/trace_decision`
- `GET /v1/sync/status?namespace=...`

### `syncd`

- default daemon mode
- `--once` one-shot sync
- remote peers come only from `peer_registry`

Internal sync endpoints:

- `POST /v1/sync/handshake`
- `POST /v1/sync/pull`
- `POST /v1/sync/apply`

### `memory-mcp`

実装済み:

- `memory.store`
- `memory.recall`
- `context.build`
- `memory.promote`
- `memory.publish`
- `memory.supersede`
- `memory.signal`
- `memory.trace_decision`
- `memory.explain`
- `memory.sync_status`

## 5. Acceptance State

Phase 1 として現在確認済み:

- shared write が remote peer に反映される
- private write は remote peer に出ない
- replay apply が安全
- schema mismatch を status surface に反映できる
- 2 peer smoke で peer B recall に shared fact が現れる
- `memory.sync_status` が healthy/fenced を返せる

未完了の主要項目:

- Iroh transport への差し替え
- transcript/search/context のさらに細かい役割分離
- transcript chunk versioning の本格運用
- promotion candidate pipeline

## 6. Next Work

次の現実的な実装順は次。

1. `transcript.search` を独立 surface として追加
2. `context.build` の packing を agent-tuned に改善
3. transcript chunk versioning と active chunk-set pointer を追加
4. promotion candidate レイヤを追加
5. `http-dev` transport を Iroh に差し替える
