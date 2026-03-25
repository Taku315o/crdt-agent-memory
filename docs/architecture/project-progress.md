# Project Progress Snapshot

Status: Current implementation snapshot
Date: 2026-03-25

## 1. What Has Been Done

This repository now has a usable local development path for shared/private memory, transcript ingest, unified retrieval, promote/publish flow, peer sync, indexing, and MCP tooling.

### Step 1: Transport abstraction

- `http-dev` transport was extracted behind interfaces.
- `syncd` now depends on transport boundaries instead of transport details.
- HTTP transport implementation was moved out of the sync core.
- This makes an Iroh transport swap much smaller than before.

### Step 2: Transport-level sync regression coverage

- Added `httptest.Server` based integration tests for the transport layer.
- Shared writes reach peers.
- Private writes stay local.
- Replay is safe.
- Schema mismatch fences peers.
- Allowlist and namespace mismatch cases are covered.

### Step 3: memoryd API contract stabilization

- `store`, `recall`, `supersede`, and `sync_status` use explicit request/response DTOs.
- HTTP error responses use a single envelope shape.
- `request_id` and `warnings` are part of the contract.
- Docs were updated so the dev surface and MCP bridge shape stay aligned.

### Step 4: indexd operational minimum

- Queue processing is retry-safe per item.
- Missing source rows clean up stale embeddings.
- Diagnostics now expose processed and pending counts.
- Shared/private reindex behavior is covered by tests.
- Queue backlog can be observed via `indexd --diag` and `index_diag` logs.

### Step 5: Transcript / Promote / Publish / Context

- Added transcript-local tables for sessions, messages, chunks, promotions, publications, and transcript artifact spans.
- Added deterministic transcript ingest with idempotent message handling.
- Added unified `retrieval_units` based recall across transcript/private/shared memory spaces.
- Added `memory.promote`, `memory.publish`, and `context.build`.
- Added publish-time redaction policy handling.
- Added transcript artifact extraction, promote-time artifact inheritance, and transcript provenance in `trace_decision`.

### Step 6: MCP expansion

- `memory-mcp` now exposes `memory.store`, `memory.recall`, `context.build`, `memory.promote`, `memory.publish`, `memory.supersede`, `memory.signal`, `memory.explain`, `memory.trace_decision`, and `memory.sync_status`.
- The MCP bridge always calls `memoryd` over HTTP.
- The bridge does not call the memory core directly.
- Tool calls forward `request_id` and `warnings` from the HTTP envelope.
- Manual smoke can now be driven through MCP tools instead of direct curl calls.

## 2. Current State By Area

| Area | Status | Notes |
| --- | --- | --- |
| Local memory core | Broadly usable for dev use | shared/private routing, unified recall, promote/publish, traceability, HTTP surface are in place |
| Sync core | Functional in `http-dev` mode | handshake, apply, replay safety, quarantine, status surface are implemented |
| Transport | Abstracted, but still `http-dev` only | Iroh is still pending |
| Transcript lane | Implemented locally | ingest, chunking, transcript retrieval, artifact extraction, promotions are local-only |
| Indexing | Retrieval-unit level reached | retry-safe processing, cleanup, diagnostics, transcript/private/shared support |
| MCP bridge | Broad enough for agent workflows | `store` / `recall` / `context.build` / `promote` / `publish` / `trace_decision` and others are available |
| Observability | Basic operational visibility exists | queue backlog and sync status are inspectable |

## 3. Remaining Work

The following items are still not implemented or are only partially implemented.

- Iroh transport replacement.
- Removing the remaining `sync_change_log` dependency in the sync path.
- Hardening the sync/index path so CRR schema changes are less brittle.
- transcript chunk version coexistence and active chunk-set pointering.
- independent `transcript.search` API.
- richer `context.build` scoring and packing.
- Default production semantic embedding rollout across all environments and model management.
- Richer graph-based explainability beyond query-aware trust/bm25 breakdown.

## 4. Important Gaps To Keep In Mind

- Sync still runs in `http-dev`, not Iroh.
- The sync extraction path still depends on the `sync_change_log` capture flow.
- `memory.recall` now merges transcript/private/shared retrieval units. It uses FTS5 when available and a token-aware lexical fallback when it is not.
- `memory.explain` is query-aware and trust-aware, but it is still a per-row breakdown helper rather than the future unified hybrid reranker.
- `context.build` exists, but it is still a first practical bundle builder, not the final agent-tuned context packing system.

## 5. Verification

- `go test ./...` passes as of this snapshot.
- Transcript ingest, promote/publish flow, traceability, API contract, index worker, and MCP bridge all have regression tests around the new behavior.
