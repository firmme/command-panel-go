package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func (c *Config) GetCommands() []Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var result []Command
	for _, cmd := range c.commands {
		if !cmd.Archived {
			result = append(result, cmd)
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
			result = append(result, cmd)
		}
	}
	return result
}

func (c *Config) GetAllCommands() []Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.commands
}

func (c *Config) GetCommand(id string) *Command {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			return &c.commands[i]
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
	if cmd.Order == 0 {
		maxOrder := 0
		for _, existing := range c.commands {
			if existing.Order > maxOrder {
				maxOrder = existing.Order
			}
		}
		cmd.Order = maxOrder + 1
	}
	c.commands = append(c.commands, cmd)
	c.save()
	return cmd
}

func (c *Config) DuplicateCommand(id string) *Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.commands {
		if c.commands[i].ID == id {
			clone := c.commands[i]
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
			maxOrder := 0
			for _, existing := range c.commands {
				if existing.Order > maxOrder {
					maxOrder = existing.Order
				}
			}
			clone.Order = maxOrder + 1
			c.commands = append(c.commands, clone)
			c.save()
			return &c.commands[len(c.commands)-1]
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
			c.commands[i] = cmd
			c.save()
			return &c.commands[i]
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
			return &c.commands[i]
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
			return &c.commands[i]
		}
	}
	return nil
}

func (c *Config) ReorderCommands(ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idOrder := make(map[string]int)
	for i, id := range ids {
		idOrder[id] = i
	}
	for i := range c.commands {
		if order, ok := idOrder[c.commands[i].ID]; ok {
			c.commands[i].Order = order
		}
	}
	c.save()
}

func generateID() string {
	return fmt.Sprintf("c%x", time.Now().UnixNano()%0xFFFFFF)
}
