# Workflows

Status: Draft v0.2
Date: 2026-03-10

## 1. Components In Scope

- `Agent Runtime`
- `Memory Service`
- `SQLite`
- `Index Worker`
- `Sync Daemon`
- `Iroh Transport`
- `Remote Peer`

## 2. Workflow A: Local Memory Ingestion

目的:

- ローカルで記憶を作る
- 同期の有無に依存せず完結する
- 出典と関係を同時に保存する

```mermaid
sequenceDiagram
    participant Agent as Agent Runtime
    participant API as Memory Service
    participant DB as SQLite
    participant IDX as Index Worker

    Agent->>API: StoreMemory(command)
    API->>DB: begin tx
    API->>DB: insert artifact_refs (optional)
    API->>DB: insert memory_nodes
    API->>DB: insert memory_edges
    API->>DB: insert memory_signals
    API->>DB: commit tx
    API->>IDX: enqueue memory_id
    IDX->>DB: rebuild FTS5 entry
    IDX->>DB: rebuild embedding/vector row
    API-->>Agent: memory_id
```

完了条件:

- `memory_nodes` に append されている
- 必要な `artifact_refs` と `memory_edges` が存在する
- index queue に対象が積まれている

ここでやらないこと:

- peer への直接送信
- embedding の共有
- trust policy の変更

## 3. Workflow B: Peer Handshake And Delta Sync

目的:

- peer identity を確認する
- protocol/schema compatibility を確認する
- `crsql_changes` の差分だけを送受信する

```mermaid
sequenceDiagram
    participant SyncA as Sync Daemon A
    participant Iroh as Iroh Stream
    participant SyncB as Sync Daemon B
    participant DBA as SQLite A
    participant DBB as SQLite B

    SyncA->>Iroh: open control stream using ticket / known peer
    Iroh->>SyncB: encrypted connection
    SyncA->>SyncB: hello(protocol_version, peer_id, schema_hash, namespaces, watermarks)
    SyncB->>SyncA: hello_ack(peer_id, schema_hash, namespaces, watermarks)
    alt compatible
        SyncA->>DBA: select outbound changes from crsql_changes
        SyncB->>DBB: select outbound changes from crsql_changes
        SyncA->>SyncB: push change batches
        SyncB->>SyncA: push change batches
        SyncA->>DBA: mark peer_watermarks
        SyncB->>DBB: mark peer_watermarks
    else incompatible
        SyncA-->>SyncB: reject(sync_error)
    end
```

完了条件:

- allowlist で許可された peer だけが同期に進む
- `schema_hash` 不一致は即 reject
- 同期 payload は changeset batch のみ

ここでやらないこと:

- query API の中継
- vector blob の送信
- relay への永続保存

## 4. Workflow C: Change Apply And Local Reindex

目的:

- 受信差分を安全に適用する
- 必要な memory だけ再索引する

```mermaid
sequenceDiagram
    participant Sync as Sync Daemon
    participant DB as SQLite
    participant IDX as Index Worker

    Sync->>DB: begin tx
    Sync->>DB: insert into crsql_changes(...)
    DB-->>Sync: merged current state
    Sync->>DB: update peer_watermarks
    Sync->>DB: commit tx
    Sync->>IDX: enqueue changed memory_ids
    IDX->>DB: fetch memory_nodes by memory_id
    IDX->>DB: update FTS5
    IDX->>DB: update memory_embeddings
```

完了条件:

- `crsql_changes` 適用が冪等である
- reindex は changed memory のみ対象
- 受信順序が前後しても最終収束する

## 5. Workflow D: Recall And Decision Trace

目的:

- recall をローカル DB だけで完結させる
- supporting artifact と contradiction を返せるようにする

```mermaid
sequenceDiagram
    participant Agent as Agent Runtime
    participant API as Memory Service
    participant DB as SQLite

    Agent->>API: Recall(query, mode)
    API->>DB: FTS5 candidate search
    API->>DB: sqlite-vec candidate search
    API->>DB: fetch memory_edges and memory_signals
    API->>DB: fetch artifact_refs/artifact_spans
    API->>API: hybrid rerank
    API-->>Agent: ranked memories + sources + contradictions
```

ranking inputs:

- lexical relevance
- semantic similarity
- graph proximity
- temporal relevance
- trust weight

ここでやらないこと:

- remote peer への live query
- vector synchronization
- remote re-ranking

## 6. Workflow E: Memory Correction By Supersede

目的:

- semantic overwrite を避ける
- 履歴説明可能性を残す

```mermaid
sequenceDiagram
    participant Agent as Agent Runtime
    participant API as Memory Service
    participant DB as SQLite

    Agent->>API: SupersedeMemory(old_id, new_body)
    API->>DB: begin tx
    API->>DB: insert new memory_nodes row
    API->>DB: insert memory_edges(relation=supersedes)
    API->>DB: update old row state=superseded
    API->>DB: commit tx
    API-->>Agent: new_memory_id
```

設計意図:

- 古い memory を hard delete しない
- graph から更新関係を説明できる
- cell-wise merge に semantic overwrite を持ち込まない

## 7. Workflow F: Sync Retry And Backoff

```mermaid
stateDiagram-v2
    [*] --> Idle
    Idle --> Discovering: schedule tick
    Discovering --> Connecting: peer available
    Connecting --> Handshaking: stream opened
    Handshaking --> Syncing: compatible
    Handshaking --> Failed: incompatible / auth failed
    Syncing --> Reindexing: apply ok
    Reindexing --> Idle: queue flushed
    Syncing --> Failed: transport / apply error
    Failed --> Backoff: retryable
    Backoff --> Discovering: timer elapsed
    Failed --> Idle: terminal reject
```

retry policy:

- transport failure は exponential backoff
- schema mismatch は terminal reject
- auth failure は terminal reject
- apply failure は quarantine queue に逃がす

## 8. Workflow Ownership Matrix

| Workflow | Main owner | Secondary owner |
| --- | --- | --- |
| Local ingestion | Memory Service | Index Worker |
| Peer handshake | Sync Daemon | Iroh transport wrapper |
| Change apply | Sync Daemon | SQLite adapter |
| Recall | Memory Service | Index Worker |
| Supersede | Memory Service | SQLite adapter |
| Retry/backoff | Sync Daemon | peer policy module |

