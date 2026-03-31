#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY_PATH="${MEMORY_MCP_BIN:-$ROOT_DIR/bin/memory-mcp}"
LOG_DIR="/tmp/crdt-agent-memory-mcp"
mkdir -p "$LOG_DIR"
{
  printf '%s wrapper pid=%s pwd=%s args=%s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$$" "$PWD" "$*"
  printf 'CRSQLITE_PATH=%s\n' "${CRSQLITE_PATH:-}"
  printf 'SQLITE_VEC_PATH=%s\n' "${SQLITE_VEC_PATH:-}"
  printf 'MEMORY_MCP_BIN=%s\n' "$BINARY_PATH"
} >>"$LOG_DIR/wrapper.log"

if [[ ! -x "$BINARY_PATH" ]]; then
  printf 'memory-mcp binary is not executable: %s\n' "$BINARY_PATH" >&2
  exit 1
fi

exec "$BINARY_PATH" "$@"
