package memory

import (
	"context"
	"strings"
	"testing"

	"crdt-agent-memory/internal/indexer"
	"crdt-agent-memory/internal/ingest"
)

func TestPromoteAndPublishFromTranscript(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()
	ingestSvc := ingest.NewService(fixture.db)

	if err := ingestSvc.IngestSession(ctx, ingest.SessionIngestRequest{
		SessionID:   "session-promote",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []ingest.SessionMessage{
			{Seq: 1, Role: "user", Content: "議論した設計を残して", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "decision: retrieval_units を導入する", AuthoredAtMS: 110},
		},
	}); err != nil {
		t.Fatal(err)
	}

	privateID, err := fixture.svc.Promote(ctx, PromoteRequest{
		ChunkIDs:   []string{"session-promote:v1:1"},
		Namespace:  "crdt-agent-memory",
		MemoryType: "decision",
		Subject:    "retrieval_units adoption",
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedID, err := fixture.svc.Publish(ctx, PublishRequest{PrivateMemoryID: privateID})
	if err != nil {
		t.Fatal(err)
	}

	worker := indexer.NewWorker(fixture.db, 0)
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}

	results, err := fixture.svc.Recall(ctx, RecallRequest{
		Query:             "retrieval_units",
		IncludePrivate:    true,
		IncludeTranscript: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Fatalf("got %d results, want transcript + private + shared", len(results))
	}

	var promotionCount, publicationCount int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_promotions WHERE memory_id = ?`, privateID).Scan(&promotionCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_publications WHERE private_memory_id = ? AND shared_memory_id = ?`, privateID, sharedID).Scan(&publicationCount); err != nil {
		t.Fatal(err)
	}
	if promotionCount != 1 {
		t.Fatalf("promotion_count = %d, want 1", promotionCount)
	}
	if publicationCount != 1 {
		t.Fatalf("publication_count = %d, want 1", publicationCount)
	}
}

func TestPromoteInheritsTranscriptArtifactSpans(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()
	ingestSvc := ingest.NewService(fixture.db)

	if err := ingestSvc.IngestSession(ctx, ingest.SessionIngestRequest{
		SessionID:   "session-artifact-promote",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []ingest.SessionMessage{
			{Seq: 1, Role: "user", Content: "修正箇所は /Users/test/project/main.go", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "decision: main.go の分岐を整理する", AuthoredAtMS: 110},
		},
	}); err != nil {
		t.Fatal(err)
	}

	privateID, err := fixture.svc.Promote(ctx, PromoteRequest{
		ChunkIDs:   []string{"session-artifact-promote:v1:1"},
		Namespace:  "crdt-agent-memory",
		MemoryType: "decision",
		Subject:    "main.go cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}

	var spanCount int
	var sourceURI string
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM private_artifact_spans WHERE memory_id = ?`, privateID).Scan(&spanCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(ctx, `SELECT source_uri FROM private_memory_nodes WHERE memory_id = ?`, privateID).Scan(&sourceURI); err != nil {
		t.Fatal(err)
	}
	if spanCount == 0 {
		t.Fatal("expected promoted private memory to inherit transcript artifact spans")
	}
	if !strings.Contains(sourceURI, "/Users/test/project/main.go") {
		t.Fatalf("source_uri = %q, want inherited artifact uri", sourceURI)
	}
}

func TestPromoteInfersSupportRelationAndTraceDecisionReturnsTranscriptSources(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()
	ingestSvc := ingest.NewService(fixture.db)

	if err := ingestSvc.IngestSession(ctx, ingest.SessionIngestRequest{
		SessionID:   "session-trace",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []ingest.SessionMessage{
			{Seq: 1, Role: "user", Content: "方針は /Users/test/project/a.go を採用", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "decision: a.go を採用する", AuthoredAtMS: 110},
			{Seq: 3, Role: "user", Content: "理由も残して", AuthoredAtMS: 120},
			{Seq: 4, Role: "assistant", Content: "rationale: a.go は変更範囲が小さい", AuthoredAtMS: 130},
		},
	}); err != nil {
		t.Fatal(err)
	}

	decisionID, err := fixture.svc.Promote(ctx, PromoteRequest{
		ChunkIDs:   []string{"session-trace:v1:1"},
		Namespace:  "crdt-agent-memory",
		MemoryType: "decision",
		Subject:    "adopt a.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	rationaleID, err := fixture.svc.Promote(ctx, PromoteRequest{
		ChunkIDs:   []string{"session-trace:v1:2"},
		Namespace:  "crdt-agent-memory",
		MemoryType: "rationale",
		Subject:    "why a.go",
	})
	if err != nil {
		t.Fatal(err)
	}

	var relationCount int
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM private_memory_edges
		WHERE from_memory_id = ? AND to_memory_id = ? AND relation_type = 'supports'
	`, rationaleID, decisionID).Scan(&relationCount); err != nil {
		t.Fatal(err)
	}
	if relationCount != 1 {
		t.Fatalf("relation_count = %d, want 1", relationCount)
	}

	trace, err := fixture.svc.TraceDecision(ctx, TraceDecisionRequest{
		MemorySpace: "private",
		MemoryID:    rationaleID,
		Depth:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.Supports) == 0 {
		t.Fatal("expected inferred support relation in trace")
	}
	if len(trace.TranscriptSources) == 0 {
		t.Fatal("expected transcript sources in trace")
	}
	hasArtifacts := false
	for _, source := range trace.TranscriptSources {
		if len(source.Artifacts) > 0 {
			hasArtifacts = true
			break
		}
	}
	if !hasArtifacts {
		t.Fatal("expected transcript artifacts in trace transcript sources")
	}
}

func TestContextBuildReturnsSectionedBundle(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()
	ingestSvc := ingest.NewService(fixture.db)

	if _, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "crdt-agent-memory",
		MemoryType:    "decision",
		Subject:       "private decision",
		Body:          "Shared sync stays disabled for raw transcript",
		SourceURI:     "docs/private.md",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityShared,
		Namespace:     "crdt-agent-memory",
		MemoryType:    "fact",
		Subject:       "shared constraint",
		Body:          "Only promoted shared memory is synchronized",
		SourceURI:     "docs/architecture.md",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "crdt-agent-memory",
		MemoryType:    "task_candidate",
		Subject:       "open task",
		Body:          "TODO add checkpoint hook",
		SourceURI:     "internal/ingest/session_ingest.go",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "crdt-agent-memory",
		MemoryType:    "note",
		Subject:       "rejected option",
		Body:          "Rejected: syncing raw transcript across peers",
		SourceURI:     "docs/rejected.md",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ingestSvc.IngestSession(ctx, ingest.SessionIngestRequest{
		SessionID:   "session-context",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []ingest.SessionMessage{
			{Seq: 1, Role: "user", Content: "最近の議論を戻したい docs/architecture.md", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "qa_pair: transcript と memory の役割を整理する", AuthoredAtMS: 110},
		},
	}); err != nil {
		t.Fatal(err)
	}

	bundle, err := fixture.svc.ContextBuild(ctx, ContextBuildRequest{
		Query:           "transcript shared sync architecture TODO rejected disabled",
		Namespaces:      []string{"crdt-agent-memory"},
		LimitPerSection: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.ActivePrivateDecisions) == 0 {
		t.Fatal("expected active_private_decisions")
	}
	if len(bundle.SharedConstraints) == 0 {
		t.Fatal("expected shared_constraints")
	}
	if len(bundle.RecentDiscussions) == 0 {
		t.Fatal("expected recent_discussions")
	}
	if len(bundle.RejectedOptions) == 0 {
		t.Fatal("expected rejected_options")
	}
	if len(bundle.OpenTasks) == 0 {
		t.Fatal("expected open_tasks")
	}
	if len(bundle.Artifacts) == 0 {
		t.Fatal("expected artifacts")
	}
}

func TestPublishAppliesDefaultRedactionPolicy(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()

	privateID, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "crdt-agent-memory",
		MemoryType:    "decision",
		Subject:       "token: abc123",
		Body:          "password=hunter2 source=/Users/test/app/config.yaml file://secret.txt",
		SourceURI:     "/Users/test/app/config.yaml",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	sharedID, err := fixture.svc.Publish(ctx, PublishRequest{
		PrivateMemoryID: privateID,
		RedactionPolicy: "default",
	})
	if err != nil {
		t.Fatal(err)
	}

	var subject, body, sourceURI string
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT subject, body, source_uri
		FROM memory_nodes
		WHERE memory_id = ?
	`, sharedID).Scan(&subject, &body, &sourceURI); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(subject), "abc123") {
		t.Fatalf("subject leaked secret: %q", subject)
	}
	if strings.Contains(body, "/Users/test") || strings.Contains(body, "hunter2") {
		t.Fatalf("body leaked private content: %q", body)
	}
	if sourceURI != "" {
		t.Fatalf("source_uri = %q, want empty after redaction", sourceURI)
	}
}

func TestPublishRejectsUnknownRedactionPolicy(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()

	privateID, err := fixture.svc.Store(ctx, StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     "crdt-agent-memory",
		Body:          "private body",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.svc.Publish(ctx, PublishRequest{
		PrivateMemoryID: privateID,
		RedactionPolicy: "none",
	}); err == nil {
		t.Fatal("expected publish to reject unknown redaction policy")
	}
}
