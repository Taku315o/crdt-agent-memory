package memory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/embedding"
	"crdt-agent-memory/internal/indexer"
	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

type memoryFixture struct {
	ctx context.Context
	db  *sql.DB
	svc *Service
}

func newMemoryFixture(t *testing.T, peerID string) *memoryFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.sqlite")
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
	return &memoryFixture{
		ctx: ctx,
		db:  db,
		svc: NewService(db, testenv.SignerForPeer(t, peerID)),
	}
}

func TestStoreRoutesSharedAndPrivateSeparately(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "memory.sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:          dbPath,
		CRSQLitePath:  testenv.CRSQLitePath(t),
		SQLiteVecPath: testenv.SQLiteVecPath(),
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
		Path:          dbPath,
		CRSQLitePath:  testenv.CRSQLitePath(t),
		SQLiteVecPath: testenv.SQLiteVecPath(),
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
		Body:          "release checklist recall body",
		Subject:       "release",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	_, _ = svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "release checklist recall body",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})

	results, err := svc.Recall(ctx, RecallRequest{
		Query:          "release checklist recall body",
		IncludePrivate: true,
		IncludeShared:  true,
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want at least 2", len(results))
	}
}

func TestRecallUsesVectorIndexWhenAvailable(t *testing.T) {
	if testenv.SQLiteVecPath() == "" {
		t.Skip("sqlite-vec not available")
	}
	fixture := newMemoryFixture(t, "peer-a")
	ctx := fixture.ctx

	sharedID, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "vector recall candidate",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateID, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "vector recall candidate",
		Subject:       "private",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	worker := indexer.NewWorker(fixture.db, 0)
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}

	var vectorCount int
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM memory_embedding_vectors
	`).Scan(&vectorCount); err != nil {
		t.Fatal(err)
	}
	if vectorCount < 2 {
		t.Fatalf("vector_count = %d, want at least 2", vectorCount)
	}

	results, err := fixture.svc.Recall(ctx, RecallRequest{
		Query:          "vector recall candidate",
		IncludePrivate: true,
		IncludeShared:  true,
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want at least 2", len(results))
	}
	if results[0].MemoryID != sharedID && results[0].MemoryID != privateID {
		t.Fatalf("first recall result = %s, want one of indexed ids", results[0].MemoryID)
	}
}

func TestRecallDetailedFallsBackToLexicalWithWarningWhenEmbeddingFails(t *testing.T) {
	if testenv.SQLiteVecPath() == "" {
		t.Skip("sqlite-vec not available")
	}
	fixture := newMemoryFixture(t, "peer-a")
	ctx := fixture.ctx

	if _, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "日本語の壁打ちメモ",
		Subject:       "shared",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	embedding.Configure(config.Embedding{Provider: "local", Dimension: 8, TimeoutMS: 1000})
	if err := indexer.NewWorker(fixture.db, 0).ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}

	embedding.Configure(config.Embedding{
		Provider:  "ruri-http",
		BaseURL:   "http://127.0.0.1:1/embed",
		Model:     "cl-nagoya-ruri-v3",
		Dimension: 768,
		TimeoutMS: 100,
	})
	t.Cleanup(func() { embedding.Configure(config.Embedding{}) })

	resp, err := fixture.svc.RecallDetailed(ctx, RecallRequest{
		Query:          "壁打ちメモ",
		IncludeShared:  true,
		IncludePrivate: false,
		Limit:          5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) == 0 {
		t.Fatal("expected lexical fallback results")
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 warning", resp.Warnings)
	}
}

func TestRecallRanksValidBeforeMissingAndByTrustWeight(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "ranking.sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:          dbPath,
		CRSQLitePath:  testenv.CRSQLitePath(t),
		SQLiteVecPath: testenv.SQLiteVecPath(),
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

func TestRecallBoostsArtifactBackedMemory(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	plainID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "artifact boost query",
		Subject:       "plain",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		AuthoredAtMS:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "artifact boost query",
		Subject:       "artifact",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		AuthoredAtMS:  1,
		ArtifactSpans: []ArtifactSpanInput{
			{
				URI:       "file:///repo/main.go",
				Title:     "main.go",
				StartLine: 12,
				EndLine:   18,
				QuoteHash: "quote-1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := fixture.svc.Recall(fixture.ctx, RecallRequest{
		Query:      "artifact boost query",
		Namespaces: []string{"team/dev"},
		Limit:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want at least 2", len(results))
	}
	if results[0].MemoryID != artifactID {
		t.Fatalf("first memory_id = %q, want artifact-backed %q", results[0].MemoryID, artifactID)
	}
	if results[1].MemoryID != plainID {
		t.Fatalf("second memory_id = %q, want plain %q", results[1].MemoryID, plainID)
	}
}

func TestRecallBoostsGraphConnectedMemory(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	plainID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "graph boost query",
		Subject:       "plain",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		AuthoredAtMS:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	graphID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "graph boost query",
		Subject:       "graph",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		AuthoredAtMS:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	supporterID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "supporting memory",
		Subject:       "supporter",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		AuthoredAtMS:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(fixture.ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES('edge-graph-1', ?, ?, 'supports', 1.0, 'peer-a', 1)
	`, supporterID, graphID); err != nil {
		t.Fatal(err)
	}

	results, err := fixture.svc.Recall(fixture.ctx, RecallRequest{
		Query:      "graph boost query",
		Namespaces: []string{"team/dev"},
		Limit:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want at least 2", len(results))
	}
	if results[0].MemoryID != graphID {
		t.Fatalf("first memory_id = %q, want graph-connected %q", results[0].MemoryID, graphID)
	}
	if results[1].MemoryID != plainID {
		t.Fatalf("second memory_id = %q, want plain %q", results[1].MemoryID, plainID)
	}
}

