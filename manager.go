package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	LogFile   *os.File
}

func NewProcessManager(baseDir string) *ProcessManager {
	logDir := filepath.Join(baseDir, "logs")
	os.MkdirAll(logDir, 0755)
	return &ProcessManager{
		processes: make(map[string]*ProcessInfo),
		logDir:    logDir,
		baseDir:   baseDir,
	}
}

// Start launches a command. `command` is what actually executes; `displayCommand`
// (with ${var} placeholders intact) is what gets written to logs and state so
// secret values never touch disk.
func (pm *ProcessManager) Start(id, name, command, displayCommand, cwd, engine string) error {
	pm.mu.Lock()
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
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		pm.mu.Unlock()
		return fmt.Errorf("failed to open log file: %v", err)
	}

	// Write log header
	now := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(logFile, "%s  START - name=%s\n", now, name)
	fmt.Fprintf(logFile, "%s  CWD - %s\n", now, resolvedCWD)
	fmt.Fprintf(logFile, "%s  ENGINE - %s\n", now, engineLabel(engine))
	fmt.Fprintf(logFile, "%s  COMMAND - %s\n", now, displayCommand)

	// Create command
	shell, args := shellCommand(engine)
	cmd := exec.Command(shell, append(args, command)...)
	cmd.Dir = resolvedCWD
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		pm.mu.Unlock()
		return fmt.Errorf("failed to start command: %v", err)
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
	}

	pm.processes[id] = info
	pm.mu.Unlock()

	// Wait for process in background
	go func() {
		err := cmd.Wait()
		pm.mu.Lock()
		info.Running = false
		info.EndTime = time.Now()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				info.ExitCode = exitErr.ExitCode()
			} else {
				info.ExitCode = -1
			}
		}
		exitCode, stopped := info.ExitCode, info.Stopped
		pm.mu.Unlock()

		if pm.onExit != nil {
			pm.onExit(id, exitCode, stopped)
		}

		exitNow := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(logFile, "%s  EXIT code=%d - pid=%d\n", exitNow, exitCode, info.PID)
		logFile.Close()
		close(done)
	}()

	return nil
}

func (pm *ProcessManager) Stop(id string) error {
	pm.mu.Lock()
	info, exists := pm.processes[id]
	if !exists || !info.Running {
		pm.mu.Unlock()
		return nil
	}
	info.Stopped = true
	pm.mu.Unlock()

	if runtime.GOOS == "windows" {
		// Use taskkill to kill process tree on Windows (hidden, no console flash)
		killCmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", info.PID), "/T", "/F")
		killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		killCmd.Run()
	} else {
		// Kill process group on Unix
		if info.Cmd.Process != nil {
			info.Cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	// Wait for process to exit with timeout
	select {
	case <-info.Done:
	case <-time.After(5 * time.Second):
		pm.mu.RLock()
		if info.Running {
			info.Cmd.Process.Kill()
		}
		pm.mu.RUnlock()
	}

	return nil
}

func (pm *ProcessManager) StopAll() {
	pm.mu.RLock()
	ids := make([]string, 0)
	for id, info := range pm.processes {
		if info.Running {
			ids = append(ids, id)
		}
	}
	pm.mu.RUnlock()

	for _, id := range ids {
		pm.Stop(id)
	}
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

func (pm *ProcessManager) GetLog(id string) string {
	logPath := filepath.Join(pm.logDir, id+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func (pm *ProcessManager) ClearLog(id string) error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	info, exists := pm.processes[id]
	if exists && info.Running {
		return fmt.Errorf("cannot clear log while command is running")
	}

	logPath := filepath.Join(pm.logDir, id+".log")
	return os.WriteFile(logPath, []byte{}, 0644)
}

func (pm *ProcessManager) GetLogTail(id string, lines int) string {
	if lines <= 0 {
		return ""
	}
	logPath := filepath.Join(pm.logDir, id+".log")
	file, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	// Read last N lines efficiently
	const chunkSize = 4096
	stat, err := file.Stat()
	if err != nil {
		return ""
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return ""
	}

	var chunks [][]byte
	totalSize := 0
	offset := fileSize
	lineCount := 0
	lastByte := []byte{0}
	if _, err := file.ReadAt(lastByte, fileSize-1); err == nil && lastByte[0] == '\n' {
		lineCount = -1 // A trailing newline terminates a line; it is not an extra line.
	}

	for offset > 0 && lineCount < lines {
		readSize := int64(chunkSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		buf := make([]byte, readSize)
		n, err := file.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return ""
		}
		buf = buf[:n]
		chunks = append(chunks, buf)
		totalSize += n

		for _, b := range buf {
			if b == '\n' {
				lineCount++
			}
		}
	}

	// Find the start of the last N lines
	content := make([]byte, 0, totalSize)
	for i := len(chunks) - 1; i >= 0; i-- {
		content = append(content, chunks[i]...)
	}
	result := strings.TrimSuffix(string(content), "\n")
	allLines := strings.Split(result, "\n")
	if len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}
	return strings.Join(allLines, "\n")
}

func (pm *ProcessManager) GetProcessInfo(id string) (string, int, string) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	info, exists := pm.processes[id]
	if !exists {
		return "", 0, ""
	}
	return info.Name, info.PID, info.Command
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
	if runtime.GOOS == "windows" {
		switch engine {
		case "cmd":
			return "cmd", []string{"/c"}
		case "powershell5":
			return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command"}
		default: // "powershell" / "": newest available
			return resolvePowerShell(), []string{"-NoProfile", "-NonInteractive", "-Command"}
		}
	}
	switch engine {
	case "bash":
		return "bash", []string{"-c"}
	case "zsh":
		return "zsh", []string{"-c"}
	default:
		return "sh", []string{"-c"}
	}
}

// engineLabel is what gets written to the log header
func engineLabel(engine string) string {
	if runtime.GOOS != "windows" {
		if engine == "" {
			return "sh"
		}
		return engine
	}
	switch engine {
	case "cmd":
		return "cmd"
	case "powershell5":
		return "Windows PowerShell 5.1"
	default:
		return "PowerShell (auto: " + resolvePowerShell() + ")"
	}
}

// TailLog returns a reader that follows the log file for real-time updates
func (pm *ProcessManager) TailLog(id string) (io.ReadCloser, error) {
	logPath := filepath.Join(pm.logDir, id+".log")
	return os.Open(logPath)
}
