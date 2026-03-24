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
