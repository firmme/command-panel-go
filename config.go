package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const configFile = "commands.json"

type Config struct {
	mu       sync.RWMutex
	commands []Command
	filePath string
}

func NewConfig(baseDir string) *Config {
	cfg := &Config{
		filePath: filepath.Join(baseDir, configFile),
	}
	cfg.load()
	return cfg
}

func (c *Config) load() {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.commands = []Command{}
			return
		}
		fmt.Printf("Error reading config: %v\n", err)
		c.commands = []Command{}
		return
	}

	// Try new format first
	var store CommandStore
	if err := json.Unmarshal(data, &store); err == nil && store.Commands != nil {
		c.commands = store.Commands
		// One-time normalization: respect legacy Order values, after which
		// the slice order itself is authoritative.
		sort.SliceStable(c.commands, func(i, j int) bool {
			return c.commands[i].Order < c.commands[j].Order
		})
		c.renumberLocked()
		return
	}

	// Try legacy format: { "name": "command" }
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err == nil {
		c.commands = []Command{}
		for name, cmd := range legacy {
			c.commands = append(c.commands, Command{
				ID:        generateID(),
				Name:      name,
				Command:   cmd,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		}
		c.save()
		return
	}

	c.commands = []Command{}
}

func (c *Config) save() {
	store := CommandStore{Commands: c.commands}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling config: %v\n", err)
		return
	}
	if err := os.WriteFile(c.filePath, data, 0644); err != nil {
		fmt.Printf("Error writing config: %v\n", err)
	}
}

// The slice order is the display order: new commands go to the top.

func (c *Config) GetCommands() []Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var result []Command
	for _, cmd := range c.commands {
		if !cmd.Archived {
			result = append(result, cloneCommand(cmd))
		}
	}
	return result
}

func (c *Config) GetArchivedCommands() []Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var result []Command
	for _, cmd := range c.commands {
		if cmd.Archived {
			result = append(result, cloneCommand(cmd))
		}
	}
	return result
}

func (c *Config) GetAllCommands() []Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]Command, len(c.commands))
	for i, cmd := range c.commands {
		result[i] = cloneCommand(cmd)
	}
	return result
}

func (c *Config) GetCommand(id string) *Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			cmd := cloneCommand(c.commands[i])
			return &cmd
		}
	}
	return nil
}

func (c *Config) AddCommand(cmd Command) Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd.ID = generateID()
	cmd.CreatedAt = time.Now()
	cmd.UpdatedAt = time.Now()
	c.commands = append([]Command{cloneCommand(cmd)}, c.commands...)
	c.renumberLocked()
	c.save()
	return cmd
}

// renumberLocked rewrites Order fields to match slice order (caller holds the lock)
func (c *Config) renumberLocked() {
	for i := range c.commands {
		c.commands[i].Order = i
	}
}

func (c *Config) DuplicateCommand(id string) *Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			clone := cloneCommand(c.commands[i])
			clone.ID = generateID()
			if clone.Name == "" {
				clone.Name = inferName(clone.Command)
			}
			if clone.Name != "" {
				clone.Name = clone.Name + " 副本"
			}
			clone.Archived = false
			clone.ArchivedAt = ""
			clone.CreatedAt = time.Now()
			clone.UpdatedAt = time.Now()
			// Insert directly after the original
			c.commands = append(c.commands, Command{})
			copy(c.commands[i+2:], c.commands[i+1:])
			c.commands[i+1] = clone
			c.renumberLocked()
			c.save()
			result := cloneCommand(c.commands[i+1])
			return &result
		}
	}
	return nil
}

func (c *Config) UpdateCommand(cmd Command) *Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.commands {
		if c.commands[i].ID == cmd.ID {
			cmd.CreatedAt = c.commands[i].CreatedAt
			cmd.UpdatedAt = time.Now()
			c.commands[i] = cloneCommand(cmd)
			c.save()
			result := cloneCommand(c.commands[i])
			return &result
		}
	}
	return nil
}

func (c *Config) DeleteCommand(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			c.commands = append(c.commands[:i], c.commands[i+1:]...)
			c.save()
			return true
		}
	}
	return false
}

func (c *Config) ArchiveCommand(id string) *Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			c.commands[i].Archived = true
			c.commands[i].ArchivedAt = time.Now().Format(time.RFC3339)
			c.commands[i].UpdatedAt = time.Now()
			c.save()
			result := cloneCommand(c.commands[i])
			return &result
		}
	}
	return nil
}

func (c *Config) RestoreCommand(id string) *Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			c.commands[i].Archived = false
			c.commands[i].ArchivedAt = ""
			c.commands[i].UpdatedAt = time.Now()
			c.save()
			result := cloneCommand(c.commands[i])
			return &result
		}
	}
	return nil
}

func (c *Config) ReorderCommands(ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	byID := make(map[string]Command, len(c.commands))
	for _, cmd := range c.commands {
		byID[cmd.ID] = cmd
	}
	ordered := make([]Command, 0, len(c.commands))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if cmd, ok := byID[id]; ok && !seen[id] {
			ordered = append(ordered, cmd)
			seen[id] = true
		}
	}
	// Keep commands not in ids (e.g. archived) at the end
	for _, cmd := range c.commands {
		if !seen[cmd.ID] {
			ordered = append(ordered, cmd)
		}
	}
	c.commands = ordered
	c.renumberLocked()
	c.save()
}

func generateID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err == nil {
		return "cmd-" + hex.EncodeToString(b)
	}
	return fmt.Sprintf("cmd-%x", time.Now().UnixNano())
}
