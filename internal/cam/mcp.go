package cam

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/extensions"

	toml "github.com/pelletier/go-toml/v2"
)

type MCPInstallOptions struct {
	Client            string
	CreateMissingDirs bool
}

func (a *App) MCPPrintConfig(ctx context.Context, client string) (string, error) {
	entry, err := a.mcpEntry(ctx, client)
	if err != nil {
		return "", err
	}
	switch normalizeMCPClient(client) {
	case "codex":
		return entry.tomlBlock(), nil
	default:
		payload := map[string]any{"mcpServers": map[string]any{entry.serverName(): entry.jsonValue()}}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		return string(raw) + "\n", nil
	}
}

func (a *App) MCPInstall(ctx context.Context, opts MCPInstallOptions) (string, error) {
	entry, err := a.mcpEntry(ctx, opts.Client)
	if err != nil {
		return "", err
	}
	client := normalizeMCPClient(opts.Client)
	path, err := mcpTargetPath(client)
	if err != nil {
		return "", err
	}
	if err := ensureTargetDir(path, opts.CreateMissingDirs); err != nil {
		return "", err
	}
	switch client {
	case "codex":
		// #nosec G304 -- path is selected from a fixed client-specific target.
		existing, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		updated := upsertManagedBlock(string(existing), entry.tomlBlock(), entry.serverName())
		if err := validateTOML(updated); err != nil {
			return "", err
		}
		if err := writeWithBackup(path, []byte(updated)); err != nil {
			return "", err
		}
	default:
		payload, err := loadJSONFile(path)
		if err != nil {
			return "", err
		}
		servers, _ := payload["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			payload["mcpServers"] = servers
		}
		servers[entry.serverName()] = entry.jsonValue()
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		if err := writeWithBackup(path, append(raw, '\n')); err != nil {
			return "", err
		}
	}
	return path, nil
}

type mcpEntryData struct {
	Command         string
	Args            []string
	Env             map[string]string
	StartupTimeout  int
	ServerNameValue string
}

func (m mcpEntryData) serverName() string {
	return m.ServerNameValue
}

func (m mcpEntryData) jsonValue() map[string]any {
	return map[string]any{
		"command": m.Command,
		"args":    m.Args,
		"env":     m.Env,
	}
}

func (m mcpEntryData) tomlBlock() string {
	return strings.Join([]string{
		fmt.Sprintf("[mcp_servers.%q]", m.serverName()),
		fmt.Sprintf("command = %q", m.Command),
		fmt.Sprintf("args = %s", tomlArray(m.Args)),
		fmt.Sprintf("env = %s", tomlInlineTable(m.Env)),
		fmt.Sprintf("startup_timeout_ms = %d", m.StartupTimeout),
	}, "\n") + "\n"
}

func (a *App) mcpEntry(ctx context.Context, client string) (mcpEntryData, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return mcpEntryData{}, err
	}
	if _, err := a.Init(ctx); err != nil {
		return mcpEntryData{}, err
	}
	cfg, err := config.Load(layout.ConfigPath)
	if err != nil {
		return mcpEntryData{}, err
	}
	mcpPath, err := ResolveBinary("memory-mcp")
	if err != nil {
		return mcpEntryData{}, err
	}
	crsqlitePath, err := configuredOrBundled(cfg.Extensions.CRSQLitePath, extensions.NameCRSQLite)
	if err != nil {
		return mcpEntryData{}, err
	}
	sqliteVecPath, err := configuredOrBundled(cfg.Extensions.SQLiteVecPath, extensions.NameSQLiteVec)
	if err != nil {
		return mcpEntryData{}, err
	}
	command := mcpPath
	if normalizeMCPClient(client) == "inspector" {
		command, err = inspectorWrapperPath()
		if err != nil {
			return mcpEntryData{}, err
		}
	}
	return mcpEntryData{
		Command: command,
		Args: []string{
			"--config", layout.ConfigPath,
		},
		Env: map[string]string{
			"CRSQLITE_PATH":   crsqlitePath,
			"SQLITE_VEC_PATH": sqliteVecPath,
		},
		StartupTimeout:  60000,
		ServerNameValue: "memory-" + a.Profile,
	}, nil
}

func configuredOrBundled(configuredPath, bundleName string) (string, error) {
	if strings.TrimSpace(configuredPath) != "" {
		return configuredPath, nil
	}
	path, ok, err := extensions.Resolve(bundleName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("bundled extension unavailable: %s", bundleName)
	}
	return path, nil
}

func inspectorWrapperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(exeDir, "memory-mcp-wrapper.sh"),
		filepath.Join(filepath.Dir(exeDir), "scripts", "memory-mcp-wrapper.sh"),
		filepath.Join(filepath.Dir(exeDir), "memory-mcp-wrapper.sh"),
		filepath.Join(filepath.Dir(filepath.Dir(exeDir)), "scripts", "memory-mcp-wrapper.sh"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to locate memory-mcp-wrapper.sh")
}

func normalizeMCPClient(client string) string {
	client = strings.TrimSpace(strings.ToLower(client))
	if client == "" {
		return "local"
	}
	return client
}

func mcpTargetPath(client string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch client {
	case "local":
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(wd, ".mcp", "config.json"), nil
	case "inspector":
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(wd, ".mcp", "inspector-config.json"), nil
	case "claude":
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
		}
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), nil
	case "codex":
		return filepath.Join(home, ".codex", "config.toml"), nil
	default:
		return "", fmt.Errorf("unsupported client %q", client)
	}
}

func ensureTargetDir(path string, create bool) error {
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); err == nil {
		return nil
	}
	if !create {
		return fmt.Errorf("target directory does not exist: %s", parent)
	}
	return os.MkdirAll(parent, 0o750)
}

func loadJSONFile(path string) (map[string]any, error) {
	// #nosec G304 -- path is selected from a fixed client-specific target.
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeWithBackup(path string, raw []byte) error {
	if err := ensureBackupPath(path); err != nil {
		return err
	}
	// #nosec G304 -- path is selected from a fixed client-specific target.
	if existing, err := os.ReadFile(path); err == nil {
		// #nosec G703 -- backup path is the validated target path with a fixed ".bak" suffix.
		if err := os.WriteFile(path+".bak", existing, 0o600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// #nosec G703 -- target path is selected from a fixed client-specific location.
	return os.WriteFile(path, raw, 0o600)
}

func upsertManagedBlock(existing, block, name string) string {
	begin := "# BEGIN managed by cam: " + name
	end := "# END managed by cam: " + name
	wrapped := begin + "\n" + strings.TrimRight(block, "\n") + "\n" + end + "\n"
	start := strings.Index(existing, begin)
	if start >= 0 {
		finish := strings.Index(existing[start:], end)
		if finish >= 0 {
			finish += start + len(end)
			if finish < len(existing) && existing[finish] == '\n' {
				finish++
			}
			return existing[:start] + wrapped + existing[finish:]
		}
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	if existing != "" {
		existing += "\n"
	}
	return existing + wrapped
}

func validateTOML(content string) error {
	var tmp map[string]any
	return toml.Unmarshal([]byte(content), &tmp)
}

func tomlArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func tomlInlineTable(values map[string]string) string {
	keys := []string{"CRSQLITE_PATH", "SQLITE_VEC_PATH"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, ok := values[key]; ok {
			parts = append(parts, fmt.Sprintf("%s = %q", key, value))
		}
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func ensureBackupPath(path string) error {
	if strings.HasSuffix(path, ".bak") {
		return fmt.Errorf("refusing nested backup path: %s", path)
	}
	return nil
}
