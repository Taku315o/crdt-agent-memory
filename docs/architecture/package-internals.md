# Package Internals Summary

Status: Current implementation summary
Date: 2026-03-25

この文書は、現在の `internal/*` 主要 package の責務を OSS 向けに短く説明する。
古い package 単位の詳細メモは `legacy/` に退避した。

## `internal/storage`

- SQLite open と extension loading
- migration 実行
- schema metadata の計算
- shared CRR tables の enablement

補足:

- shared family だけが CRDT sync 対象
- private family と transcript family は local-only

## `internal/memory`

- shared/private memory の store
- transcript/private/shared unified recall
- candidate buffer の list / approve / reject
- transcript chunk からの promote
- private memory から shared memory への publish
- supersede / signal / explain / trace decision / context build

補足:

- transcript chunk は append-only
- candidate は private memory の前段バッファ
- `trace_decision` は transcript provenance を返す

## `internal/ingest`

- transcript session/message ingest
- deterministic chunking
- transcript artifact extraction
- ingest-time candidate generation

補足:

- transcript chunk は `chunk_strategy_version` を持つ
- recall は各 session の最新 version だけを見る
- 古い chunk は provenance 用に残る

## `internal/indexer`

- index queue / retrieval index queue の処理
- FTS / vector の更新
- source row 不在時の cleanup

## `internal/memsync`

- shared memory の増分同期
- handshake / extract / apply
- replay safety
- quarantine
- sync status / diagnostics

補足:

- 現在の transport は `http-dev`
- transcript と private memory は sync 対象外

## `internal/policy`

- allowed peer registry
- peer trust metadata
- signing key lookup に必要な local policy storage

## `internal/signing`

- app-level canonical payload signing
- Ed25519 verify helpers

## `internal/scrubber`

- shared graph の repair / diagnostics
- signature / trust / relation 健全性の補助チェック
