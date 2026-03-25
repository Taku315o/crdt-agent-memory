package memory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrCandidateNotFound    = errors.New("candidate not found")
	ErrCandidateNotPending  = errors.New("candidate is not pending")
	ErrCandidateNeedsChunks = errors.New("candidate has no transcript chunks")
)

type candidateRecord struct {
	Candidate
}

func (s *Service) ListCandidates(ctx context.Context, req ListCandidatesRequest) ([]Candidate, error) {
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Status = strings.TrimSpace(req.Status)
	req.ProjectKey = strings.TrimSpace(req.ProjectKey)
	req.BranchName = strings.TrimSpace(req.BranchName)
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	query := strings.Builder{}
	query.WriteString(`
		SELECT
			candidate_id, namespace, candidate_type, status, subject, body, source_uri,
			author_agent_id, origin_peer_id, sensitivity, retention_class, project_key, branch_name,
			authored_at_ms, created_at_ms, updated_at_ms, approved_memory_id, reviewed_at_ms, review_note
		FROM memory_candidates
		WHERE 1 = 1
	`)
	args := []any{}
	if req.Namespace != "" {
		query.WriteString(" AND namespace = ?")
		args = append(args, req.Namespace)
	}
	if req.Status != "" {
		query.WriteString(" AND status = ?")
		args = append(args, req.Status)
	}
	if req.ProjectKey != "" {
		query.WriteString(" AND project_key = ?")
		args = append(args, req.ProjectKey)
	}
	if req.BranchName != "" {
		query.WriteString(" AND branch_name = ?")
		args = append(args, req.BranchName)
	}
	query.WriteString(" ORDER BY authored_at_ms DESC, created_at_ms DESC LIMIT ?")
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Candidate, 0)
	for rows.Next() {
		var item Candidate
		if err := rows.Scan(
			&item.CandidateID,
			&item.Namespace,
			&item.CandidateType,
			&item.Status,
			&item.Subject,
			&item.Body,
			&item.SourceURI,
			&item.AuthorAgentID,
			&item.OriginPeerID,
			&item.Sensitivity,
			&item.RetentionClass,
			&item.ProjectKey,
			&item.BranchName,
			&item.AuthoredAtMS,
			&item.CreatedAtMS,
			&item.UpdatedAtMS,
			&item.ApprovedMemoryID,
			&item.ReviewedAtMS,
			&item.ReviewNote,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) ApproveCandidate(ctx context.Context, req ApproveCandidateRequest) (string, error) {
	req.CandidateID = strings.TrimSpace(req.CandidateID)
	if req.CandidateID == "" {
		return "", errors.New("candidate_id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	candidate, err := loadCandidateForUpdate(ctx, tx, req.CandidateID)
	if err != nil {
		return "", err
	}
	chunkIDs, err := loadCandidateChunkIDs(ctx, tx, req.CandidateID)
	if err != nil {
		return "", err
	}
	if len(chunkIDs) == 0 {
		return "", ErrCandidateNeedsChunks
	}

	promoteReq := PromoteRequest{
		ChunkIDs:      chunkIDs,
		MemoryType:    firstNonEmpty(strings.TrimSpace(req.MemoryType), candidate.CandidateType),
		Subject:       firstNonEmpty(strings.TrimSpace(req.Subject), candidate.Subject),
		Namespace:     firstNonEmpty(strings.TrimSpace(req.Namespace), candidate.Namespace),
		AuthorAgentID: firstNonEmpty(strings.TrimSpace(req.AuthorAgentID), candidate.AuthorAgentID),
		OriginPeerID:  firstNonEmpty(strings.TrimSpace(req.OriginPeerID), candidate.OriginPeerID),
		AuthoredAtMS:  req.AuthoredAtMS,
		SourceURI:     firstNonEmpty(strings.TrimSpace(req.SourceURI), candidate.SourceURI),
	}
	if promoteReq.AuthoredAtMS == 0 {
		promoteReq.AuthoredAtMS = candidate.AuthoredAtMS
	}

	memoryID, err := s.promoteTx(ctx, tx, promoteReq)
	if err != nil {
		return "", err
	}
	if err := markCandidateApprovedTx(ctx, tx, req.CandidateID, memoryID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return memoryID, nil
}

func markCandidateApprovedTx(ctx context.Context, tx *sql.Tx, candidateID, memoryID string) error {
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `
		UPDATE memory_candidates
		SET status = 'approved',
			approved_memory_id = ?,
			reviewed_at_ms = ?,
			updated_at_ms = ?,
			review_note = ''
		WHERE candidate_id = ?
		  AND status = 'pending'
	`, memoryID, now, now, candidateID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrCandidateNotPending
	}
	return nil
}

func (s *Service) RejectCandidate(ctx context.Context, req RejectCandidateRequest) error {
	req.CandidateID = strings.TrimSpace(req.CandidateID)
	if req.CandidateID == "" {
		return errors.New("candidate_id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := loadCandidateForUpdate(ctx, tx, req.CandidateID); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `
		UPDATE memory_candidates
		SET status = 'rejected',
			reviewed_at_ms = ?,
			updated_at_ms = ?,
			review_note = ?
		WHERE candidate_id = ?
		  AND status = 'pending'
	`, now, now, strings.TrimSpace(req.ReviewNote), req.CandidateID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrCandidateNotPending
	}
	return tx.Commit()
}

func loadCandidateForUpdate(ctx context.Context, tx *sql.Tx, candidateID string) (candidateRecord, error) {
	var item candidateRecord
	err := tx.QueryRowContext(ctx, `
		SELECT
			candidate_id, namespace, candidate_type, status, subject, body, source_uri,
			author_agent_id, origin_peer_id, sensitivity, retention_class, project_key, branch_name,
			authored_at_ms, created_at_ms, updated_at_ms, approved_memory_id, reviewed_at_ms, review_note
		FROM memory_candidates
		WHERE candidate_id = ?
	`, candidateID).Scan(
		&item.CandidateID,
		&item.Namespace,
		&item.CandidateType,
		&item.Status,
		&item.Subject,
		&item.Body,
		&item.SourceURI,
		&item.AuthorAgentID,
		&item.OriginPeerID,
		&item.Sensitivity,
		&item.RetentionClass,
		&item.ProjectKey,
		&item.BranchName,
		&item.AuthoredAtMS,
		&item.CreatedAtMS,
		&item.UpdatedAtMS,
		&item.ApprovedMemoryID,
		&item.ReviewedAtMS,
		&item.ReviewNote,
	)
	if err == sql.ErrNoRows {
		return candidateRecord{}, ErrCandidateNotFound
	}
	if err != nil {
		return candidateRecord{}, err
	}
	if item.Status != "pending" {
		return candidateRecord{}, ErrCandidateNotPending
	}
	return item, nil
}

func loadCandidateChunkIDs(ctx context.Context, tx *sql.Tx, candidateID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT chunk_id
		FROM memory_candidate_chunks
		WHERE candidate_id = ?
		ORDER BY ordinal, chunk_id
	`, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var chunkID string
		if err := rows.Scan(&chunkID); err != nil {
			return nil, err
		}
		out = append(out, chunkID)
	}
	return out, rows.Err()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func upsertMemoryCandidateTx(ctx context.Context, tx *sql.Tx, req upsertCandidateRequest) error {
	now := time.Now().UnixMilli()
	if req.CreatedAtMS == 0 {
		req.CreatedAtMS = now
	}
	if req.UpdatedAtMS == 0 {
		req.UpdatedAtMS = now
	}
	if req.CandidateID == "" {
		return errors.New("candidate_id is required")
	}
	if req.Namespace == "" {
		return errors.New("namespace is required")
	}
	if req.CandidateType == "" {
		return errors.New("candidate_type is required")
	}
	if len(req.ChunkIDs) == 0 {
		return errors.New("chunk_ids is required")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_candidates(
			candidate_id, namespace, candidate_type, status, subject, body, source_uri,
			authored_at_ms, created_at_ms, updated_at_ms, author_agent_id, origin_peer_id,
			sensitivity, retention_class, project_key, branch_name, metadata_json
		) VALUES(?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}')
		ON CONFLICT(candidate_id) DO UPDATE SET
			namespace = excluded.namespace,
			candidate_type = excluded.candidate_type,
			subject = excluded.subject,
			body = excluded.body,
			source_uri = excluded.source_uri,
			authored_at_ms = excluded.authored_at_ms,
			updated_at_ms = excluded.updated_at_ms,
			author_agent_id = excluded.author_agent_id,
			origin_peer_id = excluded.origin_peer_id,
			sensitivity = excluded.sensitivity,
			retention_class = excluded.retention_class,
			project_key = excluded.project_key,
			branch_name = excluded.branch_name
		WHERE memory_candidates.status = 'pending'
	`, req.CandidateID, req.Namespace, req.CandidateType, req.Subject, req.Body, req.SourceURI,
		req.AuthoredAtMS, req.CreatedAtMS, req.UpdatedAtMS, req.AuthorAgentID, req.OriginPeerID,
		req.Sensitivity, req.RetentionClass, req.ProjectKey, req.BranchName); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_candidate_chunks WHERE candidate_id = ?`, req.CandidateID); err != nil {
		return err
	}
	for i, chunkID := range req.ChunkIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_candidate_chunks(link_id, candidate_id, chunk_id, ordinal)
			VALUES(?, ?, ?, ?)
		`, uuid.NewString(), req.CandidateID, chunkID, i); err != nil {
			return err
		}
	}
	return nil
}

type upsertCandidateRequest struct {
	CandidateID    string
	ChunkIDs       []string
	Namespace      string
	CandidateType  string
	Subject        string
	Body           string
	SourceURI      string
	AuthorAgentID  string
	OriginPeerID   string
	Sensitivity    string
	RetentionClass string
	ProjectKey     string
	BranchName     string
	AuthoredAtMS   int64
	CreatedAtMS    int64
	UpdatedAtMS    int64
}
