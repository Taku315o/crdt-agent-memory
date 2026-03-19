package api

import (
	"strings"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/memsync"
)

type Envelope struct {
	OK        bool      `json:"ok"`
	Data      any       `json:"data,omitempty"`
	Error     *APIError `json:"error,omitempty"`
	Warnings  []string  `json:"warnings"`
	RequestID string    `json:"request_id"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
}

type MemoryRef struct {
	MemorySpace string `json:"memory_space"`
	MemoryID    string `json:"memory_id"`
}

type StoreRequest struct {
	MemoryID      string            `json:"memory_id,omitempty"`
	Visibility    memory.Visibility `json:"visibility"`
	Namespace     string            `json:"namespace"`
	MemoryType    string            `json:"memory_type,omitempty"`
	Scope         string            `json:"scope,omitempty"`
	Subject       string            `json:"subject,omitempty"`
	Body          string            `json:"body"`
	SourceURI     string            `json:"source_uri,omitempty"`
	SourceHash    string            `json:"source_hash,omitempty"`
	AuthorAgentID string            `json:"author_agent_id,omitempty"`
	OriginPeerID  string            `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64             `json:"authored_at_ms,omitempty"`
}

type StoreResponse struct {
	MemoryRef    MemoryRef `json:"memory_ref"`
	Indexed      bool      `json:"indexed"`
	SyncEligible bool      `json:"sync_eligible"`
}

type RecallRequest struct {
	Query          string   `json:"query"`
	Namespace      string   `json:"namespace,omitempty"`
	Namespaces     []string `json:"namespaces,omitempty"`
	TopK           int      `json:"top_k,omitempty"`
	IncludePrivate bool     `json:"include_private,omitempty"`
	Limit          int      `json:"limit,omitempty"`
}

type RecallItem struct {
	MemoryRef      MemoryRef `json:"memory_ref"`
	Namespace      string    `json:"namespace"`
	MemoryType     string    `json:"memory_type"`
	Subject        string    `json:"subject"`
	Body           string    `json:"body"`
	LifecycleState string    `json:"lifecycle_state"`
	AuthoredAtMS   int64     `json:"authored_at_ms"`
	SourceURI      string    `json:"source_uri"`
	SourceHash     string    `json:"source_hash"`
	OriginPeerID   string    `json:"origin_peer_id"`
}

type RecallResponse struct {
	Items []RecallItem `json:"items"`
}

type SupersedeRequest struct {
	OldMemoryID  string       `json:"old_memory_id,omitempty"`
	OldMemoryRef MemoryRef    `json:"old_memory_ref,omitempty"`
	Request      StoreRequest `json:"request"`
}

type SupersedeResponse struct {
	OldMemoryRef   MemoryRef `json:"old_memory_ref"`
	NewMemoryRef   MemoryRef `json:"new_memory_ref"`
	LifecycleState string    `json:"lifecycle_state"`
}

type SignalRequest struct {
	MemoryRef      MemoryRef `json:"memory_ref"`
	SignalType     string    `json:"signal_type"`
	Value          float64   `json:"value"`
	Reason         string    `json:"reason,omitempty"`
	AuthorAgentID  string    `json:"author_agent_id,omitempty"`
	OriginPeerID   string    `json:"origin_peer_id,omitempty"`
	AuthoredAtMS   int64     `json:"authored_at_ms,omitempty"`
}

type SignalResponse struct {
	SignalID string `json:"signal_id"`
}

type ExplainRequest struct {
	MemoryRef MemoryRef `json:"memory_ref"`
	Query     string    `json:"query"`
}

type ExplainScoreBreakdown struct {
	MatchedQuery   bool    `json:"matched_query"`
	RecallEligible bool    `json:"recall_eligible"`
	LexicalBM25    float64 `json:"lexical_bm25"`
	RankingBucket  int     `json:"ranking_bucket"`
	TrustWeight    float64 `json:"trust_weight"`
	AuthoredAtMS   int64   `json:"authored_at_ms"`
}

type ExplainTrustSummary struct {
	SignatureStatus string  `json:"signature_status"`
	SignatureDetail string  `json:"signature_detail"`
	PeerTrustState  string  `json:"peer_trust_state"`
	PeerTrustWeight float64 `json:"peer_trust_weight"`
	HasSigningKey   bool    `json:"has_signing_key"`
}

type ExplainSignalSummary struct {
	Count            int     `json:"count"`
	Sum              float64 `json:"sum"`
	LatestSignalAtMS int64   `json:"latest_signal_at_ms"`
}

type ExplainResponse struct {
	Provenance     memory.ExplainProvenance            `json:"provenance"`
	ScoreBreakdown ExplainScoreBreakdown               `json:"score_breakdown"`
	TrustSummary   ExplainTrustSummary                 `json:"trust_summary"`
	SignalSummary  map[string]ExplainSignalSummary     `json:"signal_summary"`
}

type SyncStatusPeer struct {
	PeerID          string  `json:"peer_id"`
	Namespace       string  `json:"namespace"`
	LastSeenAtMS    int64   `json:"last_seen_at_ms"`
	LastTransport   string  `json:"last_transport"`
	LastPathType    string  `json:"last_path_type"`
	LastError       *string `json:"last_error"`
	LastSuccessAtMS int64   `json:"last_success_at_ms"`
	SchemaFenced    bool    `json:"schema_fenced"`
}

type SyncStatusResponse struct {
	Namespace    string           `json:"namespace"`
	State        string           `json:"state"`
	SchemaFenced bool             `json:"schema_fenced"`
	Peers        []SyncStatusPeer `json:"peers"`
}

