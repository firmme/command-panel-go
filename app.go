//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	config        *Config
	manager       *ProcessManager
	sessionMu     sync.RWMutex
	sessionExitOK map[string]bool // commands that exited(0) during this session
	trayDone      chan struct{}
	trayStopOnce  sync.Once
	commandMu     sync.Mutex
	stop          chan struct{}
	stopOnce      sync.Once
}

func NewApp() (*App, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	baseDir, err := prepareDataDir([]string{filepath.Dir(executable), cwd})
	if err != nil {
		return nil, err
	}
	manager := NewProcessManager(baseDir)
	// User data stays fixed; a new command's default directory is the executable's.
	manager.baseDir = filepath.Dir(executable)
	return &App{config: NewConfig(baseDir), manager: manager,
		sessionExitOK: make(map[string]bool), stop: make(chan struct{})}, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Track commands that exit(0) during this session and push exit events to the frontend
	a.manager.onExit = func(id string, exitCode int, stopped bool) {
		if exitCode == 0 {
			a.sessionMu.Lock()
			a.sessionExitOK[id] = true
			a.sessionMu.Unlock()
		}
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "run-state-changed", id, exitCode, stopped)
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
	if err := a.manager.StopAll(); err != nil {
		if a.ctx != nil {
			runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.ErrorDialog, Title: "退出失败", Message: err.Error()})
		}
		return
	}
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

// HideWindow hides the window (minimize to tray) and trims memory
func (a *App) HideWindow() {
	if a.ctx != nil {
		runtime.WindowHide(a.ctx)
		trimWorkingSet()
	}
}

func (a *App) waitAutoStart(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-a.stop:
		return false
	case <-timer.C:
		return true
	}
}

