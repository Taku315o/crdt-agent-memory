package signing

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func testSeed(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

func testClaim() ClaimPayload {
	return ClaimPayload{
		MemoryID:       "mem-1",
		MemoryType:     "fact",
		Namespace:      "team/dev",
		Subject:        "subject",
		Body:           "body",
		SourceURI:      "https://example.com",
		SourceHash:     "hash",
		AuthorAgentID:  "agent-a",
		OriginPeerID:   "peer-a",
		AuthoredAtMS:   111,
		ValidFromMS:    0,
		ValidToMS:      0,
		PayloadVersion: PayloadVersion,
	}
}

func TestCanonicalBytesDeterministic(t *testing.T) {
	left, err := CanonicalBytes(testClaim())
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalBytes(testClaim())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("canonical bytes are not deterministic")
	}
}

func TestSignAndVerifyAcrossPeers(t *testing.T) {
	signer, err := NewSignerFromSeed(testSeed("peer-a"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := signer.SignClaim(testClaim())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClaim(testClaim(), signature, signer.PublicKeyHex()); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleOutsideSignaturePayload(t *testing.T) {
	signer, err := NewSignerFromSeed(testSeed("peer-a"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := signer.SignClaim(testClaim())
	if err != nil {
		t.Fatal(err)
	}

	updated := testClaim()
	if err := VerifyClaim(updated, signature, signer.PublicKeyHex()); err != nil {
		t.Fatal(err)
	}
}
