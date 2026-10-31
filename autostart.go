//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func getStartupDir() string {
	appData := os.Getenv("APPDATA")
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

func getAutostartPath() string {
	return filepath.Join(getStartupDir(), "CommandPanelStartup.vbs")
}

func installAutostart() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	startupDir := getStartupDir()
	if _, err := os.Stat(startupDir); os.IsNotExist(err) {
		return fmt.Errorf("startup directory not found: %s", startupDir)
	}

	// Create VBS script that launches the app hidden
	vbsContent := startupScript(executable)

	autostartPath := getAutostartPath()
	if err := os.WriteFile(autostartPath, []byte(vbsContent), 0644); err != nil {
		return fmt.Errorf("failed to create autostart script: %v", err)
	}

	return nil
}

func uninstallAutostart() error {

	autostartPath := getAutostartPath()
	if _, err := os.Stat(autostartPath); os.IsNotExist(err) {
		return nil // Already not installed
	}

	if err := os.Remove(autostartPath); err != nil {
		return fmt.Errorf("failed to remove autostart script: %v", err)
	}

	return nil
}

func isAutostartInstalled() bool {
	_, err := os.Stat(getAutostartPath())
	return err == nil
}

// Chr(34) quotes the executable without invoking cmd or expanding shell variables.
func startupScript(executable string) string {
	escaped := strings.ReplaceAll(executable, `"`, `""`)
	return fmt.Sprintf("Set WshShell = CreateObject(\"WScript.Shell\")\r\nWshShell.Run Chr(34) & \"%s\" & Chr(34), 0, False\r\n", escaped)
}
