package cam

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/extensions"
)

type DoctorCheck struct {
	Name   string
	Status string
	Detail string
}

type DoctorReport struct {
	OK       bool
	Failures int
	Checks   []DoctorCheck
}

func (a *App) Doctor(ctx context.Context) (DoctorReport, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return DoctorReport{}, err
	}
	report := DoctorReport{OK: true}
	add := func(name string, ok bool, detail string) {
		status := "ok"
		if !ok {
			status = "fail"
			report.OK = false
			report.Failures++
		}
		report.Checks = append(report.Checks, DoctorCheck{Name: name, Status: status, Detail: detail})
	}
	warn := func(name string, detail string) {
		report.Checks = append(report.Checks, DoctorCheck{Name: name, Status: "warn", Detail: detail})
	}

	add("profile", true, layout.Profile)
	if _, err := os.Stat(layout.ConfigPath); err != nil {
		add("config", false, layout.ConfigPath)
		return report, nil
	}
	add("config", true, layout.ConfigPath)

	cfg, err := loadConfig(layout.ConfigPath)
	if err != nil {
		add("config.load", false, err.Error())
		return report, nil
	}
	add("config.load", true, cfg.API.BaseURL)
	add("search_profile", true, cfg.Search.Profile+"/"+cfg.Search.FTSTokenizer)
	add("embedding_provider", true, cfg.Embedding.Provider)

	if _, err := os.Stat(cfg.SigningKeyPath); err != nil {
		add("signing_key", false, cfg.SigningKeyPath)
	} else {
		add("signing_key", true, cfg.SigningKeyPath)
	}

	if err := ensureWritable(filepath.Dir(cfg.DatabasePath)); err != nil {
		add("database_dir", false, err.Error())
	} else {
		add("database_dir", true, filepath.Dir(cfg.DatabasePath))
	}

	if path, ok, err := extensionPath(cfg.Extensions.CRSQLitePath, extensions.NameCRSQLite); err != nil {
		add("crsqlite", false, err.Error())
	} else if !ok {
		add("crsqlite", false, "missing bundled extension")
	} else {
		add("crsqlite", true, path)
	}
	if path, ok, err := extensionPath(cfg.Extensions.SQLiteVecPath, extensions.NameSQLiteVec); err != nil {
		add("sqlite_vec", false, err.Error())
	} else if !ok {
		add("sqlite_vec", false, "missing bundled extension")
	} else {
		add("sqlite_vec", true, path)
	}

	for _, bin := range []string{"memoryd", "indexd"} {
		if path, err := ResolveBinary(bin); err != nil {
			add("binary."+bin, false, err.Error())
		} else {
			add("binary."+bin, true, path)
		}
	}

	state, err := LoadRuntime(layout.RuntimePath)
	runningServices := map[string]bool{}
	if err != nil {
		add("runtime", false, err.Error())
	} else if state == nil {
		add("runtime", true, "stopped")
	} else {
		add("runtime", true, state.StartedAt)
		for _, svc := range state.Services {
			ok, _ := processAlive(svc.PID)
			runningServices[svc.Name] = ok
			add("service."+svc.Name, ok, fmt.Sprintf("pid=%d", svc.PID))
		}
	}

	add("api_port", portAvailableOrOwned(cfg.API.ListenAddr, runningServices["memoryd"]), cfg.API.ListenAddr)
	add("sync_port", portAvailableOrOwned(cfg.Sync.ListenAddr, runningServices["syncd"]), cfg.Sync.ListenAddr)

	if memorydHealthy(ctx, cfg.API.BaseURL) {
		add("memoryd_health", true, cfg.API.BaseURL+"/healthz")
	} else if runningServices["memoryd"] {
		add("memoryd_health", false, cfg.API.BaseURL+"/healthz")
	} else {
		warn("memoryd_health", "memoryd is not running")
	}
	if ok, detail := embeddingProviderHealthy(ctx, cfg); ok {
		add("embedding_provider_health", true, detail)
	} else {
		warn("embedding_provider_health", detail)
	}
	return report, nil
}

func embeddingProviderHealthy(ctx context.Context, cfg config.Config) (bool, string) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Embedding.Provider))
	switch provider {
	case "", "local":
		return true, "local"
	case "ruri-http":
		if strings.TrimSpace(cfg.Embedding.BaseURL) == "" {
			return false, "embedding.base_url is empty"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Embedding.BaseURL, nil)
		if err != nil {
			return false, err.Error()
		}
		client := &http.Client{Timeout: time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond}
		resp, err := client.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		return true, fmt.Sprintf("%s status=%d", cfg.Embedding.BaseURL, resp.StatusCode)
	case "openai":
		if strings.TrimSpace(cfg.Embedding.Model) == "" {
			return false, "embedding.model is empty"
		}
		return true, cfg.Embedding.Model
	default:
		return false, "unsupported provider " + cfg.Embedding.Provider
	}
}

func extensionPath(configuredPath, name string) (string, bool, error) {
	if strings.TrimSpace(configuredPath) != "" {
		if _, err := os.Stat(configuredPath); err != nil {
			return "", false, err
		}
		return configuredPath, true, nil
	}
	return extensions.Resolve(name)
}

func ensureWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".cam-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func portAvailableOrOwned(addr string, owned bool) bool {
	if owned {
		return true
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
