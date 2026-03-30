package cam

import (
	"context"
	"fmt"
	"os"
)

type ConfigPaths struct {
	ConfigPath   string
	SettingsPath string
}

func (a *App) ConfigPaths(ctx context.Context) (ConfigPaths, error) {
	_ = ctx
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return ConfigPaths{}, err
	}
	return ConfigPaths{
		ConfigPath:   layout.ConfigPath,
		SettingsPath: layout.SettingsPath,
	}, nil
}

func (a *App) ConfigShow(ctx context.Context, target string) (string, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return "", err
	}
	if _, err := a.Init(ctx); err != nil {
		return "", err
	}
	var path string
	switch target {
	case "", "config":
		path = layout.ConfigPath
	case "settings":
		path = layout.SettingsPath
	default:
		return "", fmt.Errorf("unsupported config target %q", target)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
