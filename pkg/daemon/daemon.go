package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// EnsureRunning checks if the server is running on host:port, and starts it in the background if not.
// It preserves the legacy signature for compatibility; use EnsureRunningWithAuth
// to also verify product identity and authenticated readiness.
func EnsureRunning(host string, port int) error {
	return EnsureRunningWithAuth(host, port, "")
}

// EnsureRunningWithAuth verifies the expected gflow daemon identity and, when
// apiToken is set, authenticated readiness. It distinguishes an unrelated
// service/port collision from a healthy daemon.
func EnsureRunningWithAuth(host string, port int, apiToken string) error {
	healthURL := fmt.Sprintf("http://%s:%d/health", host, port)
	client := &http.Client{Timeout: 800 * time.Millisecond}

	if err := probeProduct(client, healthURL); err == nil {
		if apiToken == "" {
			return nil
		}
		if err := probeAuth(client, host, port, apiToken); err != nil {
			return err
		}
		return nil
	} else if isCollision(err) {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not resolve executable path: %w", err)
	}
	cmd := exec.Command(exePath, "serve", "--port", fmt.Sprintf("%d", port), "--host", host)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	setDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start background server: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if err := probeProduct(client, healthURL); err == nil {
			if apiToken != "" {
				if aerr := probeAuth(client, host, port, apiToken); aerr != nil {
					// Daemon restarted with fresh tokens; surface clearly.
					return aerr
				}
			}
			return nil
		} else if isCollision(err) {
			return err
		}
	}
	return fmt.Errorf("background server started but did not respond on %s within 4s", healthURL)
}

type collisionError struct{ msg string }

func (e *collisionError) Error() string { return e.msg }

func isCollision(err error) bool {
	if err == nil {
		return false
	}
	var c *collisionError
	return asCollision(err, &c)
}

func asCollision(err error, target **collisionError) bool {
	if err == nil {
		return false
	}
	if c, ok := err.(*collisionError); ok {
		*target = c
		return true
	}
	// Unwrap one level for fmt.Errorf %w chains without importing errors pkg alias.
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return asCollision(u.Unwrap(), target)
	}
	return false
}

func probeProduct(client *http.Client, healthURL string) error {
	resp, err := client.Get(healthURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &collisionError{fmt.Sprintf("unexpected status %s from %s; not a gflow daemon", resp.Status, healthURL)}
	}
	var h struct {
		Product string `json:"product"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return &collisionError{fmt.Sprintf("unexpected non-gflow service on %s", healthURL)}
	}
	if h.Product != "" && h.Product != "gflow" {
		return &collisionError{fmt.Sprintf("port collision: %s serves product %q, not gflow", healthURL, h.Product)}
	}
	return nil
}

func probeAuth(client *http.Client, host string, port int, apiToken string) error {
	url := fmt.Sprintf("http://%s:%d/v1/status", host, port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("daemon on %s:%d rejected local credentials; restart it via 'gflow serve' after 'gflow setup' to pick up current auth", host, port)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon status check failed: %s", resp.Status)
	}
	return nil
}
