package memsync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"p2p_agent/internal/policy"
	"p2p_agent/internal/storage"
)

type Service struct {
	db       *sql.DB
	meta     storage.Metadata
	policies *policy.Repository
	selfPeer string
}

func NewService(db *sql.DB, meta storage.Metadata, policies *policy.Repository, selfPeer string) *Service {
	return &Service{
		db:       db,
		meta:     meta,
		policies: policies,
		selfPeer: selfPeer,
	}
}

func (s *Service) Handshake(ctx context.Context, req HandshakeRequest) (HandshakeResponse, error) {
	allowed, err := s.policies.IsAllowed(ctx, req.PeerID)
	if err != nil {
		return HandshakeResponse{}, err
	}
	if !allowed {
		return HandshakeResponse{}, errors.New("peer is not allowlisted")
	}
	if req.SchemaHash != s.meta.SchemaHash {
		_ = s.markSyncError(ctx, req.PeerID, firstNamespace(req.Namespaces), "schema hash mismatch")
		return HandshakeResponse{}, errors.New("schema hash mismatch")
	}
	if req.CRRManifestHash != s.meta.CRRManifestHash {
		_ = s.markSyncError(ctx, req.PeerID, firstNamespace(req.Namespaces), "crr manifest hash mismatch")
		return HandshakeResponse{}, errors.New("crr manifest hash mismatch")
	}
	if req.MinCompatibleProtocolVersion > s.meta.ProtocolVersion || s.meta.MinCompatibleProtocolVersion > req.ProtocolVersion {
		_ = s.markSyncError(ctx, req.PeerID, firstNamespace(req.Namespaces), "protocol mismatch")
		return HandshakeResponse{}, errors.New("protocol mismatch")
	}
	if len(req.Namespaces) == 0 {
		return HandshakeResponse{}, errors.New("at least one namespace is required")
	}
	return HandshakeResponse{
		PeerID:             s.selfPeer,
		SchemaHash:         s.meta.SchemaHash,
		CRRManifestHash:    s.meta.CRRManifestHash,
		Namespaces:         req.Namespaces,
		NegotiatedProtocol: s.meta.ProtocolVersion,
	}, nil
}

