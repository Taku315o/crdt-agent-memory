# Technology Decisions And Responsibility Boundaries

Status: Draft v0.2
Date: 2026-03-10

## 1. Decision Summary

MVP の判断は固定する。

- Keep: `Go + SQLite + cr-sqlite + Iroh + FTS5 + sqlite-vec + Ed25519`
- Defer: partial sync, public discovery, cloud-primary topology, shared embeddings, rich ACL
- Reject for MVP core: PowerSync as source-of-truth sync engine, Ditto as the core substrate, SQLite Sync as the main dependency, Veilid as the first transport

## 2. Primary-Source Verified Facts

| Claim | What it means for design | Source |
| --- | --- | --- |
| `cr-sqlite` lets you make selected tables syncable and keep other tables regular | 同じ DB 内で shared tables と local-only tables を分離できる | https://vlcn.io/docs/cr-sqlite/quickstart |
| `crsql_changes` is the interface for reading and applying changes | 同期の wire payload は DB 全体ではなく changeset batch でよい | https://vlcn.io/docs/cr-sqlite/quickstart |
| `whole CRR sync` is documented as the most tested and performant model | MVP は namespace 単位 whole sync を選ぶのが妥当 | https://www.vlcn.io/docs/cr-sqlite/networking/whole-crr-sync |
| CRR tables cannot use checked foreign keys or non-PK unique constraints | ERD はアプリ整合性と scrubber 前提で設計する必要がある | https://www.vlcn.io/docs/cr-sqlite/constraints |
| Iroh provides encrypted QUIC connections, direct connections, and relay fallback | NAT 越えや暗号 transport を自前実装しなくてよい | https://www.iroh.computer/docs/overview and https://docs.iroh.computer/concepts/relays |
| Iroh uses Ed25519-based node identifiers and node tickets | peer identity と接続情報の配布を統一できる | https://docs.iroh.computer/concepts/identifiers and https://docs.iroh.computer/concepts/tickets |
| `sqlite-vec` is pre-v1 and is presented as the successor direction to `sqlite-vss` | 共有本体ではなく再生成可能な local index に閉じるべき | https://github.com/asg017/sqlite-vec and https://github.com/asg017/sqlite-vss |
| PowerSync centers on syncing backend databases such as Postgres to client-side SQLite | authoritative backend 前提なので純 P2P の核には向かない | https://www.powersync.com/sync-postgres |
| Ditto positions itself as edge sync with built-in connectivity and mesh replication | 市場成立性の証拠にはなるが、SQLite 中核の memory substrate とは違う | https://docs.ditto.live/home/about-ditto |
| SQLite Sync is under Elastic License 2.0 and calls out commercial licensing for production/managed service use | MVP コア依存にするとライセンス面の制約を背負う | https://github.com/sqliteai/sqlite-sync |

## 3. Responsibility Matrix

| Technology | Use it for | Do not use it for | Why |
| --- | --- | --- | --- |
| Go | memory service, sync daemon, workers, API orchestration | heavy analytics, ad-hoc notebooking | 長期運用する daemon の実装と配布がしやすい |
| SQLite | canonical storage for structured memory and local state | cross-node consensus | local-first に最も自然 |
| cr-sqlite | shared CRR tables, delta extraction, delta apply | vector sharing, attachment sync, trust policy | 同期対象をテーブル単位で限定できる |
| Iroh | encrypted transport, tickets, relay-assisted connectivity | schema validation, conflict resolution | transport と sync semantics を分離できる |
| FTS5 | lexical retrieval, keyword filters, explainability support | semantic truth | SQLite への統合が軽い |
| sqlite-vec | local semantic recall accelerator | shared canonical state | pre-v1 なので派生物に閉じるべき |
| Ed25519 | peer identity, signatures, allowlist, trust binding | content ranking by itself | transport identity と署名検証の軸になる |

## 4. Where Each Technology Is Explicitly Discarded

### 4.1 cr-sqlite

使う場所:

- `memory_nodes`
- `memory_edges`
- `memory_signals`
- `artifact_refs`
- `artifact_spans`

捨てる場所:

