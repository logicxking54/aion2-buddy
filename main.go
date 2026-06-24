package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()
	captureBinding := NewCapture()
	memReadBinding := NewMemRead()
	modBinding := NewMod()

	// Create application with options
	err := wails.Run(&options.App{
		Title:         "Aion 2 Buddy",
		Width:         850,
		Height:        1000,
		DisableResize: false,
		Frameless:     true, // custom title bar (see components/TitleBar.vue)
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			captureBinding.setContext(ctx)
			memReadBinding.setContext(ctx)
			modBinding.setContext(ctx)
		},
		Bind: []interface{}{
			app,
			captureBinding,
			memReadBinding,
			modBinding,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
