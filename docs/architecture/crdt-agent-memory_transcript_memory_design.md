# CRDT-Agent-Memory 設計書
## Transcript Layer / Promote / Publish 導入設計

Version: draft-1
Target repo: `Taku315o/crdt-agent-memory`
Target branch: `main`

---

## 1. 目的

本設計は、現在の `CRDT-Agent-Memory` に対して、以下の 3 層を追加・整理するための詳細設計である。

1. **Transcript Layer**
   - セッション単位の生会話ログを private-first で保存する。
   - 保存時は LLM を使わず、deterministic に ingest / chunk / index する。

2. **Structured Memory Layer**
   - transcript から再利用価値のある知識を private structured memory として昇格する。
   - 既存の `memory_nodes` / `private_memory_nodes` を活かしつつ、raw transcript とは役割を分離する。

3. **Publish Layer**
   - private structured memory のうち共有価値があるものだけを shared memory に publish し、既存の CRDT / P2P 同期レーンに乗せる。

この設計のキーメッセージは以下の通り。

- **保存は素朴にする**
- **検索で賢くする**
- **共有は昇格後だけにする**
- **promote と publish を分離する**
- **private-first を既定にする**

---

## 2. 背景と repo 現状

現状の `internal/memory/service.go` の `Recall` は、`req.IncludePrivate` が false の場合 shared のみを検索対象にしており、private-first ではなく shared-first 寄りの挙動である。また recall は既存 memory view に対する fused retrieval として実装されている。fileciteturn5file0

現状の `internal/indexer/worker.go` は `fetchBody()` で `memory_nodes` / `private_memory_nodes` の body を取得し、`index_queue` からベクトル化する構造であり、source-aware な transcript indexing はまだ存在しない。fileciteturn6file0

現状の `migrations/0001_base.sql` は `memory_nodes`, `private_memory_nodes`, `memory_edges`, `artifact_refs`, `index_queue`, `memory_embeddings`, `recall_memory_view` などを持つが、raw transcript lane を表す `transcript_sessions`, `transcript_messages`, `transcript_chunks` は未導入である。fileciteturn7file0

したがって今回の設計は「別システムを後付けで作る」のではなく、**既存の recall/index/sync パイプラインを source-aware に一般化しつつ、raw transcript lane を追加する** ものとして設計する。

---

## 3. 非目標

本設計では以下は初期スコープ外とする。

- transcript 全文の peer 間同期
- transcript 保存時の LLM 要約
- transcript からの fully automatic shared publish
- GUI レイヤの詳細実装
- cross-peer transcript retrieval

理由は明確で、raw transcript はサイズ・秘匿性・同期待ちコストが大きく、shared memory と同じレーンに乗せるべきではないためである。

---

## 4. 設計原則

### 4.1 Private-first

新規に保存される会話・推論・中間結論は、原則 private に入る。
shared は export lane であり、既定の格納先ではない。

### 4.2 Raw と Meaning を分ける

- transcript = 生の出来事
- private structured memory = 意味化された知識
- shared memory = 公開価値がある知識

この 3 つは役割が違う。1 テーブルや 1 index で雑に統合しない。

### 4.3 Save-path は deterministic

ingest 時に行うのは以下のみ。

- validation
- normalization
- idempotency check
- storage
- chunking
- lexical index enqueue
- embedding enqueue

保存時に LLM を呼ばない。

### 4.4 Retrieval で賢くする

検索は hybrid retrieval と source-aware rerank で改善する。
重要なのは保存時の圧縮ではなく、**検索時の選抜精度** である。

### 4.5 Promote と Publish を分ける

- **promote**: transcript / raw candidates → private structured memory
- **publish**: private structured memory → shared memory

この二つを分離しないと private-first が壊れる。

---

## 5. 用語定義

### Transcript

セッション単位の生会話ログ。ユーザー入力、エージェント応答、ツール出力、checkpoint を含む。

### Chunk

