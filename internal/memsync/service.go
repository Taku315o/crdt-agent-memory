package memsync

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
)

type Service struct {
	db        *sql.DB
	meta      storage.Metadata
	policies  *policy.Repository
	selfPeer  string
	transport string
}

func NewService(db *sql.DB, meta storage.Metadata, policies *policy.Repository, selfPeer string, transport string) *Service {
	if transport == "" {
		transport = TransportHTTPDev
	}
	return &Service{db: db, meta: meta, policies: policies, selfPeer: selfPeer, transport: transport}
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
		_ = s.markSyncError(ctx, req.PeerID, firstNamespace(req.Namespaces), "schema hash mismatch", true)
		return HandshakeResponse{}, errors.New("schema hash mismatch")
	}
	if req.CRRManifestHash != s.meta.CRRManifestHash {
		_ = s.markSyncError(ctx, req.PeerID, firstNamespace(req.Namespaces), "crr manifest hash mismatch", true)
		return HandshakeResponse{}, errors.New("crr manifest hash mismatch")
	}
	if req.MinCompatibleProtocolVersion > s.meta.ProtocolVersion || s.meta.MinCompatibleProtocolVersion > req.ProtocolVersion {
		_ = s.markSyncError(ctx, req.PeerID, firstNamespace(req.Namespaces), "protocol mismatch", false)
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
		limit = 256
	}
	cursor := int64(0)
	err := s.db.QueryRowContext(ctx, `
		SELECT version FROM sync_cursors WHERE peer_id = ? AND namespace = ?
	`, peerID, namespace).Scan(&cursor)
	if err != nil && err != sql.ErrNoRows {
		return Batch{}, err
	}

	logRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT db_version
		FROM sync_change_log
		WHERE namespace = ? AND db_version > ?
		ORDER BY db_version
		LIMIT ?
	`, namespace, cursor, limit)
	if err != nil {
		return Batch{}, err
	}
	defer logRows.Close()

	var versions []int64
	for logRows.Next() {
		var version int64
		if err := logRows.Scan(&version); err != nil {
			return Batch{}, err
		}
		versions = append(versions, version)
	}
	if err := logRows.Err(); err != nil {
		return Batch{}, err
	}
	if len(versions) == 0 {
		return Batch{
			BatchID:         uuid.NewString(),
			FromPeerID:      s.selfPeer,
			Namespace:       namespace,
			SchemaHash:      s.meta.SchemaHash,
			CRRManifestHash: s.meta.CRRManifestHash,
		}, nil
	}

	placeholders := make([]string, 0, len(versions))
	args := make([]any, 0, len(versions))
	maxVersion := versions[len(versions)-1]
	for _, version := range versions {
		placeholders = append(placeholders, "?")
		args = append(args, version)
	}
	query := fmt.Sprintf(`
		SELECT "table", pk, cid, val, col_version, db_version, site_id, cl, seq
		FROM crsql_changes
		WHERE db_version IN (%s)
		ORDER BY db_version, "table", seq
	`, strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Batch{}, err
	}
	defer rows.Close()

	var changes []Change
	for rows.Next() {
		var table, cid string
		var pk, siteID []byte
		var raw sql.RawBytes
		var colVersion, dbVersion, cl, seq int64
		if err := rows.Scan(&table, &pk, &cid, &raw, &colVersion, &dbVersion, &siteID, &cl, &seq); err != nil {
			return Batch{}, err
		}
		changes = append(changes, Change{
			Table:      table,
			PKB64:      base64.StdEncoding.EncodeToString(pk),
			CID:        cid,
			Val:        encodeValue(raw),
			ColVersion: colVersion,
			DBVersion:  dbVersion,
			SiteIDB64:  base64.StdEncoding.EncodeToString(siteID),
			CL:         cl,
			Seq:        seq,
		})
	}
	if err := rows.Err(); err != nil {
		return Batch{}, err
	}
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

	for _, change := range batch.Changes {
		pk, err := base64.StdEncoding.DecodeString(change.PKB64)
		if err != nil {
			return s.quarantineBatch(ctx, fromPeerID, batch, "invalid pk encoding")
		}
		siteID, err := base64.StdEncoding.DecodeString(change.SiteIDB64)
		if err != nil {
			return s.quarantineBatch(ctx, fromPeerID, batch, "invalid site_id encoding")
		}
		val, err := decodeValue(change.Val)
		if err != nil {
			return s.quarantineBatch(ctx, fromPeerID, batch, "invalid value encoding")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO crsql_changes("table", pk, cid, val, col_version, db_version, site_id, cl, seq)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, change.Table, pk, change.CID, val, change.ColVersion, change.DBVersion, siteID, change.CL, change.Seq); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "unique") && !strings.Contains(strings.ToLower(err.Error()), "constraint") {
				return err
			}
		}
	}

	if batch.MaxVersion > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sync_cursors(peer_id, namespace, version, updated_at_ms)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(peer_id, namespace) DO UPDATE SET
				version = excluded.version,
				updated_at_ms = excluded.updated_at_ms
		`, fromPeerID, batch.Namespace, batch.MaxVersion, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO peer_sync_state(peer_id, namespace, last_seen_at_ms, last_transport, last_path_type, last_error, last_success_at_ms, schema_fenced)
		VALUES(?, ?, ?, ?, 'direct', '', ?, 0)
		ON CONFLICT(peer_id, namespace) DO UPDATE SET
			last_seen_at_ms = excluded.last_seen_at_ms,
			last_transport = excluded.last_transport,
			last_path_type = excluded.last_path_type,
			last_error = '',
			last_success_at_ms = excluded.last_success_at_ms,
			schema_fenced = 0
	`, fromPeerID, batch.Namespace, time.Now().UnixMilli(), s.transport, time.Now().UnixMilli()); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return s.enqueueNamespace(ctx, batch.Namespace)
}

func (s *Service) enqueueNamespace(ctx context.Context, namespace string) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT memory_id
		FROM sync_change_log
		WHERE namespace = ? AND memory_id != ''
		ORDER BY id DESC
		LIMIT 32
	`, namespace)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var memoryID string
		if err := rows.Scan(&memoryID); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO index_queue(queue_id, memory_space, memory_id, enqueued_at_ms)
			VALUES(?, 'shared', ?, ?)
		`, uuid.NewString(), memoryID, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) Diagnostics(ctx context.Context) (Diagnostics, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, namespace, version, updated_at_ms
		FROM sync_cursors
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
	peerRows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, namespace, last_seen_at_ms, last_transport, last_path_type, last_error, last_success_at_ms, schema_fenced
		FROM peer_sync_state
		ORDER BY peer_id, namespace
	`)
	if err != nil {
		return Diagnostics{}, err
	}
	defer peerRows.Close()
	var peerStates []PeerState
	for peerRows.Next() {
		var state PeerState
		if err := peerRows.Scan(&state.PeerID, &state.Namespace, &state.LastSeenAtMS, &state.LastTransport, &state.LastPathType, &state.LastError, &state.LastSuccessAtMS, &state.SchemaFenced); err != nil {
			return Diagnostics{}, err
		}
		peerStates = append(peerStates, state)
	}
	var quarantine int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_quarantine`).Scan(&quarantine); err != nil {
		return Diagnostics{}, err
	}
	return Diagnostics{
		SchemaHash:      s.meta.SchemaHash,
		CRRManifestHash: s.meta.CRRManifestHash,
		TrackedPeers:    tracked,
		PeerStates:      peerStates,
		QuarantineCount: quarantine,
	}, nil
}

func (s *Service) SyncStatus(ctx context.Context, namespace string) (SyncStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, namespace, last_seen_at_ms, last_transport, last_path_type, last_error, last_success_at_ms, schema_fenced
		FROM peer_sync_state
		WHERE namespace = ?
		ORDER BY peer_id
	`, namespace)
	if err != nil {
		return SyncStatus{}, err
	}
	defer rows.Close()
	status := SyncStatus{Namespace: namespace, State: "healthy"}
	for rows.Next() {
		var state PeerState
		if err := rows.Scan(&state.PeerID, &state.Namespace, &state.LastSeenAtMS, &state.LastTransport, &state.LastPathType, &state.LastError, &state.LastSuccessAtMS, &state.SchemaFenced); err != nil {
			return SyncStatus{}, err
		}
		if state.SchemaFenced {
			status.SchemaFenced = true
			status.State = "schema_fenced"
		} else if state.LastError != "" && status.State == "healthy" {
			status.State = "degraded"
		}
		status.Peers = append(status.Peers, state)
	}
	return status, rows.Err()
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
	_ = s.markSyncError(ctx, fromPeerID, batch.Namespace, reason, strings.Contains(reason, "schema"))
	return errors.New(reason)
}

