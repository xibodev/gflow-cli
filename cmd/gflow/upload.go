package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/spf13/cobra"
)

var uploadCmd = &cobra.Command{
	Use:   "upload <file>",
	Short: "Upload a reference image or asset to Google Flow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath := args[0]
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("file not found: %s", filePath)
		}

		cfg := config.LoadConfig()
		if err := daemon.EnsureRunning(cfg.Host, cfg.Port); err != nil {
			return fmt.Errorf("background server error: %w", err)
		}

		mid, err := uploadLocalFile(cfg, filePath)
		if err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}

		if jsonOutput {
			data, _ := json.Marshal(map[string]string{
				"media_id": mid,
				"file":     filePath,
			})
			fmt.Println(string(data))
		} else {
			fmt.Println("✔ File uploaded successfully!")
			fmt.Printf("Media ID: %s\n", mid)
		}

		return nil
	},
}
