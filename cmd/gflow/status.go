package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check bridge connection and Google Flow auth status",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.LoadConfig()
		healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)

		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(healthURL)
		if err != nil {
			if jsonOutput {
				data, _ := json.Marshal(map[string]any{
					"status": "server_not_running",
					"error":  err.Error(),
				})
				fmt.Println(string(data))
			} else {
				fmt.Println("Status: Server is not running.")
				fmt.Printf("Run 'gflow setup' or any generation command to start it automatically.\n")
			}
			return nil
		}
		defer resp.Body.Close()

		var health map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&health)

		if jsonOutput {
			data, _ := json.MarshalIndent(health, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		connected, _ := health["extension_connected"].(bool)
		hasToken, _ := health["has_flow_key"].(bool)
		sessions, _ := health["active_sessions"].(float64)

		fmt.Printf("Server:             Running on http://%s:%d\n", cfg.Host, cfg.Port)
		fmt.Printf("Extension Status:   %s\n", formatBool(connected, "Connected", "Not Connected"))
		fmt.Printf("Google Flow Token:  %s\n", formatBool(hasToken, "Captured / Ready", "Missing (Open Flow)"))
		fmt.Printf("Active Workers:     %d\n", int(sessions))
		fmt.Printf("Overall Health:     %v\n", health["status"])

		if !connected || !hasToken {
			fmt.Println()
			fmt.Println("Need to connect?")
			fmt.Println("1. Run 'gflow setup' to install the Chrome extension.")
			fmt.Println("2. Open https://labs.google/fx/tools/flow in Chrome.")
		}

		return nil
	},
}

func formatBool(val bool, trueStr, falseStr string) string {
	if val {
		return "✔ " + trueStr
	}
	return "✖ " + falseStr
}
