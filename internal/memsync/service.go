package memsync

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/signing"
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
	// #nosec G201 -- placeholders are generated internally for a variable-length IN clause.
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
	verificationResults, err := s.evaluateBatchVerification(ctx, batch)
	if err != nil {
		return s.quarantineBatch(ctx, fromPeerID, batch, err.Error())
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
	for _, verification := range verificationResults {
		if err := upsertVerificationStateTX(ctx, tx, "shared", verification.MemoryID, verification.Status, verification.Detail); err != nil {
			return err
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

type verificationResult struct {
	MemoryID string
	Status   memory.SignatureStatus
	Detail   string
}

type sharedMemoryCandidate struct {
	Claim     signing.ClaimPayload
	Signature []byte
}

func (s *Service) evaluateBatchVerification(ctx context.Context, batch Batch) ([]verificationResult, error) {
	candidates := map[string]*sharedMemoryCandidate{}
	order := []string{}
	for _, change := range batch.Changes {
		if change.Table != "memory_nodes" {
			continue
		}
		memoryID := decodePrimaryKeyText(change.PKB64)
		if strings.TrimSpace(memoryID) == "" {
			continue
		}
		candidate, ok := candidates[memoryID]
		if !ok {
			loaded, err := s.loadSharedMemoryCandidate(ctx, memoryID)
			if err != nil {
				return nil, err
			}
			loaded.Claim.MemoryID = memoryID
			loaded.Claim.PayloadVersion = signing.PayloadVersion
			candidate = &loaded
			candidates[memoryID] = candidate
			order = append(order, memoryID)
		}
		if err := applyMemoryNodeChange(candidate, change); err != nil {
			return nil, err
		}
	}

	results := make([]verificationResult, 0, len(order))
	for _, memoryID := range order {
		candidate := candidates[memoryID]
		if len(candidate.Signature) == 0 {
			results = append(results, verificationResult{
				MemoryID: memoryID,
				Status:   memory.SignatureStatusMissingSignature,
			})
			continue
		}
		publicKeyHex, err := s.lookupSigningPublicKey(ctx, candidate.Claim.OriginPeerID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(publicKeyHex) == "" {
			return nil, fmt.Errorf("unknown peer signing key for %s", candidate.Claim.MemoryID)
		}
		if err := signing.VerifyClaim(candidate.Claim, candidate.Signature, publicKeyHex); err != nil {
			return nil, fmt.Errorf("invalid signature for %s: %w", candidate.Claim.MemoryID, err)
		}
		results = append(results, verificationResult{
			MemoryID: memoryID,
			Status:   memory.SignatureStatusValid,
		})
	}
	return results, nil
}

func decodePrimaryKeyText(encoded string) string {
	pk, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	decoded := string(pk)
	for idx, r := range decoded {
		if ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') {
			return decoded[idx:]
		}
	}
	return decoded
}

func (s *Service) loadSharedMemoryCandidate(ctx context.Context, memoryID string) (sharedMemoryCandidate, error) {
	var candidate sharedMemoryCandidate
	err := s.db.QueryRowContext(ctx, `
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
		WHERE memory_id = ?
	`, memoryID).Scan(
		&candidate.Claim.MemoryID,
		&candidate.Claim.MemoryType,
		&candidate.Claim.Namespace,
		&candidate.Claim.Subject,
		&candidate.Claim.Body,
		&candidate.Claim.SourceURI,
		&candidate.Claim.SourceHash,
		&candidate.Claim.AuthorAgentID,
		&candidate.Claim.OriginPeerID,
		&candidate.Claim.AuthoredAtMS,
		&candidate.Claim.ValidFromMS,
		&candidate.Claim.ValidToMS,
		&candidate.Signature,
	)
	if err == sql.ErrNoRows {
		candidate.Claim.PayloadVersion = signing.PayloadVersion
		return candidate, nil
	}
	if err != nil {
		return sharedMemoryCandidate{}, err
	}
	candidate.Claim.PayloadVersion = signing.PayloadVersion
	return candidate, nil
}

func applyMemoryNodeChange(candidate *sharedMemoryCandidate, change Change) error {
	switch change.CID {
	case "memory_id":
		candidate.Claim.MemoryID = valueAsString(change.Val)
	case "memory_type":
		candidate.Claim.MemoryType = valueAsString(change.Val)
	case "namespace":
		candidate.Claim.Namespace = valueAsString(change.Val)
	case "subject":
		candidate.Claim.Subject = valueAsString(change.Val)
	case "body":
		candidate.Claim.Body = valueAsString(change.Val)
	case "source_uri":
		candidate.Claim.SourceURI = valueAsString(change.Val)
	case "source_hash":
		candidate.Claim.SourceHash = valueAsString(change.Val)
	case "author_agent_id":
		candidate.Claim.AuthorAgentID = valueAsString(change.Val)
	case "origin_peer_id":
		candidate.Claim.OriginPeerID = valueAsString(change.Val)
	case "authored_at_ms":
		value, err := valueAsInt64(change.Val)
		if err != nil {
			return err
		}
		candidate.Claim.AuthoredAtMS = value
	case "valid_from_ms":
		value, err := valueAsInt64(change.Val)
		if err != nil {
			return err
		}
		candidate.Claim.ValidFromMS = value
	case "valid_to_ms":
		value, err := valueAsInt64(change.Val)
		if err != nil {
			return err
		}
		candidate.Claim.ValidToMS = value
	case "author_signature":
		value, err := valueAsBytes(change.Val)
		if err != nil {
			return err
		}
		candidate.Signature = value
	}
	return nil
}

func (s *Service) lookupSigningPublicKey(ctx context.Context, peerID string) (string, error) {
	var publicKeyHex string
	err := s.db.QueryRowContext(ctx, `
		SELECT signing_public_key
		FROM peer_policies
		WHERE peer_id = ?
	`, peerID).Scan(&publicKeyHex)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return publicKeyHex, err
}

func upsertVerificationStateTX(ctx context.Context, tx *sql.Tx, memorySpace, memoryID string, status memory.SignatureStatus, detail string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO memory_verification_state(memory_space, memory_id, signature_status, detail, checked_at_ms)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(memory_space, memory_id) DO UPDATE SET
			signature_status = excluded.signature_status,
			detail = excluded.detail,
			checked_at_ms = excluded.checked_at_ms
	`, memorySpace, memoryID, status, detail, time.Now().UnixMilli())
	return err
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
	if !utf8.Valid(raw) {
		blobB64 := base64.StdEncoding.EncodeToString(raw)
		return Value{BlobB64: &blobB64}
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

func valueAsString(v Value) string {
	switch {
	case v.Null:
		return ""
	case v.Text != nil:
		return *v.Text
	case v.Integer != nil:
		return strconv.FormatInt(*v.Integer, 10)
	case v.Float != nil:
		return strconv.FormatFloat(*v.Float, 'f', -1, 64)
	case v.BlobB64 != nil:
		decoded, err := base64.StdEncoding.DecodeString(*v.BlobB64)
		if err != nil {
			return ""
		}
		return string(decoded)
	default:
		return ""
	}
}

func valueAsInt64(v Value) (int64, error) {
	switch {
	case v.Null:
		return 0, nil
	case v.Integer != nil:
		return *v.Integer, nil
	case v.Text != nil:
		return strconv.ParseInt(*v.Text, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported integer value")
	}
}

func valueAsBytes(v Value) ([]byte, error) {
	switch {
	case v.Null:
		return nil, nil
	case v.BlobB64 != nil:
		return base64.StdEncoding.DecodeString(*v.BlobB64)
	case v.Text != nil:
		return []byte(*v.Text), nil
	default:
		return nil, fmt.Errorf("unsupported blob value")
	}
}
