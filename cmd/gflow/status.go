package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/gemini"
	"github.com/xibodev/gflow-cli/pkg/minimax"
	"github.com/xibodev/gflow-cli/pkg/remote"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check status of configured AI providers (Gemini, MiniMax, Flow)",
	RunE: func(cmd *cobra.Command, args []string) error {
		statusData := make(map[string]any)

		// 1. Gemini Provider Status
		geminiExe, geminiExeErr := gemini.FindGeminiExecutable()
		geminiSess, geminiSessErr := gemini.LoadSession()
		geminiReady := geminiSessErr == nil && geminiSess != nil && geminiSess.At != ""
		statusData["gemini"] = map[string]any{
			"app_installed": geminiExeErr == nil,
			"session_ready": geminiReady,
			"session_age":   time.Since(geminiSess.UpdatedAt).Round(time.Minute).String(),
		}

		// 2. MiniMax Provider Status
		mmSess, mmErr := minimax.LoadSession()
		minimaxReady := mmErr == nil && mmSess != nil && mmSess.AccessToken != ""
		statusData["minimax"] = map[string]any{
			"session_ready": minimaxReady,
			"has_device_id": mmSess != nil && mmSess.DeviceID != "",
		}

		// 3. Flow Provider Status
		cfg := config.LoadConfig()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rc := remote.New(cfg.Host, cfg.Port, cfg.APIToken)
		flowRunning := rc.Probe(ctx) == nil
		statusData["flow"] = map[string]any{
			"daemon_running": flowRunning,
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(statusData, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Println("=== AI Providers Status ===")
		fmt.Println()

		// Gemini
		fmt.Println("[Gemini] (Default: Imagen 3, Veo, Audio, Chat)")
		fmt.Printf("  App Installed:    %s\n", formatBool(geminiExeErr == nil, "Found ("+geminiExe+")", "Not Found"))
		fmt.Printf("  Session State:    %s\n", formatBool(geminiReady, fmt.Sprintf("Ready (Updated %s ago)", time.Since(geminiSess.UpdatedAt).Round(time.Minute)), "Missing (Run 'gflow chat' or open Gemini app once)"))
		fmt.Println()

		// MiniMax
		fmt.Println("[MiniMax Design] (Direct Cloud: H3 Video)")
		fmt.Printf("  Session State:    %s\n", formatBool(minimaxReady, "Ready (Captured from Desktop app)", "Missing"))
		fmt.Println()

		// Flow
		fmt.Println("[Google Flow] (Direct CDP / Daemon)")
		fmt.Printf("  Daemon Running:   %s\n", formatBool(flowRunning, fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port), "Stopped"))
		fmt.Println()

		return nil
	},
}

func formatBool(val bool, trueStr, falseStr string) string {
	if val {
		return "[OK] " + trueStr
	}
	return "[--] " + falseStr
}