transcript から deterministic に切り出した検索単位。

### Retrieval Unit

recall 時の共通検索単位。raw transcript chunk も promoted memory も同じ retrieval pipeline に載せるための論理概念。

### Promote

transcript chunk から private structured memory を生成する操作。

### Publish

private structured memory を shared memory に昇格させる操作。

---

## 6. 全体アーキテクチャ

```text
Agent/Client Hooks
  └─ session_end / checkpoint / manual_pin
       └─ ingest service
            ├─ transcript_sessions 保存
            ├─ transcript_messages 保存
            ├─ transcript_chunks 生成
            ├─ retrieval_units 生成
            ├─ lexical/vector index enqueue
            └─ artifact extraction

recall/query
  └─ retrieval_units を source-aware に検索
       ├─ transcript chunks
       ├─ private structured memories
       └─ shared memories
            └─ rerank / group / context.build

promotion path
  └─ transcript chunk(s)
       └─ private structured memory 作成
            └─ optional artifact spans / relations

publish path
  └─ private structured memory
       └─ shared memory_nodes へ変換
            └─ existing CRDT/P2P sync lane
```

---

## 7. データモデル

## 7.1 新規テーブル: transcript_sessions

```sql
CREATE TABLE transcript_sessions (
    session_id TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL,              -- claude_code / cursor / cli / chat
    namespace TEXT NOT NULL,
    repo_path TEXT NOT NULL DEFAULT '',
    repo_root_hash TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    agent_name TEXT NOT NULL DEFAULT '',
    user_identity TEXT NOT NULL DEFAULT '',
    started_at_ms INTEGER NOT NULL,
    ended_at_ms INTEGER NOT NULL,
    ingest_version INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    created_at_ms INTEGER NOT NULL
);
```

### 意図

- session を raw lane の親エンティティとして保持する。
- repo / branch / source_kind を保存し、後段の rerank に使う。
- `sensitivity` と `retention_class` を先に持たせ、secret / retention を後付けで苦しまずに済むようにする。

---

## 7.2 新規テーブル: transcript_messages

```sql
CREATE TABLE transcript_messages (
    message_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    role TEXT NOT NULL,                     -- system / user / assistant / tool
    tool_name TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(session_id, seq)
);

CREATE INDEX idx_transcript_messages_session_seq
    ON transcript_messages(session_id, seq);

CREATE INDEX idx_transcript_messages_session_hash
    ON transcript_messages(session_id, content_hash);
```

### 意図

- idempotent ingest の最小単位。
- `session_id + seq + content_hash` で重複保存を防ぐ。
- tool 出力も role と metadata で保持し、長文 trace から chunk へ昇格可能にする。

---

## 7.3 新規テーブル: transcript_chunks

```sql
CREATE TABLE transcript_chunks (
    chunk_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    chunk_strategy_version INTEGER NOT NULL,
    chunk_seq INTEGER NOT NULL,
    chunk_kind TEXT NOT NULL,               -- qa_pair / decision / rationale / debug_trace / summary_candidate
    start_seq INTEGER NOT NULL,
    end_seq INTEGER NOT NULL,
    text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    source_uri TEXT NOT NULL DEFAULT '',
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    is_indexable INTEGER NOT NULL DEFAULT 1,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(session_id, chunk_strategy_version, chunk_seq)
);

CREATE INDEX idx_transcript_chunks_session_authored
    ON transcript_chunks(session_id, authored_at_ms);

CREATE INDEX idx_transcript_chunks_kind_authored
    ON transcript_chunks(chunk_kind, authored_at_ms);
```

### 意図

- raw transcript 全文ではなく retrieval の単位を作る。
- `chunk_strategy_version` を持たせて、後で再 chunk しても上書き破壊しない。
- `chunk_kind` を持たせることで、意思決定・却下理由・デバッグ痕跡を source-aware に扱える。

---

## 7.4 新規テーブル: transcript_artifact_spans

