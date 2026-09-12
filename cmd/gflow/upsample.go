package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/remote"
	"github.com/xibodev/gflow-cli/pkg/util"
)

var (
	upResolution string
	upAspect     string
	upOutput     string
)

var upsampleCmd = &cobra.Command{
	Use:   "upsample <media_id>",
	Short: "Upsample a generated video to 1080p or 4K",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mediaID := args[0]
		cfg := config.LoadConfig()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()

		if err := daemon.EnsureRunningWithAuth(cfg.Host, cfg.Port, cfg.APIToken); err != nil {
			return fmt.Errorf("background server error: %w", err)
		}
		rc := remote.New(cfg.Host, cfg.Port, cfg.APIToken)
		rc.PollTimeout = 25 * time.Minute
		fmt.Fprintf(os.Stderr, "Upsampling %s to %s [%s]...\n", mediaID, upResolution, upAspect)

		assets, err := triggerRemoteUpsample(ctx, rc, mediaID, upAspect, upResolution, nil)
		if err != nil {
			return fmt.Errorf("upsample error: %w", err)
		}

		outBase := upOutput
		if outBase == "" {
			outBase = cfg.OutputDir
		}
		var saved []map[string]any
		var saveErrs []string
		for i := range assets {
			a := &assets[i]
			savedPath, err := util.SaveAssetIndexed(ctx, a, outBase, i, len(assets))
			if err != nil {
				saveErrs = append(saveErrs, err.Error())
				continue
			}
			absPath, _ := filepath.Abs(savedPath)
			fmt.Fprintf(os.Stderr, "Saved upsampled video: %s\n", absPath)
			if herr := history.Add(history.Entry{
				ID: a.ID, Type: "video", Prompt: fmt.Sprintf("Upsample %s (%s)", mediaID, upResolution),
				LocalPath: savedPath, URL: a.URL, Aspect: upAspect,
			}); herr != nil {
				fmt.Fprintf(os.Stderr, "Warning: history failed: %v\n", herr)
			}
			saved = append(saved, map[string]any{"id": a.ID, "local_path": savedPath, "url": a.URL})
		}
		if len(saved) == 0 {
			return fmt.Errorf("no upsampled videos saved (errors: %v)", saveErrs)
		}
		if jsonOutput {
			data, _ := json.MarshalIndent(assets, "", "  ")
			fmt.Println(string(data))
		}
		if len(saveErrs) > 0 {
			return fmt.Errorf("partial save failures: %v", saveErrs)
		}
		return nil
	},
}

func init() {
	upsampleCmd.Flags().StringVarP(&upResolution, "resolution", "r", "1080p", "Resolution: 1080p or 4k")
	upsampleCmd.Flags().StringVarP(&upAspect, "aspect", "a", "landscape", "Aspect ratio")
	upsampleCmd.Flags().StringVarP(&upOutput, "output", "o", "", "Output directory or filename")
}
