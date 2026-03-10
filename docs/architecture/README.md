# CRDT-Agent-Memory Detailed Design Pack

Status: Draft v0.3
Date: 2026-03-10
Scope: Detailed architecture pack derived from `../crdt-agent-memory-spec.md`

## 1. Final Architecture Judgment

MVP の推奨構成は次のとおり。

- Language: Go
- Canonical storage: SQLite
- Shared-memory replication: cr-sqlite
- Cursor authority for shared sync: `crsql_tracked_peers`
- Supplemental peer metadata: local regular tables
- P2P transport: Iroh
- Lexical retrieval: FTS5
- Semantic retrieval: sqlite-vec
- Identity and signatures: Ed25519

この構成で最も重要なのは、責務境界を厳密に切ることだ。

| 責務 | 真実の置き場所 | 採用技術 | 明示的に使わない場所 |
| --- | --- | --- | --- |
| 保存 | 構造化メモリ本体 | SQLite | ベクトルを共有真実にしない |
| 同期 | shared CRR tables の changeset | cr-sqlite + `crsql_tracked_peers` + Iroh | Iroh に競合解決をさせない |
| 想起 | ローカル索引と再ランキング | FTS5 + sqlite-vec | sqlite-vec を共有同期しない |
| 信頼 | peer identity、allowlist、署名、trust weight | Ed25519 + local policy tables | CRDT に信頼裁定を委ねない |

## 2. High-Level System View

```mermaid
flowchart LR
    Agent[Agent Runtime] --> API[Memory Service API]
    API --> DB[(SQLite)]
    API --> IndexQ[Index Queue]
    Sync[Sync Daemon] --> DB
    Sync --> Iroh[Iroh Transport]
    IndexW[Index Worker] --> DB
    IndexW --> VIDX[FTS5 / sqlite-vec]
    Scrubber[Scrubber Worker] --> DB

    subgraph SharedTables[Shared CRR Tables]
        MN[memory_nodes]
        ME[memory_edges]
        MS[memory_signals]
        AR[artifact_refs]
        AS[artifact_spans]
        TP[crsql_tracked_peers]
    end

    subgraph LocalTables[Local-only Regular Tables]
        PMN[private_memory_nodes]
        PME[private_memory_edges]
        PMS[private_memory_signals]
        PAR[private_artifact_refs]
        PAS[private_artifact_spans]
        EMB[memory_embeddings]
        PSM[peer_sync_state]
        PP[peer_policies]
        SJ[sync_jobs]
        RC[retrieval_cache]
        PNV[private_notes]
    end

    DB --> MN
    DB --> PMN
```

## 3. Core Design Rules

- `cr-sqlite` は shared CRR tables のみに使う
- private structured memory は shared CRR tables に混在させず、最初から local regular tables に分ける
- shared sync の cursor は `crsql_tracked_peers` を正本にする
- `peer_sync_state` は transport 補助メタデータだけを持つ
- `Iroh` は encrypted stream transport のみに使う
- `sqlite-vec` は local derived index のみに使う
- semantic content の更新は overwrite ではなく `supersede` で表現する
- `confidence` や `salience` は mutable scalar ではなく signal event として蓄積する
- shared row の時刻は ordering truth ではなく advisory metadata として扱う
- row signature は CRDT metadata ではなく app-level canonical payload のみを署名する

## 4. Document Map

- [technology-decisions.md](./technology-decisions.md)
  - 技術選定、責務分離、一次情報ベースの比較
- [transport-and-bootstrap.md](./transport-and-bootstrap.md)
  - Iroh の bootstrap、discovery、relay、allowlist の既定運用
- [developer-setup-and-usage.md](./developer-setup-and-usage.md)
  - 開発者向けセットアップ、ローカル起動、日常運用フロー
- [migration-and-compatibility.md](./migration-and-compatibility.md)
  - schema migration、互換性判定、rolling upgrade 手順
- [identity-time-and-signatures.md](./identity-time-and-signatures.md)
  - peer identity、agent identity、時刻、署名、canonical payload
- [workflows.md](./workflows.md)
  - 保存、同期、想起、訂正、障害時再試行の流れ
- [data-model-erd.md](./data-model-erd.md)
  - ERD、shared/private 分離、関係、モデリング原則
- [non-functional-requirements.md](./non-functional-requirements.md)
  - 性能、可用性、セキュリティ、運用要件
- [testing-strategy.md](./testing-strategy.md)
  - テストレイヤ、環境、CI、故障注入
- [tdd-workflow.md](./tdd-workflow.md)
  - 実装順序、Red-Green-Refactor の切り方

## 5. Verified Primary Sources Snapshot

2026-03-10 時点で設計判断に使った一次情報。

- `cr-sqlite` quickstart: https://vlcn.io/docs/cr-sqlite/quickstart
- `cr-sqlite` constraints: https://www.vlcn.io/docs/cr-sqlite/constraints
- `cr-sqlite` whole CRR sync: https://www.vlcn.io/docs/cr-sqlite/networking/whole-crr-sync
- `cr-sqlite` transactions: https://vlcn.io/docs/cr-sqlite/transactions
- `cr-sqlite` schema alterations: https://vlcn.io/docs/cr-sqlite/advanced/migrations
- `Iroh` overview: https://www.iroh.computer/docs/overview
- `Iroh` endpoint identifiers: https://docs.iroh.computer/concepts/identifiers
- `Iroh` tickets: https://docs.iroh.computer/concepts/tickets
- `Iroh` relays: https://docs.iroh.computer/concepts/relays
- `Iroh` local discovery: https://www.iroh.computer/docs/concepts/local_discovery
- `Iroh` endpoint builder / discovery defaults: https://docs.rs/iroh/latest/iroh/endpoint/struct.Builder.html
- `sqlite-vec`: https://github.com/asg017/sqlite-vec
- `PowerSync` sync-postgres: https://www.powersync.com/sync-postgres
- `Ditto` about: https://docs.ditto.live/home/about-ditto
- `SQLite Sync`: https://github.com/sqliteai/sqlite-sync
