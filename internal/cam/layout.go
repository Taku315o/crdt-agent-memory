package cam

import (
	"errors"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Layout struct {
	Profile     string
	ConfigRoot  string
	DataRoot    string
	ConfigDir   string
	DataDir     string
	LogsDir     string
	RunDir      string
	ConfigPath  string
	RuntimePath string
}

func ResolveLayout(profile string) (Layout, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return Layout{}, errors.New("profile is required")
	}
	configRoot, err := userConfigRoot()
	if err != nil {
		return Layout{}, err
	}
	dataRoot, err := userDataRoot()
	if err != nil {
		return Layout{}, err
	}
	configDir := filepath.Join(configRoot, "cam", "profiles", profile)
	dataDir := filepath.Join(dataRoot, "cam", "profiles", profile)
	return Layout{
		Profile:     profile,
		ConfigRoot:  configRoot,
		DataRoot:    dataRoot,
		ConfigDir:   configDir,
		DataDir:     dataDir,
		LogsDir:     filepath.Join(dataDir, "logs"),
		RunDir:      filepath.Join(dataDir, "run"),
		ConfigPath:  filepath.Join(configDir, "config.yaml"),
		RuntimePath: filepath.Join(dataDir, "run", "runtime.json"),
	}, nil
}

func userConfigRoot() (string, error) {
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		return base, nil
	}
	return os.UserConfigDir()
}

func userDataRoot() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if base := strings.TrimSpace(os.Getenv("LocalAppData")); base != "" {
			return base, nil
		}
		return os.UserConfigDir()
	default:
		if base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); base != "" {
			return base, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share"), nil
	}
}

func defaultPorts(profile string) (int, int) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(profile))
	offset := int(h.Sum32() % 1000)
	return 35000 + offset, 36000 + offset
}
