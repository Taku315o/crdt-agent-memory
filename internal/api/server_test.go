package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

type testEnvelope[T any] struct {
	OK        bool      `json:"ok"`
	Data      T         `json:"data"`
	Error     *APIError `json:"error"`
	Warnings  []string  `json:"warnings"`
	RequestID string    `json:"request_id"`
}

type apiFixture struct {
	db     *sql.DB
	server *httptest.Server
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "api.sqlite")
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
	syncSvc := memsync.NewService(db, meta, policies, "peer-a", memsync.TransportHTTPDev)
	srv, err := New(ctx, db, meta, syncSvc, testenv.SignerForPeer(t, "peer-a"), "peer-a")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return &apiFixture{db: db, server: server}
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func TestMemoryStoreContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		MemoryType:    "fact",
		Scope:         "team",
		Subject:       "shared",
		Body:          "shared contract body",
		SourceURI:     "https://example.com/source",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if envelope.RequestID == "" || !strings.HasPrefix(envelope.RequestID, "req_") {
		t.Fatalf("request_id = %q, want req_*", envelope.RequestID)
	}
	if len(envelope.Warnings) != 0 {
		t.Fatalf("warnings = %v, want empty", envelope.Warnings)
	}
	if envelope.Data.MemoryRef.MemorySpace != "shared" {
		t.Fatalf("memory_space = %q, want shared", envelope.Data.MemoryRef.MemorySpace)
	}
	if envelope.Data.MemoryRef.MemoryID == "" {
		t.Fatal("expected memory_id")
	}
	if !envelope.Data.SyncEligible {
		t.Fatal("expected sync_eligible=true")
	}
	var signatureLen int
	if err := fixture.db.QueryRowContext(context.Background(), `
		SELECT length(author_signature) FROM memory_nodes WHERE memory_id = ?
	`, envelope.Data.MemoryRef.MemoryID).Scan(&signatureLen); err != nil {
		t.Fatal(err)
	}
	if signatureLen == 0 {
		t.Fatal("expected non-empty author_signature")
	}

	resp, raw = doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private contract body",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var privateEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &privateEnvelope); err != nil {
		t.Fatal(err)
	}
	if privateEnvelope.Data.SyncEligible {
		t.Fatal("expected sync_eligible=false for private write")
	}
	if privateEnvelope.Data.MemoryRef.MemorySpace != "private" {
		t.Fatalf("memory_space = %q, want private", privateEnvelope.Data.MemoryRef.MemorySpace)
	}
}

