package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func getStartupDir() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	appData := os.Getenv("APPDATA")
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

func getAutostartPath() string {
	return filepath.Join(getStartupDir(), "CommandPanelStartup.vbs")
}

func installAutostart(baseDir string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("autostart is only supported on Windows")
	}

	startupDir := getStartupDir()
	if _, err := os.Stat(startupDir); os.IsNotExist(err) {
		return fmt.Errorf("startup directory not found: %s", startupDir)
	}

	// Create VBS script that launches the app hidden
	vbsContent := fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
WshShell.Run "cmd /c cd /d ""%s"" && command-panel.exe", 0, False
`, baseDir)

	autostartPath := getAutostartPath()
	if err := os.WriteFile(autostartPath, []byte(vbsContent), 0644); err != nil {
		return fmt.Errorf("failed to create autostart script: %v", err)
	}

	return nil
}

func uninstallAutostart() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("autostart is only supported on Windows")
	}

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
	if runtime.GOOS != "windows" {
		return false
	}
	_, err := os.Stat(getAutostartPath())
	return err == nil
}
