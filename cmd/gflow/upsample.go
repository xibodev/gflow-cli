package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/util"
	"github.com/spf13/cobra"
)

var (
	upResolution string
	upAspect     string
	upOutput     string
)

var upsampleCmd = &cobra.Command{
	Use:     "upsample <media_id>",
	Short:   "Upsample a generated video to 1080p or 4K",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mediaID := args[0]
		cfg := config.LoadConfig()

		if err := daemon.EnsureRunning(cfg.Host, cfg.Port); err != nil {
			return fmt.Errorf("background server error: %w", err)
		}

		if !jsonOutput {
			fmt.Printf("Upsampling %s to %s [%s]...\n", mediaID, upResolution, upAspect)
		}

		assets, err := triggerRemoteUpsample(cfg, mediaID, upAspect, upResolution)
		if err != nil {
			return fmt.Errorf("upsample error: %w", err)
		}

		outDir := upOutput
		if outDir == "" {
			outDir = cfg.OutputDir
		}
		_ = os.MkdirAll(outDir, 0755)

		for i := range assets {
			a := &assets[i]
			savedPath, err := util.SaveAsset(context.Background(), a, outDir)
			if err == nil {
				a.LocalPath = savedPath
				if !jsonOutput {
					absPath, _ := filepath.Abs(savedPath)
					fmt.Printf("✔ Saved upsampled video: %s\n", absPath)
				}
				_ = history.Add(history.Entry{
					ID:        a.ID,
					Type:      "video",
					Prompt:    fmt.Sprintf("Upsample %s (%s)", mediaID, upResolution),
					LocalPath: savedPath,
					URL:       a.URL,
					Aspect:    upAspect,
				})
			}
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(assets, "", "  ")
			fmt.Println(string(data))
		}

		return nil
	},
}

func init() {
	upsampleCmd.Flags().StringVarP(&upResolution, "resolution", "r", "1080p", "Resolution: 1080p or 4k")
	upsampleCmd.Flags().StringVarP(&upAspect, "aspect", "a", "landscape", "Aspect ratio")
	upsampleCmd.Flags().StringVarP(&upOutput, "output", "o", "", "Output directory or filename")
}
