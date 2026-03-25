# MCPサーバーセットアップ手順

## 問題
`npx @modelcontextprotocol/inspector` で `mcp-server-everything ENOENT` エラーが出た

## 原因
MCP Inspector が実行可能な MCP サーバーコマンドを見つけられなかった

## 解決策

### 1. MCPサーバーをビルド
```bash
cd /Users/hizawatakuto/Documents/MyProject/crdt-agent-memory
mkdir -p bin
PATH=/opt/homebrew/bin:$PATH CRSQLITE_PATH="./.tools/crsqlite/crsqlite.dylib" SQLITE_VEC_PATH="./.tools/sqlite-vec/vec0.dylib" \
  go build -tags sqlite_fts5 -o ./bin/memory-mcp ./cmd/memory-mcp
```

### 2. MCPサーバー用の設定ファイル作成
`mcp-dev.yaml` を作成（既に作成済み）

### 3. MCP Inspectorの設定作成
`.mcp/inspector-config.json` を作成（既に作成済み）

### 4. MCP Inspectorを起動
```bash
npx @modelcontextprotocol/inspector \
  --config /Users/hizawatakuto/Documents/MyProject/crdt-agent-memory/.mcp/inspector-config.json \
  --server memory-mcp
```

## MCPサーバーへの直接テスト
```bash
cd /Users/hizawatakuto/Documents/MyProject/crdt-agent-memory

# MCP RPC リクエストをサーバーに送信
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | \
  ./bin/memory-mcp --config mcp-dev.yaml
```

## ツール定義の確認
MCPサーバーが提供するツールは以下の通りです：

- `memory.store` - 新規メモリ作成
- `memory.recall` - メモリ検索
- `memory.supersede` - 既存メモリの置き換え
- `memory.signal` - メモリへのシグナル追加
- `memory.explain` - メモリの関連性説明
- `memory.trace_decision` - 決定トレース
- `memory.sync_status` - 同期ステータス確認
- `memory.promote` - トランスクリプトから昇格
- `memory.publish` - プライベートメモリを共有に
- `memory.candidates.list` - 昇格候補一覧
- `memory.candidates.approve` - 候補承認
- `memory.candidates.reject` - 候補却下
- `context.build` - コンテキスト構築

すべてのツール定義は `cmd/memory-mcp/main.go` の `toolDefinitions()` 関数で管理されています。
