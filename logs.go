//go:build windows

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxLogSize = 5 * 1024 * 1024
const logBackups = 2
const maxLogChunk = 256 * 1024

// rotatingLog serializes stdout/stderr, redacts values across Write boundaries,
// and keeps at most three 5 MiB files per command.
type rotatingLog struct {
	mu        sync.Mutex
	file      *os.File
	path      string
	size      int64
	secrets   []string
	pending   []byte
	maxSecret int
	revision  int64
}

func openLog(path string, secrets []string) (*rotatingLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	w := &rotatingLog{file: f, path: path, size: info.Size(), revision: time.Now().UnixNano()}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		w.secrets = append(w.secrets, secret)
		if len(secret) > w.maxSecret {
			w.maxSecret = len(secret)
		}
	}
	sort.Slice(w.secrets, func(i, j int) bool { return len(w.secrets[i]) > len(w.secrets[j]) })
	return w, nil
}

func (w *rotatingLog) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if err := os.Remove(fmt.Sprintf("%s.%d", w.path, logBackups)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := logBackups - 1; i >= 0; i-- {
		source := w.path
		if i > 0 {
			source = fmt.Sprintf("%s.%d", w.path, i)
		}
		if err := os.Rename(source, fmt.Sprintf("%s.%d", w.path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file, w.size = f, 0
	w.revision++
	return nil
}

func (w *rotatingLog) writeBytes(data []byte) error {
	for len(data) > 0 {
		if w.file == nil {
			return fmt.Errorf("log file is closed")
		}
		if w.size >= maxLogSize {
			if err := w.rotate(); err != nil {
				return err
			}
		}
		n := len(data)
		if remaining := maxLogSize - w.size; int64(n) > remaining {
			n = int(remaining)
		}
		written, err := w.file.Write(data[:n])
		w.size += int64(written)
		if err != nil {
			return err
		}
		if written != n {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (w *rotatingLog) flush(final bool) error {
	limit := len(w.pending)
	if !final && w.maxSecret > 1 {
		limit -= w.maxSecret - 1
	}
	var out strings.Builder
	i := 0
	for i < limit {
		matched := false
		for _, secret := range w.secrets {
			if bytes.HasPrefix(w.pending[i:], []byte(secret)) {
				out.WriteString("[REDACTED]")
				i += len(secret)
				matched = true
				break
			}
		}
		if !matched {
			out.WriteByte(w.pending[i])
			i++
		}
	}
	w.pending = append(w.pending[:0], w.pending[i:]...)
	if cap(w.pending) > 64*1024 && len(w.pending) < 32*1024 {
		w.pending = append([]byte(nil), w.pending...)
	}
	return w.writeBytes([]byte(out.String()))
}

func (w *rotatingLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.secrets) == 0 {
		if err := w.writeBytes(data); err != nil {
			return 0, err
		}
		return len(data), nil
	}
	w.pending = append(w.pending, data...)
	if err := w.flush(false); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *rotatingLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.flush(true)
	if w.file != nil {
		closeErr := w.file.Close()
		w.file = nil
		if err == nil {
			err = closeErr
		}
	}
	return err
}

type LogChunk struct {
	Text    string `json:"text"`
	Offset  int64  `json:"offset"`
	Version string `json:"version"`
	Reset   bool   `json:"reset"`
}

func validCommandID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\:`) && filepath.Base(id) == id
}

func (pm *ProcessManager) ReadLog(id string, offset int64, version string) (LogChunk, error) {
	if !validCommandID(id) {
		return LogChunk{}, fmt.Errorf("invalid command ID")
	}
	pm.mu.RLock()
	info := pm.processes[id]
	pm.mu.RUnlock()
	if info != nil && info.LogFile != nil {
		info.LogFile.mu.Lock()
		defer info.LogFile.mu.Unlock()
	}
	f, err := os.Open(filepath.Join(pm.logDir, id+".log"))
	if os.IsNotExist(err) {
		return LogChunk{Reset: true}, nil
	}
	if err != nil {
		return LogChunk{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return LogChunk{}, err
	}
	identity := fmt.Sprint(stat.Sys().(*syscall.Win32FileAttributeData).CreationTime.Nanoseconds())
	if info != nil && info.LogFile != nil {
		identity += fmt.Sprintf("-%d", info.LogFile.revision)
	}
	reset := version != identity || offset < 0 || offset > stat.Size()
	if reset {
		offset = 0
		if stat.Size() > maxLogChunk {
			offset = stat.Size() - maxLogChunk
		}
	}
	// A slow reader skips to a bounded tail instead of accumulating a backlog.
	if stat.Size()-offset > maxLogChunk {
		reset = true
		offset = stat.Size() - maxLogChunk
	}
	data := make([]byte, stat.Size()-offset)
	n, err := f.ReadAt(data, offset)
	if err != nil && err != io.EOF {
		return LogChunk{}, err
	}
	data = data[:n]
	start, end := 0, len(data)
	if reset {
		for start < end && !utf8.RuneStart(data[start]) {
			start++
		}
	}
	// Leave an incomplete UTF-8 suffix for the next read.
	for end > start && !utf8.Valid(data[start:end]) {
		_, size := utf8.DecodeLastRune(data[start:end])
		if size != 1 || data[end-1] < 0x80 {
			break
		}
		end--
		if len(data)-end == utf8.UTFMax {
			end = len(data)
			break
		}
	}
	return LogChunk{Text: strings.ToValidUTF8(string(data[start:end]), "�"), Offset: offset + int64(end), Version: identity, Reset: reset}, nil
}
