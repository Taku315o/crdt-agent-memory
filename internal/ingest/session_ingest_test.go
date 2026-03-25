package ingest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

func newFixture(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "ingest.sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:          dbPath,
		CRSQLitePath:  testenv.CRSQLitePath(t),
		SQLiteVecPath: testenv.SQLiteVecPath(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db, NewService(db)
}

func TestIngestSessionCreatesChunksAndRetrievalUnits(t *testing.T) {
	ctx := context.Background()
	db, svc := newFixture(t)
	err := svc.IngestSession(ctx, SessionIngestRequest{
		SessionID:   "session-1",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		BranchName:  "main",
		ProjectKey:  "Taku315o/crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []SessionMessage{
			{Seq: 1, Role: "user", Content: "方針を決めたい", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "decision: transcript は同期しない", AuthoredAtMS: 110},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunkCount, unitCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_chunks WHERE session_id = 'session-1'`).Scan(&chunkCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM retrieval_units WHERE memory_space = 'transcript'`).Scan(&unitCount); err != nil {
		t.Fatal(err)
	}
	if chunkCount != 1 {
		t.Fatalf("chunk_count = %d, want 1", chunkCount)
	}
	if unitCount != 1 {
		t.Fatalf("unit_count = %d, want 1", unitCount)
	}
}

func TestIngestSessionMarksSecretChunks(t *testing.T) {
	ctx := context.Background()
	db, svc := newFixture(t)
	err := svc.IngestSession(ctx, SessionIngestRequest{
		SessionID:   "session-secret",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []SessionMessage{
			{Seq: 1, Role: "user", Content: "api_key is abc", AuthoredAtMS: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var sensitivity string
	if err := db.QueryRowContext(ctx, `SELECT sensitivity FROM transcript_chunks WHERE session_id = 'session-secret' LIMIT 1`).Scan(&sensitivity); err != nil {
		t.Fatal(err)
	}
	if sensitivity != "secret" {
		t.Fatalf("sensitivity = %q, want secret", sensitivity)
	}
}

func TestIngestSessionExtractsTranscriptArtifactSpans(t *testing.T) {
	ctx := context.Background()
	db, svc := newFixture(t)
	err := svc.IngestSession(ctx, SessionIngestRequest{
		SessionID:   "session-artifact",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []SessionMessage{
			{Seq: 1, Role: "user", Content: "見て /Users/test/project/main.go を確認して", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "https://example.com/spec も参照する", AuthoredAtMS: 110},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var spanCount, refCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_artifact_spans WHERE chunk_id = 'session-artifact:v2:1'`).Scan(&spanCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM private_artifact_refs WHERE local_namespace = 'crdt-agent-memory'`).Scan(&refCount); err != nil {
		t.Fatal(err)
	}
	if spanCount < 2 {
		t.Fatalf("span_count = %d, want at least 2", spanCount)
	}
	if refCount < 2 {
		t.Fatalf("ref_count = %d, want at least 2", refCount)
	}
}

func TestIngestSessionKeepsOlderChunkVersionsImmutable(t *testing.T) {
	ctx := context.Background()
	db, svc := newFixture(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO transcript_chunks(
			chunk_id, session_id, chunk_strategy_version, chunk_seq, chunk_kind, start_seq, end_seq,
			text, normalized_text, content_hash, authored_at_ms, source_uri, sensitivity, retention_class,
			is_indexable, metadata_json
		) VALUES('session-versioned:v1:1', 'session-versioned', 1, 1, 'decision', 1, 2, 'old decision text', 'old decision text', 'oldhash', 100, '', 'private', 'default', 1, '{}')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO retrieval_units(
			unit_id, source_type, source_id, memory_space, namespace, unit_kind, title, body, body_hash,
			authored_at_ms, sensitivity, retention_class, state, source_uri, project_key, branch_name, schema_version
		) VALUES('session-versioned:v1:1', 'transcript_chunk', 'session-versioned:v1:1', 'transcript', 'crdt-agent-memory', 'decision', '', 'old decision text', 'oldhash', 100, 'private', 'default', 'active', '', '', '', 1)
	`); err != nil {
		t.Fatal(err)
	}

	if err := svc.IngestSession(ctx, SessionIngestRequest{
		SessionID:   "session-versioned",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []SessionMessage{
			{Seq: 1, Role: "user", Content: "方針を決めたい", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "decision: new decision text", AuthoredAtMS: 110},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_chunks WHERE session_id = 'session-versioned'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("chunk_count = %d, want 2", count)
	}
}

func TestUpsertPromotionCandidateRefreshesOriginPeerIDOnConflict(t *testing.T) {
	ctx := context.Background()
	db, _ := newFixture(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	chunk := Chunk{
		ChunkID:      "session-candidate:v2:2",
		ChunkKind:    "decision",
		Text:         "decision: keep origin peer fresh",
		AuthoredAtMS: 123,
		Sensitivity:  "private",
	}
	candidateID := "cand_" + digest(chunk.ChunkID)
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_candidates(
			candidate_id, namespace, candidate_type, status, subject, body, source_uri,
			authored_at_ms, created_at_ms, updated_at_ms, author_agent_id, origin_peer_id,
			sensitivity, retention_class, project_key, branch_name, metadata_json
		) VALUES(?, 'crdt-agent-memory', 'decision', 'pending', 'old subject', 'old body', '',
			?, ?, ?, 'agent/old', 'peer/old', 'private', 'default', '', '', '{}')
	`, candidateID, now, now, now); err != nil {
		t.Fatal(err)
	}

	if err := upsertPromotionCandidate(ctx, tx, SessionIngestRequest{
		Namespace:      "crdt-agent-memory",
		RetentionClass: "default",
	}, chunk); err != nil {
		t.Fatal(err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var originPeerID, authorAgentID, subject, body string
	if err := db.QueryRowContext(ctx, `
		SELECT origin_peer_id, author_agent_id, subject, body
		FROM memory_candidates
		WHERE candidate_id = ?
	`, candidateID).Scan(&originPeerID, &authorAgentID, &subject, &body); err != nil {
		t.Fatal(err)
	}
	if originPeerID != "peer/local" {
		t.Fatalf("origin_peer_id = %q, want peer/local", originPeerID)
	}
	if authorAgentID != "agent/ingest" {
		t.Fatalf("author_agent_id = %q, want agent/ingest", authorAgentID)
	}
	if subject != "keep origin peer fresh" {
		t.Fatalf("subject = %q, want updated subject", subject)
	}
	if body != chunk.Text {
		t.Fatalf("body = %q, want updated body", body)
	}
}
