package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"
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

// ExportPackets opens a native save dialog and writes the given text to the
// chosen path. Returns the saved path, or "" if the user cancelled.
func (a *App) ExportPackets(suggestedName, data string) (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: suggestedName,
		Title:           "Export captured packets",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON Lines (*.jsonl)", Pattern: "*.jsonl"},
			{DisplayName: "Text (*.txt)", Pattern: "*.txt"},
			{DisplayName: "All files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil // cancelled
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		return "", err
	}
	return path, nil
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
