# CRDT-Agent-Memory

![Status](https://img.shields.io/badge/status-alpha-orange)
![Go](https://img.shields.io/badge/go-1.23+-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-MIT-blue)
![Tests](https://img.shields.io/badge/tests-passing-brightgreen)



A distributed, local-first memory system for AI agents with transcript ingest, structured memory promotion, and CRDT/P2P sync for shareable memory only.

## Overview

**CRDT-Agent-Memory** enables multiple AI agents to maintain independent SQLite-backed memory databases that automatically converge through peer-to-peer synchronization. No central server required. Each agent retains full local sovereignty while sharing meaningful memories with teammates.

### Why This Exists

- **Multi-agent teams need shared context** without losing local autonomy
- **Network assumptions break in AI workflows** — agents work offline, in regions with poor connectivity, and across different organizations
- **Existing solutions** either require a central database, don't support partial sharing, or serialize all data globally
- **CRDT + SQLite + Iroh** provides the foundation, but agent memory has distinct requirements (append-mostly semantics, layered trust, local indexing)

## Key Features

- 🏠 **Local-First**: Each agent keeps a full SQLite database; network is optional
- 🤝 **P2P Sync**: Automatic convergence via CRDT without a central authority
- 🔍 **Semantic Search**: Local FTS5 + vector indexing; derived data stays private
- 📝 **Transcript Layer**: Raw session logs stay local, searchable, and source-aware
- ⬆️ **Promote / Publish**: Turn transcript into private structured memory, then explicitly publish sanitized shared memory
- 🎯 **Partial Sharing**: Choose what to sync — team memory separate from personal memory
- 🔒 **Trust-Aware**: Configure which peers you trust for each namespace
- ⚡ **Agent-Ready**: MCP adapter + HTTP API + gRPC for easy integration
- 📦 **Go Binary**: Single portable daemon; works on macOS, Linux, Windows

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  AI Agents (Claude, Cursor, Custom)                 │
│  ↓ MCP / HTTP / gRPC                                │
├─────────────────────────────────────────────────────┤
│  Memory Service  (memoryd)                          │
│  ├─ API Layer (store, recall, context.build)        │
│  ├─ Transcript + Structured Memory                  │
│  └─ Local Index (FTS5 + sqlite-vec)                 │
├─────────────────────────────────────────────────────┤
│  Sync Engine    (syncd)                             │
│  ├─ CRDT Merge via cr-sqlite                        │
│  └─ P2P Transport (Iroh)                            │
├─────────────────────────────────────────────────────┤
│  Index Worker   (indexd)                            │
│  └─ Embeddings + Retrieval Cache                    │
└─────────────────────────────────────────────────────┘
        ↓ P2P Network (encrypted, NAT-traversing)
┌─────────────────────────────────────────────────────┐
│  peer-b agent-memory.sqlite                         │
└─────────────────────────────────────────────────────┘
```

**Key Design Principle**: Raw transcript and derived retrieval state stay local. Only promoted shared memory enters the CRDT sync lane.

## Quick Start

### Prerequisites

- Go 1.23+
- macOS / Linux / Windows

### Setup (30 seconds)

```bash
git clone https://github.com/taku315o/crdt-agent-memory.git
cd crdt-agent-memory

# Bootstrap dev tools (cr-sqlite, sqlite-vec)
make bootstrap-dev

# Setup local peer configs
make setup-dev-configs

# Create and migrate local databases
make migrate-peer-a migrate-peer-b
```

### Start the System

In three terminals:

```bash
# Terminal 1: Memory service (API server)
make serve-peer-a

# Terminal 2: Sync daemon (P2P sync)
make sync-peer-a

# Terminal 3: Index worker (search indexing)
make index-peer-a
```

### Store and Recall Memory

**Store a fact:**
```bash
curl -X POST http://127.0.0.1:3101/v1/memory/store \
  -H 'Content-Type: application/json' \
  -d '{
    "visibility": "shared",
    "namespace": "team/dev",
    "subject": "Architecture decision",
    "body": "We chose Iroh for P2P because of NAT traversal",
    "source_uri": "https://iroh.computer"
  }'
```

**Recall memories:**
```bash
curl -X POST http://127.0.0.1:3101/v1/memory/recall \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "P2P transport",
    "namespace": "team/dev"
  }'
```

**Build an agent-ready context bundle:**
```bash
curl -X POST http://127.0.0.1:3101/v1/context/build \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "why raw transcript is not synchronized",
    "namespace": "team/dev",
    "limit_per_section": 4
  }'
```

**Update a memory:**
```bash
curl -X POST http://127.0.0.1:3101/v1/memory/{memory_id}/supersede \
  -H 'Content-Type: application/json' \
  -d '{
    "new_body": "We chose Iroh also for its Rust-based reliability"
  }'
```

## Integration Modes

### Mode A: MCP Adapter (Recommended for Claude Desktop / Cursor)

Register the MCP server in your Claude config:

```json
{
  "mcpServers": {
    "memory": {
      "command": "crdt-agent-memory",
      "args": ["mcp"],
      "env": {
        "CONFIG_PATH": "/path/to/config.yaml"
      }
    }
  }
}
```

Then in Claude, use tools like `memory.store`, `memory.recall`, `context.build`, `memory.promote`, and `memory.publish` naturally.

### Mode B: Local HTTP API (For Custom Agents)

Any language can call the HTTP API:

```python
import requests

# Store
resp = requests.post("http://127.0.0.1:3101/v1/memory/store", json={
    "visibility": "shared",
    "namespace": "team/dev",
    "subject": "learned fact",
    "body": "..."
})