```sql
CREATE TABLE transcript_artifact_spans (
    span_id TEXT PRIMARY KEY,
    chunk_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    start_offset INTEGER NOT NULL DEFAULT 0,
    end_offset INTEGER NOT NULL DEFAULT 0,
    start_line INTEGER NOT NULL DEFAULT 0,
    end_line INTEGER NOT NULL DEFAULT 0,
    quote_hash TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

CREATE INDEX idx_transcript_artifact_spans_chunk
    ON transcript_artifact_spans(chunk_id);
```

### 意図

- transcript と artifact traceability を接続する。
- 差別化要素として強い。後で `TraceDecision` 的 API を transcript source にも拡張できる。

---

## 7.5 retrieval_units 層の導入

### 重要

`transcript_chunk_fts` と `transcript_chunk_embeddings` を別実装として完全二重化するのではなく、**retrieval_units という共通 index 層** を導入する。

```sql
CREATE TABLE retrieval_units (
    unit_id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,              -- transcript_chunk / private_memory / shared_memory
    source_id TEXT NOT NULL,
    memory_space TEXT NOT NULL,             -- transcript / private / shared
    namespace TEXT NOT NULL,
    unit_kind TEXT NOT NULL,                -- decision / rationale / qa_pair / fact / task / note
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    state TEXT NOT NULL DEFAULT 'active',
    source_uri TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    schema_version INTEGER NOT NULL DEFAULT 1,
    UNIQUE(source_type, source_id)
);

CREATE INDEX idx_retrieval_units_space_namespace
    ON retrieval_units(memory_space, namespace, authored_at_ms DESC);

CREATE INDEX idx_retrieval_units_project
    ON retrieval_units(project_key, branch_name, authored_at_ms DESC);
```

### 意図

- recall / indexer / rerank を source-agnostic にする。
- transcript chunks と memory_nodes を別々の FTS / embeddings テーブルで二重実装しない。
- `memory_space='transcript'` を追加するだけで、既存 recall の設計と整合が取りやすい。

---

## 7.6 既存 index テーブルの一般化

既存の以下は `retrieval_units` ベースへ一般化する。

- `index_queue`
- `memory_embeddings`
- `memory_embedding_vectors`
- `memory_fts`
- `recall_memory_view`

### 新 shape 案

```sql
CREATE TABLE retrieval_index_queue (
    queue_id TEXT PRIMARY KEY,
    unit_id TEXT NOT NULL,
    enqueued_at_ms INTEGER NOT NULL,
    processed_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE retrieval_embeddings (
    unit_id TEXT PRIMARY KEY,
    embedding_json TEXT NOT NULL,
    embedding_dim INTEGER NOT NULL,
    indexed_at_ms INTEGER NOT NULL
);
```

FTS と vec のテーブル名は repo の既存実装都合で維持してもよいが、論理的には `unit_id` ベースへ寄せる。

---

## 8. 既存テーブルとの関係

### memory_nodes / private_memory_nodes

既存の `memory_nodes` / `private_memory_nodes` はそのまま活かす。fileciteturn7file0

ただし、役割を明示的に再定義する。

- `private_memory_nodes`: promote 後の private structured memory
- `memory_nodes`: publish 後の shared structured memory

### recall_memory_view

既存 `recall_memory_view` は shared/private memory の union view だが、transcript を検索対象に入れるには不足する。fileciteturn7file0

対応策は 2 つある。

1. `recall_memory_view` を拡張して transcript を union する
2. `retrieval_units_view` に移行する

本設計では **2** を推奨する。理由は transcript と memory を同列 union するより、`source_type`, `unit_kind`, `project_key` を持つ retrieval 専用 view の方が自然だからである。

---

## 9. Ingest 設計

## 9.1 入力契約

session ingest の payload は以下。

