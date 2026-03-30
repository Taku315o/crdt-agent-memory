package cam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/extensions"

	"gopkg.in/yaml.v3"
)

type App struct {
	Profile string
}

func NewApp() *App {
	return &App{Profile: "default"}
}

type InitResult struct {
	Profile       string
	ConfigPath    string
	DataDir       string
	APIBaseURL    string
	SyncPublicURL string
}

type UpOptions struct {
	WithSync bool
}

type ServiceStatus struct {
	Name    string
	PID     int
	Running bool
	Health  string
	LogPath string
}

type Status struct {
	Profile      string
	ConfigPath   string
	DatabasePath string
	StartedAt    string
	Services     []ServiceStatus
}

func (a *App) Init(ctx context.Context) (InitResult, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return InitResult{}, err
	}
	if err := os.MkdirAll(layout.ConfigDir, 0o750); err != nil {
		return InitResult{}, err
	}
	if err := os.MkdirAll(layout.DataDir, 0o750); err != nil {
		return InitResult{}, err
	}
	if err := os.MkdirAll(layout.LogsDir, 0o750); err != nil {
		return InitResult{}, err
	}
	if err := os.MkdirAll(layout.RunDir, 0o750); err != nil {
		return InitResult{}, err
	}
	if _, ok, err := extensions.Resolve(extensions.NameCRSQLite); err != nil {
		return InitResult{}, err
	} else if !ok {
		return InitResult{}, errors.New("bundled crsqlite extension is not available for this platform")
	}
	if _, ok, err := extensions.Resolve(extensions.NameSQLiteVec); err != nil {
		return InitResult{}, err
	} else if !ok {
		return InitResult{}, errors.New("bundled sqlite-vec extension is not available for this platform")
	}
	cfg, err := ensureConfig(layout)
	if err != nil {
		return InitResult{}, err
	}
	if _, err := os.Stat(cfg.SigningKeyPath); errors.Is(err, os.ErrNotExist) {
		memorydPath, err := ResolveBinary("memoryd")
		if err != nil {
			return InitResult{}, err
		}
		if err := runKeygen(ctx, memorydPath, layout.ConfigPath); err != nil {
			return InitResult{}, err
		}
	} else if err != nil {
		return InitResult{}, err
	}
	return InitResult{
		Profile:       a.Profile,
		ConfigPath:    layout.ConfigPath,
		DataDir:       layout.DataDir,
		APIBaseURL:    cfg.API.BaseURL,
		SyncPublicURL: cfg.Sync.PublicURL,
	}, nil
}

func (a *App) Up(ctx context.Context, opts UpOptions) (RuntimeState, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return RuntimeState{}, err
	}
	if _, err := a.Init(ctx); err != nil {
		return RuntimeState{}, err
	}
	cfg, err := loadConfig(layout.ConfigPath)
	if err != nil {
		return RuntimeState{}, err
	}
	existing, err := LoadRuntime(layout.RuntimePath)
	if err != nil {
		return RuntimeState{}, err
	}
	if existing != nil {
		running := false
		for _, svc := range existing.Services {
			if ok, _ := processAlive(svc.PID); ok {
				running = true
				break
			}
		}
		if running {
			return RuntimeState{}, fmt.Errorf("profile %q is already running", a.Profile)
		}
		_ = os.Remove(layout.RuntimePath)
	}

	services := []struct {
		name string
		args []string
	}{
		{name: "memoryd", args: []string{"--config", layout.ConfigPath}},
		{name: "indexd", args: []string{"--config", layout.ConfigPath}},
	}
	if opts.WithSync {
		services = append(services, struct {
			name string
			args []string
		}{name: "syncd", args: []string{"--config", layout.ConfigPath}})
	}

	state := RuntimeState{
		Profile:      a.Profile,
		ConfigPath:   layout.ConfigPath,
		DatabasePath: cfg.DatabasePath,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	for i, svc := range services {
		path, err := ResolveBinary(svc.name)
		if err != nil {
			_ = stopServices(state.Services)
			return RuntimeState{}, err
		}
		logPath := filepath.Join(layout.LogsDir, svc.name+".log")
		pid, err := startDetachedProcess(path, svc.args, logPath)
		if err != nil {
			_ = stopServices(state.Services)
			return RuntimeState{}, err
		}
		state.Services = append(state.Services, RuntimeService{
			Name:    svc.name,
			PID:     pid,
			LogPath: logPath,
		})
		if i == 0 {
			if err := waitForHealth(ctx, cfg.API.BaseURL, 15*time.Second); err != nil {
				_ = stopServices(state.Services)
				return RuntimeState{}, err
			}
		} else {
			time.Sleep(300 * time.Millisecond)
			if ok, _ := processAlive(pid); !ok {
				_ = stopServices(state.Services)
				return RuntimeState{}, fmt.Errorf("%s exited unexpectedly; inspect %s", svc.name, logPath)
			}
		}
	}
	if err := SaveRuntime(layout.RuntimePath, state); err != nil {
		_ = stopServices(state.Services)
		return RuntimeState{}, err
	}
	return state, nil
}