func TestStorePersistsArtifactSpans(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	memoryID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "artifact trace body",
		Subject:       "artifact",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		ArtifactSpans: []ArtifactSpanInput{
			{
				URI:       "file:///repo/main.go",
				Title:     "main.go",
				MimeType:  "text/x-go",
				StartLine: 12,
				EndLine:   18,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var artifactCount, spanCount, startLine int
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM artifact_refs`).Scan(&artifactCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM artifact_spans WHERE memory_id = ?`, memoryID).Scan(&spanCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT start_line FROM artifact_spans WHERE memory_id = ?`, memoryID).Scan(&startLine); err != nil {
		t.Fatal(err)
	}
	if artifactCount != 1 {
		t.Fatalf("artifact_refs count = %d, want 1", artifactCount)
	}
	if spanCount != 1 {
		t.Fatalf("artifact_spans count = %d, want 1", spanCount)
	}
	if startLine != 12 {
		t.Fatalf("start_line = %d, want 12", startLine)
	}
}

func TestStorePersistsRelationsByVisibility(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	sharedTargetID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared target",
		Subject:       "target",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedSourceID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared source",
		Subject:       "source",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		Relations: []MemoryRelationInput{
			{RelationType: "derived_from", ToMemoryID: sharedTargetID, Weight: 0.75},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var sharedEdgeCount int
	var relationType string
	var weight float64
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT COUNT(*)
		FROM memory_edges
		WHERE from_memory_id = ? AND to_memory_id = ? AND relation_type = 'derived_from'
	`, sharedSourceID, sharedTargetID).Scan(&sharedEdgeCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT relation_type, weight
		FROM memory_edges
		WHERE from_memory_id = ? AND to_memory_id = ?
	`, sharedSourceID, sharedTargetID).Scan(&relationType, &weight); err != nil {
		t.Fatal(err)
	}
	if sharedEdgeCount != 1 {
		t.Fatalf("shared edge count = %d, want 1", sharedEdgeCount)
	}
	if relationType != "derived_from" {
		t.Fatalf("relation_type = %q, want derived_from", relationType)
	}
	if weight != 0.75 {
		t.Fatalf("weight = %v, want 0.75", weight)
	}

	privateTargetID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private target",
		Subject:       "target",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateSourceID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private source",
		Subject:       "source",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		Relations: []MemoryRelationInput{
			{RelationType: "references", ToMemoryID: privateTargetID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var privateEdgeCount int
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT COUNT(*)
		FROM private_memory_edges
		WHERE from_memory_id = ? AND to_memory_id = ? AND relation_type = 'references'
	`, privateSourceID, privateTargetID).Scan(&privateEdgeCount); err != nil {
		t.Fatal(err)
	}
	if privateEdgeCount != 1 {
		t.Fatalf("private edge count = %d, want 1", privateEdgeCount)
	}
}

func TestStoreRejectsUnsupportedRelationType(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	targetID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "target",
		Subject:       "target",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "source",
		Subject:       "source",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		Relations: []MemoryRelationInput{
			{RelationType: "supersedes", ToMemoryID: targetID},
		},
	})
	if err == nil {
		t.Fatal("expected unsupported relation type error")
	}
}

