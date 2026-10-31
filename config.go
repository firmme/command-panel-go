//go:build windows

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
	mu           sync.RWMutex
	commands     []Command
	filePath     string
	loadErr      error
	needsRewrite bool
}

func NewConfig(baseDir string) *Config {
	cfg := &Config{
		filePath: filepath.Join(baseDir, configFile),
	}
	cfg.loadErr = cfg.load()
	if cfg.loadErr == nil && cfg.needsRewrite {
		cfg.loadErr = cfg.save()
	}
	return cfg
}

func (c *Config) load() error {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.commands = []Command{}
			return nil
		}
		return fmt.Errorf("读取配置失败: %w", err)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return fmt.Errorf("配置必须为 JSON 对象，已保留原文件: %s", c.filePath)
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
		seen := make(map[string]bool)
		migrate := false
		for _, cmd := range c.commands {
			if !validCommandID(cmd.ID) || seen[cmd.ID] {
				return fmt.Errorf("配置包含无效或重复的命令 ID")
			}
			seen[cmd.ID] = true
			for _, v := range cmd.Variables {
				if v.Value != "" && v.ProtectedValue == "" {
					migrate = true
				}
			}
		}
		if err := c.decryptVariables(); err != nil {
			return err
		}
		c.needsRewrite = migrate
		return nil
	}
	if _, exists := object["commands"]; exists {
		return fmt.Errorf("commands 必须为命令数组，已保留原文件: %s", c.filePath)
	}

	// Try legacy format: { "name": "command" }
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err == nil {
		c.commands = []Command{}
		names := make([]string, 0, len(legacy))
		for name := range legacy {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cmd := legacy[name]
			if string(object[name]) == "null" {
				return fmt.Errorf("旧格式命令不能为空")
			}
			c.commands = append(c.commands, Command{
				ID:        generateID(),
				Name:      name,
				Command:   cmd,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		}
		c.renumberLocked()
		c.needsRewrite = true
		return nil
	}

	return fmt.Errorf("配置格式无效，已保留原文件: %s", c.filePath)
}

func (c *Config) save() error {
	if c.loadErr != nil {
		return c.loadErr
	}
	disk := make([]Command, len(c.commands))
	for i, cmd := range c.commands {
		disk[i] = cloneCommand(cmd)
		for j := range disk[i].Variables {
			v := &disk[i].Variables[j]
			protected, err := protectSecret(v.Value)
			if err != nil {
				return fmt.Errorf("加密变量失败: %w", err)
			}
			v.ProtectedValue, v.Value = protected, ""
		}
	}
	data, err := json.MarshalIndent(CommandStore{Commands: disk}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.filePath, data)
}

func (c *Config) decryptVariables() error {
	for i := range c.commands {
		for j := range c.commands[i].Variables {
			v := &c.commands[i].Variables[j]
			if v.ProtectedValue == "" {
				continue
			}
			value, err := unprotectSecret(v.ProtectedValue)
			if err != nil {
				return fmt.Errorf("解密变量失败（需要原 Windows 用户）: %w", err)
			}
			v.Value, v.ProtectedValue = value, ""
		}
	}
	return nil
}

// Mutations keep the original in-memory snapshot until persistence succeeds.
func (c *Config) checkpointLocked() func() {
	before := make([]Command, len(c.commands))
	for i, cmd := range c.commands {
		before[i] = cloneCommand(cmd)
	}
	return func() { c.commands = before }
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

func (c *Config) AddCommand(cmd Command) (Command, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
	cmd.ID = generateID()
	cmd.CreatedAt = time.Now()
	cmd.UpdatedAt = time.Now()
	c.commands = append([]Command{cloneCommand(cmd)}, c.commands...)
	c.renumberLocked()
	if err := c.save(); err != nil {
		rollback()
		return Command{}, err
	}
	return cloneCommand(c.commands[0]), nil
}

// renumberLocked rewrites Order fields to match slice order (caller holds the lock)
func (c *Config) renumberLocked() {
	for i := range c.commands {
		c.commands[i].Order = i
	}
}

func (c *Config) DuplicateCommand(id string) (*Command, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
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
			if err := c.save(); err != nil {
				rollback()
				return nil, err
			}
			result := cloneCommand(c.commands[i+1])
			return &result, nil
		}
	}
	return nil, fmt.Errorf("命令不存在: %s", id)
}

func (c *Config) UpdateCommand(cmd Command) (*Command, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
	for i := range c.commands {
		if c.commands[i].ID == cmd.ID {
			cmd.CreatedAt = c.commands[i].CreatedAt
			cmd.Archived = c.commands[i].Archived
			cmd.ArchivedAt = c.commands[i].ArchivedAt
			cmd.Order = c.commands[i].Order
			cmd.UpdatedAt = time.Now()
			c.commands[i] = cloneCommand(cmd)
			if err := c.save(); err != nil {
				rollback()
				return nil, err
			}
			result := cloneCommand(c.commands[i])
			return &result, nil
		}
	}
	return nil, fmt.Errorf("命令不存在: %s", cmd.ID)
}

func (c *Config) DeleteCommand(id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
	for i := range c.commands {
		if c.commands[i].ID == id {
			c.commands = append(c.commands[:i], c.commands[i+1:]...)
			c.renumberLocked()
			if err := c.save(); err != nil {
				rollback()
				return false, err
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("命令不存在: %s", id)
}

func (c *Config) ArchiveCommand(id string) (*Command, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
	for i := range c.commands {
		if c.commands[i].ID == id {
			c.commands[i].Archived = true
			c.commands[i].ArchivedAt = time.Now().Format(time.RFC3339)
			c.commands[i].UpdatedAt = time.Now()
			if err := c.save(); err != nil {
				rollback()
				return nil, err
			}
			result := cloneCommand(c.commands[i])
			return &result, nil
		}
	}
	return nil, fmt.Errorf("命令不存在: %s", id)
}

func (c *Config) RestoreCommand(id string) (*Command, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
	for i := range c.commands {
		if c.commands[i].ID == id {
			c.commands[i].Archived = false
			c.commands[i].ArchivedAt = ""
			c.commands[i].UpdatedAt = time.Now()
			if err := c.save(); err != nil {
				rollback()
				return nil, err
			}
			result := cloneCommand(c.commands[i])
			return &result, nil
		}
	}
	return nil, fmt.Errorf("命令不存在: %s", id)
}

func (c *Config) ReorderCommands(ids []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rollback := c.checkpointLocked()
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
	if err := c.save(); err != nil {
		rollback()
		return err
	}
	return nil
}

func generateID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err == nil {
		return "cmd-" + hex.EncodeToString(b)
	}
	return fmt.Sprintf("cmd-%x", time.Now().UnixNano())
}
