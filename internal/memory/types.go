package memory

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

type StoreRequest struct {
	MemoryID      string     `json:"memory_id,omitempty"`
	Visibility    Visibility `json:"visibility"`
	Namespace     string     `json:"namespace"`
	MemoryType    string     `json:"memory_type,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	Subject       string     `json:"subject,omitempty"`
	Body          string     `json:"body"`
	SourceURI     string     `json:"source_uri,omitempty"`
	SourceHash    string     `json:"source_hash,omitempty"`
	AuthorAgentID string     `json:"author_agent_id,omitempty"`
	OriginPeerID  string     `json:"origin_peer_id,omitempty"`
	AuthoredAtMS  int64      `json:"authored_at_ms,omitempty"`
}

type RecallRequest struct {
	Query          string   `json:"query"`
	Namespaces     []string `json:"namespaces,omitempty"`
	IncludePrivate bool     `json:"include_private,omitempty"`
	Limit          int      `json:"limit,omitempty"`
}

type RecallResult struct {
	MemorySpace    string `json:"memory_space"`
	MemoryID       string `json:"memory_id"`
	Namespace      string `json:"namespace"`
	MemoryType     string `json:"memory_type"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	LifecycleState string `json:"lifecycle_state"`
	AuthoredAtMS   int64  `json:"authored_at_ms"`
	SourceURI      string `json:"source_uri"`
	SourceHash     string `json:"source_hash"`
	OriginPeerID   string `json:"origin_peer_id"`
}
