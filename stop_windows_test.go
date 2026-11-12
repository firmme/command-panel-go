//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Reproduces the reported bug: with a PowerShell engine whose shell starts a
// long-running child, Stop must terminate the whole tree without timing out,
// and the child must not survive.
func TestStopPowerShellTreeDoesNotTimeOut(t *testing.T) {
	dir := t.TempDir()
	sleeper := filepath.Join(dir, "sleeper.py")
	if err := os.WriteFile(sleeper, []byte("import time\nwhile True:\n    time.sleep(60)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"powershell", "powershell5", "cmd"} {
		t.Run(engine, func(t *testing.T) {
			pm := NewProcessManager(t.TempDir())
			defer pm.StopAll()
			command := fmt.Sprintf(`python "%s"`, sleeper)
			if err := pm.Start("stop-test", "stop-test", command, command, dir, engine); err != nil {
				t.Fatalf("start (%s): %v", engineLabel(engine), err)
			}
			time.Sleep(2500 * time.Millisecond)

			started := time.Now()
			if err := pm.Stop("stop-test"); err != nil {
				t.Fatalf("stop (%s) after %v: %v", engineLabel(engine), time.Since(started), err)
			}
			if took := time.Since(started); took > 4*time.Second {
				t.Fatalf("stop (%s) took %v", engineLabel(engine), took)
			}
			if state := pm.GetState("stop-test"); state.Running {
				t.Fatalf("%s still running after stop", engineLabel(engine))
			}
		})
	}
}
// The reported setup: the config file's engine was empty, which resolves to
// the newest PowerShell (7 on this machine) and used to time out on stop.
func TestStopResolvedPowerShellEngine(t *testing.T) {
	dir := t.TempDir()
	sleeper := filepath.Join(dir, "sleeper.py")
	if err := os.WriteFile(sleeper, []byte("import time\nwhile True:\n    time.sleep(60)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pm := NewProcessManager(t.TempDir())
	defer pm.StopAll()
	command := fmt.Sprintf(`python "%s"`, sleeper)
	if err := pm.Start("resolved", "resolved", command, command, dir, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	started := time.Now()
	if err := pm.Stop("resolved"); err != nil {
		t.Fatalf("stop (%s) after %v: %v", resolvePowerShell(), time.Since(started), err)
	}
	if took := time.Since(started); took > 4*time.Second {
		t.Fatalf("stop (%s) took %v", resolvePowerShell(), took)
	}
}