# Recall
resp = requests.post("http://127.0.0.1:3101/v1/memory/recall", json={
    "query": "what do we know about X?",
    "namespace": "team/dev"
})
```

### Mode C: gRPC (For Backend Services)

```bash
# Generate Go client from .proto
grpcurl -plaintext -d '{"query":"..."}' \
  127.0.0.1:50051 memory.MemoryService/Recall
```

## Configuration

Create a `config.yaml`:

```yaml
peer_id: "agent-alice"
database_path: "/path/to/agent_memory.sqlite"
signing_key_path: "/path/to/agent_alice.key"

namespaces:
  - "personal/alice"
  - "team/research"
  - "project/llm-optimization"

transport:
  discovery_profile: "dev-default"
  relay_profile: "dev-default"

peer_registry:
  - peer_id: "agent-bob"
    display_name: "Bob's Agent"
    namespace_allowlist: ["team/research", "project/llm-optimization"]
    discovery_profile: "dev-default"
```

## Core API

All endpoints return a standard envelope:

```json
{
  "ok": true,
  "data": { ... },
  "warnings": [],
  "request_id": "req_01H..."
}
```

Bundled native extensions are embedded for `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, and `windows/amd64`. Explicit extension paths remain optional overrides.

### Memory Operations

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/memory/store` | POST | Store a new shared/private structured memory |
| `/v1/memory/recall` | POST | Unified retrieval across transcript/private/shared memory |
| `/v1/context/build` | POST | Build a role-organized bundle for agent context |
| `/v1/memory/promote` | POST | Promote transcript chunk(s) into private structured memory |
| `/v1/memory/publish` | POST | Publish sanitized private memory into shared memory |
| `/v1/memory/supersede` | POST | Correct or update an existing shared memory |
| `/v1/memory/signal` | POST | Add a confidence/importance signal |
| `/v1/memory/explain` | POST | Explain recall/trust scoring for one memory |
| `/v1/memory/trace_decision` | POST | Trace decision graph, artifacts, and transcript provenance |
| `/healthz` | GET | Health check |
| `/v1/sync/status` | GET | Sync status across peers |

See [docs/architecture/mcp-tool-contract.md](docs/architecture/mcp-tool-contract.md) for full schema.

## How It Works

### Shared vs. Private Memory

**Shared memories** (marked `visibility: "shared"`) are:
- Automatically synced via CRDT to authorized peers
- Convergent — all peers see the same final state
- Useful for team knowledge, project decisions, cross-agent learnings

**Private memories** (marked `visibility: "private"`) are:
- Never synced
- Local-only storage
- Useful for personal notes, internal reasoning, debugging

### Sync Strategy

1. **Write locally** — agent calls `store()`, data is immediately available locally
2. **Index locally** — index worker embeds and caches the memory
3. **Notify peers** — sync daemon detects new shared memory
4. **Converge** — peers apply CRDT merge logic; conflicts are resolved application-aware
5. **Re-index** — each peer re-indexes settled memories locally

### Trust Model

Peers are configured per-namespace:

```yaml
peer_registry:
  - peer_id: "bob"
    namespace_allowlist: ["team/dev", "project/alpha"]
    # bob's memories in other namespaces are ignored
  - peer_id: "carol"
    namespace_allowlist: ["team/dev"]
    # carol is trusted only for team/dev
```

At recall time, results include `source_peer` so agents can decide credibility.

## Design Decisions

See [docs/architecture/README.md](docs/architecture/README.md) for detailed design rationale. Key principles:

- **Append-mostly**: Updates use `supersede` (new record) not in-place overwrites
- **Derived-local**: Embeddings and vector indices never sync
- **Dual-layer**: CRDT handles structure, app layer handles semantics
- **Cloud-optional**: Iroh relay nodes are optional; local P2P works offline

## Testing

```bash
make test

# Or verbose
go test -tags sqlite_fts5 ./... -v

# Integration smoke test
make clean-dev setup-dev-configs smoke-sync
```


## Project Structure

```
.
├── cmd/
│   ├── memoryd/      # Main API server
│   ├── syncd/        # P2P sync daemon
│   ├── indexd/       # Search index worker
│   └── memory-mcp/   # MCP adapter
├── internal/
│   ├── api/          # HTTP handlers
│   ├── memory/       # Core memory service
│   ├── memsync/      # CRDT + transport
│   ├── embedding/    # Vector operations
│   ├── indexer/      # FTS5 + cache
│   ├── policy/       # Trust + namespace rules
│   ├── signing/      # Ed25519 signatures
│   ├── storage/      # SQLite + migrations
│   └── scrubber/     # Data sanitization
├── migrations/       # SQL schema
├── docs/
│   ├── crdt-agent-memory-spec.md
│   └── architecture/ # Detailed design docs
└── Makefile
```

## Development

### Local Setup

```bash
# Install dependencies
go mod download

# Bootstrap dev tools
make bootstrap-dev

# Run tests
make test

# View docs
open docs/architecture/README.md
```

### Making Changes

1. Branch: `git checkout -b feature/your-feature`
2. Test: `make test`
3. Commit: `git commit -am "description"`
4. Push: `git push origin feature/your-feature`
5. PR: Open a pull request on GitHub


## License

MIT License

## Acknowledgments

- [cr-sqlite](https://vlcn.io) — CRDT SQLite foundation
- [Iroh](https://iroh.computer) — P2P transport
- [sqlite-vec](https://github.com/asg017/sqlite-vec) — Vector search
- [Anthropic MCP](https://modelcontextprotocol.io) — Agent integration standard

## Support

- **Docs**: [docs/](docs/)
- **Issues**: GitHub Issues
- **Discussions**: GitHub Discussions
- **Architecture**: [docs/architecture/README.md](docs/architecture/README.md)

---

**Status**: Alpha. Core functionality stable; API subject to change.

**Last Updated**: March 2026
