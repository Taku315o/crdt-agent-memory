package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/signing"
)

type Service struct {
	db     *sql.DB
	signer signing.Signer
}

func NewService(db *sql.DB, signer signing.Signer) *Service {
	return &Service{db: db, signer: signer}
}

func (s *Service) Store(ctx context.Context, req StoreRequest) (string, error) {
	req, signature, err := s.prepareStore(req)
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if err := s.storeTx(ctx, tx, req, signature); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return req.MemoryID, nil
}

func (s *Service) Recall(ctx context.Context, req RecallRequest) ([]RecallResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	query := `
		SELECT
			v.memory_space,
			v.memory_id,
			v.namespace,
			v.memory_type,
			v.subject,
			v.body,
			v.lifecycle_state,
			v.authored_at_ms,
			v.source_uri,
			v.source_hash,
			v.origin_peer_id
		FROM recall_memory_view v
		JOIN memory_fts f
		  ON f.memory_space = v.memory_space
		 AND f.memory_id = v.memory_id
		LEFT JOIN memory_verification_state vs
		  ON vs.memory_space = v.memory_space
		 AND vs.memory_id = v.memory_id
		LEFT JOIN memory_nodes mn
		  ON v.memory_space = 'shared'
		 AND mn.memory_id = v.memory_id
		LEFT JOIN peer_policies pp
		  ON v.memory_space = 'shared'
		 AND pp.peer_id = v.origin_peer_id
		WHERE memory_fts MATCH ?
		  AND COALESCE(vs.signature_status, '') != ?
	`
	args := []any{req.Query, SignatureStatusInvalidSignature}

	if !req.IncludePrivate {
		query += ` AND v.memory_space = 'shared'`
	}
	if len(req.Namespaces) > 0 {
		placeholders := make([]string, 0, len(req.Namespaces))
		for _, ns := range req.Namespaces {
			placeholders = append(placeholders, "?")
			args = append(args, ns)
		}
		query += fmt.Sprintf(" AND v.namespace IN (%s)", strings.Join(placeholders, ","))
	}
	query += `
		ORDER BY
			` + rankingBucketCase("v.memory_space", "vs.signature_status", "mn.author_signature") + `,
			COALESCE(pp.trust_weight, 1.0) DESC,
			bm25(memory_fts),
			v.authored_at_ms DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RecallResult
	for rows.Next() {
		var item RecallResult
		if err := rows.Scan(
			&item.MemorySpace,
			&item.MemoryID,
			&item.Namespace,
			&item.MemoryType,
			&item.Subject,
			&item.Body,
			&item.LifecycleState,
			&item.AuthoredAtMS,
			&item.SourceURI,
			&item.SourceHash,
			&item.OriginPeerID,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) Supersede(ctx context.Context, oldMemoryID string, req StoreRequest) (string, error) {
	req.Visibility = VisibilityShared
	req, signature, err := s.prepareStore(req)
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	exists, err := memoryExistsTx(ctx, tx, string(VisibilityShared), oldMemoryID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrMemoryNotFound
	}
	if err := s.storeTx(ctx, tx, req, signature); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE memory_nodes
		SET lifecycle_state = 'superseded'
		WHERE memory_id = ?
	`, oldMemoryID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES(?, ?, ?, 'supersedes', 1.0, ?, ?)
	`, uuid.NewString(), req.MemoryID, oldMemoryID, req.OriginPeerID, req.AuthoredAtMS); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return req.MemoryID, nil
}

func (s *Service) Signal(ctx context.Context, req SignalRequest) (string, error) {
	req, err := normalizeSignalRequest(req)
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	exists, err := memoryExistsTx(ctx, tx, req.MemorySpace, req.MemoryID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrMemoryNotFound
	}

	signalID := uuid.NewString()
	switch req.MemorySpace {
	case string(VisibilityShared):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		`, signalID, req.MemoryID, req.OriginPeerID, req.AuthorAgentID, req.SignalType, req.Value, req.Reason, req.AuthoredAtMS); err != nil {
			return "", err
		}
	case string(VisibilityPrivate):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_memory_signals(signal_id, memory_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, ?, ?, ?)
		`, signalID, req.MemoryID, req.AuthorAgentID, req.SignalType, req.Value, req.Reason, req.AuthoredAtMS); err != nil {
			return "", err
		}
	default:
		return "", errors.New("memory_space must be shared or private")
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return signalID, nil
}

func (s *Service) Explain(ctx context.Context, req ExplainRequest) (ExplainResult, error) {
	req.MemorySpace = strings.TrimSpace(req.MemorySpace)
	req.MemoryID = strings.TrimSpace(req.MemoryID)
	req.Query = strings.TrimSpace(req.Query)
	if req.MemorySpace != string(VisibilityShared) && req.MemorySpace != string(VisibilityPrivate) {
		return ExplainResult{}, errors.New("memory_space must be shared or private")
	}
	if req.MemoryID == "" {
		return ExplainResult{}, errors.New("memory_id is required")
	}
	if req.Query == "" {
		return ExplainResult{}, errors.New("query is required")
	}

	record, err := s.loadExplainRecord(ctx, req)
	if err != nil {
		return ExplainResult{}, err
	}

	lexicalBM25, matchedQuery, err := s.lookupLexicalBM25(ctx, req)
	if err != nil {
		return ExplainResult{}, err
	}
	signalSummary, err := s.loadSignalSummary(ctx, req.MemorySpace, req.MemoryID)
	if err != nil {
		return ExplainResult{}, err
	}

	return ExplainResult{
		Provenance: ExplainProvenance{
			Namespace:      record.Namespace,
			MemoryType:     record.MemoryType,
			Subject:        record.Subject,
			LifecycleState: record.LifecycleState,
			SourceURI:      record.SourceURI,
			SourceHash:     record.SourceHash,
			AuthorAgentID:  record.AuthorAgentID,
			OriginPeerID:   record.OriginPeerID,
			AuthoredAtMS:   record.AuthoredAtMS,
		},
		ScoreBreakdown: ExplainScoreBreakdown{
			MatchedQuery:   matchedQuery,
			RecallEligible: record.SignatureStatus != string(SignatureStatusInvalidSignature),
			LexicalBM25:    lexicalBM25,
			RankingBucket:  rankingBucket(record.MemorySpace, record.SignatureStatus, record.HasSignature),
			TrustWeight:    record.PeerTrustWeight,
			AuthoredAtMS:   record.AuthoredAtMS,
		},
		TrustSummary: ExplainTrustSummary{
			SignatureStatus: record.SignatureStatus,
			SignatureDetail: record.SignatureDetail,
			PeerTrustState:  record.PeerTrustState,
			PeerTrustWeight: record.PeerTrustWeight,
			HasSigningKey:   record.HasSigningKey,
		},
		SignalSummary: signalSummary,
	}, nil
}

func (s *Service) prepareStore(req StoreRequest) (StoreRequest, []byte, error) {
	req, err := normalizeStoreRequest(req)
	if err != nil {
		return StoreRequest{}, nil, err
	}
	signature, err := s.sign(req)
	if err != nil {
		return StoreRequest{}, nil, err
	}
	return req, signature, nil
}

func normalizeStoreRequest(req StoreRequest) (StoreRequest, error) {
	if strings.TrimSpace(req.Body) == "" {
		return StoreRequest{}, errors.New("body is required")
	}
	if req.Visibility != VisibilityShared && req.Visibility != VisibilityPrivate {
		return StoreRequest{}, errors.New("visibility must be shared or private")
	}
	if strings.TrimSpace(req.Namespace) == "" {
		return StoreRequest{}, errors.New("namespace is required")
	}
	if req.MemoryID == "" {
		req.MemoryID = uuid.NewString()
	}
	if req.AuthoredAtMS == 0 {
		req.AuthoredAtMS = time.Now().UnixMilli()
	}
	if req.Scope == "" {
		req.Scope = "team"
	}
	if req.MemoryType == "" {
		req.MemoryType = "fact"
	}
	if req.AuthorAgentID == "" {
		req.AuthorAgentID = "agent/default"
	}
	if req.OriginPeerID == "" {
		req.OriginPeerID = "peer/local"
	}
	return req, nil
}

func normalizeSignalRequest(req SignalRequest) (SignalRequest, error) {
	req.MemorySpace = strings.TrimSpace(req.MemorySpace)
	req.MemoryID = strings.TrimSpace(req.MemoryID)
	req.SignalType = strings.TrimSpace(req.SignalType)
	if req.MemorySpace != string(VisibilityShared) && req.MemorySpace != string(VisibilityPrivate) {
		return SignalRequest{}, errors.New("memory_space must be shared or private")
	}
	if req.MemoryID == "" {
		return SignalRequest{}, errors.New("memory_id is required")
	}
	if !isAllowedSignalType(req.SignalType) {
		return SignalRequest{}, errors.New("signal_type must be reinforce, deprecate, confirm, deny, pin, or bookmark")
	}
	if req.Value <= 0 {
		return SignalRequest{}, errors.New("value must be greater than 0")
	}
	if req.AuthoredAtMS == 0 {
		req.AuthoredAtMS = time.Now().UnixMilli()
	}
	if req.AuthorAgentID == "" {
		req.AuthorAgentID = "agent/default"
	}
	if req.MemorySpace == string(VisibilityShared) && req.OriginPeerID == "" {
		req.OriginPeerID = "peer/local"
	}
	return req, nil
}

func isAllowedSignalType(signalType string) bool {
	switch SignalType(signalType) {
	case SignalTypeReinforce, SignalTypeDeprecate, SignalTypeConfirm, SignalTypeDeny, SignalTypePin, SignalTypeBookmark:
		return true
	default:
		return false
	}
}

func (s *Service) storeTx(ctx context.Context, tx *sql.Tx, req StoreRequest, signature []byte) error {
	switch req.Visibility {
	case VisibilityShared:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_nodes(
				memory_id, memory_type, namespace, scope, subject, body, source_uri, source_hash,
				author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
				lifecycle_state, schema_version, author_signature
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 'active', 1, ?)
		`, req.MemoryID, req.MemoryType, req.Namespace, req.Scope, req.Subject, req.Body, req.SourceURI,
			req.SourceHash, req.AuthorAgentID, req.OriginPeerID, req.AuthoredAtMS, signature); err != nil {
			return err
		}
	case VisibilityPrivate:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_memory_nodes(
				memory_id, local_namespace, memory_type, subject, body, source_uri, source_hash,
				author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
				lifecycle_state, schema_version, author_signature
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 'active', 1, ?)
		`, req.MemoryID, req.Namespace, req.MemoryType, req.Subject, req.Body, req.SourceURI,
			req.SourceHash, req.AuthorAgentID, req.OriginPeerID, req.AuthoredAtMS, signature); err != nil {
			return err
		}
	default:
		return errors.New("visibility must be shared or private")
	}

	if err := upsertVerificationState(ctx, tx, string(req.Visibility), req.MemoryID, SignatureStatusValid, ""); err != nil {
		return err
	}
	if err := insertInitialSignal(ctx, tx, req); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO index_queue(queue_id, memory_space, memory_id, enqueued_at_ms)
		VALUES(?, ?, ?, ?)
	`, uuid.NewString(), req.Visibility, req.MemoryID, time.Now().UnixMilli()); err != nil {
		return err
	}
	return nil
}

