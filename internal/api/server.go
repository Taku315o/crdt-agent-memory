package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/scrubber"
	"crdt-agent-memory/internal/signing"
	"crdt-agent-memory/internal/storage"
)

type Server struct {
	Memory   *memory.Service
	Sync     *memsync.Service
	Scrubber *scrubber.Service
	Meta     storage.Metadata
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/diag", s.handleDiag)
	mux.HandleFunc("/v1/memory/store", s.handleStore)
	mux.HandleFunc("/v1/memory/recall", s.handleRecall)
	mux.HandleFunc("/v1/memory/supersede", s.handleSupersede)
	mux.HandleFunc("/v1/memory/signal", s.handleSignal)
	mux.HandleFunc("/v1/memory/explain", s.handleExplain)
	mux.HandleFunc("/v1/memory/trace_decision", s.handleTraceDecision)
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
	summary, err := s.Scrubber.Diagnose(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema_hash":       diag.SchemaHash,
		"crr_manifest_hash": diag.CRRManifestHash,
		"tracked_peers":     diag.TrackedPeers,
		"peer_states":       diag.PeerStates,
		"quarantine_count":  diag.QuarantineCount,
		"trust_summary":     summary.TrustSummary,
		"scrubber_summary":  summary.ScrubberSummary,
	})
}

func (s *Server) handleStore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "", "METHOD_NOT_ALLOWED", "method not allowed", false, nil)
		return
	}
	requestID := NewRequestID()
	var req StoreRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	id, err := s.Memory.Store(r.Context(), req.ToMemoryRequest())
	if err != nil {
		s.writeMemoryError(w, requestID, err)
		return
	}
	s.writeOK(w, requestID, StoreResponse{
		MemoryRef:    MemoryRefFromVisibility(req.Visibility, id),
		Indexed:      false,
		SyncEligible: req.Visibility == memory.VisibilityShared,
	})
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "", "METHOD_NOT_ALLOWED", "method not allowed", false, nil)
		return
	}
	requestID := NewRequestID()
	var req RecallRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	namespaces := append([]string{}, req.Namespaces...)
	if strings.TrimSpace(req.Namespace) != "" {
		namespaces = append(namespaces, req.Namespace)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = req.TopK
	}
	results, err := s.Memory.Recall(r.Context(), memory.RecallRequest{
		Query:          req.Query,
		Namespaces:     namespaces,
		IncludePrivate: req.IncludePrivate,
		Limit:          limit,
	})
	if err != nil {
		s.writeMemoryError(w, requestID, err)
		return
	}
	items := make([]RecallItem, 0, len(results))
	for _, item := range results {
		items = append(items, RecallItemFromResult(item))
	}
	s.writeOK(w, requestID, RecallResponse{Items: items})
}

func (s *Server) handleSupersede(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "", "METHOD_NOT_ALLOWED", "method not allowed", false, nil)
		return
	}
	requestID := NewRequestID()
	var req SupersedeRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	oldID := req.OldID()
	if strings.TrimSpace(oldID) == "" {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", "old_memory_id is required", false, nil)
		return
	}
	if req.OldMemoryRef.MemorySpace == string(memory.VisibilityPrivate) {
		s.writeError(w, http.StatusBadRequest, requestID, "PRIVATE_ONLY", memory.ErrPrivateOnly.Error(), false, nil)
		return
	}
	if req.OldMemoryRef.MemorySpace != "" && req.OldMemoryRef.MemorySpace != string(memory.VisibilityShared) && req.OldMemoryRef.MemorySpace != string(memory.VisibilityPrivate) {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", "old_memory_ref.memory_space must be shared or private", false, nil)
		return
	}
	id, err := s.Memory.Supersede(r.Context(), oldID, req.ToMemoryRequest())
	if err != nil {
		s.writeMemoryError(w, requestID, err)
		return
	}
	s.writeOK(w, requestID, SupersedeResponse{
		OldMemoryRef:   MemoryRef{MemorySpace: "shared", MemoryID: oldID},
		NewMemoryRef:   MemoryRef{MemorySpace: "shared", MemoryID: id},
		LifecycleState: "superseded",
	})
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "", "METHOD_NOT_ALLOWED", "method not allowed", false, nil)
		return
	}
	requestID := NewRequestID()
	var req SignalRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	if err := validateMemoryRef(req.MemoryRef); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	signalID, err := s.Memory.Signal(r.Context(), req.ToMemoryRequest())
	if err != nil {
		s.writeMemoryError(w, requestID, err)
		return
	}
	s.writeOK(w, requestID, SignalResponse{SignalID: signalID})
}

