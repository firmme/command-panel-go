//go:build windows

package main

import (
	"sync"
	"testing"
)

func TestConfigSnapshotsAndReorder(t *testing.T) {
	cfg := NewConfig(t.TempDir())
	first, err := cfg.AddCommand(Command{Name: "first", Variables: []CommandVariable{{Name: "key", Value: "keep"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := cfg.AddCommand(Command{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	all := cfg.GetAllCommands()
	all[1].Name = "changed"
	all[1].Variables[0].Value = "changed"
	got := cfg.GetCommand(first.ID)
	got.Variables[0].Value = "also changed"
	if saved := cfg.GetCommand(first.ID); saved.Name != "first" || saved.Variables[0].Value != "keep" {
		t.Fatalf("returned data mutated config: %+v", saved)
	}
	if err := cfg.ReorderCommands([]string{first.ID, first.ID, "missing", second.ID}); err != nil {
		t.Fatal(err)
	}
	ordered := cfg.GetAllCommands()
	if len(ordered) != 2 || ordered[0].ID != first.ID || ordered[1].ID != second.ID {
		t.Fatalf("reorder duplicated or lost commands: %+v", ordered)
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
