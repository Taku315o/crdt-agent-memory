package memory

import (
	"context"
	"path/filepath"
	"testing"

	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

func TestStoreRoutesSharedAndPrivateSeparately(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:         dbPath,
		CRSQLitePath: testenv.CRSQLitePath(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	svc := NewService(db, testenv.SignerForPeer(t, "peer-a"))
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_change_log WHERE table_name = 'memory_nodes'`).Scan(&sharedChanges); err != nil {
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
	var verificationStatus string
	if err := db.QueryRowContext(ctx, `
		SELECT signature_status FROM memory_verification_state WHERE memory_space = 'shared' LIMIT 1
	`).Scan(&verificationStatus); err != nil {
		t.Fatal(err)
	}
	if verificationStatus != string(SignatureStatusValid) {
		t.Fatalf("signature_status = %q, want valid", verificationStatus)
	}
}

func TestRecallUnionView(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "recall.sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:         dbPath,
		CRSQLitePath: testenv.CRSQLitePath(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	svc := NewService(db, testenv.SignerForPeer(t, "peer-a"))
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

func TestRecallRanksValidBeforeMissingAndByTrustWeight(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "ranking.sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:         dbPath,
		CRSQLitePath: testenv.CRSQLitePath(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	svc := NewService(db, testenv.SignerForPeer(t, "peer-local"))
	lowID, err := svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "ranked memory",
		Subject:       "low",
		OriginPeerID:  "peer-low",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	highID, err := svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "ranked memory",
		Subject:       "high",
		OriginPeerID:  "peer-high",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	missingID, err := svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "ranked memory",
		Subject:       "missing",
		OriginPeerID:  "peer-missing",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidID, err := svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "ranked memory",
		Subject:       "invalid",
		OriginPeerID:  "peer-invalid",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		peerID string
		weight float64
	}{
		{peerID: "peer-low", weight: 0.2},
		{peerID: "peer-high", weight: 0.9},
		{peerID: "peer-missing", weight: 1.0},
		{peerID: "peer-invalid", weight: 1.0},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO peer_policies(peer_id, display_name, trust_state, trust_weight, signing_public_key, updated_at_ms)
			VALUES(?, ?, 'allow', ?, '', 1)
		`, stmt.peerID, stmt.peerID, stmt.weight); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE memory_nodes SET author_signature = X'' WHERE memory_id = ?
	`, missingID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO memory_verification_state(memory_space, memory_id, signature_status, detail, checked_at_ms)
		VALUES
			('shared', ?, 'missing_signature', '', 1),
			('shared', ?, 'invalid_signature', '', 1)
		ON CONFLICT(memory_space, memory_id) DO UPDATE SET
			signature_status = excluded.signature_status,
			detail = excluded.detail,
			checked_at_ms = excluded.checked_at_ms
	`, missingID, invalidID); err != nil {
		t.Fatal(err)
	}

	results, err := svc.Recall(ctx, RecallRequest{
		Query:      "ranked",
		Namespaces: []string{"team/dev"},
		Limit:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].MemoryID != highID {
		t.Fatalf("first memory_id = %q, want %q", results[0].MemoryID, highID)
	}
	if results[1].MemoryID != lowID {
		t.Fatalf("second memory_id = %q, want %q", results[1].MemoryID, lowID)
	}
	if results[2].MemoryID != missingID {
		t.Fatalf("third memory_id = %q, want %q", results[2].MemoryID, missingID)
	}
}
