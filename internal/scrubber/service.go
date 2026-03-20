package scrubber

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

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

type RepairReport struct {
	DeletedQuarantineBatches  int `json:"deleted_quarantine_batches"`
	SuspendedEdges            int `json:"suspended_edges"`
	SuspendedSignals          int `json:"suspended_signals"`
	ResolvedEdgeSuspensions   int `json:"resolved_edge_suspensions"`
	ResolvedSignalSuspensions int `json:"resolved_signal_suspensions"`
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

func (s *Service) RunActiveRepair(ctx context.Context, quarantineTTL time.Duration) (RepairReport, error) {
	if quarantineTTL <= 0 {
		quarantineTTL = 7 * 24 * time.Hour
	}
	report := RepairReport{}
	now := time.Now().UnixMilli()
	cutoff := now - quarantineTTL.Milliseconds()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `DELETE FROM sync_quarantine WHERE created_at_ms < ?`, cutoff)
	if err != nil {
		return report, err
	}
	if deleted, err := result.RowsAffected(); err == nil {
		report.DeletedQuarantineBatches = int(deleted)
	}

	edgeMap, err := loadOrphanState(ctx, tx, `
		SELECT
			e.edge_id,
			COALESCE(e.from_memory_id, e.to_memory_id, ''),
			TRIM(
				CASE WHEN src.memory_id IS NULL THEN 'missing_from ' ELSE '' END ||
				CASE WHEN dst.memory_id IS NULL THEN 'missing_to' ELSE '' END
			)
		FROM memory_edges e
		LEFT JOIN memory_nodes src ON src.memory_id = e.from_memory_id
		LEFT JOIN memory_nodes dst ON dst.memory_id = e.to_memory_id
		WHERE src.memory_id IS NULL OR dst.memory_id IS NULL
	`)
	if err != nil {
		return report, err
	}
	if report.SuspendedEdges, err = upsertSuspensions(ctx, tx, "memory_edge", edgeMap, now); err != nil {
		return report, err
	}
	if report.ResolvedEdgeSuspensions, err = resolveClearedSuspensions(ctx, tx, "memory_edge", edgeMap, now); err != nil {
		return report, err
	}

	signalMap, err := loadOrphanState(ctx, tx, `
		SELECT
			s.signal_id,
			s.memory_id,
			'missing_memory'
		FROM memory_signals s
		LEFT JOIN memory_nodes n ON n.memory_id = s.memory_id
		WHERE n.memory_id IS NULL
	`)
	if err != nil {
		return report, err
	}
	if report.SuspendedSignals, err = upsertSuspensions(ctx, tx, "memory_signal", signalMap, now); err != nil {
		return report, err
	}
	if report.ResolvedSignalSuspensions, err = resolveClearedSuspensions(ctx, tx, "memory_signal", signalMap, now); err != nil {
		return report, err
	}

	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

type orphanState struct {
	MemoryID string
	Detail   string
}

func loadOrphanState(ctx context.Context, tx *sql.Tx, query string) (map[string]orphanState, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]orphanState{}
	for rows.Next() {
		var entityID, memoryID, detail string
		if err := rows.Scan(&entityID, &memoryID, &detail); err != nil {
			return nil, err
		}
		out[entityID] = orphanState{MemoryID: memoryID, Detail: strings.TrimSpace(detail)}
	}
	return out, rows.Err()
}

func upsertSuspensions(ctx context.Context, tx *sql.Tx, entityType string, states map[string]orphanState, now int64) (int, error) {
	count := 0
	for entityID, state := range states {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO local_graph_suspensions(
				entity_type, entity_id, memory_space, memory_id, reason, detail, first_seen_at_ms, last_seen_at_ms, resolved_at_ms
			) VALUES(?, ?, 'shared', ?, 'orphaned', ?, ?, ?, 0)
			ON CONFLICT(entity_type, entity_id) DO UPDATE SET
				memory_id = excluded.memory_id,
				reason = excluded.reason,
				detail = excluded.detail,
				last_seen_at_ms = excluded.last_seen_at_ms,
				resolved_at_ms = 0
		`, entityType, entityID, state.MemoryID, state.Detail, now, now); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func resolveClearedSuspensions(ctx context.Context, tx *sql.Tx, entityType string, active map[string]orphanState, now int64) (int, error) {
	query := `UPDATE local_graph_suspensions SET resolved_at_ms = ? WHERE entity_type = ? AND resolved_at_ms = 0`
	args := []any{now, entityType}
	if len(active) > 0 {
		placeholders := make([]string, 0, len(active))
		for entityID := range active {
			placeholders = append(placeholders, "?")
			args = append(args, entityID)
		}
		query += fmt.Sprintf(" AND entity_id NOT IN (%s)", strings.Join(placeholders, ","))
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(affected), nil
}
