package memsync

import (
	"context"
	"path/filepath"
	"testing"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

func setupSyncService(t *testing.T, name string) (*memory.Service, *Service) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), name+".sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:          dbPath,
		CRSQLitePath:  testenv.CRSQLitePath(t),
		SQLiteVecPath: testenv.SQLiteVecPath(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	meta, err := storage.RunMigrations(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	policies := policy.NewRepository(db)
	if err := policies.AllowPeer(ctx, "peer-a", "peer-a", testenv.PublicKeyHexForPeer("peer-a")); err != nil {
		t.Fatal(err)
	}
	if err := policies.AllowPeer(ctx, "peer-b", "peer-b", testenv.PublicKeyHexForPeer("peer-b")); err != nil {
		t.Fatal(err)
	}
	return memory.NewService(db, testenv.SignerForPeer(t, name)), NewService(db, meta, policies, name, TransportHTTPDev)
}

func TestHandshakeRejectsSchemaMismatch(t *testing.T) {
	ctx := context.Background()
	_, svc := setupSyncService(t, "peer-a")
	_, err := svc.Handshake(ctx, HandshakeRequest{
		ProtocolVersion:              "1",
		MinCompatibleProtocolVersion: "1",
		PeerID:                       "peer-b",
		SchemaHash:                   "bad",
		CRRManifestHash:              svc.meta.CRRManifestHash,
		Namespaces:                   []string{"team/dev"},
	})
	if err == nil {
		t.Fatal("expected schema mismatch error")
	}
}

func TestExtractBatchExcludesPrivateTables(t *testing.T) {
	ctx := context.Background()
	memSvc, syncSvc := setupSyncService(t, "peer-a")
	if _, err := memSvc.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared sync body",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := memSvc.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private sync body",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	batch, err := syncSvc.ExtractBatch(ctx, "peer-b", "team/dev", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Changes) == 0 {
		t.Fatal("expected shared changes")
	}
	if batch.Namespace != "team/dev" {
		t.Fatalf("unexpected namespace %q in batch", batch.Namespace)
	}
}

func TestReplayApplyIsSafeAndTracksCursor(t *testing.T) {
	ctx := context.Background()
	leftMem, leftSync := setupSyncService(t, "peer-a")
	_, rightSync := setupSyncService(t, "peer-b")

	if _, err := leftMem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared replicated body",
		Subject:       "replicated",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	batch, err := leftSync.ExtractBatch(ctx, "peer-b", "team/dev", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := rightSync.ApplyBatch(ctx, "peer-a", batch); err != nil {
		t.Fatal(err)
	}
	if err := rightSync.ApplyBatch(ctx, "peer-a", batch); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := rightSync.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_nodes`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("memory_nodes count = %d, want 1", count)
	}
	var cursor int64
	if err := rightSync.db.QueryRowContext(ctx, `
		SELECT version FROM sync_cursors WHERE peer_id = 'peer-a' AND namespace = 'team/dev'
	`).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != batch.MaxVersion {
		t.Fatalf("cursor = %d, want %d", cursor, batch.MaxVersion)
	}
}

func TestIncompatibleBatchIsQuarantined(t *testing.T) {
	ctx := context.Background()
	_, syncSvc := setupSyncService(t, "peer-b")
	err := syncSvc.ApplyBatch(ctx, "peer-a", Batch{
		BatchID:         "b1",
		FromPeerID:      "peer-a",
		Namespace:       "team/dev",
		SchemaHash:      "bad",
		CRRManifestHash: syncSvc.meta.CRRManifestHash,
	})
	if err == nil {
		t.Fatal("expected apply failure")
	}
	var count int
	if err := syncSvc.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_quarantine`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("quarantine count = %d, want 1", count)
	}
}

func TestSyncStatusReflectsFence(t *testing.T) {
	ctx := context.Background()
	_, svc := setupSyncService(t, "peer-a")
	_ = svc.markSyncError(ctx, "peer-b", "team/dev", "schema hash mismatch", true)
	status, err := svc.SyncStatus(ctx, "team/dev")
	if err != nil {
		t.Fatal(err)
	}
	if !status.SchemaFenced {
		t.Fatal("expected schema_fenced=true")
	}
	if status.State != "schema_fenced" {
		t.Fatalf("state = %q, want schema_fenced", status.State)
	}
}

func TestMissingSignatureBatchMarksVerificationState(t *testing.T) {
	ctx := context.Background()
	leftMem, leftSync := setupSyncService(t, "peer-a")
	_, rightSync := setupSyncService(t, "peer-b")

	memoryID, err := leftMem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared replicated body",
		Subject:       "replicated",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leftSync.db.ExecContext(ctx, `UPDATE memory_nodes SET author_signature = X'' WHERE memory_id = ?`, memoryID); err != nil {
		t.Fatal(err)
	}

	batch, err := leftSync.ExtractBatch(ctx, "peer-b", "team/dev", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := rightSync.ApplyBatch(ctx, "peer-a", batch); err != nil {
		t.Fatal(err)
	}

	var status string
	if err := rightSync.db.QueryRowContext(ctx, `
		SELECT signature_status FROM memory_verification_state WHERE memory_space = 'shared' AND memory_id = ?
	`, memoryID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(memory.SignatureStatusMissingSignature) {
		t.Fatalf("signature_status = %q, want missing_signature", status)
	}
}

func TestInvalidSignatureBatchIsQuarantined(t *testing.T) {
	ctx := context.Background()
	leftMem, leftSync := setupSyncService(t, "peer-a")
	_, rightSync := setupSyncService(t, "peer-b")

	memoryID, err := leftMem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared replicated body",
		Subject:       "replicated",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leftSync.db.ExecContext(ctx, `UPDATE memory_nodes SET author_signature = X'01' WHERE memory_id = ?`, memoryID); err != nil {
		t.Fatal(err)
	}

	batch, err := leftSync.ExtractBatch(ctx, "peer-b", "team/dev", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := rightSync.ApplyBatch(ctx, "peer-a", batch); err == nil {
		t.Fatal("expected invalid signature failure")
	}
	var count int
	if err := rightSync.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_quarantine`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("quarantine count = %d, want 1", count)
	}
}

func TestUnknownPeerBatchIsQuarantined(t *testing.T) {
	ctx := context.Background()
	leftMem, leftSync := setupSyncService(t, "peer-a")
	_, rightSync := setupSyncService(t, "peer-b")

	if _, err := leftMem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared replicated body",
		Subject:       "replicated",
		OriginPeerID:  "peer-z",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	batch, err := leftSync.ExtractBatch(ctx, "peer-b", "team/dev", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := rightSync.ApplyBatch(ctx, "peer-a", batch); err == nil {
		t.Fatal("expected unknown peer failure")
	}
	var count int
	if err := rightSync.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_quarantine`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("quarantine count = %d, want 1", count)
	}
}
