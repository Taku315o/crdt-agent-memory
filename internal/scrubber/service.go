package scrubber

import (
	"context"
	"database/sql"
	"strings"

	"crdt-agent-memory/internal/signing"
)

const sampleLimit = 10

type Service struct {
	db               *sql.DB
	selfPeerID       string
	selfPublicKeyHex string
}

type PeerTrust struct {
	PeerID        string  `json:"peer_id"`
	DisplayName   string  `json:"display_name"`
	TrustState    string  `json:"trust_state"`
	TrustWeight   float64 `json:"trust_weight"`
	HasSigningKey bool    `json:"has_signing_key"`
}

type TrustSummary struct {
	Peers             []PeerTrust `json:"peers"`
	ValidSignatures   int         `json:"valid_signatures"`
	MissingSignatures int         `json:"missing_signatures"`
	InvalidSignatures int         `json:"invalid_signatures"`
	UnknownPeerRows   int         `json:"unknown_peer_rows"`
}

type ScrubberSummary struct {
	OrphanEdges         int      `json:"orphan_edges"`
	OrphanSignals       int      `json:"orphan_signals"`
	OrphanEdgeIDs       []string `json:"orphan_edge_ids"`
	OrphanSignalIDs     []string `json:"orphan_signal_ids"`
	MissingSignatureIDs []string `json:"missing_signature_ids"`
	InvalidSignatureIDs []string `json:"invalid_signature_ids"`
	UnknownPeerIDs      []string `json:"unknown_peer_ids"`
}

type Diagnostics struct {
	TrustSummary    TrustSummary    `json:"trust_summary"`
	ScrubberSummary ScrubberSummary `json:"scrubber_summary"`
}

func NewService(db *sql.DB, selfPeerID, selfPublicKeyHex string) *Service {
	return &Service{
		db:               db,
		selfPeerID:       selfPeerID,
		selfPublicKeyHex: selfPublicKeyHex,
	}
}

func (s *Service) Diagnose(ctx context.Context) (Diagnostics, error) {
	peers, peerKeys, err := s.loadPeerTrust(ctx)
	if err != nil {
		return Diagnostics{}, err
	}
	if strings.TrimSpace(s.selfPeerID) != "" && strings.TrimSpace(s.selfPublicKeyHex) != "" {
		peerKeys[s.selfPeerID] = s.selfPublicKeyHex
	}

	trust := TrustSummary{Peers: peers}
	var scrubber ScrubberSummary
	if err := s.collectSignatureSummary(ctx, peerKeys, &trust, &scrubber); err != nil {
		return Diagnostics{}, err
	}
	if scrubber.OrphanEdges, scrubber.OrphanEdgeIDs, err = countAndSample(ctx, s.db, `
		SELECT edge_id
		FROM memory_edges e
		LEFT JOIN memory_nodes src ON src.memory_id = e.from_memory_id
		LEFT JOIN memory_nodes dst ON dst.memory_id = e.to_memory_id
		WHERE src.memory_id IS NULL OR dst.memory_id IS NULL
		ORDER BY edge_id
	`, sampleLimit); err != nil {
		return Diagnostics{}, err
	}
	if scrubber.OrphanSignals, scrubber.OrphanSignalIDs, err = countAndSample(ctx, s.db, `
		SELECT s.signal_id
		FROM memory_signals s
		LEFT JOIN memory_nodes n ON n.memory_id = s.memory_id
		WHERE n.memory_id IS NULL
		ORDER BY s.signal_id
	`, sampleLimit); err != nil {
		return Diagnostics{}, err
	}

	return Diagnostics{
		TrustSummary:    trust,
		ScrubberSummary: scrubber,
	}, nil
}

func (s *Service) loadPeerTrust(ctx context.Context) ([]PeerTrust, map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, display_name, trust_state, trust_weight, signing_public_key
		FROM peer_policies
		ORDER BY peer_id
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	peers := []PeerTrust{}
	peerKeys := map[string]string{}
	for rows.Next() {
		var peer PeerTrust
		var signingPublicKey string
		if err := rows.Scan(&peer.PeerID, &peer.DisplayName, &peer.TrustState, &peer.TrustWeight, &signingPublicKey); err != nil {
			return nil, nil, err
		}
		peer.HasSigningKey = strings.TrimSpace(signingPublicKey) != ""
		if peer.HasSigningKey {
			peerKeys[peer.PeerID] = signingPublicKey
		}
		peers = append(peers, peer)
	}
	return peers, peerKeys, rows.Err()
}

func (s *Service) collectSignatureSummary(ctx context.Context, peerKeys map[string]string, trust *TrustSummary, scrubber *ScrubberSummary) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			memory_id,
			memory_type,
			namespace,
			subject,
			body,
			source_uri,
			source_hash,
			author_agent_id,
			origin_peer_id,
			authored_at_ms,
			valid_from_ms,
			valid_to_ms,
			author_signature
		FROM memory_nodes
		ORDER BY memory_id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var claim signing.ClaimPayload
		var signature []byte
		if err := rows.Scan(
			&claim.MemoryID,
			&claim.MemoryType,
			&claim.Namespace,
			&claim.Subject,
			&claim.Body,
			&claim.SourceURI,
			&claim.SourceHash,
			&claim.AuthorAgentID,
			&claim.OriginPeerID,
			&claim.AuthoredAtMS,
			&claim.ValidFromMS,
			&claim.ValidToMS,
			&signature,
		); err != nil {
			return err
		}
		claim.PayloadVersion = signing.PayloadVersion
		switch {
		case len(signature) == 0:
			trust.MissingSignatures++
			appendSample(&scrubber.MissingSignatureIDs, claim.MemoryID)
		default:
			publicKeyHex := peerKeys[claim.OriginPeerID]
			if strings.TrimSpace(publicKeyHex) == "" {
				trust.UnknownPeerRows++
				appendSample(&scrubber.UnknownPeerIDs, claim.MemoryID)
				continue
			}
			if err := signing.VerifyClaim(claim, signature, publicKeyHex); err != nil {
				trust.InvalidSignatures++
				appendSample(&scrubber.InvalidSignatureIDs, claim.MemoryID)
				continue
			}
			trust.ValidSignatures++
		}
	}
	return rows.Err()
}

func countAndSample(ctx context.Context, db *sql.DB, query string, limit int) (int, []string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	count := 0
	samples := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, nil, err
		}
		count++
		if len(samples) < limit {
			samples = append(samples, id)
		}
	}
	return count, samples, rows.Err()
}

func appendSample(dst *[]string, id string) {
	if len(*dst) >= sampleLimit {
		return
	}
	*dst = append(*dst, id)
}
