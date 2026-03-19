package config

import (
	"errors"
	"os"
	"strings"

	"crdt-agent-memory/internal/signing"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PeerID         string              `yaml:"peer_id"`
	DatabasePath   string              `yaml:"database_path"`
	SigningKeyPath string              `yaml:"signing_key_path"`
	Namespaces     []string            `yaml:"namespaces"`
	Extensions     Extensions          `yaml:"extensions"`
	Transport      Transport           `yaml:"transport"`
	API            API                 `yaml:"api"`
	Sync           Sync                `yaml:"sync"`
	PeerRegistry   []PeerRegistryEntry `yaml:"peer_registry"`
}

type Extensions struct {
	CRSQLitePath  string `yaml:"crsqlite_path"`
	SQLiteVecPath string `yaml:"sqlite_vec_path"`
}

type Transport struct {
	DiscoveryProfile string `yaml:"discovery_profile"`
	RelayProfile     string `yaml:"relay_profile"`
}

type API struct {
	ListenAddr string `yaml:"listen_addr"`
	BaseURL    string `yaml:"base_url"`
}

type Sync struct {
	ListenAddr  string `yaml:"listen_addr"`
	PublicURL   string `yaml:"public_url"`
	IntervalMS  int    `yaml:"interval_ms"`
	BatchLimit  int    `yaml:"batch_limit"`
	OnceTimeout int    `yaml:"once_timeout_ms"`
}

type PeerRegistryEntry struct {
	PeerID             string   `yaml:"peer_id"`
	DisplayName        string   `yaml:"display_name"`
	SigningPublicKey   string   `yaml:"signing_public_key"`
	NamespaceAllowlist []string `yaml:"namespace_allowlist"`
	DiscoveryProfile   string   `yaml:"discovery_profile"`
	RelayProfile       string   `yaml:"relay_profile"`
	SyncURL            string   `yaml:"sync_url"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Sync.IntervalMS <= 0 {
		c.Sync.IntervalMS = 3000
	}
	if c.Sync.BatchLimit <= 0 {
		c.Sync.BatchLimit = 256
	}
	if c.Sync.OnceTimeout <= 0 {
		c.Sync.OnceTimeout = 5000
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.PeerID) == "" {
		return errors.New("peer_id is required")
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		return errors.New("database_path is required")
	}
	if strings.TrimSpace(c.SigningKeyPath) == "" {
		return errors.New("signing_key_path is required")
	}
	if strings.TrimSpace(c.Extensions.CRSQLitePath) == "" {
		return errors.New("extensions.crsqlite_path is required")
	}
	if strings.TrimSpace(c.API.ListenAddr) == "" {
		return errors.New("api.listen_addr is required")
	}
	if strings.TrimSpace(c.API.BaseURL) == "" {
		return errors.New("api.base_url is required")
	}
	if strings.TrimSpace(c.Sync.ListenAddr) == "" {
		return errors.New("sync.listen_addr is required")
	}
	if strings.TrimSpace(c.Sync.PublicURL) == "" {
		return errors.New("sync.public_url is required")
	}
	for _, peer := range c.PeerRegistry {
		if strings.TrimSpace(peer.PeerID) == "" {
			return errors.New("peer_registry.peer_id is required")
		}
		if strings.TrimSpace(peer.SigningPublicKey) == "" {
			return errors.New("peer_registry.signing_public_key is required")
		}
		if _, err := signing.ParsePublicKeyHex(peer.SigningPublicKey); err != nil {
			return err
		}
		if strings.TrimSpace(peer.SyncURL) == "" {
			return errors.New("peer_registry.sync_url is required")
		}
	}
	return nil
}
