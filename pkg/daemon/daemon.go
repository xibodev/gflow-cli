package daemon

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// EnsureRunning checks if the server is running on host:port, and starts it in the background if not.
func EnsureRunning(host string, port int) error {
	healthURL := fmt.Sprintf("http://%s:%d/health", host, port)

	// Check if already running
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(healthURL)
	if err == nil {
		resp.Body.Close()
		return nil // Already running!
	}

	// Not running, launch background process
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

	// Wait up to 4 seconds for server to start listening
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			return nil
		}
	}

	return fmt.Errorf("background server started but did not respond on %s within 4s", healthURL)
}
