# MCP Tool Contract

Status: Draft v0.1
Date: 2026-03-10

## 1. Purpose

この文書は `memory-mcp` が公開する tools の contract を固定する。

固定するもの:

- tool names
- input schema
- output schema
- error model
- idempotency semantics
- sync fence や schema fence 中の応答

この文書を正本にして、Claude Desktop / Claude Code / Codex / Cursor / Gemini CLI / OpenCode 向け adapter はこの surface だけを登録する。

## 2. General Contract Rules

### Naming

MVP の tool 名は dotted namespace に固定する。

- `memory.store`
- `memory.recall`
- `memory.supersede`
- `memory.signal`
- `memory.trace_decision`
- `memory.explain`
- `memory.sync_status`

### Response envelope

すべての tool は次の top-level 形を返す。

```json
{
  "ok": true,
  "data": {},
  "warnings": [],
  "request_id": "req_01..."
}
```

error 時:

```json
{
  "ok": false,
  "error": {
    "code": "SCHEMA_FENCED",
    "message": "Shared schema mismatch prevents sync for this namespace.",
    "retryable": false,
    "details": {}
  },
  "request_id": "req_01..."
}
```

### Common error codes

| Code | Meaning | Retryable |
| --- | --- | --- |
| `INVALID_ARGUMENT` | schema validation failure | no |
| `NOT_FOUND` | referenced memory or artifact not found | no |
| `CONFLICT` | semantic conflict that requires caller choice | no |
| `SCHEMA_FENCED` | shared sync fenced due to schema mismatch | no |
| `SYNC_DEGRADED` | transport or sync issue exists but local operation may still proceed | yes |
| `UNAUTHORIZED_NAMESPACE` | caller tried disallowed namespace or visibility | no |
| `PRIVATE_ONLY` | operation is not allowed on private/shared mismatch | no |
| `INTERNAL_ERROR` | unexpected server failure | maybe |

### Common semantics

- tools operate against local canonical state only
- tools do not block on P2P sync completion
- tools may return sync-related warnings
- `request_id` is always returned for diagnostics

## 3. Capability Downgrade Policy

MVP は tools-first なので、client capability の違いは次で吸収する。

- if a client supports only tools:
  - everything remains available
- if a client supports resources/prompts too:
  - optional sugar may be added later, but tools stay canonical
- if a client is remote-only:
  - use the same tool contract over `Streamable HTTP`

Explicit downgrade rule:

- resources/prompts non-support never removes functionality
- all essential functionality must remain reachable through tools

## 4. Tool: `memory.store`

### Purpose

- 新しい shared/private memory を append する

### Input

```json
{
  "memory_type": "fact",
  "visibility": "shared",
  "namespace": "team/dev",
  "subject": "cr-sqlite",
  "body": "crsql_changes is not a full immutable transaction log.",
  "source_uri": "https://vlcn.io/docs/cr-sqlite/transactions",
  "source_hash": null,
  "relations": [
    {
      "relation_type": "derived_from",
      "to_memory_id": "01H..."
    }
  ],
  "idempotency_key": "optional-client-key"
}
```

### Required fields

- `memory_type`
- `visibility`
- `body`

### Visibility rules

- `shared` requires `namespace`
- `private` routes to private table family

### Output

```json
{
  "ok": true,
  "data": {
    "memory_ref": {
      "memory_space": "shared",
      "memory_id": "01H..."
    },
    "indexed": false,
    "sync_eligible": true
  },
  "warnings": []
}
```

### Idempotency

- if `idempotency_key` is provided, repeated identical requests should return the same successful result when feasible
- idempotency scope is local peer only
- absence of `idempotency_key` means append semantics; duplicate semantic content may create a new row

## 5. Tool: `memory.recall`

### Purpose

- query local memory and return ranked results with provenance

### Input

```json
{
  "query": "What do we know about MCP transport choices?",
  "mode": "fact_lookup",
  "visibility_filter": "both",
  "namespace": "team/dev",
  "top_k": 8,
  "include_sources": true,
  "include_contradictions": true
}
```

### Output

