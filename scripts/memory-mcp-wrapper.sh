#!/bin/bash
set -euo pipefail

LOG_DIR="/tmp/crdt-agent-memory-mcp"
mkdir -p "$LOG_DIR"
{
  printf '%s wrapper pid=%s pwd=%s args=%s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$$" "$PWD" "$*"
  printf 'CRSQLITE_PATH=%s\n' "${CRSQLITE_PATH:-}"
  printf 'SQLITE_VEC_PATH=%s\n' "${SQLITE_VEC_PATH:-}"
} >>"$LOG_DIR/wrapper.log"

exec /Users/hizawatakuto/Documents/MyProject/crdt-agent-memory/bin/memory-mcp "$@"
