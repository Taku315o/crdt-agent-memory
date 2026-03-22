package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crdt-agent-memory/internal/config"
)

type storeHTTPRequest struct {
	MemoryID      string `json:"memory_id"`
	Visibility    string `json:"visibility"`
	Namespace     string `json:"namespace"`
	MemoryType    string `json:"memory_type"`
	Scope         string `json:"scope"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	SourceURI     string `json:"source_uri"`
	SourceHash    string `json:"source_hash"`
	AuthorAgentID string `json:"author_agent_id"`
	OriginPeerID  string `json:"origin_peer_id"`
	AuthoredAtMS  int64  `json:"authored_at_ms"`
	Relations     []relationHTTPRequest `json:"relations"`
}

type relationHTTPRequest struct {
	RelationType string  `json:"relation_type"`
	ToMemoryID   string  `json:"to_memory_id"`
	Weight       float64 `json:"weight"`
}

type recallHTTPRequest struct {
	Query          string   `json:"query"`
	Namespace      string   `json:"namespace"`
	Namespaces     []string `json:"namespaces"`
	TopK           int      `json:"top_k"`
	IncludePrivate bool     `json:"include_private"`
	Limit          int      `json:"limit"`
}

type memoryRefHTTPRequest struct {
	MemorySpace string `json:"memory_space"`
	MemoryID    string `json:"memory_id"`
}

type supersedeHTTPRequest struct {
	OldMemoryID  string               `json:"old_memory_id"`
	OldMemoryRef memoryRefHTTPRequest `json:"old_memory_ref"`
	Request      storeHTTPRequest     `json:"request"`
}

type signalHTTPRequest struct {
	MemoryRef     memoryRefHTTPRequest `json:"memory_ref"`
	SignalType    string               `json:"signal_type"`
	Value         float64              `json:"value"`
	Reason        string               `json:"reason"`
	AuthorAgentID string               `json:"author_agent_id"`
	OriginPeerID  string               `json:"origin_peer_id"`
	AuthoredAtMS  int64                `json:"authored_at_ms"`
}

type explainHTTPRequest struct {
	MemoryRef memoryRefHTTPRequest `json:"memory_ref"`
	Query     string               `json:"query"`
}

type traceDecisionHTTPRequest struct {
	MemoryRef memoryRefHTTPRequest `json:"memory_ref"`
	Depth     int                  `json:"depth"`
}

func TestToolsListIncludesStoreAndRecall(t *testing.T) {
	resp := handle(config.Config{}, rpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resp.Result)
	}
	raw, err := json.Marshal(result["tools"])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory.store", "memory.recall", "memory.supersede", "memory.signal", "memory.explain", "memory.trace_decision", "memory.sync_status"} {
		if !strings.Contains(string(raw), name) {
			t.Fatalf("tool list missing %s", name)
		}
	}
}

func TestMemoryStoreToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody storeHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK:        true,
			Data:      map[string]any{"memory_ref": map[string]any{"memory_space": "shared", "memory_id": "01HSTORE"}, "indexed": false, "sync_eligible": true},
			Warnings:  []string{"needs-index"},
			RequestID: "req_store",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.store",
			"arguments": map[string]any{
				"visibility": "shared",
				"namespace":  "team/dev",
				"body":       "store via mcp",
				"subject":    "smoke",
				"relations": []map[string]any{
					{"relation_type": "derived_from", "to_memory_id": "01HRELATE", "weight": 0.5},
				},
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/store" {
		t.Fatalf("path = %s, want /v1/memory/store", gotPath)
	}
	if gotBody.Visibility != "shared" || gotBody.Namespace != "team/dev" || gotBody.Body != "store via mcp" {
		t.Fatalf("body = %#v", gotBody)
	}
	if len(gotBody.Relations) != 1 || gotBody.Relations[0].RelationType != "derived_from" || gotBody.Relations[0].ToMemoryID != "01HRELATE" {
		t.Fatalf("relations = %#v, want derived_from relation", gotBody.Relations)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_store" {
		t.Fatalf("request_id = %v, want req_store", sc["request_id"])
	}
	if len(sc["warnings"].([]string)) != 1 {
		t.Fatalf("warnings = %#v, want 1 item", sc["warnings"])
	}
	data := sc["data"].(map[string]any)
	memoryRef := data["memory_ref"].(map[string]any)
	if memoryRef["memory_space"] != "shared" {
		t.Fatalf("memory_space = %v, want shared", memoryRef["memory_space"])
	}
}

func TestMemoryRecallToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody recallHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"items": []map[string]any{
					{
						"memory_ref": map[string]any{"memory_space": "shared", "memory_id": "01HRECALL"},
						"namespace":  "team/dev",
						"body":       "recall via mcp",
					},
				},
			},
			Warnings:  []string{"indexed"},
			RequestID: "req_recall",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.recall",
			"arguments": map[string]any{
				"query":           "recall",
				"namespaces":      []string{"team/dev"},
				"include_private": false,
				"limit":           5,
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/recall" {
		t.Fatalf("path = %s, want /v1/memory/recall", gotPath)
	}
	if gotBody.Query != "recall" || gotBody.Limit != 5 || gotBody.IncludePrivate {
		t.Fatalf("body = %#v", gotBody)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_recall" {
		t.Fatalf("request_id = %v, want req_recall", sc["request_id"])
	}
	data := sc["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("item count = %d, want 1", len(items))
	}
}

func TestMemorySyncStatusToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"namespace":     "team/dev",
				"state":         "healthy",
				"schema_fenced": false,
				"peers":         []any{},
			},
			Warnings:  []string{},
			RequestID: "req_sync",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.sync_status",
			"arguments": map[string]any{
				"namespace": "team/dev",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotMethod)
	}
	if gotPath != "/v1/sync/status" {
		t.Fatalf("path = %s, want /v1/sync/status", gotPath)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_sync" {
		t.Fatalf("request_id = %v, want req_sync", sc["request_id"])
	}
}

func TestMemoryTraceDecisionToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody traceDecisionHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"decision":       map[string]any{"memory_id": "01HDECISION"},
				"supports":       []any{},
				"contradictions": []any{},
				"artifacts":      []any{},
			},
			Warnings:  []string{},
			RequestID: "req_trace",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.trace_decision",
			"arguments": map[string]any{
				"memory_ref": map[string]any{"memory_space": "shared", "memory_id": "01HDECISION"},
				"depth":      2,
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/trace_decision" {
		t.Fatalf("path = %s, want /v1/memory/trace_decision", gotPath)
	}
	if gotBody.MemoryRef.MemoryID != "01HDECISION" || gotBody.Depth != 2 {
		t.Fatalf("body = %#v", gotBody)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_trace" {
		t.Fatalf("request_id = %v, want req_trace", sc["request_id"])
	}
}

func TestMemorySupersedeToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody supersedeHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"old_memory_ref":  map[string]any{"memory_space": "shared", "memory_id": "01HOLD"},
				"new_memory_ref":  map[string]any{"memory_space": "shared", "memory_id": "01HNEW"},
				"lifecycle_state": "superseded",
			},
			Warnings:  []string{"sync-pending"},
			RequestID: "req_supersede",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.supersede",
			"arguments": map[string]any{
				"old_memory_ref": map[string]any{"memory_space": "shared", "memory_id": "01HOLD"},
				"request": map[string]any{
					"namespace": "team/dev",
					"body":      "updated body",
					"subject":   "updated",
				},
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/supersede" {
		t.Fatalf("path = %s, want /v1/memory/supersede", gotPath)
	}
	if gotBody.OldMemoryRef.MemoryID != "01HOLD" || gotBody.Request.Body != "updated body" {
		t.Fatalf("body = %#v", gotBody)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_supersede" {
		t.Fatalf("request_id = %v, want req_supersede", sc["request_id"])
	}
}

func TestMemorySignalToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody signalHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK:        true,
			Data:      map[string]any{"signal_id": "01HSIGNAL"},
			Warnings:  []string{"local-only"},
			RequestID: "req_signal",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.signal",
			"arguments": map[string]any{
				"memory_ref":  map[string]any{"memory_space": "shared", "memory_id": "01HMEM"},
				"signal_type": "confirm",
				"value":       2.0,
				"reason":      "re-verified",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/signal" {
		t.Fatalf("path = %s, want /v1/memory/signal", gotPath)
	}
	if gotBody.MemoryRef.MemoryID != "01HMEM" || gotBody.SignalType != "confirm" || gotBody.Value != 2.0 {
		t.Fatalf("body = %#v", gotBody)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_signal" {
		t.Fatalf("request_id = %v, want req_signal", sc["request_id"])
	}
}

func TestMemoryExplainToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody explainHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"provenance":      map[string]any{"namespace": "team/dev"},
				"score_breakdown": map[string]any{"matched_query": true},
				"trust_summary":   map[string]any{"signature_status": "valid"},
				"signal_summary":  map[string]any{"store": map[string]any{"count": 1}},
			},
			Warnings:  []string{},
			RequestID: "req_explain",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.explain",
			"arguments": map[string]any{
				"memory_ref": map[string]any{"memory_space": "shared", "memory_id": "01HMEM"},
				"query":      "explain",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/explain" {
		t.Fatalf("path = %s, want /v1/memory/explain", gotPath)
	}
	if gotBody.MemoryRef.MemoryID != "01HMEM" || gotBody.Query != "explain" {
		t.Fatalf("body = %#v", gotBody)
	}
	result := resp.Result.(map[string]any)
	sc := result["structuredContent"].(map[string]any)
	if sc["request_id"] != "req_explain" {
		t.Fatalf("request_id = %v, want req_explain", sc["request_id"])
	}
}

func TestToolAPIErrorBecomesRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK:        false,
			Error:     &apiError{Code: "NOT_FOUND", Message: "memory not found", Retryable: false},
			Warnings:  []string{"check-memory-id"},
			RequestID: "req_missing",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "memory.explain",
			"arguments": map[string]any{
				"memory_ref": map[string]any{"memory_space": "shared", "memory_id": "missing"},
				"query":      "explain",
			},
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected rpc error")
	}
	errMap := resp.Error.(map[string]any)
	if errMap["message"] != "memory not found" {
		t.Fatalf("message = %v, want memory not found", errMap["message"])
	}
	data := errMap["data"].(map[string]any)
	if data["request_id"] != "req_missing" {
		t.Fatalf("request_id = %v, want req_missing", data["request_id"])
	}
	if data["api_code"] != "NOT_FOUND" {
		t.Fatalf("api_code = %v, want NOT_FOUND", data["api_code"])
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
