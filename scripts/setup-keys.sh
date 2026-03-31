#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GO_BIN="${GO_BIN:-}"
if [[ -z "$GO_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  else
    GO_BIN="$ROOT_DIR/.tools/go/bin/go"
  fi
fi
GOFLAGS="${GOFLAGS:--tags sqlite_fts5}"
TMP_BASE="${1:?TMP_BASE required}"
PEER_A_CONFIG="$TMP_BASE/peer-a/config.yaml"
PEER_B_CONFIG="$TMP_BASE/peer-b/config.yaml"

update_signing_public_key() {
  local config_path="$1"
  local public_key="$2"
  local tmp_path
  tmp_path="$(mktemp "${config_path}.tmp.XXXXXX")"
  sed -E "s#signing_public_key: \".*\"#signing_public_key: \"$public_key\"#" "$config_path" >"$tmp_path"
  mv "$tmp_path" "$config_path"
}

PUBKEY_A=$("$GO_BIN" run $GOFLAGS ./cmd/memoryd --config "$PEER_A_CONFIG" --cmd keygen 2>/dev/null)
echo "Peer-A public key: $PUBKEY_A"
update_signing_public_key "$PEER_A_CONFIG" "$PUBKEY_A"

PUBKEY_B=$("$GO_BIN" run $GOFLAGS ./cmd/memoryd --config "$PEER_B_CONFIG" --cmd keygen 2>/dev/null)
echo "Peer-B public key: $PUBKEY_B"
update_signing_public_key "$PEER_B_CONFIG" "$PUBKEY_B"

echo "Keys generated and config updated"
