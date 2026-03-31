#!/bin/bash
set -euo pipefail

MODE="${1:-all}"
case "$MODE" in
  sync|recall|all)
    ;;
  *)
    echo "usage: $0 [sync|recall|all]" >&2
    exit 1
    ;;
esac

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

GO_BIN="${GO_BIN:-/opt/homebrew/bin/go}"
GOFLAGS="${GOFLAGS:--tags sqlite_fts5}"
TMP_ROOT="${TMP_ROOT:-/tmp}"
TMP_BASE="${TMP_BASE:-}"
AUTO_TMP_BASE=0
KEEP_SMOKE_ARTIFACTS="${KEEP_SMOKE_ARTIFACTS:-0}"
SMOKE_RETRY_ATTEMPTS="${SMOKE_RETRY_ATTEMPTS:-5}"
SMOKE_RETRY_DELAY_SECS="${SMOKE_RETRY_DELAY_SECS:-1}"
SQLITE_BUSY_TIMEOUT_MS="${SQLITE_BUSY_TIMEOUT_MS:-15000}"
SQLITE_READ_ATTEMPTS="${SQLITE_READ_ATTEMPTS:-60}"
PORT_SEED="${PORT_SEED:-$$}"
BUILD_DIR=""
SMOKE_PIDS=()

CRSQLITE_DIR="$REPO_ROOT/.tools/crsqlite"
SQLITE_VEC_DIR="$REPO_ROOT/.tools/sqlite-vec"
SHARED_BODY="recall shared fact from peer a"
PRIVATE_BODY="private fact from peer a"

if [ -z "$TMP_BASE" ]; then
  TMP_BASE="$(mktemp -d "$TMP_ROOT/crdt-agent-memory-smoke.XXXXXX")"
  AUTO_TMP_BASE=1
fi

PEER_A_CONFIG="$TMP_BASE/peer-a/config.yaml"
PEER_B_CONFIG="$TMP_BASE/peer-b/config.yaml"

cleanup() {
  local exit_code=$?
  set +e
  for pid in "${SMOKE_PIDS[@]-}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  for pid in "${SMOKE_PIDS[@]-}"; do
    wait "$pid" >/dev/null 2>&1 || true
  done
  if [ -n "$BUILD_DIR" ]; then
    rm -rf "$BUILD_DIR"
  fi
  if [ "$exit_code" -ne 0 ] && [ -n "$TMP_BASE" ]; then
    echo "smoke-e2e-manual:$MODE: FAIL"
    echo "logs: $TMP_BASE/logs"
  fi
  if [ "$AUTO_TMP_BASE" -eq 1 ] && [ "$KEEP_SMOKE_ARTIFACTS" != "1" ] && [ "$exit_code" -eq 0 ] && [ -n "$TMP_BASE" ]; then
    rm -rf "$TMP_BASE"
  fi
  return "$exit_code"
}
trap cleanup EXIT INT TERM HUP QUIT

start_daemon() {
  local log_file="$1"
  shift
  "$@" >"$log_file" 2>&1 &
  SMOKE_PIDS+=("$!")
}

run_with_retry() {
  local name="$1"
  shift
  local attempts="$SMOKE_RETRY_ATTEMPTS"
  local try=1
  local log_file="$TMP_BASE/logs/$name.log"
  local status

  while true; do
    : >"$log_file"
    if "$@" >>"$log_file" 2>&1; then
      return 0
    fi
    status=$?
    if [ "$try" -ge "$attempts" ]; then
      cat "$log_file" >&2
      return "$status"
    fi
    if ! grep -Eq 'database is locked|SQLITE_BUSY' "$log_file"; then
      cat "$log_file" >&2
      return "$status"
    fi
    sleep "$SMOKE_RETRY_DELAY_SECS"
    try=$((try + 1))
  done
}

wait_http() {
  local url="$1"
  local attempts=40
  until curl -fsS "$url" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    if [ "$attempts" -le 0 ]; then
      echo "timeout waiting for $url" >&2
      return 1
    fi
    sleep 0.5
  done
}

wait_post() {
  local url="$1"
  local body="$2"
  local attempts=40
  local code
  until code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$url" -H 'Content-Type: application/json' -d "$body" 2>/dev/null) && [ "$code" != "000" ]; do
    attempts=$((attempts - 1))
    if [ "$attempts" -le 0 ]; then
      echo "timeout waiting for $url" >&2
      return 1
    fi
    sleep 0.5
  done
}