func (a *App) autoStartCommands() {
	if !a.waitAutoStart(5*time.Second) || a.config.loadErr != nil {
		return
	}
	for _, cmd := range a.config.GetCommands() {
		if cmd.AutoStart && !a.manager.GetState(cmd.ID).Running {
			if result := a.StartCommand(cmd.ID); result != "" && a.ctx != nil {
				runtime.EventsEmit(a.ctx, "operation-error", result)
			}
			if !a.waitAutoStart(2 * time.Second) {
				return
			}
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.stopOnce.Do(func() { close(a.stop) })
	if err := a.manager.StopAll(); err != nil {
		fmt.Println("Shutdown:", err)
	}
	stopTray()
}

// GetCommands returns non-archived commands
func (a *App) GetCommands() ([]Command, error) {
	return a.config.GetCommands(), a.config.loadErr
}

// GetArchivedCommands returns archived commands
func (a *App) GetArchivedCommands() ([]Command, error) {
	return a.config.GetArchivedCommands(), a.config.loadErr
}

// AddCommand creates a new command
func (a *App) AddCommand(cmd Command) (Command, error) {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if cmd.Name == "" {
		cmd.Name = inferName(cmd.Command)
	}
	result, err := a.config.AddCommand(cmd)
	if err == nil {
		a.emitUpdate()
	}
	return result, err
}

// DuplicateCommand copies an existing command into a new command entry.
func (a *App) DuplicateCommand(id string) (*Command, error) {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	result, err := a.config.DuplicateCommand(id)
	if err == nil {
		a.emitUpdate()
	}
	return result, err
}

// UpdateCommand updates an existing command
func (a *App) UpdateCommand(cmd Command) (*Command, error) {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if cmd.Name == "" {
		cmd.Name = inferName(cmd.Command)
	}
	result, err := a.config.UpdateCommand(cmd)
	if err == nil {
		a.emitUpdate()
	}
	return result, err
}

// DeleteCommand deletes a command (stops it first if running)
func (a *App) DeleteCommand(id string) (bool, error) {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if a.config.loadErr != nil {
		return false, a.config.loadErr
	}
	if err := a.manager.Stop(id); err != nil {
		return false, err
	}
	deleted, err := a.config.DeleteCommand(id)
	if err != nil {
		return false, err
	}
	a.emitUpdate()
	// Configuration deletion is durable even if cleanup fails; report that distinction.
	if err := a.manager.Remove(id); err != nil {
		return deleted, fmt.Errorf("命令已删除，但日志清理失败: %w", err)
	}
	a.sessionMu.Lock()
	delete(a.sessionExitOK, id)
	a.sessionMu.Unlock()
	return deleted, nil
}

// StartCommand starts a command
func (a *App) StartCommand(id string) string {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if a.config.loadErr != nil {
		return a.config.loadErr.Error()
	}
	cmd := a.config.GetCommand(id)
	if cmd == nil || cmd.Archived {
		return "Command not found"
	}
	// Substitute sensitive variables for execution; the log/state keep the
	// placeholder form. Child output is redacted before writing logs.
	actual := substituteVariables(cmd.Command, cmd.Variables)
	secrets := make([]string, 0, len(cmd.Variables))
	for _, v := range cmd.Variables {
		secrets = append(secrets, v.Value)
	}
	if err := a.manager.Start(cmd.ID, cmd.Name, actual, cmd.Command, cmd.CWD, cmd.Engine, secrets...); err != nil {
		return err.Error()
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "run-state-changed", id, 0, false)
	}
	return ""
}

// substituteVariables replaces ${name} placeholders with their stored values
func substituteVariables(command string, vars []CommandVariable) string {
	pairs := make([]string, 0, len(vars)*2)
	for _, v := range vars {
		pairs = append(pairs, "${"+v.Name+"}", v.Value)
	}
	return strings.NewReplacer(pairs...).Replace(command)
}

// StopCommand stops a running command
func (a *App) StopCommand(id string) string {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.manager.Stop(id); err != nil {
		return err.Error()
	}
	return ""
}

// ArchiveCommand archives a command (stops it first if running)
func (a *App) ArchiveCommand(id string) (*Command, error) {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if a.config.loadErr != nil {
		return nil, a.config.loadErr
	}
	if err := a.manager.Stop(id); err != nil {
		return nil, err
	}
	result, err := a.config.ArchiveCommand(id)
	if err == nil {
		a.emitUpdate()
	}
	return result, err
}

func (a *App) RestoreCommand(id string) (*Command, error) {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	result, err := a.config.RestoreCommand(id)
	if err == nil {
		a.emitUpdate()
	}
	return result, err
}

func (a *App) ReadLog(id string, offset int64, version string) (LogChunk, error) {
	if a.config.GetCommand(id) == nil {
		return LogChunk{}, fmt.Errorf("命令不存在")
	}
	return a.manager.ReadLog(id, offset, version)
}

// ClearLog clears the log file for a command
func (a *App) ClearLog(id string) string {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if a.config.GetCommand(id) == nil {
		return "命令不存在"
	}
	if err := a.manager.ClearLog(id); err != nil {
		return err.Error()
	}
	return ""
}

// ReorderCommands updates the order of commands
func (a *App) ReorderCommands(ids []string) error {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.config.ReorderCommands(ids); err != nil {
		return err
	}
	a.emitUpdate()
	return nil
}

// GetRunStates returns the run state of all commands
func (a *App) GetRunStates() RunStateSnapshot {
	states := a.manager.GetAllStates()
	// Also include states for commands that haven't been started (for log info)
	for _, cmd := range a.config.GetAllCommands() {
		if _, exists := states[cmd.ID]; !exists {
			states[cmd.ID] = a.manager.GetState(cmd.ID)
		}
	}
	return RunStateSnapshot{States: states}
}

// OpenURL opens an external URL in the system browser (http/https only)
func (a *App) OpenURL(url string) {
	if a.ctx == nil {
		return
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		runtime.BrowserOpenURL(a.ctx, url)
	}
}

// ExecutablePick is the result of choosing an executable in the file dialog
type ExecutablePick struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// PickExecutable opens a file dialog for choosing an executable program
func (a *App) PickExecutable() *ExecutablePick {
	if a.ctx == nil {
		return nil
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择可执行程序",
		Filters: []runtime.FileFilter{
			{DisplayName: "可执行文件 (*.exe;*.bat;*.cmd;*.ps1)", Pattern: "*.exe;*.bat;*.cmd;*.ps1"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return nil
	}
	return &ExecutablePick{Path: path, Dir: filepath.Dir(path)}
}

// InstallAutostart installs Windows autostart
func (a *App) InstallAutostart() string {
	if err := installAutostart(); err != nil {
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
