package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConfigSnapshotsAndReorder(t *testing.T) {
	cfg := NewConfig(t.TempDir())
	first := cfg.AddCommand(Command{Name: "first", Variables: []CommandVariable{{Name: "key", Value: "keep"}}})
	second := cfg.AddCommand(Command{Name: "second"})
	all := cfg.GetAllCommands()
	all[1].Name = "changed"
	all[1].Variables[0].Value = "changed"
	got := cfg.GetCommand(first.ID)
	got.Variables[0].Value = "also changed"
	if saved := cfg.GetCommand(first.ID); saved.Name != "first" || saved.Variables[0].Value != "keep" {
		t.Fatalf("returned data mutated config: %+v", saved)
	}
	cfg.ReorderCommands([]string{first.ID, first.ID, "missing", second.ID})
	ordered := cfg.GetAllCommands()
	if len(ordered) != 2 || ordered[0].ID != first.ID || ordered[1].ID != second.ID {
		t.Fatalf("reorder duplicated or lost commands: %+v", ordered)
	}
}

func TestLogTailHandlesTrailingNewline(t *testing.T) {
	pm := NewProcessManager(t.TempDir())
	for _, value := range []string{"one\ntwo\nthree\n", "one\ntwo\nthree"} {
		if err := os.WriteFile(filepath.Join(pm.logDir, "test.log"), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		if tail := pm.GetLogTail("test", 1); tail != "three" {
			t.Fatalf("tail = %q", tail)
		}
		if tail := pm.GetLogTail("test", 2); tail != "two\nthree" {
			t.Fatalf("tail = %q", tail)
		}
	}
	if tail := pm.GetLogTail("test", -1); tail != "" {
		t.Fatalf("negative tail = %q", tail)
	}
}

func TestConcurrentStartsLaunchOnce(t *testing.T) {
	pm := NewProcessManager(t.TempDir())
	defer pm.StopAll()
	var group sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errors <- pm.Start("test", "test", "Start-Sleep -Seconds 10", "sleep", "", "powershell")
		}()
	}
	group.Wait()
	close(errors)
	started := 0
	for err := range errors {
		if err == nil {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("started %d processes", started)
	}
}
