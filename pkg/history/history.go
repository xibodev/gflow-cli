package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry records details of a generated asset.
type Entry struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // "image" or "video"
	Prompt    string    `json:"prompt"`
	LocalPath string    `json:"local_path"`
	URL       string    `json:"url,omitempty"`
	Aspect    string    `json:"aspect,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	mu           sync.Mutex
	pathOverride string
	lockTimeout  = 5 * time.Second
)

// SetPathOverride injects the history file path in tests.
func SetPathOverride(p string) { mu.Lock(); defer mu.Unlock(); pathOverride = p }

// lockFile provides bounded cross-process mutual exclusion using an
// exclusively-created lock file with stale-lock recovery. It avoids new
// dependencies while preventing parallel CLI/MCP processes from losing entries.
func lockFile(path string) (func(), error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, serr := os.Stat(lockPath); serr == nil && time.Since(info.ModTime()) > 10*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for history lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Add appends a new generation entry atomically across processes.
func Add(entry Entry) error {
	mu.Lock()
	defer mu.Unlock()
	path, err := getHistoryPathLocked()
	if err != nil {
		return err
	}
	unlock, err := lockFile(path)
	if err != nil {
		return err
	}
	defer unlock()

	var entries []Entry
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("history is corrupt; refusing to overwrite: %w", err)
		}
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	entries = append([]Entry{entry}, entries...)
	if len(entries) > 500 {
		entries = entries[:500]
	}
	updated, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "history-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// List returns the most recent history entries up to limit.
func List(limit int) ([]Entry, error) {
	mu.Lock()
	defer mu.Unlock()
	path, err := getHistoryPathLocked()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, nil
		}
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func getHistoryPathLocked() (string, error) {
	ov := pathOverride
	if ov != "" {
		if err := os.MkdirAll(filepath.Dir(ov), 0755); err != nil {
			return "", err
		}
		return ov, nil
	}
	if h := os.Getenv("GFLOW_HISTORY_PATH"); h != "" {
		if err := os.MkdirAll(filepath.Dir(h), 0755); err != nil {
			return "", err
		}
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".gflow")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.json"), nil
}
