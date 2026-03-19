package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"crdt-agent-memory/internal/signing"
)

func SeedForPeer(peerID string) []byte {
	sum := sha256.Sum256([]byte("crdt-agent-memory/" + peerID))
	return sum[:]
}

func SignerForPeer(t *testing.T, peerID string) *signing.Ed25519Signer {
	t.Helper()
	signer, err := signing.NewSignerFromSeed(SeedForPeer(peerID))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func PublicKeyHexForPeer(peerID string) string {
	signer, err := signing.NewSignerFromSeed(SeedForPeer(peerID))
	if err != nil {
		panic(err)
	}
	return signer.PublicKeyHex()
}

func WriteSeedFile(t *testing.T, dir string, peerID string) string {
	t.Helper()
	path := filepath.Join(dir, peerID+".seed")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(SeedForPeer(peerID))), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