func (s *Service) ExtractBatch(ctx context.Context, peerID, namespace string, limit int) (Batch, error) {
	if limit <= 0 {
		limit = 1000
	}
	cursor := int64(0)
	err := s.db.QueryRowContext(ctx, `
		SELECT version FROM crsql_tracked_peers WHERE peer_id = ? AND namespace = ?
	`, peerID, namespace).Scan(&cursor)
	if err != nil && err != sql.ErrNoRows {
		return Batch{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT db_version, table_name, pk, op, row_json, memory_id, namespace, changed_at_ms
		FROM crsql_changes
		WHERE namespace = ? AND db_version > ?
		ORDER BY db_version, table_name, pk
		LIMIT ?
	`, namespace, cursor, limit)
	if err != nil {
		return Batch{}, err
	}
	defer rows.Close()

	var changes []Change
	var maxVersion int64
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.DBVersion, &c.TableName, &c.PK, &c.Op, &c.RowJSON, &c.MemoryID, &c.Namespace, &c.ChangedAtMS); err != nil {
			return Batch{}, err
		}
		if c.DBVersion > maxVersion {
			maxVersion = c.DBVersion
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return Batch{}, err
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].DBVersion != changes[j].DBVersion {
			return changes[i].DBVersion < changes[j].DBVersion
		}
		if changes[i].TableName != changes[j].TableName {
			return changes[i].TableName < changes[j].TableName
		}
		return changes[i].PK < changes[j].PK
	})

	return Batch{
		BatchID:         uuid.NewString(),
		FromPeerID:      s.selfPeer,
		Namespace:       namespace,
		SchemaHash:      s.meta.SchemaHash,
		CRRManifestHash: s.meta.CRRManifestHash,
		MaxVersion:      maxVersion,
		Changes:         changes,
	}, nil
}

func (s *Service) ApplyBatch(ctx context.Context, fromPeerID string, batch Batch) error {
	if batch.SchemaHash != s.meta.SchemaHash || batch.CRRManifestHash != s.meta.CRRManifestHash {
		return s.quarantineBatch(ctx, fromPeerID, batch, "incompatible batch metadata")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE capture_control SET suppress = 1 WHERE singleton = 1`); err != nil {
		return err
	}
	defer func() {
		_, _ = tx.ExecContext(context.Background(), `UPDATE capture_control SET suppress = 0 WHERE singleton = 1`)
	}()

	for _, change := range batch.Changes {
		if change.Namespace != batch.Namespace {
			return s.quarantineBatch(ctx, fromPeerID, batch, "mixed namespace batch")
		}
		switch change.TableName {
		case "memory_nodes":
			var row struct {
				MemoryID       string `json:"memory_id"`
				MemoryType     string `json:"memory_type"`
				Namespace      string `json:"namespace"`
				Scope          string `json:"scope"`
				Subject        string `json:"subject"`
				Body           string `json:"body"`
				SourceURI      string `json:"source_uri"`
				SourceHash     string `json:"source_hash"`
				AuthorAgentID  string `json:"author_agent_id"`
				OriginPeerID   string `json:"origin_peer_id"`
				AuthoredAtMS   int64  `json:"authored_at_ms"`
				ValidFromMS    int64  `json:"valid_from_ms"`
				ValidToMS      int64  `json:"valid_to_ms"`
				LifecycleState string `json:"lifecycle_state"`
				SchemaVersion  int64  `json:"schema_version"`
			}
			if err := json.Unmarshal([]byte(change.RowJSON), &row); err != nil {
				return s.quarantineBatch(ctx, fromPeerID, batch, "invalid memory_nodes row")
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO memory_nodes(
					memory_id, memory_type, namespace, scope, subject, body, source_uri, source_hash,
					author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
					lifecycle_state, schema_version, author_signature
				) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, X'')
				ON CONFLICT(memory_id) DO UPDATE SET
					memory_type = excluded.memory_type,
					namespace = excluded.namespace,
					scope = excluded.scope,
					subject = excluded.subject,
					body = excluded.body,
					source_uri = excluded.source_uri,
					source_hash = excluded.source_hash,
					author_agent_id = excluded.author_agent_id,
					origin_peer_id = excluded.origin_peer_id,
					authored_at_ms = excluded.authored_at_ms,
					valid_from_ms = excluded.valid_from_ms,
					valid_to_ms = excluded.valid_to_ms,
					lifecycle_state = excluded.lifecycle_state,
					schema_version = excluded.schema_version
			`, row.MemoryID, row.MemoryType, row.Namespace, row.Scope, row.Subject, row.Body, row.SourceURI,
				row.SourceHash, row.AuthorAgentID, row.OriginPeerID, row.AuthoredAtMS, row.ValidFromMS,
				row.ValidToMS, row.LifecycleState, row.SchemaVersion); err != nil {
				return err
			}
		case "memory_edges":
			var row struct {
				EdgeID       string  `json:"edge_id"`
				FromMemoryID string  `json:"from_memory_id"`
				ToMemoryID   string  `json:"to_memory_id"`
				RelationType string  `json:"relation_type"`
				Weight       float64 `json:"weight"`
				OriginPeerID string  `json:"origin_peer_id"`
				AuthoredAtMS int64   `json:"authored_at_ms"`
			}
			if err := json.Unmarshal([]byte(change.RowJSON), &row); err != nil {
				return s.quarantineBatch(ctx, fromPeerID, batch, "invalid memory_edges row")
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
				VALUES(?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(edge_id) DO UPDATE SET
					from_memory_id = excluded.from_memory_id,
					to_memory_id = excluded.to_memory_id,
					relation_type = excluded.relation_type,
					weight = excluded.weight,
					origin_peer_id = excluded.origin_peer_id,
					authored_at_ms = excluded.authored_at_ms
			`, row.EdgeID, row.FromMemoryID, row.ToMemoryID, row.RelationType, row.Weight, row.OriginPeerID, row.AuthoredAtMS); err != nil {
				return err
			}
		case "memory_signals":
			var row struct {
				SignalID     string  `json:"signal_id"`
				MemoryID     string  `json:"memory_id"`
				PeerID       string  `json:"peer_id"`
				AgentID      string  `json:"agent_id"`
				SignalType   string  `json:"signal_type"`
				Value        float64 `json:"value"`
				Reason       string  `json:"reason"`
				AuthoredAtMS int64   `json:"authored_at_ms"`
			}
			if err := json.Unmarshal([]byte(change.RowJSON), &row); err != nil {
				return s.quarantineBatch(ctx, fromPeerID, batch, "invalid memory_signals row")
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(signal_id) DO UPDATE SET
					memory_id = excluded.memory_id,
					peer_id = excluded.peer_id,
					agent_id = excluded.agent_id,
					signal_type = excluded.signal_type,
					value = excluded.value,
					reason = excluded.reason,
					authored_at_ms = excluded.authored_at_ms
			`, row.SignalID, row.MemoryID, row.PeerID, row.AgentID, row.SignalType, row.Value, row.Reason, row.AuthoredAtMS); err != nil {
				return err
			}
		case "artifact_refs":
			var row struct {
				ArtifactID   string `json:"artifact_id"`
				Namespace    string `json:"namespace"`
				URI          string `json:"uri"`
				ContentHash  string `json:"content_hash"`
				Title        string `json:"title"`
				MimeType     string `json:"mime_type"`
				OriginPeerID string `json:"origin_peer_id"`
				AuthoredAtMS int64  `json:"authored_at_ms"`
			}
			if err := json.Unmarshal([]byte(change.RowJSON), &row); err != nil {
				return s.quarantineBatch(ctx, fromPeerID, batch, "invalid artifact_refs row")
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO artifact_refs(artifact_id, namespace, uri, content_hash, title, mime_type, origin_peer_id, authored_at_ms)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(artifact_id) DO UPDATE SET
					namespace = excluded.namespace,
					uri = excluded.uri,
					content_hash = excluded.content_hash,
					title = excluded.title,
					mime_type = excluded.mime_type,
					origin_peer_id = excluded.origin_peer_id,
					authored_at_ms = excluded.authored_at_ms
			`, row.ArtifactID, row.Namespace, row.URI, row.ContentHash, row.Title, row.MimeType, row.OriginPeerID, row.AuthoredAtMS); err != nil {
				return err
			}
		default:
			return s.quarantineBatch(ctx, fromPeerID, batch, fmt.Sprintf("unsupported table %s", change.TableName))
		}
		if change.MemoryID != "" {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO index_queue(queue_id, memory_space, memory_id, enqueued_at_ms)
				VALUES(?, 'shared', ?, ?)
				ON CONFLICT(queue_id) DO NOTHING
			`, uuid.NewString(), change.MemoryID, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
	}

	if batch.MaxVersion > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO crsql_tracked_peers(peer_id, namespace, version, updated_at_ms)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(peer_id, namespace) DO UPDATE SET
				version = excluded.version,
				updated_at_ms = excluded.updated_at_ms
		`, fromPeerID, batch.Namespace, batch.MaxVersion, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE capture_control SET suppress = 0 WHERE singleton = 1`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO peer_sync_state(peer_id, namespace, last_seen_at_ms, last_transport, last_path_type, last_error, last_success_at_ms)
		VALUES(?, ?, ?, 'iroh', 'direct-or-relay', '', ?)
		ON CONFLICT(peer_id, namespace) DO UPDATE SET
			last_seen_at_ms = excluded.last_seen_at_ms,
			last_transport = excluded.last_transport,
			last_path_type = excluded.last_path_type,
			last_error = '',
			last_success_at_ms = excluded.last_success_at_ms
	`, fromPeerID, batch.Namespace, time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		return err
	}

	return tx.Commit()
}

func SyncPair(ctx context.Context, left, right *Service, namespace string, limit int) error {
	leftReq := HandshakeRequest{
		ProtocolVersion:              left.meta.ProtocolVersion,
		MinCompatibleProtocolVersion: left.meta.MinCompatibleProtocolVersion,
		PeerID:                       left.selfPeer,
		SchemaHash:                   left.meta.SchemaHash,
		CRRManifestHash:              left.meta.CRRManifestHash,
		Namespaces:                   []string{namespace},
	}
	rightReq := HandshakeRequest{
		ProtocolVersion:              right.meta.ProtocolVersion,
		MinCompatibleProtocolVersion: right.meta.MinCompatibleProtocolVersion,
		PeerID:                       right.selfPeer,
		SchemaHash:                   right.meta.SchemaHash,
		CRRManifestHash:              right.meta.CRRManifestHash,
		Namespaces:                   []string{namespace},
	}
	if _, err := right.Handshake(ctx, leftReq); err != nil {
		return err
	}
	if _, err := left.Handshake(ctx, rightReq); err != nil {
		return err
	}

	leftBatch, err := left.ExtractBatch(ctx, right.selfPeer, namespace, limit)
	if err != nil {
		return err
	}
	if err := right.ApplyBatch(ctx, left.selfPeer, leftBatch); err != nil {
		return err
	}
	rightBatch, err := right.ExtractBatch(ctx, left.selfPeer, namespace, limit)
	if err != nil {
		return err
	}
	return left.ApplyBatch(ctx, right.selfPeer, rightBatch)
}

func (s *Service) Diagnostics(ctx context.Context) (Diagnostics, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, namespace, version, updated_at_ms
		FROM crsql_tracked_peers
		ORDER BY peer_id, namespace
	`)
	if err != nil {
		return Diagnostics{}, err
	}
	defer rows.Close()

	var tracked []TrackedPeer
	for rows.Next() {
		var tp TrackedPeer
		if err := rows.Scan(&tp.PeerID, &tp.Namespace, &tp.Version, &tp.UpdatedAtMS); err != nil {
			return Diagnostics{}, err
		}
		tracked = append(tracked, tp)
	}
	var quarantine int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_quarantine`).Scan(&quarantine); err != nil {
		return Diagnostics{}, err
	}
	return Diagnostics{
		SchemaHash:      s.meta.SchemaHash,
		CRRManifestHash: s.meta.CRRManifestHash,
		TrackedPeers:    tracked,
		QuarantineCount: quarantine,
	}, nil
}