func (s *Service) markSyncError(ctx context.Context, peerID, namespace, msg string, schemaFenced bool) error {
	fenced := 0
	if schemaFenced {
		fenced = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO peer_sync_state(peer_id, namespace, last_seen_at_ms, last_transport, last_path_type, last_error, last_success_at_ms, schema_fenced)
		VALUES(?, ?, ?, ?, 'direct', ?, 0, ?)
		ON CONFLICT(peer_id, namespace) DO UPDATE SET
			last_seen_at_ms = excluded.last_seen_at_ms,
			last_transport = excluded.last_transport,
			last_path_type = excluded.last_path_type,
			last_error = excluded.last_error,
			schema_fenced = excluded.schema_fenced
	`, peerID, namespace, time.Now().UnixMilli(), s.transport, msg, fenced)
	return err
}

func firstNamespace(namespaces []string) string {
	if len(namespaces) == 0 {
		return ""
	}
	return namespaces[0]
}

func encodeValue(raw []byte) Value {
	if raw == nil {
		return Value{Null: true}
	}
	if len(raw) == 0 {
		text := ""
		return Value{Text: &text}
	}
	if json.Valid(raw) {
		text := string(raw)
		return Value{Text: &text}
	}
	text := string(raw)
	return Value{Text: &text}
}

func decodeValue(v Value) (any, error) {
	switch {
	case v.Null:
		return nil, nil
	case v.Integer != nil:
		return *v.Integer, nil
	case v.Float != nil:
		return *v.Float, nil
	case v.Text != nil:
		return *v.Text, nil
	case v.BlobB64 != nil:
		return base64.StdEncoding.DecodeString(*v.BlobB64)
	default:
		return nil, nil
	}
}