```json
{
  "session_id": "uuid",
  "source_kind": "claude_code",
  "namespace": "crdt-agent-memory",
  "repo_path": "/path/to/repo",
  "branch_name": "feat/transcript-ingest",
  "started_at_ms": 1711111111111,
  "ended_at_ms": 1711112222222,
  "messages": [
    {
      "seq": 1,
      "role": "user",
      "content": "...",
      "authored_at_ms": 1711111111111,
      "metadata_json": {}
    }
  ],
  "metadata_json": {}
}
```

## 9.2 Ingest 手順

1. payload validate
2. `session_id` existence check
3. session row upsert
4. message rows insert or ignore
5. message stream normalize
6. chunk 戦略で `transcript_chunks` 生成
7. artifact extraction
8. `retrieval_units` upsert
9. lexical / vector index queue enqueue
10. ingest audit record 保存

### 原則

- ingest は append-mostly
- 既存 session の再 ingest は allowed
- chunk strategy version が同じなら idempotent
- chunk strategy version が上がれば新 chunk set を作る

---

## 9.3 冪等性

### session 単位

- `session_id` が同じで payload が同じなら no-op
- `session_id` が同じで message 増分があるなら append update

### message 単位

- `UNIQUE(session_id, seq)`
- `content_hash` も照合し、不正な seq 再利用を検知

### chunk 単位

- `content_hash`
- `chunk_strategy_version`
- `UNIQUE(session_id, chunk_strategy_version, chunk_seq)`

### 理由

エージェント hook は retry, partial resend, duplicated close event が起こりうる。ここを雑にすると transcript lane が汚染される。

---

## 10. Chunking 設計

## 10.1 基本戦略

最初は deterministic rule-based chunking のみ。

### 基本単位

- user 1 発言 + assistant 1 発言
- tool 出力は前後の会話に吸収
- 長い assistant 出力は段落・見出し・箇条書きで再分割

### chunk_kind 判定ルール

- `decision`: 「決める」「方針」「採用」「やめる」「却下」
- `rationale`: 「理由」「なぜなら」「背景」「トレードオフ」
- `task_candidate`: TODO / next step / action item
- `debug_trace`: エラー再現、原因調査、修正案
- `qa_pair`: 上記に該当しない通常対話

### 補助ルール

- 文字数閾値超えで split
- 長い tool trace は別 chunk
- コードブロック単体は artifact 候補

## 10.2 chunk strategy versioning

`chunk_strategy_version` を必須にする。

理由:

- 後で chunk quality がほぼ確実に改善される
- 上書き更新だと過去の検索再現性が壊れる
- AB test しやすい

推奨:

- v1: QA pair + keyword-based semantic chunk kinds
- v2: tool-aware segmentation
- v3: artifact-aware segmentation

---

## 11. Indexing 設計

## 11.1 現状との差分

現状の indexer は `fetchBody()` で shared/private memory body を取得し、その body を embedding 化するだけである。fileciteturn6file0

これを以下へ拡張する。

```text
fetchIndexableUnit(unit_id)
  -> source_type に応じて retrieval_units から body を取得
  -> body/sensitivity/state を見て index 可否を判断
  -> lexical index upsert
  -> vector index upsert
```

## 11.2 source-aware index

indexer は `retrieval_units` を見ればよい。source table へ毎回分岐しない。

利点:

- indexer 実装が単純になる
- transcript 追加で worker を二重化しなくて済む
- recall 側も `unit_id` ベースで統一可能

## 11.3 secrets / sensitivity

全 private transcript を無条件 index しない。

### ルール

- `sensitivity='secret'` の unit は vector index しない
- `sensitivity='secret'` の unit は lexical index も masked 保存にするか、全文 index しない
- `retention_class='ephemeral'` は短 TTL を適用

### 理由

private でも index 面は平文複製になるため、raw 保存より漏えい面積が広い。

---

## 12. Recall 設計

## 12.1 外向き API

agent 向け API は最初から増やしすぎない。外向きは以下 2 本で十分。

