package ingest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

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
