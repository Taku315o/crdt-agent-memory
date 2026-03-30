package cam

import (
	"os"

	"crdt-agent-memory/internal/config"

	"gopkg.in/yaml.v3"
)

func saveConfig(path string, cfg config.Config) error {
	raw, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
