#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSET_DIR="$ROOT_DIR/internal/extensions/assets"
CRSQLITE_VERSION="${CRSQLITE_VERSION:-v0.16.3}"
SQLITE_VEC_VERSION="${SQLITE_VEC_VERSION:-v0.1.6}"
SQLITE_VEC_VERSION_NUMBER="${SQLITE_VEC_VERSION#v}"
REQUESTED_PLATFORMS=("$@")

download() {
  local url="$1"
  local output="$2"
  curl -fsSL "$url" -o "$output"
}

vendor_platform() {
  local platform_dir="$1"
  local crsqlite_url="$2"
  local sqlite_vec_url="$3"
  local crsqlite_name="$4"
  local sqlite_vec_name="$5"

  if [ "${#REQUESTED_PLATFORMS[@]}" -gt 0 ]; then
    local wanted=false
    local platform
    for platform in "${REQUESTED_PLATFORMS[@]}"; do
      if [ "$platform" = "$platform_dir" ]; then
        wanted=true
        break
      fi
    done
    if [ "$wanted" != true ]; then
      return
    fi
  fi

  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  mkdir -p "$ASSET_DIR/$platform_dir"

  download "$crsqlite_url" "$tmp/crsqlite.archive"
  case "$crsqlite_url" in
    *.zip) unzip -q "$tmp/crsqlite.archive" -d "$tmp/crsqlite" ;;
    *) echo "unsupported archive format: $crsqlite_url" >&2; exit 1 ;;
  esac
  cp "$tmp/crsqlite/$crsqlite_name" "$ASSET_DIR/$platform_dir/$crsqlite_name"

  download "$sqlite_vec_url" "$tmp/sqlite-vec.archive"
  case "$sqlite_vec_url" in
    *.tar.gz) tar -xzf "$tmp/sqlite-vec.archive" -C "$tmp" ;;
    *) echo "unsupported archive format: $sqlite_vec_url" >&2; exit 1 ;;
  esac
  cp "$tmp/$sqlite_vec_name" "$ASSET_DIR/$platform_dir/$sqlite_vec_name"
}

vendor_platform "darwin-arm64" \
  "https://github.com/vlcn-io/cr-sqlite/releases/download/${CRSQLITE_VERSION}/crsqlite-darwin-aarch64.zip" \
  "https://github.com/asg017/sqlite-vec/releases/download/${SQLITE_VEC_VERSION}/sqlite-vec-${SQLITE_VEC_VERSION_NUMBER}-loadable-macos-aarch64.tar.gz" \
  "crsqlite.dylib" \
  "vec0.dylib"

vendor_platform "darwin-amd64" \
  "https://github.com/vlcn-io/cr-sqlite/releases/download/${CRSQLITE_VERSION}/crsqlite-darwin-x86_64.zip" \
  "https://github.com/asg017/sqlite-vec/releases/download/${SQLITE_VEC_VERSION}/sqlite-vec-${SQLITE_VEC_VERSION_NUMBER}-loadable-macos-x86_64.tar.gz" \
  "crsqlite.dylib" \
  "vec0.dylib"

vendor_platform "linux-amd64" \
  "https://github.com/vlcn-io/cr-sqlite/releases/download/${CRSQLITE_VERSION}/crsqlite-linux-x86_64.zip" \
  "https://github.com/asg017/sqlite-vec/releases/download/${SQLITE_VEC_VERSION}/sqlite-vec-${SQLITE_VEC_VERSION_NUMBER}-loadable-linux-x86_64.tar.gz" \
  "crsqlite.so" \
  "vec0.so"

vendor_platform "linux-arm64" \
  "https://github.com/vlcn-io/cr-sqlite/releases/download/${CRSQLITE_VERSION}/crsqlite-linux-aarch64.zip" \
  "https://github.com/asg017/sqlite-vec/releases/download/${SQLITE_VEC_VERSION}/sqlite-vec-${SQLITE_VEC_VERSION_NUMBER}-loadable-linux-aarch64.tar.gz" \
  "crsqlite.so" \
  "vec0.so"

vendor_platform "windows-amd64" \
  "https://github.com/vlcn-io/cr-sqlite/releases/download/${CRSQLITE_VERSION}/crsqlite-win-x86_64.zip" \
  "https://github.com/asg017/sqlite-vec/releases/download/${SQLITE_VEC_VERSION}/sqlite-vec-${SQLITE_VEC_VERSION_NUMBER}-loadable-windows-x86_64.tar.gz" \
  "crsqlite.dll" \
  "vec0.dll"
