package memory

type Visibility string

const (
	VisibilityShared  Visibility = "shared"
	VisibilityPrivate Visibility = "private"
)

type StoreRequest struct {
	MemoryID      string
	Visibility    Visibility
	Namespace     string
	MemoryType    string
	Scope         string
	Subject       string
	Body          string
	SourceURI     string
	SourceHash    string
	AuthorAgentID string
	OriginPeerID  string
	AuthoredAtMS  int64
}

type RecallRequest struct {
	Query          string
	Namespaces     []string
	IncludePrivate bool
	Limit          int
}

type RecallResult struct {
	MemorySpace   string
	MemoryID      string
	Namespace     string
	MemoryType    string
	Subject       string
	Body          string
	LifecycleState string
	AuthoredAtMS  int64
	SourceURI     string
	SourceHash    string
	OriginPeerID  string
}
