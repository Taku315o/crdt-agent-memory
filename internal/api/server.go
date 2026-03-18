package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/storage"
)

type Server struct {
	Memory   *memory.Service
	Sync     *memsync.Service
	Meta     storage.Metadata
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/diag", s.handleDiag)
	mux.HandleFunc("/v1/memory/store", s.handleStore)
	mux.HandleFunc("/v1/memory/recall", s.handleRecall)
	mux.HandleFunc("/v1/memory/supersede", s.handleSupersede)
	mux.HandleFunc("/v1/sync/status", s.handleSyncStatus)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	diag, err := s.Sync.Diagnostics(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(diag)
}

func (s *Server) handleStore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Visibility    memory.Visibility `json:"visibility"`
		Namespace     string            `json:"namespace"`
		MemoryType    string            `json:"memory_type"`
		Scope         string            `json:"scope"`
		Subject       string            `json:"subject"`
		Body          string            `json:"body"`
		SourceURI     string            `json:"source_uri"`
		SourceHash    string            `json:"source_hash"`
		AuthorAgentID string            `json:"author_agent_id"`
		OriginPeerID  string            `json:"origin_peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Memory.Store(r.Context(), memory.StoreRequest{
		Visibility:    req.Visibility,
		Namespace:     req.Namespace,
		MemoryType:    req.MemoryType,
		Scope:         req.Scope,
		Subject:       req.Subject,
		Body:          req.Body,
		SourceURI:     req.SourceURI,
		SourceHash:    req.SourceHash,
		AuthorAgentID: req.AuthorAgentID,
		OriginPeerID:  req.OriginPeerID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"memory_id": id})
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req memory.RecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	results, err := s.Memory.Recall(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) handleSupersede(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		OldMemoryID string              `json:"old_memory_id"`
		Request     memory.StoreRequest `json:"request"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Memory.Supersede(r.Context(), req.OldMemoryID, req.Request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"memory_id": id})
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if namespace == "" {
		http.Error(w, "namespace is required", http.StatusBadRequest)
		return
	}
	status, err := s.Sync.SyncStatus(r.Context(), namespace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(status)
}

func New(ctx context.Context, db *sql.DB, meta storage.Metadata, sync *memsync.Service) (*Server, error) {
	return &Server{
		Memory: memory.NewService(db),
		Sync:   sync,
		Meta:   meta,
	}, nil
}
