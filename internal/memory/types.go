package memory

import "errors"

type Visibility string

const (
	VisibilityShared  Visibility = "shared"
	VisibilityPrivate Visibility = "private"
)

type SignatureStatus string

const (
	SignatureStatusValid            SignatureStatus = "valid"
	SignatureStatusMissingSignature SignatureStatus = "missing_signature"
	SignatureStatusInvalidSignature SignatureStatus = "invalid_signature"
	SignatureStatusUnknownPeer      SignatureStatus = "unknown_peer"
)

type SignalType string

const (
	SignalTypeReinforce SignalType = "reinforce"
	SignalTypeDeprecate SignalType = "deprecate"
	SignalTypeConfirm   SignalType = "confirm"
	SignalTypeDeny      SignalType = "deny"
	SignalTypePin       SignalType = "pin"
	SignalTypeBookmark  SignalType = "bookmark"
)

var (
	ErrMemoryNotFound = errors.New("memory not found")
	ErrPrivateOnly    = errors.New("private memory cannot be superseded")
)

type Candidate struct {
	CandidateID      string `json:"candidate_id"`
	Namespace        string `json:"namespace"`
	CandidateType    string `json:"candidate_type"`
	Status           string `json:"status"`
	Subject          string `json:"subject"`
	Body             string `json:"body"`
	SourceURI        string `json:"source_uri"`
	AuthorAgentID    string `json:"author_agent_id"`
	OriginPeerID     string `json:"origin_peer_id"`
	Sensitivity      string `json:"sensitivity"`
	RetentionClass   string `json:"retention_class"`
	ProjectKey       string `json:"project_key"`
	BranchName       string `json:"branch_name"`
	AuthoredAtMS     int64  `json:"authored_at_ms"`
	CreatedAtMS      int64  `json:"created_at_ms"`
	UpdatedAtMS      int64  `json:"updated_at_ms"`
	ApprovedMemoryID string `json:"approved_memory_id"`
	ReviewedAtMS     int64  `json:"reviewed_at_ms"`
	ReviewNote       string `json:"review_note"`
}

type StoreRequest struct {
	MemoryID      string                `json:"memory_id,omitempty"`
	Visibility    Visibility            `json:"visibility"`
	Namespace     string                `json:"namespace"`
	MemoryType    string                `json:"memory_type,omitempty"`
	Scope         string                `json:"scope,omitempty"`
	Subject       string                `json:"subject,omitempty"`
	Body          string                `json:"body"`
	SourceURI     string                `json:"source_uri,omitempty"`
	SourceHash    string                `json:"source_hash,omitempty"`
	AuthorAgentID string                `json:"author_agent_id,omitempty"`
	OriginPeerID  string                `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64                 `json:"authored_at_ms,omitempty"`
	ArtifactSpans []ArtifactSpanInput   `json:"artifact_spans,omitempty"`
	Relations     []MemoryRelationInput `json:"relations,omitempty"`
}

type RecallRequest struct {
	Query             string   `json:"query"`
	Namespaces        []string `json:"namespaces,omitempty"`
	IncludePrivate    bool     `json:"include_private,omitempty"`
	IncludeShared     bool     `json:"include_shared,omitempty"`
	IncludeTranscript bool     `json:"include_transcript,omitempty"`
	ProjectKey        string   `json:"project_key,omitempty"`
	BranchName        string   `json:"branch_name,omitempty"`
	UnitKinds         []string `json:"unit_kinds,omitempty"`
	SourceTypes       []string `json:"source_types,omitempty"`
	Limit             int      `json:"limit,omitempty"`
}

type RecallResult struct {
	UnitID         string `json:"unit_id"`
	SourceType     string `json:"source_type"`
	MemorySpace    string `json:"memory_space"`
	MemoryID       string `json:"memory_id"`
	Namespace      string `json:"namespace"`
	UnitKind       string `json:"unit_kind"`
	MemoryType     string `json:"memory_type"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	LifecycleState string `json:"lifecycle_state"`
	AuthoredAtMS   int64  `json:"authored_at_ms"`
	SourceURI      string `json:"source_uri"`
	SourceHash     string `json:"source_hash"`
	OriginPeerID   string `json:"origin_peer_id"`
}

type RecallResponse struct {
	Items    []RecallResult `json:"items"`
	Warnings []string       `json:"warnings"`
}

type PromoteRequest struct {
	ChunkIDs      []string `json:"chunk_ids"`
	MemoryType    string   `json:"memory_type,omitempty"`
	Subject       string   `json:"subject,omitempty"`
	Namespace     string   `json:"namespace"`
	AuthorAgentID string   `json:"author_agent_id,omitempty"`
	OriginPeerID  string   `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64    `json:"authored_at_ms,omitempty"`
	SourceURI     string   `json:"source_uri,omitempty"`
}

type ListCandidatesRequest struct {
	Namespace  string `json:"namespace,omitempty"`
	Status     string `json:"status,omitempty"`
	ProjectKey string `json:"project_key,omitempty"`
	BranchName string `json:"branch_name,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type ApproveCandidateRequest struct {
	CandidateID   string `json:"candidate_id"`
	MemoryType    string `json:"memory_type,omitempty"`
	Subject       string `json:"subject,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	AuthorAgentID string `json:"author_agent_id,omitempty"`
	OriginPeerID  string `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64  `json:"authored_at_ms,omitempty"`
	SourceURI     string `json:"source_uri,omitempty"`
}

