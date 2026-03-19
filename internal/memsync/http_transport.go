package memsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPServer struct {
	Service           *Service
	AllowedNamespaces func(peerID string) map[string]struct{}
}

func NewHTTPServer(service *Service, allowed func(peerID string) map[string]struct{}) TransportServer {
	return &HTTPServer{
		Service:           service,
		AllowedNamespaces: allowed,
	}
}

func (h *HTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync/handshake", h.handleHandshake)
	mux.HandleFunc("/v1/sync/pull", h.handlePull)
	mux.HandleFunc("/v1/sync/apply", h.handleApply)
	return mux
}

func (h *HTTPServer) handleHandshake(w http.ResponseWriter, r *http.Request) {
	var req HandshakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.namespacesAllowed(req.PeerID, req.Namespaces) {
		http.Error(w, "namespace not allowlisted", http.StatusForbidden)
		return
	}
	resp, err := h.Service.Handshake(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *HTTPServer) handlePull(w http.ResponseWriter, r *http.Request) {
	var req PullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.namespacesAllowed(req.PeerID, []string{req.Namespace}) {
		http.Error(w, "namespace not allowlisted", http.StatusForbidden)
		return
	}
	batch, err := h.Service.ExtractBatch(r.Context(), req.PeerID, req.Namespace, req.Limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(batch)
}

func (h *HTTPServer) handleApply(w http.ResponseWriter, r *http.Request) {
	var req ApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.namespacesAllowed(req.FromPeerID, []string{req.Batch.Namespace}) {
		http.Error(w, "namespace not allowlisted", http.StatusForbidden)
		return
	}
	if err := h.Service.ApplyBatch(r.Context(), req.FromPeerID, req.Batch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPServer) namespacesAllowed(peerID string, requested []string) bool {
	if h.AllowedNamespaces == nil {
		return true
	}
	allowed := h.AllowedNamespaces(peerID)
	if len(allowed) == 0 {
		return false
	}
	for _, namespace := range requested {
		if _, ok := allowed[namespace]; !ok {
			return false
		}
	}
	return true
}

type HTTPClient struct {
	Client  *http.Client
	BaseURL string
}

func NewHTTPClient(baseURL string, timeout time.Duration) TransportClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPClient{
		Client:  &http.Client{Timeout: timeout},
		BaseURL: strings.TrimRight(baseURL, "/"),
	}
}

func (c *HTTPClient) Handshake(ctx context.Context, req HandshakeRequest) (HandshakeResponse, error) {
	var resp HandshakeResponse
	err := c.postJSON(ctx, "/v1/sync/handshake", req, &resp)
	return resp, err
}

func (c *HTTPClient) Pull(ctx context.Context, req PullRequest) (Batch, error) {
	var batch Batch
	err := c.postJSON(ctx, "/v1/sync/pull", req, &batch)
	return batch, err
}

func (c *HTTPClient) Apply(ctx context.Context, req ApplyRequest) error {
	return c.postJSON(ctx, "/v1/sync/apply", req, nil)
}

func (c *HTTPClient) postJSON(ctx context.Context, path string, reqBody any, respBody any) error {
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 5 * time.Second}
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("%s", strings.TrimSpace(buf.String()))
	}
	if respBody == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}

var _ TransportClient = (*HTTPClient)(nil)
var _ TransportServer = (*HTTPServer)(nil)