func (a *App) Status(ctx context.Context) (Status, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return Status{}, err
	}
	cfg, err := loadConfig(layout.ConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	status := Status{
		Profile:    a.Profile,
		ConfigPath: layout.ConfigPath,
	}
	if cfg.PeerID != "" {
		status.DatabasePath = cfg.DatabasePath
	}
	state, err := LoadRuntime(layout.RuntimePath)
	if err != nil {
		return Status{}, err
	}
	if state == nil {
		return status, nil
	}
	status.StartedAt = state.StartedAt
	for _, svc := range state.Services {
		running, _ := processAlive(svc.PID)
		svcStatus := ServiceStatus{
			Name:    svc.Name,
			PID:     svc.PID,
			Running: running,
			Health:  "unknown",
			LogPath: svc.LogPath,
		}
		if svc.Name == "memoryd" {
			switch {
			case !running:
				svcStatus.Health = "stopped"
			case cfg.API.BaseURL == "":
				svcStatus.Health = "unknown"
			case memorydHealthy(ctx, cfg.API.BaseURL):
				svcStatus.Health = "ok"
			default:
				svcStatus.Health = "unhealthy"
			}
		} else if running {
			svcStatus.Health = "running"
		} else {
			svcStatus.Health = "stopped"
		}
		status.Services = append(status.Services, svcStatus)
	}
	return status, nil
}

func (a *App) Stop(ctx context.Context) (bool, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return false, err
	}
	state, err := LoadRuntime(layout.RuntimePath)
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}
	if err := stopServices(state.Services); err != nil {
		return false, err
	}
	if err := os.Remove(layout.RuntimePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	_ = ctx
	return true, nil
}

func ensureConfig(layout Layout) (config.Config, error) {
	if cfg, err := loadConfig(layout.ConfigPath); err == nil {
		return cfg, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}
	apiPort, syncPort := defaultPorts(layout.Profile)
	cfg := config.Config{
		PeerID:         "cam-" + layout.Profile,
		DatabasePath:   filepath.Join(layout.DataDir, "agent_memory.sqlite"),
		SigningKeyPath: filepath.Join(layout.DataDir, "peer.key"),
		Namespaces:     []string{"local/" + layout.Profile},
		Transport: config.Transport{
			DiscoveryProfile: "http-dev",
			RelayProfile:     "none",
		},
		API: config.API{
			ListenAddr: fmt.Sprintf("127.0.0.1:%d", apiPort),
			BaseURL:    fmt.Sprintf("http://127.0.0.1:%d", apiPort),
		},
		Sync: config.Sync{
			ListenAddr:  fmt.Sprintf("127.0.0.1:%d", syncPort),
			PublicURL:   fmt.Sprintf("http://127.0.0.1:%d", syncPort),
			IntervalMS:  3000,
			BatchLimit:  256,
			OnceTimeout: 5000,
		},
	}
	raw, err := yaml.Marshal(&cfg)
	if err != nil {
		return config.Config{}, err
	}
	if err := os.WriteFile(layout.ConfigPath, raw, 0o600); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func loadConfig(path string) (config.Config, error) {
	return config.Load(path)
}

func waitForHealth(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if memorydHealthy(ctx, baseURL) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("memoryd did not become healthy at %s/healthz", baseURL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func memorydHealthy(ctx context.Context, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func stopServices(services []RuntimeService) error {
	var stopErr error
	for i := len(services) - 1; i >= 0; i-- {
		if err := stopProcess(services[i].PID, 5*time.Second); err != nil && stopErr == nil {
			stopErr = err
		}
	}
	return stopErr
}
