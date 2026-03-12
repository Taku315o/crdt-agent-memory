package memsync

type HandshakeRequest struct {
	ProtocolVersion              string   `json:"protocol_version"`
	MinCompatibleProtocolVersion string   `json:"min_compatible_protocol_version"`
	PeerID                       string   `json:"peer_id"`
	SchemaHash                   string   `json:"schema_hash"`
	CRRManifestHash              string   `json:"crr_manifest_hash"`
	Namespaces                   []string `json:"namespaces"`
	InviteTicket                 string   `json:"invite_ticket,omitempty"`
}

type HandshakeResponse struct {
	PeerID             string   `json:"peer_id"`
	SchemaHash         string   `json:"schema_hash"`
	CRRManifestHash    string   `json:"crr_manifest_hash"`
	Namespaces         []string `json:"namespaces"`
	NegotiatedProtocol string   `json:"negotiated_protocol"`
}

type Change struct {
	DBVersion   int64  `json:"db_version"`
	TableName   string `json:"table_name"`
	PK          string `json:"pk"`
	Op          string `json:"op"`
	RowJSON     string `json:"row_json"`
	MemoryID    string `json:"memory_id"`
	Namespace   string `json:"namespace"`
	ChangedAtMS int64  `json:"changed_at_ms"`
}

type Batch struct {
	BatchID         string   `json:"batch_id"`
	FromPeerID      string   `json:"from_peer_id"`
	Namespace       string   `json:"namespace"`
	SchemaHash      string   `json:"schema_hash"`
	CRRManifestHash string   `json:"crr_manifest_hash"`
	MaxVersion      int64    `json:"max_version"`
	Changes         []Change `json:"changes"`
}

type Diagnostics struct {
	SchemaHash      string
	CRRManifestHash string
	TrackedPeers    []TrackedPeer
	QuarantineCount int
}

type TrackedPeer struct {
	PeerID      string
	Namespace   string
	Version     int64
	UpdatedAtMS int64
}
