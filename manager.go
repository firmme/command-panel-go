//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProcessManager struct {
	mu        sync.RWMutex
	processes map[string]*ProcessInfo
	logDir    string
	baseDir   string
	closed    bool
	onExit    func(id string, exitCode int, stopped bool) // called when a process exits
}

type ProcessInfo struct {
	Cmd       *exec.Cmd
	ID        string
	Name      string
	Command   string
	CWD       string
	PID       int
	StartTime time.Time
	EndTime   time.Time
	ExitCode  int
	Running   bool
	Stopped   bool // true when stopped by the user, not a crash
	Done      chan struct{}
	LogFile   *rotatingLog
	Job       *processJob
}

func NewProcessManager(baseDir string) *ProcessManager {
	logDir := filepath.Join(baseDir, "logs")
	return &ProcessManager{
		processes: make(map[string]*ProcessInfo),
		logDir:    logDir,
		baseDir:   baseDir,
	}
}

// Start launches a command. `command` is what actually executes; `displayCommand`
// (with ${var} placeholders intact) is what gets written to logs and state so
// child output is redacted before persistence.
func (pm *ProcessManager) Start(id, name, command, displayCommand, cwd, engine string, secrets ...string) error {
	pm.mu.Lock()
	if pm.closed {
		pm.mu.Unlock()
		return fmt.Errorf("程序正在退出")
	}
	if !validCommandID(id) {
		pm.mu.Unlock()
		return fmt.Errorf("invalid command ID")
	}
	if engine != "" && engine != "powershell" && engine != "powershell5" && engine != "cmd" {
		pm.mu.Unlock()
		return fmt.Errorf("不支持的执行引擎: %s", engine)
	}
	if err := os.MkdirAll(pm.logDir, 0700); err != nil {
		pm.mu.Unlock()
		return err
	}
	if info, exists := pm.processes[id]; exists && info.Running {
		pm.mu.Unlock()
		return fmt.Errorf("command %s is already running", id)
	}
	// Resolve CWD
	resolvedCWD := cwd
	if resolvedCWD == "" {
		resolvedCWD = pm.baseDir
	}

	// Open log file
	logPath := filepath.Join(pm.logDir, id+".log")
	logFile, err := openLog(logPath, secrets)
	if err != nil {
		pm.mu.Unlock()
		return fmt.Errorf("failed to open log file: %v", err)
	}

	// Write log header
	now := time.Now().Format("2006-01-02 15:04:05")
	if _, err := fmt.Fprintf(logFile, "%s  START - name=%s\n%s  CWD - %s\n%s  ENGINE - %s\n%s  COMMAND - %s\n",
		now, name, now, resolvedCWD, now, engineLabel(engine), now, displayCommand); err != nil {
		logFile.Close()
		pm.mu.Unlock()
		return fmt.Errorf("failed to write log header: %w", err)
	}

	// Create command
	shell, args := shellCommand(engine)
	cmd := exec.Command(shell, append(args, command)...)
	cmd.Dir = resolvedCWD
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000200 | 0x00000004, // NEW_PROCESS_GROUP | SUSPENDED
	}
	if engine == "cmd" {
		// cmd.exe does not use the CommandLineToArgvW quoting convention.
		// /s strips only this outer quote pair and preserves quotes in the script.
		cmd.SysProcAttr.CmdLine = `cmd.exe /d /s /c "` + command + `"`
	}

	job, err := newProcessJob()
	if err != nil {
		logFile.Close()
		pm.mu.Unlock()
		return err
	}
	if err := cmd.Start(); err != nil {
		job.close()
		logFile.Close()
		pm.mu.Unlock()
		return fmt.Errorf("failed to start command: %w", err)
	}
	if err := job.assignAndResume(uint32(cmd.Process.Pid)); err != nil {
		job.terminate()
		cmd.Process.Kill()
		cmd.Wait()
		job.close()
		logFile.Close()
		pm.mu.Unlock()
		return fmt.Errorf("failed to track process tree: %w", err)
	}

	done := make(chan struct{})
	info := &ProcessInfo{
		Cmd:       cmd,
		ID:        id,
		Name:      name,
		Command:   displayCommand,
		CWD:       resolvedCWD,
		PID:       cmd.Process.Pid,
		StartTime: time.Now(),
		Running:   true,
		Done:      done,
		LogFile:   logFile,
		Job:       job,
	}

	pm.processes[id] = info
	pm.mu.Unlock()

	// Wait for process in background
	go func() {
		err := cmd.Wait()
		jobErr := job.wait()
		job.close()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		if jobErr != nil {
			exitCode = -1
		}
		exitNow := time.Now().Format("2006-01-02 15:04:05")
		_, writeErr := fmt.Fprintf(logFile, "%s  EXIT code=%d - pid=%d\n", exitNow, exitCode, info.PID)
		closeErr := logFile.Close()
		if writeErr != nil || closeErr != nil {
			exitCode = -1
		}
		pm.mu.Lock()
		info.Running, info.EndTime, info.ExitCode = false, time.Now(), exitCode
		stopped := info.Stopped
		close(done)
		pm.mu.Unlock()
		if pm.onExit != nil {
			pm.onExit(id, exitCode, stopped)
		}

	}()

	return nil
}

