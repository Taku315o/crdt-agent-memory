package cam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type RuntimeState struct {
	Profile      string           `json:"profile"`
	ConfigPath   string           `json:"config_path"`
	DatabasePath string           `json:"database_path"`
	StartedAt    string           `json:"started_at"`
	Services     []RuntimeService `json:"services"`
}

type RuntimeService struct {
	Name    string `json:"name"`
	PID     int    `json:"pid"`
	LogPath string `json:"log_path"`
}

func ResolveBinary(name string) (string, error) {
	candidates := make([]string, 0, 4)
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, withExe(name)),
			filepath.Join(exeDir, "bin", withExe(name)),
			filepath.Join(filepath.Dir(exeDir), "bin", withExe(name)),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, withExe(name)),
			filepath.Join(wd, "bin", withExe(name)),
		)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to locate bundled binary %q; expected it next to cam or in ./bin", name)
}

func runKeygen(ctx context.Context, memorydPath, configPath string) error {
	if err := ensureBundledBinary(memorydPath, "memoryd"); err != nil {
		return err
	}
	// #nosec G204 -- memorydPath is resolved from the bundled binary set and validated above.
	cmd := exec.CommandContext(ctx, memorydPath, "--config", configPath, "--cmd", "keygen")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate signing key: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func startDetachedProcess(binPath string, args []string, logPath string) (int, error) {
	base := filepath.Base(binPath)
	if err := ensureBundledBinary(binPath, strings.TrimSuffix(base, filepath.Ext(base))); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return 0, err
	}
	// #nosec G304 -- logPath is produced from the managed layout.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()

	// #nosec G204 -- binPath is resolved from the bundled binary set and validated above.
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

func LoadRuntime(path string) (*RuntimeState, error) {
	// #nosec G304 -- path is produced from ResolveLayout and stored under the managed runtime directory.
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state RuntimeState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func ensureBundledBinary(path, name string) error {
	resolved, err := ResolveBinary(name)
	if err != nil {
		return err
	}
	if path != resolved {
		return fmt.Errorf("unexpected binary path for %s", name)
	}
	return nil
}

func SaveRuntime(path string, state RuntimeState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func withExe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
