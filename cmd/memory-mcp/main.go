package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"crdt-agent-memory/internal/config"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type apiEnvelope struct {
	OK        bool      `json:"ok"`
	Data      any       `json:"data,omitempty"`
	Error     *apiError `json:"error,omitempty"`
	Warnings  []string  `json:"warnings"`
	RequestID string    `json:"request_id"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type storeToolRequest struct {
	MemoryID      string                    `json:"memory_id,omitempty"`
	Visibility    string                    `json:"visibility"`
	Namespace     string                    `json:"namespace"`
	MemoryType    string                    `json:"memory_type,omitempty"`
	Scope         string                    `json:"scope,omitempty"`
	Subject       string                    `json:"subject,omitempty"`
	Body          string                    `json:"body"`
	SourceURI     string                    `json:"source_uri,omitempty"`
	SourceHash    string                    `json:"source_hash,omitempty"`
	AuthorAgentID string                    `json:"author_agent_id,omitempty"`
	OriginPeerID  string                    `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64                     `json:"authored_at_ms,omitempty"`
	ArtifactSpans []artifactSpanToolRequest `json:"artifact_spans,omitempty"`
	Relations     []relationToolRequest     `json:"relations,omitempty"`
}

type recallToolRequest struct {
	Query             string   `json:"query"`
	Namespace         string   `json:"namespace,omitempty"`
	Namespaces        []string `json:"namespaces,omitempty"`
	TopK              int      `json:"top_k,omitempty"`
	IncludePrivate    bool     `json:"include_private,omitempty"`
	IncludeShared     bool     `json:"include_shared,omitempty"`
	IncludeTranscript bool     `json:"include_transcript,omitempty"`
	ProjectKey        string   `json:"project_key,omitempty"`
	BranchName        string   `json:"branch_name,omitempty"`
	UnitKinds         []string `json:"unit_kinds,omitempty"`
	SourceTypes       []string `json:"source_types,omitempty"`
	Limit             int      `json:"limit,omitempty"`
}

type contextBuildToolRequest struct {
	Query           string   `json:"query"`
	Namespace       string   `json:"namespace,omitempty"`
	Namespaces      []string `json:"namespaces,omitempty"`
	ProjectKey      string   `json:"project_key,omitempty"`
	BranchName      string   `json:"branch_name,omitempty"`
	LimitPerSection int      `json:"limit_per_section,omitempty"`
}

type promoteToolRequest struct {
	ChunkIDs      []string `json:"chunk_ids"`
	MemoryType    string   `json:"memory_type,omitempty"`
	Subject       string   `json:"subject,omitempty"`
	Namespace     string   `json:"namespace"`
	AuthorAgentID string   `json:"author_agent_id,omitempty"`
	OriginPeerID  string   `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64    `json:"authored_at_ms,omitempty"`
	SourceURI     string   `json:"source_uri,omitempty"`
}

type listCandidatesToolRequest struct {
	Namespace  string `json:"namespace,omitempty"`
	Status     string `json:"status,omitempty"`
	ProjectKey string `json:"project_key,omitempty"`
	BranchName string `json:"branch_name,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type approveCandidateToolRequest struct {
	CandidateID   string `json:"candidate_id"`
	MemoryType    string `json:"memory_type,omitempty"`
	Subject       string `json:"subject,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	AuthorAgentID string `json:"author_agent_id,omitempty"`
	OriginPeerID  string `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64  `json:"authored_at_ms,omitempty"`
	SourceURI     string `json:"source_uri,omitempty"`
}

type rejectCandidateToolRequest struct {
	CandidateID string `json:"candidate_id"`
	ReviewNote  string `json:"review_note,omitempty"`
}

type publishToolRequest struct {
	PrivateMemoryID string `json:"private_memory_id"`
	RedactionPolicy string `json:"redaction_policy,omitempty"`
}

type memoryRef struct {
	MemorySpace string `json:"memory_space"`
	MemoryID    string `json:"memory_id"`
}

type supersedeToolRequest struct {
	OldMemoryID  string           `json:"old_memory_id,omitempty"`
	OldMemoryRef memoryRef        `json:"old_memory_ref,omitempty"`
	Request      storeToolRequest `json:"request"`
}

