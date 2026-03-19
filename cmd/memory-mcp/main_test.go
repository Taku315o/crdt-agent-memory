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
}

type recallHTTPRequest struct {
	Query          string   `json:"query"`
	Namespace      string   `json:"namespace"`
	Namespaces     []string `json:"namespaces"`
	TopK           int      `json:"top_k"`
	IncludePrivate bool     `json:"include_private"`
	Limit          int      `json:"limit"`
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
	for _, name := range []string{"memory.store", "memory.recall", "memory.sync_status"} {
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

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
