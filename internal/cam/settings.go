package cam

import (
	"encoding/json"
	"os"
)

type Settings struct {
	SyncEnabled bool `json:"sync_enabled"`
}

func loadSettings(path string) (Settings, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func saveSettings(path string, settings Settings) error {
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