1. `memory.recall` 拡張版
2. `context.build`

### memory.recall v2

- default: `include_private=true`
- `include_transcript=true`
- `include_shared=true`
- optional filter: namespace, source_type, unit_kind, project_key, branch_name

### context.build

- recall の結果をそのまま返さず、役割ごとに整理した context bundle を返す

### 理由

内部で `transcript.search` と `memory.recall` を分けるのはよいが、外向き API を最初から細かく分けすぎると運用が重い。

## 12.2 private-first 既定

現状 `Recall()` は `IncludePrivate=false` だと shared のみ検索する構造だが、今後は **既定を private-first** に変更する。fileciteturn5file0

提案:

- `IncludePrivate` の default を true
- `IncludeShared` を明示フラグとして追加
- `IncludeTranscript` を追加

## 12.3 スコアリング

基本式:

```text
final_score
= hybrid_score
× freshness_decay
× project_boost
× namespace_boost
× type_boost
× pin_boost
× sensitivity_penalty
× state_penalty
```

### hybrid_score

- lexical rank と vector rank を RRF で融合
- 既存 recall の fused スコア思想は再利用可能。fileciteturn5file0

### freshness_decay

half-life を unit_kind ごとに変える。

- qa_pair: 14–30日
- debug_trace: 30–60日
- decision / rationale: 180–365日
- durable fact: 365日+

### project_boost

現行 repo / branch に一致する retrieval unit を加点する。

### type_boost

クエリが設計判断っぽい場合は `decision`, `rationale`, `rejected_option` を優先。

### state_penalty

- active: 1.0
- superseded: 0.4
- retracted/deleted: 0.0 or hidden

---

## 13. Context Build 設計

`context.build` は recall 生結果ではなく、以下の bundle を返す。

1. Active private decisions
2. Relevant shared decisions
3. Recent related transcript chunks
4. Rejected options / contradictions
5. Open tasks
6. Related artifacts

出力例:

```json
{
  "active_private_decisions": [...],
  "shared_constraints": [...],
  "recent_discussions": [...],
  "rejected_options": [...],
  "open_tasks": [...],
  "artifacts": [...]
}
```

### 理由

エージェントは raw top-k より、役割で束ねられた文脈を読む方が強い。

---

## 14. Promote 設計

## 14.1 promote の意味

promote は transcript / candidate から **private structured memory を作る操作** である。
shared 化ではない。

## 14.2 promote 入力

- chunk_id 単体
- chunk_id 複数
- manual selection
- future: auto candidate acceptance

## 14.3 promote 出力

格納先は `private_memory_nodes`。
関係・artifact span も private 側へ作る。

```text
transcript_chunk(s)
  -> promote
     -> private_memory_nodes
     -> private_memory_edges
     -> private_memory_signals
     -> private_artifact_spans
```

## 14.4 promote 手順

1. input chunks load
2. candidate summary build（最初は rule-based）
3. memory_type 決定
4. subject/body/source_uri/source_hash 決定
5. `private_memory_nodes` insert
6. transcript ↔ promoted memory link 保存
7. retrieval_units upsert
8. index enqueue

## 14.5 transcript-to-memory link

新規に以下を追加する。

```sql
CREATE TABLE transcript_promotions (
    promotion_id TEXT PRIMARY KEY,
    chunk_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    UNIQUE(chunk_id, memory_id)
);
```

### 理由

後で「この private memory がどの会話から来たか」を追跡できる。

---

## 15. Publish 設計

## 15.1 publish の意味

publish は **private structured memory を shared memory に変換する操作** である。
ここだけが sync lane への入口になる。

## 15.2 publish 原則

- input は `private_memory_nodes.memory_id`
- output は `memory_nodes`
- 同一 body の repeated publish は idempotent
- publish 後も private 原本は残す

## 15.3 publish 手順