type RejectCandidateRequest struct {
	CandidateID string `json:"candidate_id"`
	ReviewNote  string `json:"review_note,omitempty"`
}

type PublishRequest struct {
	PrivateMemoryID string `json:"private_memory_id"`
	RedactionPolicy string `json:"redaction_policy,omitempty"`
}

type ContextBuildRequest struct {
	Query           string   `json:"query"`
	Namespaces      []string `json:"namespaces,omitempty"`
	ProjectKey      string   `json:"project_key,omitempty"`
	BranchName      string   `json:"branch_name,omitempty"`
	LimitPerSection int      `json:"limit_per_section,omitempty"`
}

type ContextArtifact struct {
	URI   string `json:"uri"`
	Title string `json:"title"`
}

type ContextBundle struct {
	ActivePrivateDecisions []RecallResult    `json:"active_private_decisions"`
	SharedConstraints      []RecallResult    `json:"shared_constraints"`
	RecentDiscussions      []RecallResult    `json:"recent_discussions"`
	RejectedOptions        []RecallResult    `json:"rejected_options"`
	OpenTasks              []RecallResult    `json:"open_tasks"`
	Artifacts              []ContextArtifact `json:"artifacts"`
}

type SignalRequest struct {
	MemorySpace   string  `json:"memory_space"`
	MemoryID      string  `json:"memory_id"`
	SignalType    string  `json:"signal_type"`
	Value         float64 `json:"value"`
	Reason        string  `json:"reason,omitempty"`
	AuthorAgentID string  `json:"author_agent_id,omitempty"`
	OriginPeerID  string  `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64   `json:"authored_at_ms,omitempty"`
}

type ExplainRequest struct {
	MemorySpace string `json:"memory_space"`
	MemoryID    string `json:"memory_id"`
	Query       string `json:"query"`
}

type ArtifactSpanInput struct {
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

type MemoryRelationInput struct {
	RelationType string  `json:"relation_type"`
	ToMemoryID   string  `json:"to_memory_id"`
	Weight       float64 `json:"weight,omitempty"`
}

type TraceDecisionRequest struct {
	MemorySpace string `json:"memory_space"`
	MemoryID    string `json:"memory_id"`
	Depth       int    `json:"depth,omitempty"`
}

type TraceDecisionNode struct {
	MemorySpace    string `json:"memory_space"`
	MemoryID       string `json:"memory_id"`
	Namespace      string `json:"namespace"`
	MemoryType     string `json:"memory_type"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	LifecycleState string `json:"lifecycle_state"`
	SourceURI      string `json:"source_uri"`
	SourceHash     string `json:"source_hash"`
	OriginPeerID   string `json:"origin_peer_id"`
	AuthoredAtMS   int64  `json:"authored_at_ms"`
}

type TraceDecisionHop struct {
	RelationType string            `json:"relation_type"`
	Depth        int               `json:"depth"`
	Memory       TraceDecisionNode `json:"memory"`
}

type TraceDecisionArtifact struct {
	ArtifactID  string `json:"artifact_id"`
	MemoryID    string `json:"memory_id"`
	URI         string `json:"uri"`
	Title       string `json:"title"`
	MimeType    string `json:"mime_type"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	QuoteHash   string `json:"quote_hash"`
}

type TraceTranscriptSource struct {
	SessionID    string                  `json:"session_id"`
	ChunkID      string                  `json:"chunk_id"`
	ChunkKind    string                  `json:"chunk_kind"`
	StartSeq     int                     `json:"start_seq"`
	EndSeq       int                     `json:"end_seq"`
	Text         string                  `json:"text"`
	AuthoredAtMS int64                   `json:"authored_at_ms"`
	Artifacts    []TraceDecisionArtifact `json:"artifacts"`
}

type TraceDecisionResult struct {
	Decision          TraceDecisionNode       `json:"decision"`
	Supports          []TraceDecisionHop      `json:"supports"`
	Contradictions    []TraceDecisionHop      `json:"contradictions"`
	Artifacts         []TraceDecisionArtifact `json:"artifacts"`
	TranscriptSources []TraceTranscriptSource `json:"transcript_sources"`
}

type ExplainProvenance struct {
	Namespace      string `json:"namespace"`
	MemoryType     string `json:"memory_type"`
	Subject        string `json:"subject"`
	LifecycleState string `json:"lifecycle_state"`
	SourceURI      string `json:"source_uri"`
	SourceHash     string `json:"source_hash"`
	AuthorAgentID  string `json:"author_agent_id"`
	OriginPeerID   string `json:"origin_peer_id"`
	AuthoredAtMS   int64  `json:"authored_at_ms"`
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

type ExplainResult struct {
	Provenance     ExplainProvenance               `json:"provenance"`
	ScoreBreakdown ExplainScoreBreakdown           `json:"score_breakdown"`
	TrustSummary   ExplainTrustSummary             `json:"trust_summary"`
	SignalSummary  map[string]ExplainSignalSummary `json:"signal_summary"`
}
