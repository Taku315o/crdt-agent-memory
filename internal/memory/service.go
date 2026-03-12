package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Store(ctx context.Context, req StoreRequest) (string, error) {
	if strings.TrimSpace(req.Body) == "" {
		return "", errors.New("body is required")
	}
	if req.Visibility != VisibilityShared && req.Visibility != VisibilityPrivate {
		return "", errors.New("visibility must be shared or private")
	}
	if strings.TrimSpace(req.Namespace) == "" {
		return "", errors.New("namespace is required")
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	switch req.Visibility {
	case VisibilityShared:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO memory_nodes(
				memory_id, memory_type, namespace, scope, subject, body, source_uri, source_hash,
				author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
				lifecycle_state, schema_version, author_signature
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 'active', 1, X'')
		`, req.MemoryID, req.MemoryType, req.Namespace, req.Scope, req.Subject, req.Body, req.SourceURI,
			req.SourceHash, req.AuthorAgentID, req.OriginPeerID, req.AuthoredAtMS)
	case VisibilityPrivate:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO private_memory_nodes(
				memory_id, local_namespace, memory_type, subject, body, source_uri, source_hash,
				author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
				lifecycle_state, schema_version, author_signature
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 'active', 1, X'')
		`, req.MemoryID, req.Namespace, req.MemoryType, req.Subject, req.Body, req.SourceURI,
			req.SourceHash, req.AuthorAgentID, req.OriginPeerID, req.AuthoredAtMS)
	}
	if err != nil {
		return "", err
	}

	if req.Visibility == VisibilityShared {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, 'store', 1.0, 'initial write', ?)
		`, uuid.NewString(), req.MemoryID, req.OriginPeerID, req.AuthorAgentID, req.AuthoredAtMS); err != nil {
			return "", err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_memory_signals(signal_id, memory_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, 'store', 1.0, 'initial write', ?)
		`, uuid.NewString(), req.MemoryID, req.AuthorAgentID, req.AuthoredAtMS); err != nil {
			return "", err
		}
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
		WHERE memory_fts MATCH ?
	`
	args := []any{req.Query}

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
	query += ` ORDER BY bm25(memory_fts), v.authored_at_ms DESC LIMIT ?`
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
	newID, err := s.Store(ctx, req)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE memory_nodes SET lifecycle_state = 'superseded' WHERE memory_id = ?`, oldMemoryID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES(?, ?, ?, 'supersedes', 1.0, ?, ?)
	`, uuid.NewString(), newID, oldMemoryID, req.OriginPeerID, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	return newID, tx.Commit()
}
