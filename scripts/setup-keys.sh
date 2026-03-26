#!/bin/bash
set -euo pipefail

GO_BIN="${GO_BIN:-/opt/homebrew/bin/go}"
GOFLAGS="${GOFLAGS:--tags sqlite_fts5}"
TMP_BASE="${1:?TMP_BASE required}"
PEER_A_CONFIG="$TMP_BASE/peer-a/config.yaml"
PEER_B_CONFIG="$TMP_BASE/peer-b/config.yaml"

# Generate public key for peer-a
PUBKEY_A=$(PATH=/opt/homebrew/bin:$PATH "$GO_BIN" run $GOFLAGS ./cmd/memoryd --config "$PEER_A_CONFIG" --cmd keygen 2>/dev/null)
echo "Peer-A public key: $PUBKEY_A"
sed -i '' -E "s#signing_public_key: \".*\"#signing_public_key: \"$PUBKEY_A\"#" "$PEER_A_CONFIG"

# Generate public key for peer-b
PUBKEY_B=$(PATH=/opt/homebrew/bin:$PATH "$GO_BIN" run $GOFLAGS ./cmd/memoryd --config "$PEER_B_CONFIG" --cmd keygen 2>/dev/null)
echo "Peer-B public key: $PUBKEY_B"
sed -i '' -E "s#signing_public_key: \".*\"#signing_public_key: \"$PUBKEY_B\"#" "$PEER_B_CONFIG"

echo "Keys generated and config updated"
