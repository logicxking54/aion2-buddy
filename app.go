package main

import (
	"context"
	"os"
	"path/filepath"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// configPath returns %APPDATA%\aion2-buddy\skills-config.json, creating the dir.
func (a *App) configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "aion2-buddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "skills-config.json"), nil
}

// SaveConfig writes the UI's skill-list config (a JSON string) to disk.
func (a *App) SaveConfig(data string) error {
	p, err := a.configPath()
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(data), 0o644)
}

// LoadConfig returns the saved config JSON, or "" if none/unreadable.
func (a *App) LoadConfig() string {
	p, err := a.configPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}
