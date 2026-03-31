# Implementation Plan

Status: Current forward plan
Date: 2026-03-25

## Purpose

この文書は、2026-03-25 時点の実装完了範囲を前提に、次に進めるべき作業を固定する。
transcript / promote / publish / candidate buffer は実装済みとして扱う。

## Completed Foundations

### Memory Core

- shared/private memory write routing
- recall
- supersede
- signals
- explain
- trace decision
- HTTP envelope contract

### Transcript Layer

- `transcript_sessions`, `transcript_messages`, `transcript_chunks`
- deterministic ingest
- transcript artifact extraction
- append-only chunk versioning
- latest-version-only transcript recall
- transcript provenance in trace

### Promotion / Publish

- manual `memory.promote`
- candidate buffer with `list / approve / reject`
- `memory.publish`
- publish-time redaction policy

### Sync / MCP

- `http-dev` sync transport
- replay-safe apply
- sync diagnostics / status surface
- MCP bridge over HTTP

## Next Priorities

1. `transcript.search` を独立 surface として追加する
2. `context.build` の scoring / packing を agent-tuned に強化する
3. `http-dev` transport を Iroh transport に置き換える
4. sync extraction path の hardening を進める
5. embedding rollout とモデル運用を固定する

## Non-Priorities For The Next Release

- transcript の peer-to-peer 同期
- transcript からの automatic shared publish
- GUI triage workflow の本実装
- full graph-native explain UI

## Public Surface Expected To Remain Stable

### `memoryd`

- `POST /v1/memory/store`
- `POST /v1/memory/recall`
- `GET /v1/memory/candidates`
- `POST /v1/memory/candidates/approve`
- `POST /v1/memory/candidates/reject`
- `POST /v1/memory/promote`
- `POST /v1/memory/publish`
- `POST /v1/memory/supersede`
- `POST /v1/memory/signal`
- `POST /v1/memory/explain`
- `POST /v1/memory/trace_decision`
- `POST /v1/context/build`
- `GET /v1/sync/status`

### `memory-mcp`

- `memory.store`
- `memory.recall`
- `memory.candidates.list`
- `memory.candidates.approve`
- `memory.candidates.reject`
- `memory.promote`
- `memory.publish`
- `memory.supersede`
- `memory.signal`
- `memory.explain`
- `memory.trace_decision`
- `context.build`
- `memory.sync_status`

## Acceptance Criteria For The Next Milestone

- 独立 `transcript.search` surface が入る
- transport doc と実装のズレが減る
- `context.build` の出力品質を改善する回帰テストが入る
- release note が current docs だけで読める状態になる
