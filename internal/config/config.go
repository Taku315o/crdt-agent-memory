package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PeerID           string `yaml:"peer_id"`
	DatabasePath     string `yaml:"database_path"`
	SigningKeyPath   string `yaml:"signing_key_path"`
	Namespaces       []string `yaml:"namespaces"`
	Extensions       Extensions `yaml:"extensions"`
	Transport        Transport `yaml:"transport"`
	PeerRegistry     []PeerRegistryEntry `yaml:"peer_registry"`
}

type Extensions struct {
	CRSQLitePath string `yaml:"crsqlite_path"`
	SQLiteVecPath string `yaml:"sqlite_vec_path"`
}

type Transport struct {
	DiscoveryProfile string `yaml:"discovery_profile"`
	RelayProfile string `yaml:"relay_profile"`
}

type PeerRegistryEntry struct {
	PeerID string `yaml:"peer_id"`
	DisplayName string `yaml:"display_name"`
	NamespaceAllowlist []string `yaml:"namespace_allowlist"`
	DiscoveryProfile string `yaml:"discovery_profile"`
	RelayProfile string `yaml:"relay_profile"`
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
	return cfg, nil
}
