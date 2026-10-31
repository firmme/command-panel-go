//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestLogRedactionAcrossWritesAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := openLog(path, []string{"test-password", "test-password-long"})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"before test-pass", "word-long after\n", "test-password"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before [REDACTED] after\n[REDACTED]" {
		t.Fatalf("redaction failed: %q", data)
	}
	w, err = openLog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 1024*1024)
	for i := 0; i < 18; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(path + "*")
	if err != nil || len(files) != logBackups+1 {
		t.Fatalf("unexpected backups: %v, %v", files, err)
	}
	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil || stat.Size() > maxLogSize {
			t.Fatalf("oversized log: %v", err)
		}
	}
}

func TestReadLogCursorBoundsUTF8AndReset(t *testing.T) {
	pm := NewProcessManager(t.TempDir())
	if err := os.MkdirAll(pm.logDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pm.logDir, "test.log")
	if err := os.WriteFile(path, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := pm.ReadLog("test", 0, "")
	if err != nil || !first.Reset || first.Text != "first\n" {
		t.Fatalf("first read: %+v %v", first, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately split a UTF-8 code point across reads.
	f.Write([]byte{0xe4, 0xb8})
	partial, err := pm.ReadLog("test", first.Offset, first.Version)
	if err != nil || partial.Text != "" || partial.Offset != first.Offset {
		t.Fatalf("incomplete UTF-8 consumed: %+v %v", partial, err)
	}
	f.Write([]byte{0xad, '\n'})
	f.Close()
	next, err := pm.ReadLog("test", partial.Offset, partial.Version)
	if err != nil || next.Reset || next.Text != "中\n" {
		t.Fatalf("increment: %+v %v", next, err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxLogChunk*2), 0600); err != nil {
		t.Fatal(err)
	}
	bounded, err := pm.ReadLog("test", 0, "")
	if err != nil || len(bounded.Text) > maxLogChunk || !bounded.Reset {
		t.Fatalf("unbounded read: %v", err)
	}
	if err := pm.ClearLog("test"); err != nil {
		t.Fatal(err)
	}
	reset, err := pm.ReadLog("test", bounded.Offset, bounded.Version)
	if err != nil || !reset.Reset || reset.Text != "" {
		t.Fatalf("clear reset failed: %+v %v", reset, err)
	}
	if _, err := pm.ReadLog("../escape", 0, ""); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestRunningLogsRotateResetAndRedact(t *testing.T) {
	pm := NewProcessManager(t.TempDir())
	defer pm.StopAll()
	if err := pm.Start("test", "test", "Write-Output 'test-secret'; Start-Sleep -Seconds 30", "display", "", "powershell", "test-secret"); err != nil {
		t.Fatal(err)
	}
	first, err := pm.ReadLog("test", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	pm.mu.RLock()
	writer := pm.processes["test"].LogFile
	pm.mu.RUnlock()
	if _, err := writer.Write(bytes.Repeat([]byte("x"), maxLogSize+100)); err != nil {
		t.Fatal(err)
	}
	next, err := pm.ReadLog("test", first.Offset, first.Version)
	if err != nil || !next.Reset || next.Version == first.Version {
		t.Fatalf("rotation not detected: %v", err)
	}
	if err := pm.ClearLog("test"); err == nil {
		t.Fatal("cleared running log")
	}
	if err := pm.Stop("test"); err != nil {
		t.Fatal(err)
	}
	if pm.GetState("test").Running {
		t.Fatal("stop returned before completion")
	}
	files, _ := filepath.Glob(filepath.Join(pm.logDir, "test.log*"))
	for _, file := range files {
		data, _ := os.ReadFile(file)
		if bytes.Contains(data, []byte("test-secret")) {
			t.Fatal("secret output reached disk")
		}
	}
}

func TestStopFailurePreventsDeleteAndArchive(t *testing.T) {
	dir := t.TempDir()
	cfg := NewConfig(dir)
	cmd := addTestCommand(t, cfg, Command{Name: "must remain"})
	pm := NewProcessManager(dir)
	// A query-only job handle cannot terminate a job: deterministic failure,
	// without launching an unkillable external process.
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("CommandPanelTest-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	const jobQuery = 0x0004
	var query windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), job, windows.CurrentProcess(), &query, jobQuery, false, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(query)
	pm.processes[cmd.ID] = &ProcessInfo{Running: true, Job: &processJob{handle: query}, Done: make(chan struct{})}
	app := &App{config: cfg, manager: pm}
	if _, err := app.DeleteCommand(cmd.ID); err == nil {
		t.Fatal("delete ignored stop failure")
	}
	if _, err := app.ArchiveCommand(cmd.ID); err == nil {
		t.Fatal("archive ignored stop failure")
	}
	if cfg.GetCommand(cmd.ID) == nil || cfg.GetCommand(cmd.ID).Archived {
		t.Fatal("configuration changed despite stop failure")
	}
	if pm.processes[cmd.ID].Stopped {
		t.Fatal("failed stop incorrectly marked stopped")
	}
}

func TestProcessTreeRemainsManagedAfterShellExits(t *testing.T) {
	pm := NewProcessManager(t.TempDir())
	defer pm.StopAll()
	command := "Start-Process powershell -ArgumentList '-NoProfile -NonInteractive -Command Start-Sleep -Seconds 30' -WindowStyle Hidden"
	if err := pm.Start("tree", "tree", command, "child test", "", "powershell"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if !pm.GetState("tree").Running {
		t.Fatal("child process lost after shell exit")
	}
	if err := pm.Stop("tree"); err != nil {
		t.Fatal(err)
	}
	if state := pm.GetState("tree"); state.Running || !state.Stopped {
		t.Fatalf("unexpected stopped state: %+v", state)
	}
	if err := pm.StopAll(); err != nil {
		t.Fatal(err)
	}
	if err := pm.Start("new", "new", "echo no", "no", "", "cmd"); err == nil || !strings.Contains(err.Error(), "退出") {
		t.Fatal("start allowed during shutdown")
	}
}

func TestQuotedExecutablePathsExecuteInBothShells(t *testing.T) {
	for _, engine := range []string{"powershell", "cmd"} {
		t.Run(engine, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "Program Files & O'Neil")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "test.ps1")
			contents := "Write-Output 'picked-executable-ran'"
			command := "& '" + strings.ReplaceAll(path, "'", "''") + "'"
			if engine == "cmd" {
				path = filepath.Join(dir, "test.cmd")
				contents = "@echo picked-executable-ran\r\n"
				command = `"` + path + `"`
			}
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			pm := NewProcessManager(t.TempDir())
			defer pm.StopAll()
			if err := pm.Start("pick", "pick", command, command, "", engine); err != nil {
				t.Fatal(err)
			}
			pm.mu.RLock()
			done := pm.processes["pick"].Done
			pm.mu.RUnlock()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("command did not exit")
			}
			chunk, err := pm.ReadLog("pick", 0, "")
			if err != nil || !strings.Contains(chunk.Text, "picked-executable-ran") || pm.GetState("pick").ExitCode != 0 {
				t.Fatalf("picked executable failed: %s, %v", chunk.Text, err)
			}
		})
	}
}
