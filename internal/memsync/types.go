package memsync

type HandshakeRequest struct {
	ProtocolVersion              string   `json:"protocol_version"`
	MinCompatibleProtocolVersion string   `json:"min_compatible_protocol_version"`
	PeerID                       string   `json:"peer_id"`
	SchemaHash                   string   `json:"schema_hash"`
	CRRManifestHash              string   `json:"crr_manifest_hash"`
	Namespaces                   []string `json:"namespaces"`
}

type HandshakeResponse struct {
	PeerID             string   `json:"peer_id"`
	SchemaHash         string   `json:"schema_hash"`
	CRRManifestHash    string   `json:"crr_manifest_hash"`
	Namespaces         []string `json:"namespaces"`
	NegotiatedProtocol string   `json:"negotiated_protocol"`
}

type Value struct {
	Null   bool    `json:"null,omitempty"`
	Integer *int64  `json:"integer,omitempty"`
	Float   *float64 `json:"float,omitempty"`
	Text    *string `json:"text,omitempty"`
	BlobB64 *string `json:"blob_b64,omitempty"`
}

type Change struct {
	Table     string `json:"table"`
	PKB64     string `json:"pk_b64"`
	CID       string `json:"cid"`
	Val       Value  `json:"val"`
	ColVersion int64 `json:"col_version"`
	DBVersion int64  `json:"db_version"`
	SiteIDB64 string `json:"site_id_b64"`
	CL        int64  `json:"cl"`
	Seq       int64  `json:"seq"`
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

type PullRequest struct {
	PeerID    string `json:"peer_id"`
	Namespace string `json:"namespace"`
	Limit     int    `json:"limit"`
}

type ApplyRequest struct {
	FromPeerID string `json:"from_peer_id"`
	Batch      Batch  `json:"batch"`
}

type PeerState struct {
	PeerID          string `json:"peer_id"`
	Namespace       string `json:"namespace"`
	LastSeenAtMS    int64  `json:"last_seen_at_ms"`
	LastTransport   string `json:"last_transport"`
	LastPathType    string `json:"last_path_type"`
	LastError       string `json:"last_error"`
	LastSuccessAtMS int64  `json:"last_success_at_ms"`
	SchemaFenced    bool   `json:"schema_fenced"`
}

type TrackedPeer struct {
	PeerID      string `json:"peer_id"`
	Namespace   string `json:"namespace"`
	Version     int64  `json:"version"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

type Diagnostics struct {
	SchemaHash      string      `json:"schema_hash"`
	CRRManifestHash string      `json:"crr_manifest_hash"`
	TrackedPeers    []TrackedPeer `json:"tracked_peers"`
	PeerStates      []PeerState `json:"peer_states"`
	QuarantineCount int         `json:"quarantine_count"`
}

type SyncStatus struct {
	Namespace    string      `json:"namespace"`
	State        string      `json:"state"`
	SchemaFenced bool        `json:"schema_fenced"`
	Peers        []PeerState `json:"peers"`
}