func insertInitialSignal(ctx context.Context, tx *sql.Tx, req StoreRequest) error {
	if req.Visibility == VisibilityShared {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, 'store', 1.0, 'initial write', ?)
		`, uuid.NewString(), req.MemoryID, req.OriginPeerID, req.AuthorAgentID, req.AuthoredAtMS)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO private_memory_signals(signal_id, memory_id, agent_id, signal_type, value, reason, authored_at_ms)
		VALUES(?, ?, ?, 'store', 1.0, 'initial write', ?)
	`, uuid.NewString(), req.MemoryID, req.AuthorAgentID, req.AuthoredAtMS)
	return err
}

type explainRecord struct {
	MemorySpace     string
	Namespace       string
	MemoryType      string
	Subject         string
	LifecycleState  string
	SourceURI       string
	SourceHash      string
	AuthorAgentID   string
	OriginPeerID    string
	AuthoredAtMS    int64
	HasSignature    bool
	SignatureStatus string
	SignatureDetail string
	PeerTrustState  string
	PeerTrustWeight float64
	HasSigningKey   bool
}

func (s *Service) loadExplainRecord(ctx context.Context, req ExplainRequest) (explainRecord, error) {
	var row *sql.Row
	switch req.MemorySpace {
	case string(VisibilityShared):
		row = s.db.QueryRowContext(ctx, `
			SELECT
				mn.namespace,
				mn.memory_type,
				mn.subject,
				mn.lifecycle_state,
				mn.source_uri,
				mn.source_hash,
				mn.author_agent_id,
				mn.origin_peer_id,
				mn.authored_at_ms,
				CASE WHEN length(COALESCE(mn.author_signature, X'')) > 0 THEN 1 ELSE 0 END,
				COALESCE(vs.signature_status, ''),
				COALESCE(vs.detail, ''),
				COALESCE(pp.trust_state, ''),
				COALESCE(pp.trust_weight, 1.0),
				CASE WHEN length(COALESCE(pp.signing_public_key, '')) > 0 THEN 1 ELSE 0 END
			FROM memory_nodes mn
			LEFT JOIN memory_verification_state vs
			  ON vs.memory_space = 'shared'
			 AND vs.memory_id = mn.memory_id
			LEFT JOIN peer_policies pp
			  ON pp.peer_id = mn.origin_peer_id
			WHERE mn.memory_id = ?
		`, req.MemoryID)
	case string(VisibilityPrivate):
		row = s.db.QueryRowContext(ctx, `
			SELECT
				pmn.local_namespace,
				pmn.memory_type,
				pmn.subject,
				pmn.lifecycle_state,
				pmn.source_uri,
				pmn.source_hash,
				pmn.author_agent_id,
				pmn.origin_peer_id,
				pmn.authored_at_ms,
				CASE WHEN length(COALESCE(pmn.author_signature, X'')) > 0 THEN 1 ELSE 0 END,
				COALESCE(vs.signature_status, ''),
				COALESCE(vs.detail, ''),
				COALESCE(pp.trust_state, ''),
				COALESCE(pp.trust_weight, 1.0),
				CASE WHEN length(COALESCE(pp.signing_public_key, '')) > 0 THEN 1 ELSE 0 END
			FROM private_memory_nodes pmn
			LEFT JOIN memory_verification_state vs
			  ON vs.memory_space = 'private'
			 AND vs.memory_id = pmn.memory_id
			LEFT JOIN peer_policies pp
			  ON pp.peer_id = pmn.origin_peer_id
			WHERE pmn.memory_id = ?
		`, req.MemoryID)
	default:
		return explainRecord{}, errors.New("memory_space must be shared or private")
	}

	record := explainRecord{
		MemorySpace:     req.MemorySpace,
		PeerTrustWeight: 1.0,
	}
	var hasSignature int
	var hasSigningKey int
	err := row.Scan(
		&record.Namespace,
		&record.MemoryType,
		&record.Subject,
		&record.LifecycleState,
		&record.SourceURI,
		&record.SourceHash,
		&record.AuthorAgentID,
		&record.OriginPeerID,
		&record.AuthoredAtMS,
		&hasSignature,
		&record.SignatureStatus,
		&record.SignatureDetail,
		&record.PeerTrustState,
		&record.PeerTrustWeight,
		&hasSigningKey,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return explainRecord{}, ErrMemoryNotFound
		}
		return explainRecord{}, err
	}
	record.HasSignature = hasSignature == 1
	record.HasSigningKey = hasSigningKey == 1
	return record, nil
}

