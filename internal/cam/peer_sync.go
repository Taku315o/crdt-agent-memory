package cam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"crdt-agent-memory/internal/api"
	"crdt-agent-memory/internal/config"
)

type PeerAddOptions struct {
	PeerID             string
	DisplayName        string
	SigningPublicKey   string
	SyncURL            string
	NamespaceAllowlist []string
	DiscoveryProfile   string
	RelayProfile       string
}

func (a *App) PeerList(ctx context.Context) ([]config.PeerRegistryEntry, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return nil, err
	}
	if _, err := a.Init(ctx); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(layout.ConfigPath)
	if err != nil {
		return nil, err
	}
	return cfg.PeerRegistry, nil
}

func (a *App) PeerAdd(ctx context.Context, opts PeerAddOptions) (config.PeerRegistryEntry, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return config.PeerRegistryEntry{}, err
	}
	if _, err := a.Init(ctx); err != nil {
		return config.PeerRegistryEntry{}, err
	}
	cfg, err := loadConfig(layout.ConfigPath)
	if err != nil {
		return config.PeerRegistryEntry{}, err
	}
	namespaces := opts.NamespaceAllowlist
	if len(namespaces) == 0 {
		namespaces = append([]string{}, cfg.Namespaces...)
	}
	entry := config.PeerRegistryEntry{
		PeerID:             strings.TrimSpace(opts.PeerID),
		DisplayName:        strings.TrimSpace(opts.DisplayName),
		SigningPublicKey:   strings.TrimSpace(opts.SigningPublicKey),
		NamespaceAllowlist: namespaces,
		DiscoveryProfile:   strings.TrimSpace(opts.DiscoveryProfile),
		RelayProfile:       strings.TrimSpace(opts.RelayProfile),
		SyncURL:            strings.TrimSpace(opts.SyncURL),
	}
	if entry.DisplayName == "" {
		entry.DisplayName = entry.PeerID
	}
	replaced := false
	for i := range cfg.PeerRegistry {
		if cfg.PeerRegistry[i].PeerID == entry.PeerID {
			cfg.PeerRegistry[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.PeerRegistry = append(cfg.PeerRegistry, entry)
	}
	if err := cfg.Validate(); err != nil {
		return config.PeerRegistryEntry{}, err
	}
	if err := saveConfig(layout.ConfigPath, cfg); err != nil {
		return config.PeerRegistryEntry{}, err
	}
	return entry, nil
}

func (a *App) SyncStatus(ctx context.Context, namespace string) (api.SyncStatusResponse, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return api.SyncStatusResponse{}, err
	}
	if _, err := a.Init(ctx); err != nil {
		return api.SyncStatusResponse{}, err
	}
	cfg, err := loadConfig(layout.ConfigPath)
	if err != nil {
		return api.SyncStatusResponse{}, err
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" && len(cfg.Namespaces) > 0 {
		namespace = cfg.Namespaces[0]
	}
	if namespace == "" {
		return api.SyncStatusResponse{}, fmt.Errorf("namespace is required")
	}
	query := url.Values{}
	query.Set("namespace", namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.API.BaseURL, "/")+"/v1/sync/status?"+query.Encode(), nil)
	if err != nil {
		return api.SyncStatusResponse{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return api.SyncStatusResponse{}, err
	}
	defer resp.Body.Close()
	var envelope struct {
		OK    bool                   `json:"ok"`
		Data  api.SyncStatusResponse `json:"data"`
		Error *api.APIError          `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return api.SyncStatusResponse{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest || !envelope.OK {
		if envelope.Error != nil && envelope.Error.Message != "" {
			return api.SyncStatusResponse{}, fmt.Errorf(envelope.Error.Message)
		}
		return api.SyncStatusResponse{}, fmt.Errorf("sync status request failed")
	}
	return envelope.Data, nil
}
