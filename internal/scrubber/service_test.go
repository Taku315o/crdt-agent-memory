package scrubber

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

func TestDiagnoseSummarizesTrustAndOrphans(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "scrubber.sqlite")
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

	policies := policy.NewRepository(db)
	if err := policies.AllowPeer(ctx, "peer-a", "peer-a", testenv.PublicKeyHexForPeer("peer-a")); err != nil {
		t.Fatal(err)
	}
	mem := memory.NewService(db, testenv.SignerForPeer(t, "peer-a"))
	validID, err := mem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "valid shared body",
		Subject:       "valid",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	missingID, err := mem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "missing shared body",
		Subject:       "missing",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE memory_nodes SET author_signature = X'' WHERE memory_id = ?
	`, missingID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO memory_nodes(
			memory_id, memory_type, namespace, scope, subject, body, source_uri, source_hash,
			author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
			lifecycle_state, schema_version, author_signature
		) VALUES('unknown-memory', 'fact', 'team/dev', 'team', 'unknown', 'unknown body', '', '', 'agent-a', 'peer-z', 1, 0, 0, 'active', 1, X'01')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES('edge-1', ?, 'missing-target', 'supports', 1.0, 'peer-a', 1)
	`, validID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
		VALUES('signal-1', 'missing-memory', 'peer-a', 'agent-a', 'confirm', 1.0, '', 1)
	`); err != nil {
		t.Fatal(err)
	}

	diag, err := NewService(db, "peer-a", testenv.PublicKeyHexForPeer("peer-a")).Diagnose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diag.TrustSummary.ValidSignatures != 1 {
		t.Fatalf("valid_signatures = %d, want 1", diag.TrustSummary.ValidSignatures)
	}
	if diag.TrustSummary.MissingSignatures != 1 {
		t.Fatalf("missing_signatures = %d, want 1", diag.TrustSummary.MissingSignatures)
	}
	if diag.TrustSummary.UnknownPeerRows != 1 {
		t.Fatalf("unknown_peer_rows = %d, want 1", diag.TrustSummary.UnknownPeerRows)
	}
	if diag.ScrubberSummary.OrphanEdges != 1 {
		t.Fatalf("orphan_edges = %d, want 1", diag.ScrubberSummary.OrphanEdges)
	}
	if diag.ScrubberSummary.OrphanSignals != 1 {
		t.Fatalf("orphan_signals = %d, want 1", diag.ScrubberSummary.OrphanSignals)
	}
}

func TestRunActiveRepairDeletesOldQuarantineAndSuspendsOrphans(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "repair.sqlite")
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

	if _, err := db.ExecContext(ctx, `
		INSERT INTO sync_quarantine(batch_id, peer_id, namespace, reason, payload_json, created_at_ms)
		VALUES('old-batch', 'peer-a', 'team/dev', 'bad', '{}', ?)
	`, time.Now().Add(-8*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES('edge-repair', 'missing-from', 'missing-to', 'supports', 1.0, 'peer-a', 1)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
		VALUES('signal-repair', 'missing-memory', 'peer-a', 'agent-a', 'confirm', 1.0, '', 1)
	`); err != nil {
		t.Fatal(err)
	}

	report, err := NewService(db, "", "").RunActiveRepair(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.DeletedQuarantineBatches != 1 {
		t.Fatalf("deleted_quarantine_batches = %d, want 1", report.DeletedQuarantineBatches)
	}
	if report.SuspendedEdges != 1 {
		t.Fatalf("suspended_edges = %d, want 1", report.SuspendedEdges)
	}
	if report.SuspendedSignals != 1 {
		t.Fatalf("suspended_signals = %d, want 1", report.SuspendedSignals)
	}

	var activeSuspensions int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM local_graph_suspensions WHERE resolved_at_ms = 0
	`).Scan(&activeSuspensions); err != nil {
		t.Fatal(err)
	}
	if activeSuspensions != 2 {
		t.Fatalf("active suspensions = %d, want 2", activeSuspensions)
	}
}