func TestTraceDecisionIncludesArtifactsAndSkipsSuspendedEdges(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	decisionID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "decision body",
		Subject:       "decision",
		MemoryType:    "decision",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
		ArtifactSpans: []ArtifactSpanInput{
			{
				URI:       "file:///repo/decision.md",
				Title:     "decision.md",
				StartLine: 1,
				EndLine:   4,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supportID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "support body",
		Subject:       "support",
		OriginPeerID:  "peer-a",
		AuthorAgentID: "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(fixture.ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES('edge-trace', ?, ?, 'supports', 1.0, 'peer-a', 1)
	`, decisionID, supportID); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.svc.TraceDecision(fixture.ctx, TraceDecisionRequest{
		MemorySpace: string(VisibilityShared),
		MemoryID:    decisionID,
		Depth:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Supports) != 1 {
		t.Fatalf("supports count = %d, want 1", len(result.Supports))
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts count = %d, want 1", len(result.Artifacts))
	}

	if _, err := fixture.db.ExecContext(fixture.ctx, `
		INSERT INTO local_graph_suspensions(
			entity_type, entity_id, memory_space, memory_id, reason, detail, first_seen_at_ms, last_seen_at_ms, resolved_at_ms
		) VALUES('memory_edge', 'edge-trace', 'shared', ?, 'orphaned', '', 1, 1, 0)
	`, decisionID); err != nil {
		t.Fatal(err)
	}
	suspended, err := fixture.svc.TraceDecision(fixture.ctx, TraceDecisionRequest{
		MemorySpace: string(VisibilityShared),
		MemoryID:    decisionID,
		Depth:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(suspended.Supports) != 0 {
		t.Fatalf("supports count = %d, want 0 after suspension", len(suspended.Supports))
	}
}

func TestSupersedeFailsWhenOldMemoryMissing(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	_, err := fixture.svc.Supersede(fixture.ctx, "missing-memory", StoreRequest{
		Namespace:     "team/dev",
		Body:          "new body",
		Subject:       "new",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("err = %v, want ErrMemoryNotFound", err)
	}

	var memoryCount, edgeCount, queueCount int
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM memory_nodes`).Scan(&memoryCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM memory_edges`).Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM index_queue`).Scan(&queueCount); err != nil {
		t.Fatal(err)
	}
	if memoryCount != 0 {
		t.Fatalf("memory count = %d, want 0", memoryCount)
	}
	if edgeCount != 0 {
		t.Fatalf("edge count = %d, want 0", edgeCount)
	}
	if queueCount != 0 {
		t.Fatalf("queue count = %d, want 0", queueCount)
	}
}

func TestSupersedeMarksOldMemoryAndCreatesEdgeAtomically(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	oldID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "old body",
		Subject:       "old",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	newID, err := fixture.svc.Supersede(fixture.ctx, oldID, StoreRequest{
		Namespace:     "team/dev",
		Body:          "new body",
		Subject:       "new",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	var lifecycle string
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT lifecycle_state FROM memory_nodes WHERE memory_id = ?
	`, oldID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "superseded" {
		t.Fatalf("lifecycle_state = %q, want superseded", lifecycle)
	}

	var edgeCount int
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT COUNT(*) FROM memory_edges
		WHERE from_memory_id = ? AND to_memory_id = ? AND relation_type = 'supersedes'
	`, newID, oldID).Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 1 {
		t.Fatalf("edge count = %d, want 1", edgeCount)
	}
}

func TestSignalRoutesSharedAndPrivate(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")

	sharedID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared signal body",
		Subject:       "shared",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	privateID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private signal body",
		Subject:       "private",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.svc.Signal(fixture.ctx, SignalRequest{
		MemorySpace:   "shared",
		MemoryID:      sharedID,
		SignalType:    "confirm",
		Value:         2.0,
		Reason:        "verified",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.svc.Signal(fixture.ctx, SignalRequest{
		MemorySpace:   "private",
		MemoryID:      privateID,
		SignalType:    "bookmark",
		Value:         1.0,
		Reason:        "keep local",
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}

	var sharedSignals, privateSignals int
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT COUNT(*) FROM memory_signals WHERE memory_id = ? AND signal_type = 'confirm'
	`, sharedID).Scan(&sharedSignals); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(fixture.ctx, `
		SELECT COUNT(*) FROM private_memory_signals WHERE memory_id = ? AND signal_type = 'bookmark'
	`, privateID).Scan(&privateSignals); err != nil {
		t.Fatal(err)
	}
	if sharedSignals != 1 {
		t.Fatalf("shared signal count = %d, want 1", sharedSignals)
	}
	if privateSignals != 1 {
		t.Fatalf("private signal count = %d, want 1", privateSignals)
	}
}

func TestSignalRejectsReservedTypeAndNonPositiveValue(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	memoryID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "signal validation body",
		Subject:       "shared",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.svc.Signal(fixture.ctx, SignalRequest{
		MemorySpace: "shared",
		MemoryID:    memoryID,
		SignalType:  "store",
		Value:       1.0,
	}); err == nil {
		t.Fatal("expected reserved signal_type error")
	}
	if _, err := fixture.svc.Signal(fixture.ctx, SignalRequest{
		MemorySpace: "shared",
		MemoryID:    memoryID,
		SignalType:  "confirm",
		Value:       0,
	}); err == nil {
		t.Fatal("expected value validation error")
	}
}

func TestExplainReturnsTrustAndSignalBreakdown(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	if _, err := fixture.db.ExecContext(fixture.ctx, `
		INSERT INTO peer_policies(peer_id, display_name, trust_state, trust_weight, signing_public_key, updated_at_ms)
		VALUES('peer-a', 'peer-a', 'allow', 0.6, ?, 1)
	`, testenv.PublicKeyHexForPeer("peer-a")); err != nil {
		t.Fatal(err)
	}

	memoryID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "explainable ranked memory",
		Subject:       "explain",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.svc.Signal(fixture.ctx, SignalRequest{
		MemorySpace:   "shared",
		MemoryID:      memoryID,
		SignalType:    "confirm",
		Value:         2.0,
		Reason:        "re-verified",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	}); err != nil {
		t.Fatal(err)
	}

	explained, err := fixture.svc.Explain(fixture.ctx, ExplainRequest{
		MemorySpace: "shared",
		MemoryID:    memoryID,
		Query:       "ranked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explained.ScoreBreakdown.MatchedQuery {
		t.Fatal("expected matched_query=true")
	}
	if !explained.ScoreBreakdown.RecallEligible {
		t.Fatal("expected recall_eligible=true")
	}
	if explained.TrustSummary.SignatureStatus != string(SignatureStatusValid) {
		t.Fatalf("signature_status = %q, want valid", explained.TrustSummary.SignatureStatus)
	}
	if explained.TrustSummary.PeerTrustWeight != 0.6 {
		t.Fatalf("trust_weight = %v, want 0.6", explained.TrustSummary.PeerTrustWeight)
	}
	if !explained.TrustSummary.HasSigningKey {
		t.Fatal("expected has_signing_key=true")
	}
	if explained.SignalSummary["confirm"].Count != 1 {
		t.Fatalf("confirm count = %d, want 1", explained.SignalSummary["confirm"].Count)
	}
	if explained.SignalSummary["confirm"].Sum != 2.0 {
		t.Fatalf("confirm sum = %v, want 2.0", explained.SignalSummary["confirm"].Sum)
	}
	if explained.SignalSummary["store"].Count != 1 {
		t.Fatalf("store count = %d, want 1", explained.SignalSummary["store"].Count)
	}
}

func TestExplainReturnsQueryMismatchAndInvalidSignature(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	memoryID, err := fixture.svc.Store(fixture.ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "team/dev",
		Body:          "query mismatch memory",
		Subject:       "explain",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	mismatch, err := fixture.svc.Explain(fixture.ctx, ExplainRequest{
		MemorySpace: "shared",
		MemoryID:    memoryID,
		Query:       "unmatched",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mismatch.ScoreBreakdown.MatchedQuery {
		t.Fatal("expected matched_query=false")
	}
	if mismatch.ScoreBreakdown.LexicalBM25 != 0 {
		t.Fatalf("lexical_bm25 = %v, want 0", mismatch.ScoreBreakdown.LexicalBM25)
	}

	if err := upsertVerificationState(fixture.ctx, fixture.db, "shared", memoryID, SignatureStatusInvalidSignature, "bad signature"); err != nil {
		t.Fatal(err)
	}
	invalid, err := fixture.svc.Explain(fixture.ctx, ExplainRequest{
		MemorySpace: "shared",
		MemoryID:    memoryID,
		Query:       "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	if invalid.ScoreBreakdown.RecallEligible {
		t.Fatal("expected recall_eligible=false")
	}
	if invalid.TrustSummary.SignatureStatus != string(SignatureStatusInvalidSignature) {
		t.Fatalf("signature_status = %q, want invalid_signature", invalid.TrustSummary.SignatureStatus)
	}
}
