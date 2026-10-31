//go:build windows && !bindings

package main

import (
	"embed"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	updater.HandleHelperMode()
	service, err := NewApp()
	if err != nil {
		showStartupError(err)
		return
	}
	app := application.New(application.Options{
		Name:           "命令面板",
		Assets:         application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Services:       []application.Service{application.NewService(service)},
		OnShutdown:     service.shutdown,
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "command-panel-fd1cf8db-46b4-4d69-a670-afd919f3a32c", OnSecondInstanceLaunch: func(application.SecondInstanceData) { service.ShowWindow() }},
	})
	service.wails = app
	if err := service.initUpdater(); err != nil {
		println("Updater:", err.Error())
	}
	service.window = app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "命令面板", Width: 1100, Height: 750, MinWidth: 800, MinHeight: 600, BackgroundColour: application.NewRGBA(30, 30, 30, 255), Windows: application.WindowsWindow{Theme: application.Dark}})
	service.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel(); service.HideWindow() })
	service.startTray(service.ShowWindow, service.Quit)
	service.startup()
	if err := app.Run(); err != nil {
		println("Error:", err.Error())
	}
}
