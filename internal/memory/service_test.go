package memory

import (
	"context"
	"path/filepath"
	"testing"

	"crdt-agent-memory/internal/storage"
)

func TestStoreRoutesSharedAndPrivateSeparately(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.sqlite")
	db, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	svc := NewService(db)
	if _, err := svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared fact for sync",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private note",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	var sharedCount, privateCount, sharedChanges int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_nodes`).Scan(&sharedCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM private_memory_nodes`).Scan(&privateCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crsql_changes WHERE table_name = 'memory_nodes'`).Scan(&sharedChanges); err != nil {
		t.Fatal(err)
	}

	if sharedCount != 1 {
		t.Fatalf("shared count = %d, want 1", sharedCount)
	}
	if privateCount != 1 {
		t.Fatalf("private count = %d, want 1", privateCount)
	}
	if sharedChanges == 0 {
		t.Fatal("expected shared change capture")
	}
}

func TestRecallUnionView(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "recall.sqlite")
	db, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	svc := NewService(db)
	_, _ = svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "release checklist sync issue",
		Subject:       "release",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	_, _ = svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private sync note",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})

	results, err := svc.Recall(ctx, RecallRequest{
		Query:          "sync",
		IncludePrivate: true,
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want at least 2", len(results))
	}
}
