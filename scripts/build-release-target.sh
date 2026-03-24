#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: $0 <version> <target-os> <target-arch> <output-dir>" >&2
  exit 1
fi

VERSION="$1"
TARGET_OS="$2"
TARGET_ARCH="$3"
OUTPUT_DIR="$4"
APP_NAME="crdt-agent-memory"
if [[ "$OUTPUT_DIR" = /* ]]; then
  OUTPUT_DIR_ABS="$OUTPUT_DIR"
else
  OUTPUT_DIR_ABS="$(pwd)/$OUTPUT_DIR"
fi
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

STAGE_NAME="${APP_NAME}_${VERSION}_${TARGET_OS}_${TARGET_ARCH}"
STAGE_DIR="$WORK_DIR/$STAGE_NAME"
mkdir -p "$STAGE_DIR/bin" "$OUTPUT_DIR_ABS"

BIN_EXT=""
if [ "$TARGET_OS" = "windows" ]; then
  BIN_EXT=".exe"
fi

build_cmd() {
  local cmd_name="$1"
  CGO_ENABLED=1 \
    GOOS="$TARGET_OS" \
    GOARCH="$TARGET_ARCH" \
    go build \
      -trimpath \
      -tags sqlite_fts5 \
      -ldflags="-s -w" \
      -o "$STAGE_DIR/bin/${cmd_name}${BIN_EXT}" \
      "./cmd/${cmd_name}"
}

for cmd in memoryd indexd syncd memory-mcp; do
  build_cmd "$cmd"
done

cp README.md "$STAGE_DIR/README.md"
mkdir -p "$STAGE_DIR/configs"
cp configs/*.yaml.example "$STAGE_DIR/configs/"

ARCHIVE_BASENAME="${STAGE_NAME}"
if [ "$TARGET_OS" = "windows" ]; then
  (
    cd "$WORK_DIR"
    zip -rq "$OUTPUT_DIR_ABS/${ARCHIVE_BASENAME}.zip" "$STAGE_NAME"
  )
else
  tar -C "$WORK_DIR" -czf "$OUTPUT_DIR_ABS/${ARCHIVE_BASENAME}.tar.gz" "$STAGE_NAME"
fi
