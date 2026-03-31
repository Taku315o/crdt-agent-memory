package cam

import (
	"errors"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type Layout struct {
	Profile      string
	ConfigRoot   string
	DataRoot     string
	ConfigDir    string
	DataDir      string
	LogsDir      string
	RunDir       string
	ConfigPath   string
	SettingsPath string
	RuntimePath  string
}

func (l Layout) logPath(service string) string {
	return filepath.Join(l.LogsDir, service+".log")
}

func ResolveLayout(profile string) (Layout, error) {
	profile, err := normalizeProfileName(profile)
	if err != nil {
		return Layout{}, err
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
		Profile:      profile,
		ConfigRoot:   configRoot,
		DataRoot:     dataRoot,
		ConfigDir:    configDir,
		DataDir:      dataDir,
		LogsDir:      filepath.Join(dataDir, "logs"),
		RunDir:       filepath.Join(dataDir, "run"),
		ConfigPath:   filepath.Join(configDir, "config.yaml"),
		SettingsPath: filepath.Join(configDir, "settings.json"),
		RuntimePath:  filepath.Join(dataDir, "run", "runtime.json"),
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

func normalizeProfileName(profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", errors.New("profile is required")
	}
	for _, r := range profile {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_':
			continue
		default:
			return "", errors.New("profile may only contain letters, digits, hyphen, and underscore")
		}
	}
	return profile, nil
}

func validateServiceName(service string) error {
	if !slices.Contains([]string{"memoryd", "indexd", "syncd"}, service) {
		return errors.New("unsupported service")
	}
	return nil
}

func ensureWithinRoot(path, root string) error {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes managed root")
	}
	return nil
}