pick_free_port() {
  local p="$1"
  while lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; do
    p=$((p + 1))
  done
  printf '%s\n' "$p"
}

pick_port_block() {
  local seed="$1"
  local api_base=$((20000 + (seed % 5000) * 4))
  local sync_base=$((api_base + 2000))
  API_A_PORT="$(pick_free_port "$api_base")"
  API_B_PORT="$(pick_free_port "$((API_A_PORT + 1))")"
  SYNC_A_PORT="$(pick_free_port "$sync_base")"
  SYNC_B_PORT="$(pick_free_port "$((SYNC_A_PORT + 1))")"
}

sqlite_scalar() {
  local db="$1"
  local query="$2"
  local attempts="$SQLITE_READ_ATTEMPTS"
  local result
  until result=$(sqlite3 -cmd "PRAGMA busy_timeout=$SQLITE_BUSY_TIMEOUT_MS;" "$db" "$query" 2>/dev/null | tail -n 1); do
    attempts=$((attempts - 1))
    if [ "$attempts" -le 0 ]; then
      echo "timeout reading $db" >&2
      return 1
    fi
    sleep 0.5
  done
  printf '%s\n' "$result"
}

print_log_location() {
  if [ "$AUTO_TMP_BASE" -eq 1 ] && [ "$KEEP_SMOKE_ARTIFACTS" != "1" ]; then
    echo "logs: $TMP_BASE/logs (removed on success; set KEEP_SMOKE_ARTIFACTS=1 to preserve)"
    return
  fi
  echo "logs: $TMP_BASE/logs"
}

setup_environment() {
  rm -rf "$TMP_BASE"
  mkdir -p "$TMP_BASE/peer-a" "$TMP_BASE/peer-b" "$TMP_BASE/logs"
  cp configs/peer-a.yaml.example "$PEER_A_CONFIG"
  cp configs/peer-b.yaml.example "$PEER_B_CONFIG"
  bash scripts/setup-keys.sh "$TMP_BASE"
  run_with_retry migrate-peer-a env PATH=/opt/homebrew/bin:$PATH "$GO_BIN" run $GOFLAGS ./cmd/memoryd --config "$PEER_A_CONFIG" --cmd migrate
  run_with_retry migrate-peer-b env PATH=/opt/homebrew/bin:$PATH "$GO_BIN" run $GOFLAGS ./cmd/memoryd --config "$PEER_B_CONFIG" --cmd migrate

  BUILD_DIR="$(mktemp -d "$TMP_BASE/smoke-build.XXXXXX")"
  PATH=/opt/homebrew/bin:$PATH CGO_ENABLED=1 "$GO_BIN" build $GOFLAGS -o "$BUILD_DIR/memoryd" ./cmd/memoryd
  PATH=/opt/homebrew/bin:$PATH CGO_ENABLED=1 "$GO_BIN" build $GOFLAGS -o "$BUILD_DIR/syncd" ./cmd/syncd

  pick_port_block "$PORT_SEED"

  sed -i '' -e "s#127.0.0.1:3101#127.0.0.1:$API_A_PORT#g" -e "s#127.0.0.1:3201#127.0.0.1:$SYNC_A_PORT#g" -e "s#127.0.0.1:3202#127.0.0.1:$SYNC_B_PORT#g" "$PEER_A_CONFIG"
  sed -i '' -e "s#127.0.0.1:3102#127.0.0.1:$API_B_PORT#g" -e "s#127.0.0.1:3202#127.0.0.1:$SYNC_B_PORT#g" -e "s#127.0.0.1:3201#127.0.0.1:$SYNC_A_PORT#g" "$PEER_B_CONFIG"
  sed -i '' -e "s#/tmp/crdt-agent-memory-dev/peer-a#$TMP_BASE/peer-a#g" "$PEER_A_CONFIG"
  sed -i '' -e "s#/tmp/crdt-agent-memory-dev/peer-b#$TMP_BASE/peer-b#g" "$PEER_B_CONFIG"
  sed -i '' -E 's#signing_public_key: ".*"#signing_public_key: "c96c5a7dcbe46299db6d31f5bbdd9e2aad4d8cf2c255f9249b79f246ab703c5d"#' "$PEER_A_CONFIG"
  sed -i '' -E 's#signing_public_key: ".*"#signing_public_key: "94e1db860da5fd064970847a5e13b54d2548e62881e66ef17414a4a16c43b605"#' "$PEER_B_CONFIG"

  printf 'crdt-agent-memory/peer-a' | shasum -a 256 | awk '{print $1}' >"$TMP_BASE/peer-a/peer.key"
  printf 'crdt-agent-memory/peer-b' | shasum -a 256 | awk '{print $1}' >"$TMP_BASE/peer-b/peer.key"
  chmod 600 "$TMP_BASE/peer-a/peer.key" "$TMP_BASE/peer-b/peer.key"

  start_daemon "$TMP_BASE/logs/memoryd-peer-a.log" "$BUILD_DIR/memoryd" --config "$PEER_A_CONFIG"
  start_daemon "$TMP_BASE/logs/memoryd-peer-b.log" "$BUILD_DIR/memoryd" --config "$PEER_B_CONFIG"
  start_daemon "$TMP_BASE/logs/syncd-peer-b.log" "$BUILD_DIR/syncd" --config "$PEER_B_CONFIG"

  wait_http "http://127.0.0.1:$API_A_PORT/healthz"
  wait_http "http://127.0.0.1:$API_B_PORT/healthz"
  wait_post "http://127.0.0.1:$SYNC_B_PORT/v1/sync/handshake" '{}'

  curl -sS -X POST "http://127.0.0.1:$API_A_PORT/v1/memory/store" \
    -H 'Content-Type: application/json' \
    -d "{\"visibility\":\"shared\",\"namespace\":\"team/dev\",\"subject\":\"shared\",\"body\":\"$SHARED_BODY\",\"origin_peer_id\":\"peer-a\",\"author_agent_id\":\"agent-a\"}" >/dev/null
  curl -sS -X POST "http://127.0.0.1:$API_A_PORT/v1/memory/store" \
    -H 'Content-Type: application/json' \
    -d "{\"visibility\":\"private\",\"namespace\":\"local/dev\",\"subject\":\"private\",\"body\":\"$PRIVATE_BODY\",\"origin_peer_id\":\"peer-a\",\"author_agent_id\":\"agent-a\"}" >/dev/null

  run_with_retry sync-once-peer-a env PATH=/opt/homebrew/bin:$PATH "$GO_BIN" run $GOFLAGS ./cmd/syncd --config "$PEER_A_CONFIG" --once
}