- `memory_embeddings`
- `retrieval_cache`
- `sync_jobs`
- `peer_policies`
- `private_notes`
- attachment 本体

理由:

- 共有に値するのは構造化 memory
- 派生索引はモデル変更で簡単に invalid になる
- 信頼ポリシーはローカル裁定に残すべき

### 4.2 Iroh

使う場所:

- control stream
- changeset data streams
- relay fallback
- ticket-based peer bootstrap

捨てる場所:

- data model
- version vectors
- trust policy
- query protocol semantics

理由:

- Iroh は transport であって DB ではない
- 接続問題と同期意味論を混ぜると設計が鈍る

### 4.3 sqlite-vec

使う場所:

- local recall acceleration
- optional hybrid ranking
- re-embedding after sync apply

捨てる場所:

- cross-node replication payload
- audit log
- source-of-truth storage

理由:

- モデル、次元、index layout が変わりやすい
- 同じテキストでも埋め込み互換性が保証されない

## 5. Recommended MVP Architecture

```mermaid
flowchart TB
    subgraph NodeA[Peer Node]
        AgentA[Agent Runtime]
        MemA[Memory Service]
        DBA[(SQLite)]
        SyncA[Sync Daemon]
        IndexA[Index Worker]
        FTSA[FTS5]
        VecA[sqlite-vec]
    end

    subgraph Transport[Transport Layer]
        IROH[Iroh direct QUIC / relay fallback]
    end

    subgraph NodeB[Peer Node]
        AgentB[Agent Runtime]
        MemB[Memory Service]
        DBB[(SQLite)]
        SyncB[Sync Daemon]
        IndexB[Index Worker]
        FTSB[FTS5]
        VecB[sqlite-vec]
    end

    AgentA --> MemA --> DBA
    SyncA --> DBA
    IndexA --> DBA
    IndexA --> FTSA
    IndexA --> VecA

    AgentB --> MemB --> DBB
    SyncB --> DBB
    IndexB --> DBB
    IndexB --> FTSB
    IndexB --> VecB

    SyncA <--> IROH <--> SyncB
```

## 6. Alternatives And Why They Are Not Default

| Option | When it becomes attractive | Why it is not the default |
| --- | --- | --- |
| Veilid | privacy-first public overlay, stronger overlay anonymity requirements | MVP の責務としては transport が重い |
| Ditto | commercial edge sync, mobile-heavy deployment, attachments first | SQLite 中核と OSS 主導の substrate から離れる |
| PowerSync | server-centric SaaS, authoritative Postgres backend | 純 P2P ではなく中央 source-of-truth 前提 |
| SQLite Sync | rapidly testing a CRDT SQLite extension with built-in networking | ライセンス制約が重く、コア依存に向かない |
| libSQL/Turso | cloud-optional hub, analytics node, remote backup | MVP の canonical P2P memory fabric ではない |

## 7. Phase Decisions

### Phase 0

- SQLite only
- FTS5 + sqlite-vec local retrieval
- no sync

### Phase 1

- add `cr-sqlite`
- add `Iroh`
- use whole namespace sync

### Phase 2

- signed rows
- trust weighting
- artifact trace
- scrubber hardening

### Phase 3

- partial sync
- optional cloud hub
- optional alternative transport

## 8. Architectural Non-Negotiables

- shared truth is structured memory, not embeddings
- all sync payloads must be replay-safe and idempotent
- overwrite of semantic content must be replaced by append-plus-supersede
- relay must not become a hidden source of truth
- private scope must remain private even when stored alongside CRR tables

## 9. Source Links

- https://vlcn.io/docs/cr-sqlite/quickstart
- https://www.vlcn.io/docs/cr-sqlite/networking/whole-crr-sync
- https://www.vlcn.io/docs/cr-sqlite/constraints
- https://www.iroh.computer/docs/overview
- https://docs.iroh.computer/concepts/identifiers
- https://docs.iroh.computer/concepts/tickets
- https://docs.iroh.computer/concepts/relays
- https://github.com/asg017/sqlite-vec
- https://github.com/asg017/sqlite-vss
- https://www.powersync.com/sync-postgres
- https://docs.ditto.live/home/about-ditto
- https://github.com/sqliteai/sqlite-sync