func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "", "METHOD_NOT_ALLOWED", "method not allowed", false, nil)
		return
	}
	requestID := NewRequestID()
	var req ExplainRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	if err := validateMemoryRef(req.MemoryRef); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	result, err := s.Memory.Explain(r.Context(), req.ToMemoryRequest())
	if err != nil {
		s.writeMemoryError(w, requestID, err)
		return
	}
	s.writeOK(w, requestID, ExplainResponseFromResult(result))
}

func (s *Server) handleTraceDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "", "METHOD_NOT_ALLOWED", "method not allowed", false, nil)
		return
	}
	requestID := NewRequestID()
	var req TraceDecisionRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	if err := validateMemoryRef(req.MemoryRef); err != nil {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
		return
	}
	result, err := s.Memory.TraceDecision(r.Context(), req.ToMemoryRequest())
	if err != nil {
		s.writeMemoryError(w, requestID, err)
		return
	}
	s.writeOK(w, requestID, TraceDecisionResponseFromResult(result))
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	requestID := NewRequestID()
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if namespace == "" {
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", "namespace is required", false, nil)
		return
	}
	status, err := s.Sync.SyncStatus(r.Context(), namespace)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, requestID, "INTERNAL_ERROR", err.Error(), true, nil)
		return
	}
	s.writeOK(w, requestID, SyncStatusResponseFromService(status))
}

func New(ctx context.Context, db *sql.DB, meta storage.Metadata, sync *memsync.Service, signer signing.Signer, selfPeerID string) (*Server, error) {
	publicKeyHex := ""
	if signer != nil {
		publicKeyHex = signer.PublicKeyHex()
	}
	return &Server{
		Memory:   memory.NewService(db, signer),
		Sync:     sync,
		Scrubber: scrubber.NewService(db, selfPeerID, publicKeyHex),
		Meta:     meta,
	}, nil
}

func decodeRequest(body io.Reader, dst any) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func (s *Server) writeOK(w http.ResponseWriter, requestID string, data any) {
	writeEnvelope(w, http.StatusOK, NewEnvelope(requestID, data))
}

func (s *Server) writeError(w http.ResponseWriter, status int, requestID, code, message string, retryable bool, details any) {
	writeEnvelope(w, status, NewErrorEnvelope(requestID, code, message, retryable, details))
}

func (s *Server) writeMemoryError(w http.ResponseWriter, requestID string, err error) {
	switch {
	case errors.Is(err, memory.ErrMemoryNotFound):
		s.writeError(w, http.StatusNotFound, requestID, "NOT_FOUND", err.Error(), false, nil)
	case errors.Is(err, memory.ErrPrivateOnly):
		s.writeError(w, http.StatusBadRequest, requestID, "PRIVATE_ONLY", err.Error(), false, nil)
	default:
		s.writeError(w, http.StatusBadRequest, requestID, "INVALID_ARGUMENT", err.Error(), false, nil)
	}
}

func validateMemoryRef(ref MemoryRef) error {
	if strings.TrimSpace(ref.MemoryID) == "" {
		return errors.New("memory_ref.memory_id is required")
	}
	switch ref.MemorySpace {
	case string(memory.VisibilityShared), string(memory.VisibilityPrivate):
		return nil
	default:
		return errors.New("memory_ref.memory_space must be shared or private")
	}
}

func writeEnvelope(w http.ResponseWriter, status int, payload Envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
