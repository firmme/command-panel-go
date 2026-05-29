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
	onExit    func(id string, exitCode int) // called when a process exits
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

func (pm *ProcessManager) Start(id, name, command, cwd string) error {
	pm.mu.Lock()
	if info, exists := pm.processes[id]; exists && info.Running {
		pm.mu.Unlock()
		return fmt.Errorf("command %s is already running", id)
	}
	pm.mu.Unlock()

	// Resolve CWD
	resolvedCWD := cwd
	if resolvedCWD == "" {
		resolvedCWD = pm.baseDir
	}

	// Open log file
	logPath := filepath.Join(pm.logDir, id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	// Write log header
	now := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(logFile, "%s  START - name=%s\n", now, name)
	fmt.Fprintf(logFile, "%s  CWD - %s\n", now, resolvedCWD)
	fmt.Fprintf(logFile, "%s  COMMAND - %s\n", now, command)

	// Create command
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
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
		return fmt.Errorf("failed to start command: %v", err)
	}

	done := make(chan struct{})
	info := &ProcessInfo{
		Cmd:       cmd,
		ID:        id,
		Name:      name,
		Command:   command,
		CWD:       resolvedCWD,
		PID:       cmd.Process.Pid,
		StartTime: time.Now(),
		Running:   true,
		Done:      done,
		LogFile:   logFile,
	}

	pm.mu.Lock()
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
		pm.mu.Unlock()

		if pm.onExit != nil {
			pm.onExit(id, info.ExitCode)
		}

		exitNow := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(logFile, "%s  EXIT code=%d - pid=%d\n", exitNow, info.ExitCode, info.PID)
		logFile.Close()
		close(done)
	}()

	return nil
}

func (pm *ProcessManager) Stop(id string) error {
	pm.mu.RLock()
	info, exists := pm.processes[id]
	pm.mu.RUnlock()

	if !exists || !info.Running {
		return nil
	}

	if runtime.GOOS == "windows" {
		// Use taskkill to kill process tree on Windows
		killCmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", info.PID), "/T", "/F")
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
	info, exists := pm.processes[id]
	pm.mu.RUnlock()
	if exists && info.Running {
		return fmt.Errorf("cannot clear log while command is running")
	}

	logPath := filepath.Join(pm.logDir, id+".log")
	return os.WriteFile(logPath, []byte{}, 0644)
}

func (pm *ProcessManager) GetLogTail(id string, lines int) string {
	logPath := filepath.Join(pm.logDir, id+".log")
	file, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	// Read last N lines efficiently
	const chunkSize = 4096
	stat, _ := file.Stat()
	fileSize := stat.Size()
	if fileSize == 0 {
		return ""
	}

	var content []byte
	offset := fileSize
	lineCount := 0

	for offset > 0 && lineCount < lines {
		readSize := int64(chunkSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		buf := make([]byte, readSize)
		file.ReadAt(buf, offset)
		content = append(buf, content...)

		for _, b := range buf {
			if b == '\n' {
				lineCount++
			}
		}
	}

	// Find the start of the last N lines
	result := string(content)
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

// TailLog returns a reader that follows the log file for real-time updates
func (pm *ProcessManager) TailLog(id string) (io.ReadCloser, error) {
	logPath := filepath.Join(pm.logDir, id+".log")
	return os.Open(logPath)
}
