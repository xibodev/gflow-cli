package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/remote"
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
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()

		if err := daemon.EnsureRunningWithAuth(cfg.Host, cfg.Port, cfg.APIToken); err != nil {
			return fmt.Errorf("background server error: %w", err)
		}
		rc := remote.New(cfg.Host, cfg.Port, cfg.APIToken)
		mid, err := rc.UploadFile(ctx, filePath)
		if err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}
		if jsonOutput {
			data, _ := json.Marshal(map[string]string{"media_id": mid, "file": filePath})
			fmt.Println(string(data))
		} else {
			fmt.Println("File uploaded successfully!")
			fmt.Printf("Media ID: %s\n", mid)
		}
		return nil
	},
}
