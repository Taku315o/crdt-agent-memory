#!/bin/bash
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: $0 command [args...]" >&2
  exit 1
fi

COMMAND=("$@")
CHILD_PID=""
CHILD_PGID=""

cleanup() {
  set +e
  if [ -n "$CHILD_PGID" ]; then
    kill -- "-$CHILD_PGID" >/dev/null 2>&1 || true
  fi
  if [ -n "$CHILD_PID" ]; then
    kill "$CHILD_PID" >/dev/null 2>&1 || true
    wait "$CHILD_PID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM HUP QUIT

if command -v setsid >/dev/null 2>&1; then
  setsid "${COMMAND[@]}" &
else
  "${COMMAND[@]}" &
fi
CHILD_PID="$!"
CHILD_PGID="$(ps -o pgid= -p "$CHILD_PID" | tr -d '[:space:]')"

wait "$CHILD_PID"
