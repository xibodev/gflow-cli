package main

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/embedded"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:     "setup",
	Aliases: []string{"install-ext", "init"},
	Short:   "Install the Chrome extension and start the background agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("⚡ Setting up gflow...")

		// 1. Extract embedded extension
		extDir, err := embedded.ExtractExtension("")
		if err != nil {
			return fmt.Errorf("failed to extract extension: %w", err)
		}

		fmt.Println()
		fmt.Printf("✔ Chrome Extension extracted to:\n   %s\n", extDir)
		fmt.Println()

		// 2. Ensure background daemon is running
		cfg := config.LoadConfig()
		if err := daemon.EnsureRunning(cfg.Host, cfg.Port); err != nil {
			fmt.Printf("Notice: could not start background server automatically: %v\n", err)
		} else {
			fmt.Printf("✔ Background agent server running on http://%s:%d\n", cfg.Host, cfg.Port)
		}

		fmt.Println()
		fmt.Println("=================================================================")
		fmt.Println("  ONE-TIME STEP TO FINISH SETUP (Takes 10 seconds):")
		fmt.Println("=================================================================")
		fmt.Println("  1. In Chrome, open:  chrome://extensions")
		fmt.Println("  2. Turn ON 'Developer mode' (toggle switch in the top-right).")
		fmt.Println("  3. Click 'Load unpacked' and select the directory:")
		fmt.Printf("     %s\n", extDir)
		fmt.Println("  4. Open Google Flow: https://labs.google/fx/tools/flow")
		fmt.Println("=================================================================")
		fmt.Println()

		// Open browser to Flow
		openBrowser("https://labs.google/fx/tools/flow")

		return nil
	},
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