func TestMemoryStoreAcceptsRelations(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()

	targetResp, targetRaw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "relation target",
		Subject:       "target",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if targetResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", targetResp.StatusCode)
	}
	var targetEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(targetRaw, &targetEnvelope); err != nil {
		t.Fatal(err)
	}

	sourceResp, sourceRaw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "relation source",
		Subject:       "source",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
		Relations: []memory.MemoryRelationInput{
			{RelationType: "references", ToMemoryID: targetEnvelope.Data.MemoryRef.MemoryID, Weight: 0.5},
		},
	})
	if sourceResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", sourceResp.StatusCode)
	}
	var sourceEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(sourceRaw, &sourceEnvelope); err != nil {
		t.Fatal(err)
	}

	var edgeCount int
	if err := fixture.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		FROM memory_edges
		WHERE from_memory_id = ? AND to_memory_id = ? AND relation_type = 'references'
	`, sourceEnvelope.Data.MemoryRef.MemoryID, targetEnvelope.Data.MemoryRef.MemoryID).Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 1 {
		t.Fatalf("edge count = %d, want 1", edgeCount)
	}
}

func TestMemoryRecallContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()
	_, _ = doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "recall contract body",
		Subject:       "recall",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/recall", RecallRequest{
		Query:          "recall",
		Namespaces:     []string{"team/dev"},
		IncludePrivate: false,
		Limit:          10,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[RecallResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if envelope.RequestID == "" {
		t.Fatal("expected request_id")
	}
	if len(envelope.Data.Items) == 0 {
		t.Fatal("expected recall items")
	}
	if envelope.Data.Items[0].MemoryRef.MemoryID == "" {
		t.Fatal("expected item memory_id")
	}
}

func TestContextBuildContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()

	_, _ = doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "team/dev",
		MemoryType:    "decision",
		Body:          "context build private decision",
		Subject:       "private",
		SourceURI:     "docs/private.md",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/context/build", ContextBuildRequest{
		Query:           "context build private decision",
		Namespace:       "team/dev",
		LimitPerSection: 3,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[ContextBuildResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if envelope.RequestID == "" {
		t.Fatal("expected request_id")
	}
	if len(envelope.Data.ActivePrivateDecisions) == 0 {
		t.Fatal("expected active_private_decisions")
	}
}

func TestMemorySupersedeContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()
	_, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "old body",
		Subject:       "old",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	var storeEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &storeEnvelope); err != nil {
		t.Fatal(err)
	}

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/supersede", SupersedeRequest{
		OldMemoryID: storeEnvelope.Data.MemoryRef.MemoryID,
		Request: StoreRequest{
			Visibility:    memory.VisibilityShared,
			Namespace:     "team/dev",
			Body:          "new body",
			Subject:       "new",
			AuthorAgentID: "agent-a",
			OriginPeerID:  "peer-a",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[SupersedeResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if envelope.Data.OldMemoryRef.MemoryID != storeEnvelope.Data.MemoryRef.MemoryID {
		t.Fatal("old_memory_ref mismatch")
	}
	if envelope.Data.NewMemoryRef.MemoryID == "" {
		t.Fatal("expected new_memory_ref")
	}
	if envelope.Data.LifecycleState != "superseded" {
		t.Fatalf("lifecycle_state = %q, want superseded", envelope.Data.LifecycleState)
	}
}

func TestMemorySupersedeNotFoundContract(t *testing.T) {
	fixture := newAPIFixture(t)
	resp, raw := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/memory/supersede", SupersedeRequest{
		OldMemoryID: "missing-memory",
		Request: StoreRequest{
			Visibility:    memory.VisibilityShared,
			Namespace:     "team/dev",
			Body:          "new body",
			Subject:       "new",
			AuthorAgentID: "agent-a",
			OriginPeerID:  "peer-a",
		},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var envelope testEnvelope[SupersedeResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK {
		t.Fatal("expected ok=false")
	}
	if envelope.Error == nil || envelope.Error.Code != "NOT_FOUND" {
		t.Fatalf("error = %#v, want NOT_FOUND", envelope.Error)
	}
}

func TestMemorySupersedeRejectsPrivateTargetContract(t *testing.T) {
	fixture := newAPIFixture(t)
	resp, raw := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/memory/supersede", SupersedeRequest{
		OldMemoryRef: MemoryRef{MemorySpace: "private", MemoryID: "local-memory"},
		Request: StoreRequest{
			Visibility:    memory.VisibilityShared,
			Namespace:     "team/dev",
			Body:          "new body",
			Subject:       "new",
			AuthorAgentID: "agent-a",
			OriginPeerID:  "peer-a",
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var envelope testEnvelope[SupersedeResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK {
		t.Fatal("expected ok=false")
	}
	if envelope.Error == nil || envelope.Error.Code != "PRIVATE_ONLY" {
		t.Fatalf("error = %#v, want PRIVATE_ONLY", envelope.Error)
	}
}

func TestMemorySignalContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()
	_, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "signal contract body",
		Subject:       "signal",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	var storeEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &storeEnvelope); err != nil {
		t.Fatal(err)
	}

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/signal", SignalRequest{
		MemoryRef:     storeEnvelope.Data.MemoryRef,
		SignalType:    "confirm",
		Value:         2.0,
		Reason:        "re-verified",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[SignalResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if envelope.Data.SignalID == "" {
		t.Fatal("expected signal_id")
	}
}

func TestMemoryExplainContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()
	_, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "explain contract body",
		Subject:       "explain",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	var storeEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &storeEnvelope); err != nil {
		t.Fatal(err)
	}

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/explain", ExplainRequest{
		MemoryRef: storeEnvelope.Data.MemoryRef,
		Query:     "explain",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[ExplainResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if !envelope.Data.ScoreBreakdown.MatchedQuery {
		t.Fatal("expected matched_query=true")
	}
	if envelope.Data.TrustSummary.SignatureStatus != string(memory.SignatureStatusValid) {
		t.Fatalf("signature_status = %q, want valid", envelope.Data.TrustSummary.SignatureStatus)
	}
	if envelope.Data.SignalSummary["store"].Count != 1 {
		t.Fatalf("store count = %d, want 1", envelope.Data.SignalSummary["store"].Count)
	}
}

func TestMemoryTraceDecisionContract(t *testing.T) {
	fixture := newAPIFixture(t)
	client := fixture.server.Client()
	_, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "trace decision body",
		Subject:       "decision",
		MemoryType:    "decision",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
		ArtifactSpans: []memory.ArtifactSpanInput{
			{URI: "file:///repo/decision.md", Title: "decision.md", StartLine: 5, EndLine: 8},
		},
	})
	var decisionEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &decisionEnvelope); err != nil {
		t.Fatal(err)
	}
	_, raw = doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/store", StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "support body",
		Subject:       "support",
		AuthorAgentID: "agent-a",
		OriginPeerID:  "peer-a",
	})
	var supportEnvelope testEnvelope[StoreResponse]
	if err := json.Unmarshal(raw, &supportEnvelope); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(context.Background(), `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES('edge-api-trace', ?, ?, 'supports', 1.0, 'peer-a', 1)
	`, decisionEnvelope.Data.MemoryRef.MemoryID, supportEnvelope.Data.MemoryRef.MemoryID); err != nil {
		t.Fatal(err)
	}

	resp, raw := doJSON(t, client, http.MethodPost, fixture.server.URL+"/v1/memory/trace_decision", TraceDecisionRequest{
		MemoryRef: decisionEnvelope.Data.MemoryRef,
		Depth:     1,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[TraceDecisionResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if len(envelope.Data.Supports) != 1 {
		t.Fatalf("supports count = %d, want 1", len(envelope.Data.Supports))
	}
	if len(envelope.Data.Artifacts) != 1 {
		t.Fatalf("artifacts count = %d, want 1", len(envelope.Data.Artifacts))
	}
}

func TestSyncStatusContract(t *testing.T) {
	fixture := newAPIFixture(t)
	ctx := context.Background()
	if _, err := fixture.db.ExecContext(ctx, `
		INSERT INTO peer_sync_state(
			peer_id, namespace, last_seen_at_ms, last_transport, last_path_type,
			last_error, last_success_at_ms, schema_fenced
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, "peer-b", "team/dev", 123, "http-dev", "direct", "", 456, 0); err != nil {
		t.Fatal(err)
	}

	resp, raw := doJSON(t, fixture.server.Client(), http.MethodGet, fixture.server.URL+"/v1/sync/status?namespace=team/dev", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope testEnvelope[SyncStatusResponse]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("expected ok=true")
	}
	if envelope.RequestID == "" {
		t.Fatal("expected request_id")
	}
	if len(envelope.Warnings) != 0 {
		t.Fatalf("warnings = %v, want empty", envelope.Warnings)
	}
	if envelope.Data.Namespace != "team/dev" {
		t.Fatalf("namespace = %q, want team/dev", envelope.Data.Namespace)
	}
	if len(envelope.Data.Peers) != 1 {
		t.Fatalf("peer count = %d, want 1", len(envelope.Data.Peers))
	}
	if envelope.Data.Peers[0].LastError != nil {
		t.Fatalf("last_error = %v, want null", *envelope.Data.Peers[0].LastError)
	}
}

func TestDiagIncludesTrustAndScrubberSummary(t *testing.T) {
	fixture := newAPIFixture(t)
	resp, raw := doJSON(t, fixture.server.Client(), http.MethodGet, fixture.server.URL+"/v1/diag", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["trust_summary"]; !ok {
		t.Fatal("expected trust_summary")
	}
	if _, ok := payload["scrubber_summary"]; !ok {
		t.Fatal("expected scrubber_summary")
	}
}

func TestHTTPErrorContract(t *testing.T) {
	fixture := newAPIFixture(t)
	resp, raw := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/memory/store", map[string]any{
		"visibility": "shared",
		"namespace":  "team/dev",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var envelope testEnvelope[struct{}]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK {
		t.Fatal("expected ok=false")
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("code = %q, want INVALID_ARGUMENT", envelope.Error.Code)
	}
	if envelope.RequestID == "" {
		t.Fatal("expected request_id")
	}
}
