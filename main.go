package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	// Register the tray on the main thread and let Wails/WebView pump messages.
	app.startTray(func() {
		app.ShowWindow()
	}, func() {
		app.Quit()
	})

	err := wails.Run(&options.App{
		Title:     "命令面板",
		Width:     1100,
		Height:    750,
		MinWidth:  800,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:  &options.RGBA{R: 30, G: 30, B: 30, A: 255},
		OnStartup:         app.startup,
		HideWindowOnClose: true,
		OnShutdown:        app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
