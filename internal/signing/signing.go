package signing

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const PayloadVersion = 1

type ClaimPayload struct {
	MemoryID       string
	MemoryType     string
	Namespace      string
	Subject        string
	Body           string
	SourceURI      string
	SourceHash     string
	AuthorAgentID  string
	OriginPeerID   string
	AuthoredAtMS   int64
	ValidFromMS    int64
	ValidToMS      int64
	PayloadVersion int
}

type Signer interface {
	SignClaim(claim ClaimPayload) ([]byte, error)
	PublicKeyHex() string
}

type Ed25519Signer struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

func NewSignerFromSeed(seed []byte) (*Ed25519Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("ed25519 seed must be %d bytes", ed25519.SeedSize)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return &Ed25519Signer{
		privateKey: privateKey,
		publicKey:  publicKey,
	}, nil
}

func LoadSigner(path string) (*Ed25519Signer, error) {
	// #nosec G304 -- signer path is operator-controlled application config.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seedHex := strings.TrimSpace(string(raw))
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		return nil, fmt.Errorf("decode signing key seed: %w", err)
	}
	return NewSignerFromSeed(seed)
}

func ParsePublicKeyHex(publicKeyHex string) (ed25519.PublicKey, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(publicKeyHex))
	if err != nil {
		return nil, fmt.Errorf("decode signing public key: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}

func (s *Ed25519Signer) SignClaim(claim ClaimPayload) ([]byte, error) {
	if s == nil {
		return nil, errors.New("signer is required")
	}
	canonical, err := CanonicalBytes(claim)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(s.privateKey, canonical), nil
}

func (s *Ed25519Signer) PublicKeyHex() string {
	if s == nil {
		return ""
	}
	return hex.EncodeToString(s.publicKey)
}

func CanonicalBytes(claim ClaimPayload) ([]byte, error) {
	if claim.PayloadVersion == 0 {
		claim.PayloadVersion = PayloadVersion
	}
	type canonicalClaim struct {
		MemoryID       string `json:"memory_id"`
		MemoryType     string `json:"memory_type"`
		Namespace      string `json:"namespace"`
		Subject        string `json:"subject"`
		Body           string `json:"body"`
		SourceURI      string `json:"source_uri"`
		SourceHash     string `json:"source_hash"`
		AuthorAgentID  string `json:"author_agent_id"`
		OriginPeerID   string `json:"origin_peer_id"`
		AuthoredAtMS   int64  `json:"authored_at_ms"`
		ValidFromMS    int64  `json:"valid_from_ms"`
		ValidToMS      int64  `json:"valid_to_ms"`
		PayloadVersion int    `json:"payload_version"`
	}
	return json.Marshal(canonicalClaim{
		MemoryID:       claim.MemoryID,
		MemoryType:     claim.MemoryType,
		Namespace:      claim.Namespace,
		Subject:        claim.Subject,
		Body:           claim.Body,
		SourceURI:      claim.SourceURI,
		SourceHash:     claim.SourceHash,
		AuthorAgentID:  claim.AuthorAgentID,
		OriginPeerID:   claim.OriginPeerID,
		AuthoredAtMS:   claim.AuthoredAtMS,
		ValidFromMS:    claim.ValidFromMS,
		ValidToMS:      claim.ValidToMS,
		PayloadVersion: claim.PayloadVersion,
	})
}

func VerifyClaim(claim ClaimPayload, signature []byte, publicKeyHex string) error {
	publicKey, err := ParsePublicKeyHex(publicKeyHex)
	if err != nil {
		return err
	}
	canonical, err := CanonicalBytes(claim)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, canonical, signature) {
		return errors.New("signature verification failed")
	}
	return nil
}