func (s *Service) lookupLexicalBM25(ctx context.Context, req ExplainRequest) (float64, bool, error) {
	var lexicalBM25 float64
	err := s.db.QueryRowContext(ctx, `
		SELECT bm25(memory_fts)
		FROM memory_fts
		WHERE memory_fts MATCH ?
		  AND memory_space = ?
		  AND memory_id = ?
		LIMIT 1
	`, req.Query, req.MemorySpace, req.MemoryID).Scan(&lexicalBM25)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return lexicalBM25, true, nil
}

func (s *Service) loadSignalSummary(ctx context.Context, memorySpace, memoryID string) (map[string]ExplainSignalSummary, error) {
	tableName, err := signalTableName(memorySpace)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT signal_type, COUNT(*), COALESCE(SUM(value), 0), COALESCE(MAX(authored_at_ms), 0)
		FROM %s
		WHERE memory_id = ?
		GROUP BY signal_type
		ORDER BY signal_type
	`, tableName), memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]ExplainSignalSummary)
	for rows.Next() {
		var signalType string
		var item ExplainSignalSummary
		if err := rows.Scan(&signalType, &item.Count, &item.Sum, &item.LatestSignalAtMS); err != nil {
			return nil, err
		}
		out[signalType] = item
	}
	return out, rows.Err()
}

func signalTableName(memorySpace string) (string, error) {
	switch memorySpace {
	case string(VisibilityShared):
		return "memory_signals", nil
	case string(VisibilityPrivate):
		return "private_memory_signals", nil
	default:
		return "", errors.New("memory_space must be shared or private")
	}
}

func memoryExistsTx(ctx context.Context, tx *sql.Tx, memorySpace, memoryID string) (bool, error) {
	tableName := ""
	switch memorySpace {
	case string(VisibilityShared):
		tableName = "memory_nodes"
	case string(VisibilityPrivate):
		tableName = "private_memory_nodes"
	default:
		return false, errors.New("memory_space must be shared or private")
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT EXISTS(SELECT 1 FROM %s WHERE memory_id = ?)
	`, tableName), memoryID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func rankingBucket(memorySpace, signatureStatus string, hasSignature bool) int {
	if memorySpace == string(VisibilityPrivate) {
		return 0
	}
	switch signatureStatus {
	case string(SignatureStatusValid):
		return 0
	case string(SignatureStatusMissingSignature):
		return 1
	case string(SignatureStatusUnknownPeer):
		return 2
	default:
		if !hasSignature {
			return 1
		}
		return 1
	}
}