func NewEnvelope(requestID string, data any) Envelope {
	if requestID == "" {
		requestID = newRequestID()
	}
	return Envelope{
		OK:        true,
		Data:      data,
		Warnings:  []string{},
		RequestID: requestID,
	}
}

func NewErrorEnvelope(requestID, code, message string, retryable bool, details any) Envelope {
	if requestID == "" {
		requestID = newRequestID()
	}
	return Envelope{
		OK:        false,
		Error:     &APIError{Code: code, Message: message, Retryable: retryable, Details: details},
		Warnings:  []string{},
		RequestID: requestID,
	}
}

func NewRequestID() string {
	return newRequestID()
}

func newRequestID() string {
	return "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func (r StoreRequest) ToMemoryRequest() memory.StoreRequest {
	return memory.StoreRequest{
		MemoryID:      r.MemoryID,
		Visibility:    r.Visibility,
		Namespace:     r.Namespace,
		MemoryType:    r.MemoryType,
		Scope:         r.Scope,
		Subject:       r.Subject,
		Body:          r.Body,
		SourceURI:     r.SourceURI,
		SourceHash:    r.SourceHash,
		AuthorAgentID: r.AuthorAgentID,
		OriginPeerID:  r.OriginPeerID,
		AuthoredAtMS:  r.AuthoredAtMS,
	}
}

func (r SupersedeRequest) ToMemoryRequest() memory.StoreRequest {
	return r.Request.ToMemoryRequest()
}

func (r SupersedeRequest) OldID() string {
	if strings.TrimSpace(r.OldMemoryID) != "" {
		return r.OldMemoryID
	}
	return r.OldMemoryRef.MemoryID
}

func (r SignalRequest) ToMemoryRequest() memory.SignalRequest {
	return memory.SignalRequest{
		MemorySpace:   r.MemoryRef.MemorySpace,
		MemoryID:      r.MemoryRef.MemoryID,
		SignalType:    r.SignalType,
		Value:         r.Value,
		Reason:        r.Reason,
		AuthorAgentID: r.AuthorAgentID,
		OriginPeerID:  r.OriginPeerID,
		AuthoredAtMS:  r.AuthoredAtMS,
	}
}

func (r ExplainRequest) ToMemoryRequest() memory.ExplainRequest {
	return memory.ExplainRequest{
		MemorySpace: r.MemoryRef.MemorySpace,
		MemoryID:    r.MemoryRef.MemoryID,
		Query:       r.Query,
	}
}

func MemoryRefFromVisibility(visibility memory.Visibility, memoryID string) MemoryRef {
	memorySpace := string(visibility)
	if memorySpace == "" {
		memorySpace = "shared"
	}
	return MemoryRef{MemorySpace: memorySpace, MemoryID: memoryID}
}

func RecallItemFromResult(result memory.RecallResult) RecallItem {
	return RecallItem{
		MemoryRef: MemoryRef{
			MemorySpace: result.MemorySpace,
			MemoryID:    result.MemoryID,
		},
		Namespace:      result.Namespace,
		MemoryType:     result.MemoryType,
		Subject:        result.Subject,
		Body:           result.Body,
		LifecycleState: result.LifecycleState,
		AuthoredAtMS:   result.AuthoredAtMS,
		SourceURI:      result.SourceURI,
		SourceHash:     result.SourceHash,
		OriginPeerID:   result.OriginPeerID,
	}
}

func ExplainResponseFromResult(result memory.ExplainResult) ExplainResponse {
	signalSummary := make(map[string]ExplainSignalSummary, len(result.SignalSummary))
	for signalType, item := range result.SignalSummary {
		signalSummary[signalType] = ExplainSignalSummary{
			Count:            item.Count,
			Sum:              item.Sum,
			LatestSignalAtMS: item.LatestSignalAtMS,
		}
	}
	return ExplainResponse{
		Provenance: result.Provenance,
		ScoreBreakdown: ExplainScoreBreakdown{
			MatchedQuery:   result.ScoreBreakdown.MatchedQuery,
			RecallEligible: result.ScoreBreakdown.RecallEligible,
			LexicalBM25:    result.ScoreBreakdown.LexicalBM25,
			RankingBucket:  result.ScoreBreakdown.RankingBucket,
			TrustWeight:    result.ScoreBreakdown.TrustWeight,
			AuthoredAtMS:   result.ScoreBreakdown.AuthoredAtMS,
		},
		TrustSummary: ExplainTrustSummary{
			SignatureStatus: result.TrustSummary.SignatureStatus,
			SignatureDetail: result.TrustSummary.SignatureDetail,
			PeerTrustState:  result.TrustSummary.PeerTrustState,
			PeerTrustWeight: result.TrustSummary.PeerTrustWeight,
			HasSigningKey:   result.TrustSummary.HasSigningKey,
		},
		SignalSummary: signalSummary,
	}
}

func SyncStatusResponseFromService(status memsync.SyncStatus) SyncStatusResponse {
	peers := make([]SyncStatusPeer, 0, len(status.Peers))
	for _, peer := range status.Peers {
		peers = append(peers, SyncStatusPeer{
			PeerID:          peer.PeerID,
			Namespace:       peer.Namespace,
			LastSeenAtMS:    peer.LastSeenAtMS,
			LastTransport:   peer.LastTransport,
			LastPathType:    peer.LastPathType,
			LastError:       stringPtrOrNil(peer.LastError),
			LastSuccessAtMS: peer.LastSuccessAtMS,
			SchemaFenced:    peer.SchemaFenced,
		})
	}
	return SyncStatusResponse{
		Namespace:    status.Namespace,
		State:        status.State,
		SchemaFenced: status.SchemaFenced,
		Peers:        peers,
	}
}

func stringPtrOrNil(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}
