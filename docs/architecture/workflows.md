# Workflows

Status: Current reference
Date: 2026-03-26

## 1. Transcript Ingest

```mermaid
sequenceDiagram
    participant Client
    participant Ingest
    participant DB

    Client->>Ingest: ingest session/messages
    Ingest->>DB: upsert transcript_sessions
    Ingest->>DB: upsert transcript_messages
    Ingest->>DB: insert transcript_chunks (append-only)
    Ingest->>DB: insert transcript_artifact_spans
    Ingest->>DB: insert retrieval_units for transcript
    Ingest->>DB: insert memory_candidates for promotable chunks
```

要点:

- transcript chunk は append-only
- 同一 session の旧 chunk version は残す
- candidate はまだ正式 memory ではない

## 2. Candidate Triage

```mermaid
flowchart LR
    Chunk[transcript chunk] --> Candidate[memory_candidates status=pending]
    Candidate -->|approve| Promote[private_memory_nodes]
    Candidate -->|reject| Rejected[status=rejected]
```

要点:

- approve でのみ正式 private memory を作る
- reject は transcript を消さない
- provenance は chunk link のまま保持される

## 3. Manual Promote

```mermaid
sequenceDiagram
    participant Client
    participant API
    participant DB

    Client->>API: memory.promote(chunk_ids)
    API->>DB: load transcript_chunks
    API->>DB: create private_memory_nodes
    API->>DB: copy transcript artifact spans
    API->>DB: insert transcript_promotions
    API->>DB: upsert retrieval_units for private memory
```

## 4. Publish

```mermaid
sequenceDiagram
    participant Client
    participant API
    participant DB
    participant Sync

    Client->>API: memory.publish(private_memory_id)
    API->>DB: create memory_nodes
    API->>DB: insert memory_publications
    API->>DB: enqueue shared sync-visible changes
    Sync->>DB: extract/apply shared CRR changes
```

## 5. Recall

```mermaid
flowchart TB
    Query --> RetrievalUnits[retrieval_units]
    RetrievalUnits --> Transcript[transcript latest chunks only]
    RetrievalUnits --> Private[private memory]
    RetrievalUnits --> Shared[shared memory]
    Transcript --> Rank
    Private --> Rank
    Shared --> Rank
    Rank --> Results
```

要点:

- transcript/private/shared を unified recall する
- transcript は各 session の最新 `chunk_strategy_version` だけ対象
- old chunk は trace 用に残す

## 6. Trace Decision

```mermaid
flowchart LR
    Decision[private/shared memory] --> Edges[memory edges]
    Decision --> Promotions[transcript_promotions]
    Promotions --> Chunks[transcript_chunks]
    Chunks --> Artifacts[transcript_artifact_spans]
```

要点:

- trace は memory graph と transcript provenance の両方を返す
- promote 後に chunk version が増えても、既存 promotion link は壊さない

## 7. Shared Sync Boundary

- sync 対象は shared table family のみ
- private memory, transcript, candidate は peer に同期しない
- transcript から shared へ行くには `promote -> publish` の 2 段階が必要
