# Project Progress Snapshot

Status: Current implementation snapshot
Date: 2026-03-19

## 1. What Has Been Done

This repository now has a usable local development path for memory writes, recall, peer sync, indexing, and MCP tooling. The codebase is still in a dev-oriented state, but the most important boundaries are now explicit.

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

### Step 5: MCP expansion

- `memory-mcp` now exposes `memory.store`, `memory.recall`, and `memory.sync_status`.
- The MCP bridge always calls `memoryd` over HTTP.
- The bridge does not call the memory core directly.
- Tool calls forward `request_id` and `warnings` from the HTTP envelope.
- Manual smoke can now be driven through MCP tools instead of direct curl calls.

## 2. Current State By Area

| Area | Status | Notes |
| --- | --- | --- |
| Local memory core | Mostly complete for dev use | shared/private routing, recall, supersede, HTTP surface are in place |
| Sync core | Functional in `http-dev` mode | handshake, apply, replay safety, quarantine, status surface are implemented |
| Transport | Abstracted, but still `http-dev` only | Iroh is still pending |
| Indexing | Minimum operational level reached | retry-safe processing, cleanup, diagnostics, tests |
| MCP bridge | Partial but useful | `store` / `recall` / `sync_status` are available |
| Observability | Basic operational visibility exists | queue backlog and sync status are inspectable |

## 3. Remaining Work

The following items are still not implemented or are only partially implemented.

- Iroh transport replacement.
- Removing the remaining `sync_change_log` dependency in the sync path.
- Hardening the sync/index path so CRR schema changes are less brittle.
- `memory.supersede` MCP tool.
- `memory.signal` MCP tool.
- `memory.trace_decision` MCP tool.
- `memory.explain` MCP tool.
- Production semantic embedding instead of deterministic local embeddings.
- Signature / trust / scrubber work planned for the next phase.

## 4. Important Gaps To Keep In Mind

- Sync still runs in `http-dev`, not Iroh.
- The sync extraction path still depends on the `sync_change_log` capture flow.
- `memory.recall` is still FTS-backed; the index worker maintains derived embedding state and queue health, but it is not the canonical recall engine.
- The MCP bridge currently covers only the tools needed for day-to-day smoke and client integration.

## 5. Verification

- `make test` passes as of this snapshot.
- Transport, API contract, index worker, and MCP bridge all have regression tests around the new behavior.

