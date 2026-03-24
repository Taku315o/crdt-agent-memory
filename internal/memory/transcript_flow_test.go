package memory

import (
	"context"
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
