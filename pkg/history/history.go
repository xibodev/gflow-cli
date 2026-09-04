package history

import (
	"encoding/json"
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

var mu sync.Mutex

func getHistoryPath() (string, error) {
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

// Add appends a new generation entry to history.json.
func Add(entry Entry) error {
	mu.Lock()
	defer mu.Unlock()

	path, err := getHistoryPath()
	if err != nil {
		return err
	}

	var entries []Entry
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &entries)
	}

	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	// Prepend newest entry first
	entries = append([]Entry{entry}, entries...)

	// Cap at 500 entries
	if len(entries) > 500 {
		entries = entries[:500]
	}

	updated, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, updated, 0644)
}

// List returns the most recent history entries up to limit.
func List(limit int) ([]Entry, error) {
	mu.Lock()
	defer mu.Unlock()

	path, err := getHistoryPath()
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
