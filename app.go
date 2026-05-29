package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	config        *Config
	manager       *ProcessManager
	baseDir       string
	sessionMu     sync.RWMutex
	sessionExitOK map[string]bool // commands that exited(0) during this session
	trayDone      chan struct{}
	trayStopOnce  sync.Once
}

func NewApp() *App {
	baseDir, _ := os.Getwd()
	return &App{
		config:        NewConfig(baseDir),
		manager:       NewProcessManager(baseDir),
		baseDir:       baseDir,
		sessionExitOK: make(map[string]bool),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Track commands that exit(0) during this session
	a.manager.onExit = func(id string, exitCode int) {
		if exitCode == 0 {
			a.sessionMu.Lock()
			a.sessionExitOK[id] = true
			a.sessionMu.Unlock()
		}
	}
	// Auto-start commands after 5 seconds
	go a.autoStartCommands()
}

// ShowWindow brings the window to front
func (a *App) ShowWindow() {
	if a.ctx != nil {
		runtime.WindowShow(a.ctx)
		runtime.WindowUnminimise(a.ctx)
	}
}

func (a *App) Quit() {
	if a.ctx != nil {
		runtime.Quit(a.ctx)
		return
	}
	a.shutdown(nil)
}

// HideWindow hides the window (minimize to tray) and trims memory
func (a *App) HideWindow() {
	if a.ctx != nil {
		// Ask frontend to release resources before hiding
		runtime.WindowExecJS(a.ctx, `
			if (window.gc) window.gc();
			if (window.caches) {
				caches.keys().then(names => names.forEach(n => caches.delete(n)));
			}
		`)
		runtime.WindowHide(a.ctx)
		trimWorkingSet()
	}
}

func (a *App) autoStartCommands() {
	time.Sleep(5 * time.Second)
	commands := a.config.GetCommands()
	for _, cmd := range commands {
		if cmd.AutoStart && !cmd.Archived {
			state := a.manager.GetState(cmd.ID)
			if !state.Running {
				a.StartCommand(cmd.ID)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.manager.StopAll()
}

// GetCommands returns non-archived commands
func (a *App) GetCommands() []Command {
	return a.config.GetCommands()
}

// GetArchivedCommands returns archived commands
func (a *App) GetArchivedCommands() []Command {
	return a.config.GetArchivedCommands()
}

// GetCommand returns a single command by ID
func (a *App) GetCommand(id string) *Command {
	return a.config.GetCommand(id)
}

// AddCommand creates a new command
func (a *App) AddCommand(cmd Command) Command {
	if cmd.Name == "" {
		cmd.Name = inferName(cmd.Command)
	}
	result := a.config.AddCommand(cmd)
	a.emitUpdate()
	return result
}

// DuplicateCommand copies an existing command into a new command entry.
func (a *App) DuplicateCommand(id string) *Command {
	result := a.config.DuplicateCommand(id)
	if result != nil {
		a.emitUpdate()
	}
	return result
}

// UpdateCommand updates an existing command
func (a *App) UpdateCommand(cmd Command) *Command {
	if cmd.Name == "" {
		cmd.Name = inferName(cmd.Command)
	}
	result := a.config.UpdateCommand(cmd)
	a.emitUpdate()
	return result
}

// DeleteCommand deletes a command (stops it first if running)
func (a *App) DeleteCommand(id string) bool {
	a.manager.Stop(id)
	a.config.DeleteCommand(id)
	// Clean up log file
	logPath := filepath.Join(a.baseDir, "logs", id+".log")
	os.Remove(logPath)
	a.emitUpdate()
	return true
}

// StartCommand starts a command
func (a *App) StartCommand(id string) string {
	cmd := a.config.GetCommand(id)
	if cmd == nil {
		return "Command not found"
	}
	if err := a.manager.Start(cmd.ID, cmd.Name, cmd.Command, cmd.CWD); err != nil {
		return err.Error()
	}
	a.emitUpdate()
	return ""
}

// StopCommand stops a running command
func (a *App) StopCommand(id string) string {
	if err := a.manager.Stop(id); err != nil {
		return err.Error()
	}
	a.emitUpdate()
	return ""
}

// ArchiveCommand archives a command (stops it first if running)
func (a *App) ArchiveCommand(id string) *Command {
	state := a.manager.GetState(id)
	if state.Running {
		a.manager.Stop(id)
	}
	result := a.config.ArchiveCommand(id)
	a.emitUpdate()
	return result
}

// RestoreCommand restores an archived command
func (a *App) RestoreCommand(id string) *Command {
	result := a.config.RestoreCommand(id)
	a.emitUpdate()
	return result
}

// GetLog returns the full log content for a command
func (a *App) GetLog(id string) string {
	return a.manager.GetLog(id)
}

// ClearLog clears the log file for a command
func (a *App) ClearLog(id string) string {
	if err := a.manager.ClearLog(id); err != nil {
		return err.Error()
	}
	return ""
}

// ReorderCommands updates the order of commands
func (a *App) ReorderCommands(ids []string) {
	a.config.ReorderCommands(ids)
	a.emitUpdate()
}

// GetRunStates returns the run state of all commands
func (a *App) GetRunStates() map[string]*RunState {
	states := a.manager.GetAllStates()
	// Also include states for commands that haven't been started (for log info)
	for _, cmd := range a.config.GetAllCommands() {
		if _, exists := states[cmd.ID]; !exists {
			states[cmd.ID] = a.manager.GetState(cmd.ID)
		}
	}
	return states
}

// GetRunState returns the run state of a single command
func (a *App) GetRunState(id string) *RunState {
	return a.manager.GetState(id)
}

// GetBaseDir returns the base directory
func (a *App) GetBaseDir() string {
	return a.baseDir
}

// InstallAutostart installs Windows autostart
func (a *App) InstallAutostart() string {
	if err := installAutostart(a.baseDir); err != nil {
		return err.Error()
	}
	return ""
}

// UninstallAutostart removes Windows autostart
func (a *App) UninstallAutostart() string {
	if err := uninstallAutostart(); err != nil {
		return err.Error()
	}
	return ""
}

// IsAutostartInstalled checks if autostart is installed
func (a *App) IsAutostartInstalled() bool {
	return isAutostartInstalled()
}

// emitUpdate sends an event to the frontend to refresh data
func (a *App) emitUpdate() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "commands-updated")
	}
}

// inferName extracts a display name from the command string
func inferName(command string) string {
	if command == "" {
		return ""
	}
	// Take the first part of the command
	for i, c := range command {
		if c == ' ' || c == '\t' {
			if i > 0 {
				return command[:i]
			}
		}
	}
	return command
}

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
