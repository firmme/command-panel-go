//go:build windows && bindings

package main

import (
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// Binding generation must not create user data or register a system tray.
func main() {
	if err := wails.Run(&options.App{Bind: []interface{}{&App{}}}); err != nil {
		log.Fatal(err)
	}
}
