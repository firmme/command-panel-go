//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func addTestCommand(t *testing.T, cfg *Config, cmd Command) Command {
	t.Helper()
	result, err := cfg.AddCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConfigRejectsCorruptionAndRollsBackFailedWrites(t *testing.T) {
	for _, invalid := range []string{`{"commands":`, `{"commands":null}`, `null`, `{"commands":[{"id":"../escape"}]}`} {
		t.Run(invalid, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, configFile)
			if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := NewConfig(dir)
			if cfg.loadErr == nil {
				t.Fatal("invalid config accepted")
			}
			if _, err := cfg.AddCommand(Command{Name: "replacement"}); err == nil {
				t.Fatal("invalid config overwritten")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != invalid {
				t.Fatal("original config changed")
			}
		})
	}
	cfg := NewConfig(t.TempDir())
	cmd := addTestCommand(t, cfg, Command{Name: "original"})
	before := cfg.GetAllCommands()
	cfg.filePath = filepath.Join(t.TempDir(), "missing", configFile)
	operations := []func() error{
		func() error { _, err := cfg.AddCommand(Command{Name: "new"}); return err },
		func() error { _, err := cfg.UpdateCommand(Command{ID: cmd.ID, Name: "updated"}); return err },
		func() error { _, err := cfg.DuplicateCommand(cmd.ID); return err },
		func() error { _, err := cfg.ArchiveCommand(cmd.ID); return err },
		func() error { _, err := cfg.RestoreCommand(cmd.ID); return err },
		func() error { _, err := cfg.DeleteCommand(cmd.ID); return err },
		func() error { return cfg.ReorderCommands([]string{cmd.ID}) },
	}
	for _, operation := range operations {
		if err := operation(); err == nil {
			t.Fatal("expected persistence failure")
		}
		if !reflect.DeepEqual(before, cfg.GetAllCommands()) {
			t.Fatal("failed operation mutated memory")
		}
	}
}

func TestEditingPreservesArchiveAndOrderAfterReload(t *testing.T) {
	dir := t.TempDir()
	cfg := NewConfig(dir)
	first := addTestCommand(t, cfg, Command{Name: "first"})
	second := addTestCommand(t, cfg, Command{Name: "second"})
	archived, err := cfg.ArchiveCommand(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cfg.UpdateCommand(Command{ID: first.ID, Name: "edited", Command: "echo updated"})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Archived || updated.ArchivedAt != archived.ArchivedAt || updated.Order != archived.Order {
		t.Fatal("metadata lost")
	}
	reloaded := NewConfig(dir)
	if reloaded.loadErr != nil {
		t.Fatal(reloaded.loadErr)
	}
	if got := reloaded.GetAllCommands(); got[0].ID != second.ID || got[1].ID != first.ID || !got[1].Archived {
		t.Fatalf("unexpected reload: %+v", got)
	}
}

func TestSecretsEncryptedAtRestAndLegacyValuesMigrate(t *testing.T) {
	dir := t.TempDir()
	const secret = "test-only-secret-敏感"
	plain := []byte(`{"commands":[{"id":"test","variables":[{"name":"password","value":"` + secret + `"}]}]}`)
	path := filepath.Join(dir, configFile)
	if err := os.WriteFile(path, plain, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(dir)
	if cfg.loadErr != nil {
		t.Fatal(cfg.loadErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) || !bytes.Contains(data, []byte("protectedValue")) {
		t.Fatal("secret persisted as plaintext")
	}
	reloaded := NewConfig(dir)
	if reloaded.loadErr != nil {
		t.Fatal(reloaded.loadErr)
	}
	if got := reloaded.GetCommand("test").Variables[0].Value; got != secret {
		t.Fatal("secret did not round trip")
	}
}

func TestMigrationPreservesWorkingDirectoriesAndExistingTarget(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	legacy := t.TempDir()
	cfg := NewConfig(legacy)
	cmd := addTestCommand(t, cfg, Command{Name: "relative", CWD: "scripts", Variables: []CommandVariable{{Name: "key", Value: "migration-secret"}}})
	if err := os.MkdirAll(filepath.Join(legacy, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "logs", cmd.ID+".log"), []byte("old migration-secret log"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := prepareDataDir([]string{legacy})
	if err != nil {
		t.Fatal(err)
	}
	got := NewConfig(dir)
	if got.loadErr != nil {
		t.Fatal(got.loadErr)
	}
	if got.GetCommand(cmd.ID).CWD != filepath.Join(legacy, "scripts") {
		t.Fatal("relative cwd changed meaning")
	}
	log, err := os.ReadFile(filepath.Join(dir, "logs", cmd.ID+".log"))
	if err != nil || string(log) != "old [REDACTED] log" {
		t.Fatalf("log migration failed: %q, %v", log, err)
	}
	addTestCommand(t, cfg, Command{Name: "later"})
	if _, err := prepareDataDir([]string{legacy}); err != nil {
		t.Fatal(err)
	}
	if len(NewConfig(dir).GetAllCommands()) != 1 {
		t.Fatal("existing target was overwritten")
	}
	if _, err := os.Stat(filepath.Join(legacy, configFile)); err != nil {
		t.Fatal("legacy config removed")
	}
}

func TestAutostartUsesQuotedExecutableWithoutShell(t *testing.T) {
	script := startupScript(`C:\Program Files\A&B\command-panel.exe`)
	if bytes.Contains([]byte(script), []byte("cmd /c")) || !bytes.Contains([]byte(script), []byte(`Chr(34) & "C:\Program Files\A&B\command-panel.exe" & Chr(34)`)) {
		t.Fatal(script)
	}
}