func (pm *ProcessManager) Stop(id string) error {
	pm.mu.Lock()
	info := pm.processes[id]
	if info == nil || !info.Running {
		pm.mu.Unlock()
		return nil
	}
	info.Stopped = true
	pm.mu.Unlock()
	if err := info.Job.terminate(); err != nil {
		pm.mu.Lock()
		running := info.Running
		if running {
			info.Stopped = false
		}
		pm.mu.Unlock()
		if running {
			return fmt.Errorf("终止进程树失败: %w", err)
		}
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-info.Done:
		return nil
	case <-timer.C:
		return fmt.Errorf("停止超时，进程尚未确认退出: %s", id)
	}
}

func (pm *ProcessManager) StopAll() error {
	pm.mu.Lock()
	pm.closed = true
	ids := make([]string, 0)
	for id, info := range pm.processes {
		if info.Running {
			ids = append(ids, id)
		}
	}
	pm.mu.Unlock()
	results := make(chan error, len(ids))
	for _, id := range ids {
		go func(id string) { results <- pm.Stop(id) }(id)
	}
	var errs []error
	for range ids {
		if err := <-results; err != nil {
			errs = append(errs, err)
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		pm.mu.Lock()
		pm.closed = false
		pm.mu.Unlock()
	}
	return err
}

func (pm *ProcessManager) Remove(id string) error {
	if !validCommandID(id) {
		return fmt.Errorf("invalid command ID")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if info := pm.processes[id]; info != nil && info.Running {
		return fmt.Errorf("command is running")
	}
	for i := 0; i <= logBackups; i++ {
		path := filepath.Join(pm.logDir, id+".log")
		if i > 0 {
			path = fmt.Sprintf("%s.%d", path, i)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	delete(pm.processes, id)
	return nil
}

func (pm *ProcessManager) logFileInfo(id string) (string, int64) {
	logPath := filepath.Join(pm.logDir, id+".log")
	info, err := os.Stat(logPath)
	if err != nil {
		return id + ".log", 0
	}
	return id + ".log", info.Size()
}

func computeDuration(info *ProcessInfo) string {
	if info.StartTime.IsZero() {
		return ""
	}
	if info.Running {
		return formatDuration(time.Since(info.StartTime))
	}
	if !info.EndTime.IsZero() {
		return formatDuration(info.EndTime.Sub(info.StartTime))
	}
	return ""
}

func (pm *ProcessManager) GetState(id string) *RunState {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	info, exists := pm.processes[id]
	logName, logSize := pm.logFileInfo(id)
	if !exists {
		return &RunState{Running: false, LogPath: logName, LogSize: logSize}
	}
	return &RunState{
		Cmd:       &ManagedCommand{ID: info.ID, Command: info.Command, CWD: info.CWD, Name: info.Name},
		PID:       info.PID,
		StartTime: info.StartTime,
		EndTime:   info.EndTime,
		ExitCode:  info.ExitCode,
		Running:   info.Running,
		Stopped:   info.Stopped,
		Duration:  computeDuration(info),
		LogPath:   logName,
		LogSize:   logSize,
	}
}

func (pm *ProcessManager) GetAllStates() map[string]*RunState {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	states := make(map[string]*RunState)
	for id, info := range pm.processes {
		logName, logSize := pm.logFileInfo(id)
		states[id] = &RunState{
			Cmd:       &ManagedCommand{ID: info.ID, Command: info.Command, CWD: info.CWD, Name: info.Name},
			PID:       info.PID,
			StartTime: info.StartTime,
			EndTime:   info.EndTime,
			ExitCode:  info.ExitCode,
			Running:   info.Running,
			Stopped:   info.Stopped,
			Duration:  computeDuration(info),
			LogPath:   logName,
			LogSize:   logSize,
		}
	}
	return states
}

func (pm *ProcessManager) ClearLog(id string) error {
	if !validCommandID(id) {
		return fmt.Errorf("invalid command ID")
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	info, exists := pm.processes[id]
	if exists && info.Running {
		return fmt.Errorf("cannot clear log while command is running")
	}

	if info != nil && info.LogFile != nil {
		info.LogFile.mu.Lock()
		defer info.LogFile.mu.Unlock()
		info.LogFile.revision++
	}
	logPath := filepath.Join(pm.logDir, id+".log")
	return os.WriteFile(logPath, []byte{}, 0600)
}

// ── 执行引擎 ─────────────────────────────────────

var (
	psResolveOnce sync.Once
	psPath        string
)

// resolvePowerShell finds the newest PowerShell available:
//  1. versioned installs under Program Files\PowerShell\* (stable beats preview)
//  2. pwsh on PATH (includes the Store version via WindowsApps)
//  3. built-in Windows PowerShell 5.1
func resolvePowerShell() string {
	psResolveOnce.Do(func() {
		var bestPath string
		var bestVer [3]int
		bestHasVer, bestPreview := false, false

		if pf := os.Getenv("ProgramFiles"); pf != "" {
			if matches, _ := filepath.Glob(filepath.Join(pf, "PowerShell", "*", "pwsh.exe")); len(matches) > 0 {
				for _, m := range matches {
					ver, preview := parsePSVersionDir(filepath.Base(filepath.Dir(m)))
					if bestPath == "" || !bestHasVer ||
						versionGreater(ver, bestVer) ||
						(ver == bestVer && !preview && bestPreview) {
						bestPath, bestVer, bestHasVer, bestPreview = m, ver, true, preview
					}
				}
			}
		}
		if bestPath == "" {
			if p, err := exec.LookPath("pwsh"); err == nil {
				bestPath = p
			}
		}
		if bestPath == "" {
			bestPath = "powershell" // Windows PowerShell 5.1
		}
		psPath = bestPath
	})
	return psPath
}

func versionGreater(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// parsePSVersionDir parses install dir names like "7", "7.4", "7-preview",
// "7.4.2-preview" into a version triple and a preview flag.
func parsePSVersionDir(dir string) ([3]int, bool) {
	preview := strings.Contains(dir, "preview")
	base := strings.SplitN(dir, "-", 2)[0]
	var v [3]int
	for i, p := range strings.Split(base, ".") {
		if i >= 3 {
			break
		}
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				n = 0
				break
			}
			n = n*10 + int(ch-'0')
		}
		v[i] = n
	}
	return v, preview
}

// shellCommand maps an engine name to the executable + base arguments.
func shellCommand(engine string) (string, []string) {
	switch engine {
	case "cmd":
		return "cmd", []string{"/d", "/s", "/c"}
	case "powershell5":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command"}
	default:
		return resolvePowerShell(), []string{"-NoProfile", "-NonInteractive", "-Command"}
	}
}

// engineLabel is what gets written to the log header
func engineLabel(engine string) string {
	switch engine {
	case "cmd":
		return "cmd"
	case "powershell5":
		return "Windows PowerShell 5.1"
	default:
		return "PowerShell (auto: " + resolvePowerShell() + ")"
	}
}