func rankingBucketCase(memorySpaceExpr, signatureStatusExpr, signatureExpr string) string {
	return fmt.Sprintf(`CASE
			WHEN %s = 'private' THEN 0
			WHEN COALESCE(%s, '') = 'valid' THEN 0
			WHEN COALESCE(%s, '') = 'missing_signature'
				OR length(COALESCE(%s, X'')) = 0 THEN 1
			WHEN COALESCE(%s, '') = 'unknown_peer' THEN 2
			ELSE 1
		END`, memorySpaceExpr, signatureStatusExpr, signatureStatusExpr, signatureExpr, signatureStatusExpr)
}

func (s *Service) sign(req StoreRequest) ([]byte, error) {
	if s.signer == nil {
		return nil, errors.New("signer is required")
	}
	return s.signer.SignClaim(signing.ClaimPayload{
		MemoryID:       req.MemoryID,
		MemoryType:     req.MemoryType,
		Namespace:      req.Namespace,
		Subject:        req.Subject,
		Body:           req.Body,
		SourceURI:      req.SourceURI,
		SourceHash:     req.SourceHash,
		AuthorAgentID:  req.AuthorAgentID,
		OriginPeerID:   req.OriginPeerID,
		AuthoredAtMS:   req.AuthoredAtMS,
		ValidFromMS:    0,
		ValidToMS:      0,
		PayloadVersion: signing.PayloadVersion,
	})
}

func upsertVerificationState(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, memorySpace, memoryID string, status SignatureStatus, detail string) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO memory_verification_state(memory_space, memory_id, signature_status, detail, checked_at_ms)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(memory_space, memory_id) DO UPDATE SET
			signature_status = excluded.signature_status,
			detail = excluded.detail,
			checked_at_ms = excluded.checked_at_ms
	`, memorySpace, memoryID, status, detail, time.Now().UnixMilli())
	return err
}
