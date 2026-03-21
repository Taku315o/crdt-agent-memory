package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

type indexFixture struct {
	db     *sql.DB
	worker *Worker
	memory *memory.Service
}

func newIndexFixture(t *testing.T) *indexFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.sqlite")
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
	return &indexFixture{
		db:     db,
		worker: NewWorker(db, time.Second),
		memory: memory.NewService(db, testenv.SignerForPeer(t, "peer-a")),
	}
}

func enqueueIndexJob(t *testing.T, db *sql.DB, memorySpace, memoryID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO index_queue(queue_id, memory_space, memory_id, enqueued_at_ms)
		VALUES(?, ?, ?, ?)
	`, uuid.NewString(), memorySpace, memoryID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func setMemoryBody(t *testing.T, db *sql.DB, table, memoryID, body string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE `+table+` SET body = ? WHERE memory_id = ?`, body, memoryID); err != nil {
		t.Fatal(err)
	}
}

func embeddingJSON(t *testing.T, db *sql.DB, memorySpace, memoryID string) string {
	t.Helper()
	ctx := context.Background()
	var raw string
	if err := db.QueryRowContext(ctx, `
		SELECT embedding_json FROM memory_embeddings WHERE memory_space = ? AND memory_id = ?
	`, memorySpace, memoryID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertEmbeddingMatches(t *testing.T, db *sql.DB, memorySpace, memoryID, body string) {
	t.Helper()
	raw := embeddingJSON(t, db, memorySpace, memoryID)
	expected, err := json.Marshal(embed(body))
	if err != nil {
		t.Fatal(err)
	}
	if raw != string(expected) {
		t.Fatalf("embedding_json = %s, want %s", raw, string(expected))
	}
}

func TestWorkerDiagnosticsTracksPendingAndProcessed(t *testing.T) {
	fixture := newIndexFixture(t)
	ctx := context.Background()

	if _, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared diagnostic body",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private diagnostic body",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	diag, err := fixture.worker.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diag.PendingCount != 2 {
		t.Fatalf("pending_count = %d, want 2", diag.PendingCount)
	}
	if diag.ProcessedCount != 0 {
		t.Fatalf("processed_count = %d, want 0", diag.ProcessedCount)
	}
	if diag.EmbeddingCount != 0 {
		t.Fatalf("embedding_count = %d, want 0", diag.EmbeddingCount)
	}
	if diag.OldestPendingEnqueuedAtMS == 0 {
		t.Fatal("expected oldest_pending_enqueued_at_ms")
	}

	if err := fixture.worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	diag, err = fixture.worker.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diag.PendingCount != 0 {
		t.Fatalf("pending_count = %d, want 0", diag.PendingCount)
	}
	if diag.ProcessedCount != 2 {
		t.Fatalf("processed_count = %d, want 2", diag.ProcessedCount)
	}
	if diag.EmbeddingCount != 2 {
		t.Fatalf("embedding_count = %d, want 2", diag.EmbeddingCount)
	}
}

func TestWorkerReindexesSharedAndPrivate(t *testing.T) {
	fixture := newIndexFixture(t)
	ctx := context.Background()

	sharedID, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared v1",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateID, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private v1",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := fixture.worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertEmbeddingMatches(t, fixture.db, "shared", sharedID, "shared v1")
	assertEmbeddingMatches(t, fixture.db, "private", privateID, "private v1")

	setMemoryBody(t, fixture.db, "memory_nodes", sharedID, "shared v2")
	setMemoryBody(t, fixture.db, "private_memory_nodes", privateID, "private v2")
	enqueueIndexJob(t, fixture.db, "shared", sharedID)
	enqueueIndexJob(t, fixture.db, "private", privateID)

	if err := fixture.worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertEmbeddingMatches(t, fixture.db, "shared", sharedID, "shared v2")
	assertEmbeddingMatches(t, fixture.db, "private", privateID, "private v2")
}

func TestWorkerContinuesAfterItemError(t *testing.T) {
	fixture := newIndexFixture(t)
	ctx := context.Background()

	sharedID, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared retry-safe body",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateID, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private retry-safe body",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.db.ExecContext(ctx, `DROP TABLE private_memory_nodes`); err != nil {
		t.Fatal(err)
	}

	err = fixture.worker.ProcessOnce(ctx)
	if err == nil {
		t.Fatal("expected process_once to report the failed private item")
	}

	assertEmbeddingMatches(t, fixture.db, "shared", sharedID, "shared retry-safe body")

	var sharedQueueCount int
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM index_queue
		WHERE memory_space = 'shared' AND memory_id = ? AND processed_at_ms > 0
	`, sharedID).Scan(&sharedQueueCount); err != nil {
		t.Fatal(err)
	}
	if sharedQueueCount != 1 {
		t.Fatalf("shared processed count = %d, want 1", sharedQueueCount)
	}

	var privateQueueCount int
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM index_queue
		WHERE memory_space = 'private' AND memory_id = ? AND processed_at_ms = 0
	`, privateID).Scan(&privateQueueCount); err != nil {
		t.Fatal(err)
	}
	if privateQueueCount != 1 {
		t.Fatalf("private pending count = %d, want 1", privateQueueCount)
	}
}

func TestWorkerCleansMissingRows(t *testing.T) {
	fixture := newIndexFixture(t)
	ctx := context.Background()

	sharedID, err := fixture.memory.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared cleanup body",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `DELETE FROM memory_nodes WHERE memory_id = ?`, sharedID); err != nil {
		t.Fatal(err)
	}
	enqueueIndexJob(t, fixture.db, "shared", sharedID)

	if err := fixture.worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM memory_embeddings WHERE memory_space = 'shared' AND memory_id = ?
	`, sharedID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("embedding count = %d, want 0", count)
	}
}