1. private memory load
2. publish policy check
3. redaction / sanitization
4. shared `Store` equivalent を実行
5. artifact / relation の shared 版を複製
6. `published_from_private_memory` link 保存
7. sync queue / change log は既存 shared lane を利用

## 15.4 mapping table

```sql
CREATE TABLE memory_publications (
    publication_id TEXT PRIMARY KEY,
    private_memory_id TEXT NOT NULL,
    shared_memory_id TEXT NOT NULL,
    published_at_ms INTEGER NOT NULL,
    UNIQUE(private_memory_id, shared_memory_id)
);
```

### 理由

publish 後の追跡と supersede 時の整合のため。

---

## 16. Artifact Traceability

これは必須寄り。差別化要素として強い。

## 16.1 transcript 側

- ファイルパス
- 関数名
- PR 番号
- issue 番号
- commit SHA
- error string

を抽出して `transcript_artifact_spans` または artifact ref へ紐づける。

## 16.2 promote 側

promote 時に、関連 chunk の artifact spans を private memory に引き継ぐ。

## 16.3 publish 側

publish 時に artifact を shared に複製するかは policy 次第。

推奨:

- public source URI のみ shared 複製
- local file path は shared へ出さない

---

## 17. Retention / Sensitivity

## 17.1 sensitivity

候補値:

- `private`
- `secret`
- `restricted`
- `shareable`

### 運用

- transcript ingest 時に secret detector を通す
- API keys / tokens / passwords は `secret`
- `secret` は vector index しない
- `secret` の publish は禁止

## 17.2 retention

候補値:

- `ephemeral`
- `default`
- `durable`
- `pinned`

### 運用

- transcript qa_pair は default
- debug traces は default or ephemeral
- promoted decisions は durable
- pinned signals は pinned

---

## 18. Migration 設計

## 18.1 マイグレーション分割

### 0002_transcript_lane.sql

- transcript_sessions
- transcript_messages
- transcript_chunks
- transcript_artifact_spans
- transcript_promotions
- memory_publications

### 0003_retrieval_units.sql

- retrieval_units
- retrieval_index_queue
- retrieval_embeddings
- retrieval_units_view
- FTS / vec migration

### 0004_private_first_recall.sql

- recall defaults
- API schema changes
- optional backward compatibility view

## 18.2 後方互換

初期段階では既存 `memory_embeddings` / `index_queue` を残したまま、worker 側で両対応してもよい。
ただし最終的には retrieval unit ベースへ寄せる。

---

## 19. 実装変更ポイント

## 19.1 internal/memory/service.go

### 変更

- `RecallRequest` に以下を追加
  - `IncludeTranscript`
  - `IncludeShared`
  - `ProjectKey`
  - `BranchName`
  - `UnitKinds`
  - `SourceTypes`
- default を private-first に変更
- recall 対象を `retrieval_units_view` へ移行
- `Promote()` を追加
- `Publish()` を追加

### 注意

既存 `Store` は shared/private memory 書き込みの核なので残す。promote は private `Store` の薄いラッパではなく、transcript provenance を付ける higher-level command にする。

## 19.2 internal/indexer/worker.go

### 変更

- queue source を `retrieval_index_queue` に寄せる
- `fetchBody()` を `fetchIndexableUnit()` に置換
- transcript / private memory / shared memory の差分は retrieval_units 吸収

### 理由

現状 worker は body 取得元が memory table 固定であり、transcript source を増やすと分岐が増殖するため。fileciteturn6file0

## 19.3 migrations/0001_base.sql 以降

raw transcript lane が無いため、後続 migration で追加する。fileciteturn7file0

---

## 20. API 仕様

## 20.1 memory.recall

入力:

```json
{
  "query": "なぜ raw transcript を同期しない方針にしたか",
  "limit": 12,
  "include_private": true,
  "include_shared": true,
  "include_transcript": true,
  "namespaces": ["crdt-agent-memory"],
  "project_key": "Taku315o/crdt-agent-memory",
  "branch_name": "main",
  "unit_kinds": ["decision", "rationale", "qa_pair"]
}
```