run_sync_check() {
  local status_json="$TMP_BASE/logs/sync-status-peer-b.json"
  curl -sS "http://127.0.0.1:$API_B_PORT/v1/sync/status?namespace=team/dev" >"$status_json"

  local sync_db="$TMP_BASE/peer-b/agent_memory.sqlite"
  local shared_count private_count
  shared_count="$(sqlite_scalar "$sync_db" "select count(*) from recall_memory_view where body = '$SHARED_BODY';")"
  private_count="$(sqlite_scalar "$sync_db" "select count(*) from recall_memory_view where body = '$PRIVATE_BODY';")"

  [ "$shared_count" -eq 1 ]
  [ "$private_count" -eq 0 ]
  grep -Eq '"state"[[:space:]]*:[[:space:]]*"healthy"' "$status_json"
  grep -Eq '"schema_fenced"[[:space:]]*:[[:space:]]*false' "$status_json"
  grep -Eq '"peer_id"[[:space:]]*:[[:space:]]*"peer-a"' "$status_json"
  grep -Eq '"last_success_at_ms"[[:space:]]*:[[:space:]]*[1-9][0-9]*' "$status_json"
}

run_recall_check() {
  run_with_retry index-once-peer-a env PATH=/opt/homebrew/bin:$PATH "$GO_BIN" run $GOFLAGS ./cmd/indexd --config "$PEER_A_CONFIG" --once

  local recall_json="$TMP_BASE/logs/recall-peer-a.json"
  curl -sS -X POST "http://127.0.0.1:$API_A_PORT/v1/memory/recall" \
    -H 'Content-Type: application/json' \
    -d '{"query":"recall shared fact from peer a","namespaces":["team/dev"],"include_private":false,"include_shared":true,"limit":10}' >"$recall_json"

  grep -q "$SHARED_BODY" "$recall_json"
  ! grep -q "$PRIVATE_BODY" "$recall_json"
}

setup_environment

case "$MODE" in
  sync)
    run_sync_check
    ;;
  recall)
    run_recall_check
    ;;
  all)
    run_sync_check
    run_recall_check
    ;;
esac

echo "smoke-e2e-manual:$MODE: PASS"
print_log_location