```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "memory_ref": {
          "memory_space": "shared",
          "memory_id": "01H..."
        },
        "memory_type": "fact",
        "body": "MCP standard transports are stdio and Streamable HTTP.",
        "score": 0.91,
        "score_breakdown": {
          "lexical": 0.22,
          "semantic": 0.41,
          "graph": 0.10,
          "temporal": 0.06,
          "trust": 0.12
        },
        "sources": [],
        "contradictions": []
      }
    ]
  },
  "warnings": []
}
```

### Idempotency

- pure read

## 6. Tool: `memory.supersede`

### Purpose

- create a new memory claim and mark an old one superseded

### Input

```json
{
  "old_memory_ref": {
    "memory_space": "shared",
    "memory_id": "01H..."
  },
  "new_body": "Updated claim body.",
  "reason": "Source document changed."
}
```

### Output

```json
{
  "ok": true,
  "data": {
    "old_memory_ref": {
      "memory_space": "shared",
      "memory_id": "01H..."
    },
    "new_memory_ref": {
      "memory_space": "shared",
      "memory_id": "01J..."
    },
    "lifecycle_state": "superseded"
  },
  "warnings": []
}
```

### Idempotency

- not naturally idempotent
- caller should use a client-side idempotency key if duplicate submission is a risk

## 7. Tool: `memory.signal`

### Purpose

- append a reinforce/deprecate/confirm/deny signal

### Input

```json
{
  "memory_ref": {
    "memory_space": "shared",
    "memory_id": "01H..."
  },
  "signal_type": "confirm",
  "value": 1.0,
  "reason": "Re-verified against upstream docs."
}
```

### Output

```json
{
  "ok": true,
  "data": {
    "signal_id": "01S..."
  },
  "warnings": []
}
```

### Idempotency

- append semantic
- duplicate prevention is caller responsibility unless explicit idempotency key support is later added

## 8. Tool: `memory.trace_decision`

### Purpose

- explain a decision by walking supporting and contradicting edges

### Input

```json
{
  "memory_ref": {
    "memory_space": "shared",
    "memory_id": "01H..."
  },
  "depth": 2
}
```

### Output

```json
{
  "ok": true,
  "data": {
    "decision": {},
    "supports": [],
    "contradictions": [],
    "artifacts": []
  },
  "warnings": []
}
```

## 9. Tool: `memory.explain`

### Purpose

- explain why a memory appeared in recall or why it is trusted

### Input

```json
{
  "memory_ref": {
    "memory_space": "shared",
    "memory_id": "01H..."
  }
}
```

### Output

```json
{
  "ok": true,
  "data": {
    "score_breakdown": {},
    "provenance": {},
    "trust_summary": {}
  },
  "warnings": []
}
```

## 10. Tool: `memory.sync_status`

### Purpose

- return local sync health without mutating state

### Input

```json
{
  "namespace": "team/dev"
}
```

### Output

```json
{
  "ok": true,
  "data": {
    "namespace": "team/dev",
    "state": "healthy",
    "schema_fenced": false,
    "peers": [
      {
        "peer_id": "endpointid:peer-b",
        "last_success_at_ms": 1741600000000,
        "last_error": null
      }
    ]
  },
  "warnings": []
}
```

## 11. Fence And Degraded-State Behavior

### Shared schema fence

- `memory.recall` continues to work locally
- `memory.store` for shared visibility still succeeds locally unless local policy disables it
- `memory.sync_status` must surface `schema_fenced=true`
- warnings should mention that remote peers may not receive new shared writes until compatibility is restored

### Transport degraded

- local read/write tools continue
- `warnings` may include `SYNC_DEGRADED`

### Namespace unauthorized

- mutating tools fail with `UNAUTHORIZED_NAMESPACE`

## 12. Validation Rules

- unknown fields should be ignored only if explicitly allowed in future versions; MVP is fail-closed on unknown required semantics
- enum values must be validated strictly
- `visibility=shared` without `namespace` is invalid
- `memory_space` in refs must be either `shared` or `private`

## 13. Versioning

- tool names remain stable within MVP
- additive optional fields are preferred
- breaking schema changes require protocol/version bump

