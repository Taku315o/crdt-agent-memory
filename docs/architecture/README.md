# CRDT-Agent-Memory Architecture Docs

Status: Current documentation portal
Date: 2026-03-25

このディレクトリは、OSS 公開向けのアーキテクチャ文書セットです。
正本、現行実装ガイド、参考資料、archive を分けて管理します。

## Canonical Docs

- [crdt-agent-memory_transcript_memory_design.md](./crdt-agent-memory_transcript_memory_design.md)
  - transcript / retrieval / promote / publish / candidate buffer の正本設計
- [developer-setup-and-usage.md](./developer-setup-and-usage.md)
  - 開発者向けセットアップと日常の操作手順
- [project-progress.md](./project-progress.md)
  - 2026-03-25 時点の現行実装スナップショット
- [implementation-plan.md](./implementation-plan.md)
  - 現在の残タスクと実装優先順位

## Current Reference Docs

- [package-internals.md](./package-internals.md)
  - `internal/storage`, `internal/memory`, `internal/memsync`, `internal/policy` の現行要約
- [data-model-erd.md](./data-model-erd.md)
  - データモデル全体像
- [workflows.md](./workflows.md)
  - store / recall / promote / publish / sync の流れ
- [mcp-tool-contract.md](./mcp-tool-contract.md)
  - MCP tool surface
- [identity-time-and-signatures.md](./identity-time-and-signatures.md)
  - identity / time / signature の扱い
- [migration-and-compatibility.md](./migration-and-compatibility.md)
  - migration / compatibility / schema fencing
- [non-functional-requirements.md](./non-functional-requirements.md)
  - 性能・運用・安全性要件
- [testing-strategy.md](./testing-strategy.md)
  - テスト戦略
- [technology-decisions.md](./technology-decisions.md)
  - 技術選定メモ
- [transport-and-bootstrap.md](./transport-and-bootstrap.md)
  - transport と bootstrap の方針

## Historical / Concept Docs

- [../crdt-agent-memory-spec.md](../crdt-agent-memory-spec.md)
  - 初期の概念仕様。現行実装との差分があるため、正本としては扱わない
- [legacy/README.md](./legacy/README.md)
  - 古い package-level note と廃止前の設計メモ

## Reading Order

初めて読む場合は以下の順を推奨します。

1. [crdt-agent-memory_transcript_memory_design.md](./crdt-agent-memory_transcript_memory_design.md)
2. [project-progress.md](./project-progress.md)
3. [developer-setup-and-usage.md](./developer-setup-and-usage.md)
4. [package-internals.md](./package-internals.md)

## Documentation Policy

- 正本は明示的に 1 つに寄せる
- 実装スナップショットは `project-progress.md` に集約する
- package-level の古い説明文書は更新されない限り archive に移す
- 内部作業由来の断片や会話引用トークンは公開文書に残さない