出力:

```json
{
  "results": [
    {
      "unit_id": "...",
      "source_type": "transcript_chunk",
      "memory_space": "transcript",
      "unit_kind": "decision",
      "namespace": "crdt-agent-memory",
      "body": "...",
      "authored_at_ms": 1711111111111,
      "score": 12.34,
      "provenance": {...}
    }
  ]
}
```

## 20.2 context.build

入力:

```json
{
  "query": "今回の transcript layer 導入方針を議論したい",
  "namespace": "crdt-agent-memory",
  "project_key": "Taku315o/crdt-agent-memory",
  "branch_name": "main",
  "limit_per_section": 5
}
```

出力は sectioned bundle。

## 20.3 memory.promote

入力:

```json
{
  "chunk_ids": ["chunk-1", "chunk-2"],
  "memory_type": "decision",
  "subject": "raw transcript sync policy",
  "namespace": "crdt-agent-memory"
}
```

出力:

```json
{
  "private_memory_id": "..."
}
```

## 20.4 memory.publish

入力:

```json
{
  "private_memory_id": "...",
  "redaction_policy": "default"
}
```

出力:

```json
{
  "shared_memory_id": "..."
}
```

---

## 21. テスト戦略

## 21.1 ingest

- same session resent → no duplicate messages
- partial resend → append only
- chunk strategy version change → new chunk set
- secret transcript → masked / non-indexed

## 21.2 recall

- private-first default
- transcript + private memory + shared memory fused ranking
- namespace / project / branch filtering
- superseded memory penalty

## 21.3 promote

- chunk -> private memory
- provenance mapping 作成
- artifact spans 引継ぎ

## 21.4 publish

- private -> shared
- no transcript leak
- existing sync_change_log に正しく載る

## 21.5 indexer

- retrieval unit queue 処理
- deleted source cleanup
- vector disabled fallback

---

## 22. 観測性

追加メトリクス:

- transcript_sessions_total
- transcript_messages_total
- transcript_chunks_total
- retrieval_units_total by source_type
- promote_total
- publish_total
- recall_hit_ratio by source_type
- secret_redaction_total
- index_queue_oldest_age_ms

ログ:

- ingest request id
- session_id
- chunk_strategy_version
- promoted memory ids
- published memory ids

---

## 23. 段階的実装順

### Phase 1: raw transcript lane

- transcript_sessions / messages / chunks migration
- session_end ingest
- deterministic chunking
- retrieval_units for transcript only
- lexical recall only

### Phase 2: unified recall

- retrieval_units for private/shared memory も導入
- indexer を source-aware に拡張
- hybrid recall を transcript + memory へ拡張
- private-first default へ変更

### Phase 3: promote

- transcript_promotions
- `memory.promote`
- artifact trace inheritance

### Phase 4: publish

- `memory.publish`
- memory_publications
- redaction / share policy
- existing CRDT/P2P lane 接続

### Phase 5: refinement

- context.build
- chunk strategy v2
- retention jobs
- publish recommendation

---

## 24. 最重要判断まとめ

1. **transcript と memory_nodes は分離する**
2. **検索 index は retrieval_units で共通化し、完全二重化しない**
3. **外向き API は recall/context.build を中心に絞る**
4. **promote は private structured memory 化、publish は shared 化として固定する**
5. **private-first を既定に変更する**
6. **transcript は同期しない。publish 済み shared memory のみ既存 CRDT lane に乗せる**

---

## 25. 実装開始点

この設計書に基づく、最初の実装着手点は次の 3 つ。

1. `0002_transcript_lane.sql` を追加する
2. `internal/ingest/session_ingest.go` を新設する
3. `internal/indexer/worker.go` を retrieval unit aware に変えるための抽象を入れる

この順に入れば、設計は机上論ではなく、そのまま repo の進化線になる。
