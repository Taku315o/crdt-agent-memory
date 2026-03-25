package memory

import (
	"context"
	"errors"
	"testing"

	"crdt-agent-memory/internal/ingest"
)

func TestMarkCandidateApprovedTxRejectsNonPendingCandidate(t *testing.T) {
	fixture := newMemoryFixture(t, "peer-a")
	ctx := context.Background()
	ingestSvc := ingest.NewService(fixture.db)

	if err := ingestSvc.IngestSession(ctx, ingest.SessionIngestRequest{
		SessionID:   "session-approved-guard",
		SourceKind:  "cli",
		Namespace:   "crdt-agent-memory",
		StartedAtMS: 100,
		EndedAtMS:   200,
		Messages: []ingest.SessionMessage{
			{Seq: 1, Role: "user", Content: "軽く残して", AuthoredAtMS: 100},
			{Seq: 2, Role: "assistant", Content: "TODO review later", AuthoredAtMS: 110},
		},
	}); err != nil {
		t.Fatal(err)
	}

	items, err := fixture.svc.ListCandidates(ctx, ListCandidatesRequest{
		Namespace: "crdt-agent-memory",
		Status:    "pending",
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(items))
	}

	if _, err := fixture.db.ExecContext(ctx, `
		UPDATE memory_candidates
		SET status = 'rejected'
		WHERE candidate_id = ?
	`, items[0].CandidateID); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	err = markCandidateApprovedTx(ctx, tx, items[0].CandidateID, "memory-guard")
	if !errors.Is(err, ErrCandidateNotPending) {
		t.Fatalf("markCandidateApprovedTx error = %v, want ErrCandidateNotPending", err)
	}

	var status, approvedID string
	if err := fixture.db.QueryRowContext(ctx, `
		SELECT status, COALESCE(approved_memory_id, '')
		FROM memory_candidates
		WHERE candidate_id = ?
	`, items[0].CandidateID).Scan(&status, &approvedID); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" {
		t.Fatalf("status = %q, want rejected", status)
	}
	if approvedID != "" {
		t.Fatalf("approved_memory_id = %q, want empty", approvedID)
	}
}
