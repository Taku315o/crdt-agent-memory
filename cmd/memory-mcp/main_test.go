package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/testenv"
)

type storeHTTPRequest struct {
	MemoryID      string                `json:"memory_id"`
	Visibility    string                `json:"visibility"`
	Namespace     string                `json:"namespace"`
	MemoryType    string                `json:"memory_type"`
	Scope         string                `json:"scope"`
	Subject       string                `json:"subject"`
	Body          string                `json:"body"`
	SourceURI     string                `json:"source_uri"`
	SourceHash    string                `json:"source_hash"`
	AuthorAgentID string                `json:"author_agent_id"`
	OriginPeerID  string                `json:"origin_peer_id"`
	AuthoredAtMS  int64                 `json:"authored_at_ms"`
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

type contextBuildHTTPRequest struct {
	Query           string   `json:"query"`
	Namespace       string   `json:"namespace"`
	Namespaces      []string `json:"namespaces"`
	ProjectKey      string   `json:"project_key"`
	BranchName      string   `json:"branch_name"`
	LimitPerSection int      `json:"limit_per_section"`
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
	for _, name := range []string{"memory.store", "memory.recall", "memory.candidates.list", "memory.candidates.approve", "memory.candidates.reject", "context.build", "memory.supersede", "memory.signal", "memory.explain", "memory.trace_decision", "memory.sync_status"} {
		if !strings.Contains(string(raw), name) {
			t.Fatalf("tool list missing %s", name)
		}
	}
}

func TestInitializeAdvertisesResourcesAndPrompts(t *testing.T) {
	resp := handle(config.Config{}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-06-18",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resp.Result)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T, want map[string]any", result["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatal("capabilities missing tools")
	}
	if _, ok := caps["resources"]; !ok {
		t.Fatal("capabilities missing resources")
	}
	if _, ok := caps["prompts"]; !ok {
		t.Fatal("capabilities missing prompts")
	}
}

func TestInitializeNegotiatesSupportedProtocolVersion(t *testing.T) {
	resp := handle(config.Config{}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2025-06-18",
			"clientInfo": map[string]any{
				"name":    "inspector",
				"version": "1.0.0",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resp.Result)
	}
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want 2025-06-18", result["protocolVersion"])
	}
}

func TestInitializeFallsBackToDefaultProtocolVersion(t *testing.T) {
	resp := handle(config.Config{}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustJSON(t, map[string]any{
			"protocolVersion": "2026-01-01",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resp.Result)
	}
	if result["protocolVersion"] != defaultMCPProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %s", result["protocolVersion"], defaultMCPProtocolVersion)
	}
}

func TestEmptyResourcesAndPromptsList(t *testing.T) {
	for _, method := range []string{"resources/list", "resources/templates/list", "prompts/list"} {
		resp := handle(config.Config{}, rpcRequest{JSONRPC: "2.0", ID: 1, Method: method})
		if resp.Error != nil {
			t.Fatalf("%s returned error: %#v", method, resp.Error)
		}
	}
}

func TestReadMessageSupportsJSONLines(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","method":"ping"}` + "\n"))
	body, err := readMessage(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"jsonrpc":"2.0","method":"ping"}` {
		t.Fatalf("body = %q", string(body))
	}
}

func TestReadMessageSupportsContentLengthFraming(t *testing.T) {
	msg := `{"jsonrpc":"2.0","method":"ping"}`
	input := "Content-Length: " + strconv.Itoa(len(msg)) + "\r\n\r\n" + msg
	r := bufio.NewReader(strings.NewReader(input))
	body, err := readMessage(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != msg {
		t.Fatalf("body = %q", string(body))
	}
}

func TestWriteMessageUsesJSONLineFraming(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	writeMessage(rpcResponse{JSONRPC: "2.0", ID: 1, Result: map[string]any{"ok": true}})
	_ = w.Close()

	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "Content-Length:") {
		t.Fatalf("unexpected content-length framing: %q", s)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("expected trailing newline, got %q", s)
	}
}

func TestMemoryMCPSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}

	handle := startMemoryMCP(t, smokeConfigPath(t, "http://127.0.0.1:3101"))

	handle.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
		},
	})
	resp := handle.read(t)
	if got := resp["id"]; got != float64(1) {
		t.Fatalf("initialize id = %v, want 1", got)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result type = %T", resp["result"])
	}
	if got := result["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want 2025-06-18", got)
	}

	handle.send(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	handle.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	resp = handle.read(t)
	if got := resp["id"]; got != float64(2) {
		t.Fatalf("tools/list id = %v, want 2", got)
	}
	result, ok = resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result type = %T", resp["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools type = %T", result["tools"])
	}
	if len(tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
}

func TestMemoryMCPStoreSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}

	var gotMethod string
	var gotPath string
	var gotBody storeHTTPRequest
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"memory_ref":     map[string]any{"memory_space": "shared", "memory_id": "01HSTORE"},
				"indexed":        true,
				"sync_eligible":  true,
				"source_applied": true,
			},
			Warnings:  []string{"stored"},
			RequestID: "req_store",
		})
	}))
	t.Cleanup(apiServer.Close)

	handle := startMemoryMCP(t, smokeConfigPath(t, apiServer.URL))
	handle.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
		},
	})
	resp := handle.read(t)
	if resp["result"] == nil {
		t.Fatalf("initialize failed: %#v", resp)
	}

	handle.send(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	handle.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory.store",
			"arguments": map[string]any{
				"visibility": "shared",
				"namespace":  "team/dev",
				"body":       "stored via smoke test",
				"subject":    "smoke",
			},
		},
	})
	resp = handle.read(t)
	if got := resp["id"]; got != float64(2) {
		t.Fatalf("tools/call id = %v, want 2", got)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call result type = %T", resp["result"])
	}
	sc, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent type = %T", result["structuredContent"])
	}
	if sc["request_id"] != "req_store" {
		t.Fatalf("request_id = %v, want req_store", sc["request_id"])
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("api method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/memory/store" {
		t.Fatalf("api path = %s, want /v1/memory/store", gotPath)
	}
	if gotBody.Visibility != "shared" || gotBody.Namespace != "team/dev" || gotBody.Body != "stored via smoke test" {
		t.Fatalf("api body = %#v", gotBody)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(cwd, "..", ".."))
}

type memoryMCPHandle struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	reader    *bufio.Reader
	stderrBuf *bytes.Buffer
}

func startMemoryMCP(t *testing.T, configPath string) *memoryMCPHandle {
	t.Helper()
	bin := buildMemoryMCPBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bin, "--config", configPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	var stderrBuf bytes.Buffer
	go func() {
		_, _ = stderrBuf.ReadFrom(stderr)
	}()

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	return &memoryMCPHandle{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		reader:    bufio.NewReader(stdout),
		stderrBuf: &stderrBuf,
	}
}

func (h *memoryMCPHandle) send(t *testing.T, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(h.stdin, "%s\n", raw); err != nil {
		t.Fatal(err)
	}
}

func (h *memoryMCPHandle) read(t *testing.T) map[string]any {
	t.Helper()
	line, err := readLine(h.reader, 15*time.Second)
	if err != nil {
		t.Fatalf("read response: %v\nstderr:\n%s", err, h.stderrBuf.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response: %v\nline=%s\nstderr:\n%s", err, string(line), h.stderrBuf.String())
	}
	return resp
}

func smokeConfigPath(t *testing.T, apiBaseURL string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	keyPath := testenv.WriteSeedFile(t, dir, "mcp-dev")
	content := fmt.Sprintf(`peer_id: "mcp-dev"
database_path: %q
signing_key_path: %q
namespaces:
  - "test"
transport:
  discovery_profile: "http-dev"
  relay_profile: "none"
api:
  listen_addr: "127.0.0.1:3099"
  base_url: %q
sync:
  listen_addr: "127.0.0.1:3199"
  public_url: "http://127.0.0.1:3199"
  interval_ms: 3000
  batch_limit: 256
  once_timeout_ms: 5000
extensions:
  crsqlite_path: %q
  sqlite_vec_path: %q
`, filepath.Join(dir, "agent_memory.sqlite"), keyPath, apiBaseURL, testenv.CRSQLitePath(t), testenv.SQLiteVecPath())
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func buildMemoryMCPBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "/opt/homebrew/bin/go"
	}
	if _, err := os.Stat(goBin); err != nil {
		t.Fatalf("go binary not found at %s: %v", goBin, err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "memory-mcp")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, goBin, "build", "-tags", "sqlite_fts5", "-o", binPath, "./cmd/memory-mcp")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build memory-mcp: %v\n%s", err, string(out))
	}
	return binPath
}

func readLine(r io.Reader, timeout time.Duration) ([]byte, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		reader, ok := r.(*bufio.Reader)
		if !ok {
			reader = bufio.NewReader(r)
		}
		line, err := reader.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-deadline.C:
		return nil, fmt.Errorf("timed out after %s", timeout)
	}
}

func TestToolDescriptionsExplainWorkflowBoundaries(t *testing.T) {
	tools := toolDefinitions()
	descriptions := map[string]string{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		description, _ := tool["description"].(string)
		descriptions[name] = description
	}

	cases := []struct {
		name          string
		useCaseCue    string
		negativeCue   string
		mutabilityCue string
		toolRefs      []string
	}{
		{
			name:          "memory.store",
			useCaseCue:    "durable",
			negativeCue:   "Do not use this for search",
			mutabilityCue: "Create a new structured memory entry",
			toolRefs:      []string{"memory.recall", "memory.promote", "memory.publish", "memory.supersede"},
		},
		{
			name:          "memory.recall",
			useCaseCue:    "Search existing transcript",
			negativeCue:   "Prefer context.build instead",
			mutabilityCue: "read-only",
			toolRefs:      []string{"context.build"},
		},
		{
			name:          "context.build",
			useCaseCue:    "answer-ready context bundle",
			negativeCue:   "Prefer this over memory.recall",
			mutabilityCue: "do not use it to create or modify memory",
			toolRefs:      []string{"memory.recall"},
		},
		{
			name:          "memory.candidates.list",
			useCaseCue:    "promotion candidates for review",
			negativeCue:   "does not create memory",
			mutabilityCue: "read-only",
			toolRefs:      []string{"memory.promote", "memory.candidates.approve", "memory.candidates.reject"},
		},
		{
			name:          "memory.candidates.approve",
			useCaseCue:    "pending promotion candidate",
			negativeCue:   "Do not use this for direct transcript promotion",
			mutabilityCue: "materialize it as private structured memory",
			toolRefs:      []string{"memory.promote", "memory.publish"},
		},
		{
			name:          "memory.candidates.reject",
			useCaseCue:    "pending promotion candidate",
			negativeCue:   "does not alter existing memories",
			mutabilityCue: "updates candidate review state",
			toolRefs:      []string{"memory.candidates.approve"},
		},
		{
			name:          "memory.promote",
			useCaseCue:    "transcript chunks",
			negativeCue:   "Do not use this for direct memory authoring",
			mutabilityCue: "private structured memory",
			toolRefs:      []string{"memory.store", "memory.publish"},
		},
		{
			name:          "memory.publish",
			useCaseCue:    "private structured memory",
			negativeCue:   "Do not use it for first-time creation from scratch",
			mutabilityCue: "creates a shared copy",
			toolRefs:      []string{"memory.store", "memory.promote"},
		},
		{
			name:          "memory.supersede",
			useCaseCue:    "history should remain traceable",
			negativeCue:   "Do not use this for brand-new memory",
			mutabilityCue: "marking the prior one superseded",
			toolRefs:      []string{"memory.store"},
		},
		{
			name:          "memory.signal",
			useCaseCue:    "review or trust signal",
			negativeCue:   "Do not use this to correct the claim itself",
			mutabilityCue: "mutates signal state",
			toolRefs:      []string{"memory.supersede"},
		},
		{
			name:          "memory.explain",
			useCaseCue:    "why a specific memory was retrieved",
			negativeCue:   "do not use it as a general search tool",
			mutabilityCue: "read-only",
			toolRefs:      []string{"memory.recall", "context.build", "memory.trace_decision"},
		},
		{
			name:          "memory.trace_decision",
			useCaseCue:    "support graph",
			negativeCue:   "do not use it for broad retrieval",
			mutabilityCue: "read-only",
			toolRefs:      []string{"memory.explain"},
		},
		{
			name:          "memory.sync_status",
			useCaseCue:    "operational diagnostics",
			negativeCue:   "does not fetch memory content",
			mutabilityCue: "read-only",
		},
	}

	for _, tc := range cases {
		description := descriptions[tc.name]
		if description == "" {
			t.Fatalf("missing description for %s", tc.name)
		}
		for _, want := range []string{tc.useCaseCue, tc.negativeCue, tc.mutabilityCue} {
			if !strings.Contains(description, want) {
				t.Fatalf("description for %s = %q, want substring %q", tc.name, description, want)
			}
		}
		for _, toolRef := range tc.toolRefs {
			if !strings.Contains(description, toolRef) {
				t.Fatalf("description for %s = %q, want tool reference %q", tc.name, description, toolRef)
			}
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

func TestContextBuildToolCallsHTTP(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody contextBuildHTTPRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			OK: true,
			Data: map[string]any{
				"active_private_decisions": []map[string]any{},
				"shared_constraints":       []map[string]any{},
				"recent_discussions":       []map[string]any{},
				"rejected_options":         []map[string]any{},
				"open_tasks":               []map[string]any{},
				"artifacts":                []map[string]any{{"uri": "docs/architecture.md", "title": "architecture.md"}},
			},
			Warnings:  []string{},
			RequestID: "req_context",
		})
	}))
	t.Cleanup(server.Close)

	resp := handle(config.Config{API: config.API{BaseURL: server.URL}}, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustJSON(t, map[string]any{
			"name": "context.build",
			"arguments": map[string]any{
				"query":             "sync policy",
				"namespace":         "team/dev",
				"limit_per_section": 3,
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %#v", resp.Error)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/context/build" {
		t.Fatalf("path = %s, want /v1/context/build", gotPath)
	}
	if gotBody.Query != "sync policy" || gotBody.Namespace != "team/dev" || gotBody.LimitPerSection != 3 {
		t.Fatalf("body = %#v", gotBody)
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
