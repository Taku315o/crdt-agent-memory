# Project Progress Snapshot

Status: Current implementation snapshot
Date: 2026-03-25

## Summary

この repository は、shared/private memory、transcript ingest、unified retrieval、candidate buffer、promote/publish、traceability、MCP bridge、`http-dev` ベースの peer sync を持つローカル実装として成立している。

現時点の重要な到達点は以下。

- transcript / private / shared を `retrieval_units` で統合検索できる
- transcript chunk は append-only で複数 strategy version を共存できる
- recall は各 session の最新 `chunk_strategy_version` の transcript だけを検索対象にする
- promote candidate buffer が入り、ingest → candidate → approve/reject → private memory の 2 段階昇格になった
- `trace_decision` は promoted memory から元 transcript chunk を追跡できる
- shared memory のみが CRDT sync lane に乗る

## Implemented Areas

| Area | Status | Notes |
| --- | --- | --- |
| Local memory core | Implemented | shared/private routing, signals, supersede, explain, trace are available |
| Transcript lane | Implemented | session/message/chunk storage, artifact extraction, append-only chunk versioning, candidate generation |
| Promotion flow | Implemented | manual promote と candidate approve/reject の両方を提供 |
| Retrieval | Implemented | transcript/private/shared unified recall, FTS5 fallback, sqlite-vec support |
| Publishing | Implemented | private structured memory から shared memory への explicit publish |
| Sync core | Functional in `http-dev` mode | handshake, apply, replay safety, quarantine, sync status は実装済み |
| MCP bridge | Implemented | `memory.store`, `memory.recall`, `memory.promote`, `memory.publish`, `memory.candidates.*` などを公開 |
| Indexing | Implemented | retrieval-unit level indexing, cleanup, retry-safe queue processing |

## Newly Landed Since The Initial Transcript Design

- promotion candidate tables and approval flow
- ingest-time candidate generation for `decision`, `task_candidate`, `rationale`, `debug_trace`
- transcript chunk version coexistence
- latest-version-only transcript recall filtering

## Remaining Work

- Iroh transport replacement
- `transcript.search` の独立 surface
- `context.build` の packing / ranking 強化
- sync path からの `sync_change_log` 依存の縮小
- production embedding rollout と運用方針の固定

## Release Readiness Notes

OSS としてのコア説明は成立しているが、release 前に継続して見るべき点は以下。

- transport docs を `http-dev` 現状に明確に寄せる
- concept note と current docs の境界を維持する
- package internals は current summary に集約し、古いメモを archive 扱いにする

## Verification

- `go test ./...` passes on the current branch
- transcript ingest, candidate flow, promote/publish, traceability, API contract, MCP bridge, sync, index worker に回帰テストがある
