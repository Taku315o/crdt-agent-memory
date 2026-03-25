# MCP Tool Contract

Status: Current implementation contract
Date: 2026-03-25

## Purpose

この文書は `memory-mcp` が現在公開している tool surface を固定する。
MCP bridge は常に `memoryd` HTTP API を呼び、memory core を直接呼ばない。

## General Rules

- top-level response は `ok`, `data`, `warnings`, `request_id` を持つ
- local operation は P2P sync 完了を待たない
- sync 関連の劣化は `warnings` または `memory.sync_status` で扱う
- essential functionality はすべて tools から到達可能である

## Current Tools

### Read / Context

- `memory.recall`
  - transcript / private / shared の unified recall
- `context.build`
  - role-organized context bundle を返す
- `memory.explain`
  - recall match の理由と trust の影響を返す
- `memory.trace_decision`
  - relation graph, artifacts, transcript provenance を返す
- `memory.sync_status`
  - local sync health を返す

### Write / Lifecycle

- `memory.store`
  - structured memory を直接保存する
- `memory.supersede`
  - shared memory を履歴付きで置き換える
- `memory.signal`
  - shared/private memory に signal を追加する

### Transcript Promotion

- `memory.promote`
  - transcript chunk(s) から private structured memory を作る
- `memory.publish`
  - private structured memory を shared memory に公開する
- `memory.candidates.list`
  - pending / reviewed promotion candidates を列挙する
- `memory.candidates.approve`
  - candidate を承認して private memory を作る
- `memory.candidates.reject`
  - candidate を却下する

## Tool Boundaries

- transcript 由来の durable memory 化は `memory.store` ではなく `memory.promote` または `memory.candidates.approve`
- shared 化は常に `memory.publish`
- shared memory の訂正は `memory.supersede`
- candidate triage は `memory.candidates.*`

## Error Model

| Code | Meaning |
| --- | --- |
| `INVALID_ARGUMENT` | request schema or validation failure |
| `NOT_FOUND` | referenced memory / candidate / artifact not found |
| `PRIVATE_ONLY` | operation is not allowed on private/shared mismatch |
| `CANDIDATE_NOT_PENDING` | candidate was already reviewed |
| `METHOD_NOT_ALLOWED` | wrong HTTP method behind the bridge |

## Notes On Current Semantics

- `memory.recall` は transcript/private/shared retrieval unit を統合する
- transcript chunk は append-only で、recall は各 session の最新 `chunk_strategy_version` だけを見る
- candidate buffer は ingest 時に `decision`, `task_candidate`, `rationale`, `debug_trace` から生成される
- `memory.trace_decision` は promoted memory から元 transcript chunk を辿れる
