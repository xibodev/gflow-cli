package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/config"
)

var browserUrl string

var browserCmd = &cobra.Command{
	Use:     "browser",
	Aliases: []string{"chrome", "open-browser"},
	Short:   "Launch an extension-free browser window with CDP debugging enabled for Google Flow",
	Long: `Launches Chrome with --remote-debugging-port=9222 and a dedicated profile.
This allows gflow to generate images and videos via Google Flow completely extension-free.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.LoadConfig()
		port := cfg.CDPPort
		if port <= 0 {
			port = 9222
		}

		chromePath, err := findChromePath()
		if err != nil {
			return err
		}

		profileDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "FlowProfile")
		_ = os.MkdirAll(profileDir, 0700)

		targetURL := browserUrl
		if targetURL == "" {
			targetURL = "https://labs.google/fx/tools/flow"
		}

		argsList := []string{
			fmt.Sprintf("--remote-debugging-port=%d", port),
			fmt.Sprintf("--user-data-dir=%s", profileDir),
			"--no-first-run",
			"--no-default-browser-check",
			targetURL,
		}

		fmt.Printf("Launching Chrome for Google Flow (extension-free, CDP port %d)...\n", port)
		fmt.Printf("Profile location: %s\n", profileDir)
		fmt.Println("Log in to your Google Account once in this window. Then run 'gflow image ... --provider flow' from any terminal!")

		proc := exec.Command(chromePath, argsList...)
		if err := proc.Start(); err != nil {
			return fmt.Errorf("failed to start Chrome: %w", err)
		}

		return nil
	},
}

func init() {
	browserCmd.Flags().StringVarP(&browserUrl, "url", "u", "https://labs.google/fx/tools/flow", "URL to open in the browser")
	rootCmd.AddCommand(browserCmd)
}

func findChromePath() (string, error) {
	if runtime.GOOS == "windows" {
		candidates := []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
		return "", errors.New("Chrome executable not found on Windows. Please install Google Chrome")
	}

	if runtime.GOOS == "darwin" {
		c := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
		return "", errors.New("Google Chrome not found in /Applications")
	}

	for _, bin := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(bin); err == nil {
			return path, nil
		}
	}

	return "", errors.New("Chrome or Chromium browser not found on PATH")
}
