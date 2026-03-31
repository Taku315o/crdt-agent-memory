package cam

import "context"

func (a *App) SetSyncEnabled(ctx context.Context, enabled bool) (Settings, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return Settings{}, err
	}
	if _, err := a.Init(ctx); err != nil {
		return Settings{}, err
	}
	settings, err := loadSettings(layout.SettingsPath)
	if err != nil {
		return Settings{}, err
	}
	settings.SyncEnabled = enabled
	if err := saveSettings(layout.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (a *App) SyncSettings(ctx context.Context) (Settings, error) {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return Settings{}, err
	}
	if _, err := a.Init(ctx); err != nil {
		return Settings{}, err
	}
	return loadSettings(layout.SettingsPath)
}