type signalToolRequest struct {
	MemoryRef     memoryRef `json:"memory_ref"`
	SignalType    string    `json:"signal_type"`
	Value         float64   `json:"value"`
	Reason        string    `json:"reason,omitempty"`
	AuthorAgentID string    `json:"author_agent_id,omitempty"`
	OriginPeerID  string    `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64     `json:"authored_at_ms,omitempty"`
}

type explainToolRequest struct {
	MemoryRef memoryRef `json:"memory_ref"`
	Query     string    `json:"query"`
}

type traceDecisionToolRequest struct {
	MemoryRef memoryRef `json:"memory_ref"`
	Depth     int       `json:"depth,omitempty"`
}

type artifactSpanToolRequest struct {
	ArtifactID  string `json:"artifact_id,omitempty"`
	URI         string `json:"uri,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	Title       string `json:"title,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	StartOffset int    `json:"start_offset,omitempty"`
	EndOffset   int    `json:"end_offset,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	QuoteHash   string `json:"quote_hash,omitempty"`
}

type relationToolRequest struct {
	RelationType string  `json:"relation_type"`
	ToMemoryID   string  `json:"to_memory_id"`
	Weight       float64 `json:"weight,omitempty"`
}

var apiClient = &http.Client{Timeout: 10 * time.Second}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.Parse()
	if configPath == "" {
		log.Fatal("--config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		req, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Fatal(err)
		}
		var rpcReq rpcRequest
		if err := json.Unmarshal(req, &rpcReq); err != nil {
			writeMessage(rpcResponse{JSONRPC: "2.0", Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		resp := handle(cfg, rpcReq)
		if rpcReq.ID != nil {
			writeMessage(resp)
		}
	}
}

func handle(cfg config.Config, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "memory-mcp",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}
	case "notifications/initialized":
		return rpcResponse{}
	case "tools/list":
		resp.Result = map[string]any{
			"tools": toolDefinitions(),
		}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = map[string]any{"code": -32602, "message": err.Error()}
			return resp
		}
		result, err := callTool(cfg, params)
		if err != nil {
			resp.Error = err
			return resp
		}
		resp.Result = result
	default:
		resp.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	return resp
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "memory.store",
			"description": "Create a new structured memory entry. Use this when the agent has a durable fact, decision, task, or observation worth preserving directly as private or shared memory. Do not use this for search, transcript-to-memory promotion, publishing private memory to shared memory, or revising an existing memory; use memory.recall, memory.promote, memory.publish, or memory.supersede for those cases.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"memory_id":       map[string]any{"type": "string"},
					"visibility":      map[string]any{"type": "string", "enum": []string{"private", "shared"}},
					"namespace":       map[string]any{"type": "string"},
					"memory_type":     map[string]any{"type": "string"},
					"scope":           map[string]any{"type": "string"},
					"subject":         map[string]any{"type": "string"},
					"body":            map[string]any{"type": "string"},
					"source_uri":      map[string]any{"type": "string"},
					"source_hash":     map[string]any{"type": "string"},
					"author_agent_id": map[string]any{"type": "string"},
					"origin_peer_id":  map[string]any{"type": "string"},
					"authored_at_ms":  map[string]any{"type": "integer"},
					"artifact_spans": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"artifact_id":  map[string]any{"type": "string"},
								"uri":          map[string]any{"type": "string"},
								"content_hash": map[string]any{"type": "string"},
								"title":        map[string]any{"type": "string"},
								"mime_type":    map[string]any{"type": "string"},
								"start_offset": map[string]any{"type": "integer"},
								"end_offset":   map[string]any{"type": "integer"},
								"start_line":   map[string]any{"type": "integer"},
								"end_line":     map[string]any{"type": "integer"},
								"quote_hash":   map[string]any{"type": "string"},
							},
						},
					},
					"relations": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"relation_type": map[string]any{"type": "string", "enum": []string{"supports", "contradicts", "derived_from", "about", "caused_by", "references"}},
								"to_memory_id":  map[string]any{"type": "string"},
								"weight":        map[string]any{"type": "number"},
							},
							"required": []string{"relation_type", "to_memory_id"},
						},
					},
				},
				"required": []string{"visibility", "namespace", "body"},
			},
		},
		{
			"name":        "memory.recall",
			"description": "Search existing transcript, private memory, and shared memory for relevant prior knowledge. Use this before answering or acting when you need to retrieve what is already known. This is read-only and does not create, publish, or update memory. Prefer context.build instead when you need a packed answer-ready bundle rather than raw ranked hits.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":              map[string]any{"type": "string"},
					"namespace":          map[string]any{"type": "string"},
					"namespaces":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"top_k":              map[string]any{"type": "integer"},
					"include_private":    map[string]any{"type": "boolean"},
					"include_shared":     map[string]any{"type": "boolean"},
					"include_transcript": map[string]any{"type": "boolean"},
					"project_key":        map[string]any{"type": "string"},
					"branch_name":        map[string]any{"type": "string"},
					"unit_kinds":         map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"decision", "rationale", "qa_pair", "fact", "task", "note"}}},
					"source_types":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"transcript_chunk", "private_memory", "shared_memory"}}},
					"limit":              map[string]any{"type": "integer"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "context.build",
			"description": "Build an answer-ready context bundle from transcript, private memory, and shared memory. Use this when the agent is about to respond, reason, or plan and wants organized sections such as active private decisions, shared constraints, and recent discussions. Prefer this over memory.recall when raw search hits would need extra packing; do not use it to create or modify memory.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":             map[string]any{"type": "string"},
					"namespace":         map[string]any{"type": "string"},
					"namespaces":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"project_key":       map[string]any{"type": "string"},
					"branch_name":       map[string]any{"type": "string"},
					"limit_per_section": map[string]any{"type": "integer"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "memory.candidates.list",
			"description": "List transcript-derived promotion candidates for review. Use this when promotion is review-driven and you want to inspect pending, approved, or rejected candidates before deciding what to materialize. This is read-only and does not create memory; use memory.promote for direct promotion from chosen chunk IDs, memory.candidates.approve to materialize a candidate, and memory.candidates.reject to dismiss one.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":   map[string]any{"type": "string"},
					"status":      map[string]any{"type": "string", "enum": []string{"pending", "approved", "rejected"}},
					"project_key": map[string]any{"type": "string"},
					"branch_name": map[string]any{"type": "string"},
					"limit":       map[string]any{"type": "integer"},
				},
			},
		},
		{
			"name":        "memory.candidates.approve",
			"description": "Approve a pending promotion candidate and materialize it as private structured memory. Use this when a candidate already exists in the review queue and should become durable private memory. Do not use this for direct transcript promotion without a candidate or for sharing to peers; use memory.promote for direct chunk-based promotion and memory.publish after review if the resulting private memory should become shared.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"candidate_id":    map[string]any{"type": "string"},
					"memory_type":     map[string]any{"type": "string"},
					"subject":         map[string]any{"type": "string"},
					"namespace":       map[string]any{"type": "string"},
					"author_agent_id": map[string]any{"type": "string"},
					"origin_peer_id":  map[string]any{"type": "string"},
					"authored_at_ms":  map[string]any{"type": "integer"},
					"source_uri":      map[string]any{"type": "string"},
				},
				"required": []string{"candidate_id"},
			},
		},
		{
			"name":        "memory.candidates.reject",
			"description": "Reject a pending promotion candidate without creating memory. Use this when a transcript-derived candidate should not become durable memory. This only updates candidate review state and does not alter existing memories. Use memory.candidates.approve instead if the candidate should materialize as private memory.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"candidate_id": map[string]any{"type": "string"},
					"review_note":  map[string]any{"type": "string"},
				},
				"required": []string{"candidate_id"},
			},
		},
		{
			"name":        "memory.promote",
			"description": "Promote transcript chunks into a new private structured memory with provenance. Use this when useful knowledge currently lives in transcript history and should become durable private memory. Do not use this for direct memory authoring or for sharing to peers; use memory.store for direct creation and memory.publish to move vetted private memory into shared memory.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"chunk_ids":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"memory_type":     map[string]any{"type": "string", "description": "Optional label for the promoted memory such as decision, rationale, task, note, or fact. Defaults to note when omitted."},
					"subject":         map[string]any{"type": "string"},
					"namespace":       map[string]any{"type": "string"},
					"author_agent_id": map[string]any{"type": "string"},
					"origin_peer_id":  map[string]any{"type": "string"},
					"authored_at_ms":  map[string]any{"type": "integer"},
					"source_uri":      map[string]any{"type": "string"},
				},
				"required": []string{"chunk_ids", "namespace"},
			},
		},
		{
			"name":        "memory.publish",
			"description": "Publish an existing private structured memory as shared memory. Use this after a private memory has been reviewed or deemed worth sharing with other peers. This preserves the private original and creates a shared copy, optionally with redaction. Do not use it for first-time creation from scratch or transcript promotion; use memory.store or memory.promote for those cases.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"private_memory_id": map[string]any{"type": "string"},
					"redaction_policy":  map[string]any{"type": "string", "enum": []string{"default", "strict"}},
				},
				"required": []string{"private_memory_id"},
			},
		},
		{
			"name":        "memory.supersede",
			"description": "Revise an existing shared memory by creating a replacement and linking it as superseding the older claim. Use this when a shared memory is outdated, corrected, or refined and history should remain traceable. Do not use this for brand-new memory or private memory updates; use memory.store for new entries. This mutates shared memory state by adding a new claim and marking the prior one superseded.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"old_memory_id": map[string]any{"type": "string"},
					"old_memory_ref": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"memory_space": map[string]any{"type": "string", "enum": []string{"shared"}},
							"memory_id":    map[string]any{"type": "string"},
						},
					},
					"request": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"memory_id":       map[string]any{"type": "string"},
							"visibility":      map[string]any{"type": "string", "enum": []string{"shared"}},
							"namespace":       map[string]any{"type": "string"},
							"memory_type":     map[string]any{"type": "string"},
							"scope":           map[string]any{"type": "string"},
							"subject":         map[string]any{"type": "string"},
							"body":            map[string]any{"type": "string"},
							"source_uri":      map[string]any{"type": "string"},
							"source_hash":     map[string]any{"type": "string"},
							"author_agent_id": map[string]any{"type": "string"},
							"origin_peer_id":  map[string]any{"type": "string"},
							"authored_at_ms":  map[string]any{"type": "integer"},
						},
						"required": []string{"namespace", "body"},
					},
				},
				"required": []string{"request"},
			},
		},
		{
			"name":        "memory.signal",
			"description": "Attach a lightweight review or trust signal to an existing private or shared memory. Use this for reinforcement, deprecation, confirmation, denial, pinning, or bookmarking when you want to influence ranking or downstream interpretation without rewriting the memory body. Do not use this to correct the claim itself; use memory.supersede for content revisions. This mutates signal state but leaves the original memory text unchanged.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"memory_ref": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"memory_space": map[string]any{"type": "string", "enum": []string{"private", "shared"}},
							"memory_id":    map[string]any{"type": "string"},
						},
						"required": []string{"memory_space", "memory_id"},
					},
					"signal_type":     map[string]any{"type": "string", "enum": []string{"reinforce", "deprecate", "confirm", "deny", "pin", "bookmark"}},
					"value":           map[string]any{"type": "number"},
					"reason":          map[string]any{"type": "string"},
					"author_agent_id": map[string]any{"type": "string"},
					"origin_peer_id":  map[string]any{"type": "string"},
					"authored_at_ms":  map[string]any{"type": "integer"},
				},
				"required": []string{"memory_ref", "signal_type", "value"},
			},
		},
		{
			"name":        "memory.explain",
			"description": "Explain why a specific memory was retrieved or considered relevant for a query. Use this after memory.recall, context.build, or manual inspection when you want ranking, provenance, and trust diagnostics for one memory. This is read-only and diagnostic; do not use it as a general search tool, and prefer memory.trace_decision when you need graph neighbors and artifact lineage rather than retrieval scoring.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"memory_ref": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"memory_space": map[string]any{"type": "string", "enum": []string{"private", "shared"}},
							"memory_id":    map[string]any{"type": "string"},
						},
						"required": []string{"memory_space", "memory_id"},
					},
					"query": map[string]any{"type": "string"},
				},
				"required": []string{"memory_ref", "query"},
			},
		},
		{
			"name":        "memory.trace_decision",
			"description": "Trace the support graph, contradictions, artifacts, and transcript provenance around a specific decision memory. Use this when you need to inspect why a decision is connected to other memories or source material. This is read-only and graph-oriented; do not use it for broad retrieval, and prefer memory.explain when you need scoring and trust diagnostics for a single retrieved item.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"memory_ref": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"memory_space": map[string]any{"type": "string", "enum": []string{"private", "shared"}},
							"memory_id":    map[string]any{"type": "string"},
						},
						"required": []string{"memory_space", "memory_id"},
					},
					"depth": map[string]any{"type": "integer"},
				},
				"required": []string{"memory_ref"},
			},
		},
		{
			"name":        "memory.sync_status",
			"description": "Return local shared-memory sync health for a namespace. Use this for operational diagnostics when a caller needs to know whether peer replication is healthy, degraded, or schema-fenced. This is read-only and does not fetch memory content or repair anything.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace": map[string]any{"type": "string"},
				},
				"required": []string{"namespace"},
			},
		},
	}
}

func callTool(cfg config.Config, params toolCallParams) (any, map[string]any) {
	switch params.Name {
	case "memory.sync_status":
		var args struct {
			Namespace string `json:"namespace"`
		}
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.Namespace) == "" {
			return nil, map[string]any{"code": -32602, "message": "namespace is required"}
		}
		payload, err := callAPI(cfg, http.MethodGet, "/v1/sync/status", url.Values{"namespace": []string{args.Namespace}}, nil)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.store":
		var args storeToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.Visibility) == "" {
			return nil, map[string]any{"code": -32602, "message": "visibility is required"}
		}
		if strings.TrimSpace(args.Namespace) == "" {
			return nil, map[string]any{"code": -32602, "message": "namespace is required"}
		}
		if strings.TrimSpace(args.Body) == "" {
			return nil, map[string]any{"code": -32602, "message": "body is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/store", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.recall":
		var args recallToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.Query) == "" {
			return nil, map[string]any{"code": -32602, "message": "query is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/recall", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "context.build":
		var args contextBuildToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.Query) == "" {
			return nil, map[string]any{"code": -32602, "message": "query is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/context/build", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.promote":
		var args promoteToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if len(args.ChunkIDs) == 0 || strings.TrimSpace(args.Namespace) == "" {
			return nil, map[string]any{"code": -32602, "message": "chunk_ids and namespace are required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/promote", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.candidates.list":
		var args listCandidatesToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		values := url.Values{}
		if strings.TrimSpace(args.Namespace) != "" {
			values.Set("namespace", args.Namespace)
		}
		if strings.TrimSpace(args.Status) != "" {
			values.Set("status", args.Status)
		}
		if strings.TrimSpace(args.ProjectKey) != "" {
			values.Set("project_key", args.ProjectKey)
		}
		if strings.TrimSpace(args.BranchName) != "" {
			values.Set("branch_name", args.BranchName)
		}
		if args.Limit > 0 {
			values.Set("limit", strconv.Itoa(args.Limit))
		}
		payload, err := callAPI(cfg, http.MethodGet, "/v1/memory/candidates", values, nil)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.candidates.approve":
		var args approveCandidateToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.CandidateID) == "" {
			return nil, map[string]any{"code": -32602, "message": "candidate_id is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/candidates/approve", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.candidates.reject":
		var args rejectCandidateToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.CandidateID) == "" {
			return nil, map[string]any{"code": -32602, "message": "candidate_id is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/candidates/reject", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.publish":
		var args publishToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.PrivateMemoryID) == "" {
			return nil, map[string]any{"code": -32602, "message": "private_memory_id is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/publish", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.supersede":
		var args supersedeToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.OldMemoryID) == "" && strings.TrimSpace(args.OldMemoryRef.MemoryID) == "" {
			return nil, map[string]any{"code": -32602, "message": "old_memory_id is required"}
		}
		if strings.TrimSpace(args.Request.Namespace) == "" {
			return nil, map[string]any{"code": -32602, "message": "request.namespace is required"}
		}
		if strings.TrimSpace(args.Request.Body) == "" {
			return nil, map[string]any{"code": -32602, "message": "request.body is required"}
		}
		if err := validateToolMemoryRef(args.OldMemoryRef, false); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/supersede", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.signal":
		var args signalToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if err := validateToolMemoryRef(args.MemoryRef, true); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.SignalType) == "" {
			return nil, map[string]any{"code": -32602, "message": "signal_type is required"}
		}
		if args.Value <= 0 {
			return nil, map[string]any{"code": -32602, "message": "value must be greater than 0"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/signal", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.explain":
		var args explainToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if err := validateToolMemoryRef(args.MemoryRef, true); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.Query) == "" {
			return nil, map[string]any{"code": -32602, "message": "query is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/explain", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	case "memory.trace_decision":
		var args traceDecisionToolRequest
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, map[string]any{"code": -32602, "message": err.Error()}
		}
		if strings.TrimSpace(args.MemoryRef.MemorySpace) == "" || strings.TrimSpace(args.MemoryRef.MemoryID) == "" {
			return nil, map[string]any{"code": -32602, "message": "memory_ref is required"}
		}
		payload, err := callAPI(cfg, http.MethodPost, "/v1/memory/trace_decision", nil, args)
		if err != nil {
			return nil, rpcErrorFromEnvelope(payload, err)
		}
		return toolResultFromEnvelope(payload), nil
	default:
		return nil, map[string]any{"code": -32601, "message": "unknown tool"}
	}
}

func validateToolMemoryRef(ref memoryRef, required bool) error {
	if strings.TrimSpace(ref.MemoryID) == "" {
		if required {
			return fmt.Errorf("memory_ref.memory_id is required")
		}
		if strings.TrimSpace(ref.MemorySpace) != "" {
			return fmt.Errorf("old_memory_ref.memory_id is required")
		}
		return nil
	}
	if strings.TrimSpace(ref.MemorySpace) == "" {
		if required {
			return fmt.Errorf("memory_ref.memory_space is required")
		}
		return fmt.Errorf("old_memory_ref.memory_space is required")
	}
	if ref.MemorySpace != "shared" && ref.MemorySpace != "private" {
		if required {
			return fmt.Errorf("memory_ref.memory_space must be shared or private")
		}
		return fmt.Errorf("old_memory_ref.memory_space must be shared or private")
	}
	return nil
}

func decodeArguments(arguments map[string]any, dst any) error {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func callAPI(cfg config.Config, method, path string, query url.Values, body any) (apiEnvelope, error) {
	var payload apiEnvelope
	base := strings.TrimRight(cfg.API.BaseURL, "/")
	fullURL := base + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return apiEnvelope{}, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, fullURL, reader)
	if err != nil {
		return apiEnvelope{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return apiEnvelope{}, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return apiEnvelope{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest || !payload.OK {
		return payload, fmt.Errorf("request failed")
	}
	return payload, nil
}

func toolResultFromEnvelope(payload apiEnvelope) map[string]any {
	data := payload.Data
	if data == nil {
		data = map[string]any{}
	}
	text, _ := json.Marshal(data)
	return map[string]any{
		"structuredContent": map[string]any{
			"ok":         true,
			"data":       data,
			"warnings":   payload.Warnings,
			"request_id": payload.RequestID,
		},
		"content": []map[string]any{
			{"type": "text", "text": string(text)},
		},
	}
}

func rpcErrorFromEnvelope(payload apiEnvelope, err error) map[string]any {
	message := err.Error()
	if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		message = payload.Error.Message
	}
	out := map[string]any{
		"code":    -32000,
		"message": message,
	}
	details := map[string]any{}
	if payload.RequestID != "" {
		details["request_id"] = payload.RequestID
	}
	if len(payload.Warnings) > 0 {
		details["warnings"] = payload.Warnings
	}
	if payload.Error != nil {
		details["api_code"] = payload.Error.Code
		details["retryable"] = payload.Error.Retryable
		details["details"] = payload.Error.Details
	}
	if len(details) > 0 {
		out["data"] = details
	}
	return out
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length <= 0 {
		return nil, io.EOF
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeMessage(resp rpcResponse) {
	if resp.JSONRPC == "" && resp.Result == nil && resp.Error == nil {
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		log.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(raw))
	_, _ = buf.Write(raw)
	_, _ = os.Stdout.Write(buf.Bytes())
}
