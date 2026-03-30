package cam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type ProfileInfo struct {
	Name         string
	ConfigPath   string
	SettingsPath string
	DataDir      string
}

func (a *App) ProfileList(ctx context.Context) ([]ProfileInfo, error) {
	_ = ctx
	configRoot, err := userConfigRoot()
	if err != nil {
		return nil, err
	}
	dataRoot, err := userDataRoot()
	if err != nil {
		return nil, err
	}
	names := map[string]struct{}{}
	for _, root := range []string{
		filepath.Join(configRoot, "cam", "profiles"),
		filepath.Join(dataRoot, "cam", "profiles"),
	} {
		entries, err := os.ReadDir(root)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				names[entry.Name()] = struct{}{}
			}
		}
	}
	out := make([]ProfileInfo, 0, len(names))
	for name := range names {
		layout, err := ResolveLayout(name)
		if err != nil {
			return nil, err
		}
		out = append(out, ProfileInfo{
			Name:         name,
			ConfigPath:   layout.ConfigPath,
			SettingsPath: layout.SettingsPath,
			DataDir:      layout.DataDir,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (a *App) ProfileCreate(ctx context.Context) (InitResult, error) {
	return a.Init(ctx)
}

func (a *App) ProfileRemove(ctx context.Context) error {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return err
	}
	state, err := LoadRuntime(layout.RuntimePath)
	if err != nil {
		return err
	}
	if state != nil {
		for _, svc := range state.Services {
			if ok, _ := processAlive(svc.PID); ok {
				return fmt.Errorf("profile %q is still running; stop it before removal", a.Profile)
			}
		}
	}
	if err := os.RemoveAll(layout.ConfigDir); err != nil {
		return err
	}
	if err := os.RemoveAll(layout.DataDir); err != nil {
		return err
	}
	return nil
}