func (s *Service) quarantineBatch(ctx context.Context, fromPeerID string, batch Batch, reason string) error {
	payload, _ := json.Marshal(batch)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_quarantine(batch_id, peer_id, namespace, reason, payload_json, created_at_ms)
		VALUES(?, ?, ?, ?, ?, ?)
	`, batch.BatchID, fromPeerID, batch.Namespace, reason, string(payload), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	_ = s.markSyncError(ctx, fromPeerID, batch.Namespace, reason)
	return errors.New(reason)
}

func (s *Service) markSyncError(ctx context.Context, peerID, namespace, msg string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO peer_sync_state(peer_id, namespace, last_seen_at_ms, last_transport, last_path_type, last_error, last_success_at_ms)
		VALUES(?, ?, ?, 'iroh', 'direct-or-relay', ?, 0)
		ON CONFLICT(peer_id, namespace) DO UPDATE SET
			last_seen_at_ms = excluded.last_seen_at_ms,
			last_transport = excluded.last_transport,
			last_path_type = excluded.last_path_type,
			last_error = excluded.last_error
	`, peerID, namespace, time.Now().UnixMilli(), msg)
	return err
}

func firstNamespace(namespaces []string) string {
	if len(namespaces) == 0 {
		return ""
	}
	return namespaces[0]
}
