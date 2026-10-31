//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".command-panel-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func protectSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	data := []byte(value)
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

func unprotectSecret(value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty encrypted value")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}

func prepareDataDir(legacyDirs []string) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "CommandPanel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, configFile)); err == nil {
		return dir, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	for _, legacy := range legacyDirs {
		if legacy == "" || filepath.Clean(legacy) == filepath.Clean(dir) {
			continue
		}
		source := filepath.Join(legacy, configFile)
		if _, err := os.Stat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		// Parse without rewriting the original configuration during migration.
		cfg := &Config{filePath: source}
		cfg.loadErr = cfg.load()
		if cfg.loadErr != nil {
			return "", fmt.Errorf("旧配置迁移失败: %w", cfg.loadErr)
		}
		// Preserve the meaning of previously implicit/relative working directories.
		for i := range cfg.commands {
			if !filepath.IsAbs(cfg.commands[i].CWD) {
				cfg.commands[i].CWD = filepath.Join(legacy, cfg.commands[i].CWD)
			}
		}
		cfg.filePath = filepath.Join(dir, configFile)
		if err := migrateLogs(legacy, dir, cfg.commands); err != nil {
			return "", err
		}
		if err := cfg.save(); err != nil {
			return "", err
		}
		return dir, nil
	}
	if err := NewConfig(dir).save(); err != nil {
		return "", err
	}
	return dir, nil
}

func migrateLogs(sourceDir, targetDir string, commands []Command) error {
	logDir := filepath.Join(targetDir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	for _, cmd := range commands {
		secrets := make([]string, 0, len(cmd.Variables))
		for _, v := range cmd.Variables {
			if v.Value != "" {
				secrets = append(secrets, v.Value)
			}
		}
		sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
		pairs := make([]string, 0, len(secrets)*2)
		for _, secret := range secrets {
			pairs = append(pairs, secret, "[REDACTED]")
		}
		redactor := strings.NewReplacer(pairs...)
		for i := 0; i <= logBackups; i++ {
			name := cmd.ID + ".log"
			if i > 0 {
				name = fmt.Sprintf("%s.%d", name, i)
			}
			data, err := readLegacyLog(filepath.Join(sourceDir, "logs", name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("迁移日志失败: %w", err)
			}
			data = []byte(redactor.Replace(string(data)))
			if len(data) > maxLogSize {
				data = data[len(data)-maxLogSize:]
			}
			if err := atomicWrite(filepath.Join(logDir, name), data); err != nil {
				return err
			}
		}
	}
	return nil
}

func readLegacyLog(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := int64(0)
	if stat.Size() > maxLogSize {
		start = stat.Size() - maxLogSize
	}
	return io.ReadAll(io.NewSectionReader(f, start, maxLogSize))
}

func showStartupError(err error) {
	message, _ := windows.UTF16PtrFromString(err.Error())
	title, _ := windows.UTF16PtrFromString("命令面板启动失败")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
