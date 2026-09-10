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
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/remote"
	"github.com/xibodev/gflow-cli/pkg/util"
)

var (
	vidAspect     string
	vidDuration   int
	vidResolution string
	vidOutput     string
	vidStart      string
	vidEnd        string
	vidSeed       int64
)

var videoCmd = &cobra.Command{
	Use:     "video <prompt>",
	Aliases: []string{"vid"},
	Short:   "Generate AI videos (Veo 3.1)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
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
		if err := rc.Probe(ctx); err != nil {
			return err
		}
		status, err := rc.DetailedStatus(ctx)
		if err != nil {
			return err
		}
		if connected, _ := status["extension_connected"].(bool); !connected {
			return fmt.Errorf("Chrome extension not connected. Run 'gflow setup' and open https://labs.google/fx/tools/flow")
		}
		if hasToken, _ := status["has_flow_key"].(bool); !hasToken {
			return fmt.Errorf("Google Flow session not ready. Open https://labs.google/fx/tools/flow in Chrome")
		}

		startID, err := resolveFrame(ctx, rc, vidStart, "start")
		if err != nil {
			return err
		}
		endID, err := resolveFrame(ctx, rc, vidEnd, "end")
		if err != nil {
			return err
		}

		var seedPtr *int64
		if cmd.Flags().Changed("seed") {
			if vidSeed < 0 || vidSeed > models.MaxSeed {
				return fmt.Errorf("seed must be in [0,%d]", models.MaxSeed)
			}
			seedPtr = &vidSeed
		}
		aspect, err := config.ResolveVideoAspect(vidAspect)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Submitting video generation [%s, %ds]...\n", aspect, vidDuration)

		// Submit native generation; upsampling is orchestrated below.
		sub, err := rc.SubmitVideo(ctx, models.VideoSubmitRequest{
			Prompt: prompt, Aspect: aspect, Duration: vidDuration,
			StartImage: startID, EndImage: endID, Seed: seedPtr,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Job submitted: %s\nRendering video...\n", sub.JobID)
		st, err := waitWithProgress(ctx, rc, sub.JobID)
		if err != nil {
			return err
		}
		if st.Status == "failed" {
			msg := ""
			if st.Error != nil {
				msg = st.Error.Message
			}
			return fmt.Errorf("video generation failed: %s", msg)
		}
		assets := st.Assets

		if vidResolution == "1080p" || vidResolution == "4k" {
			fmt.Fprintf(os.Stderr, "Upsampling to %s...\n", vidResolution)
			upAssets, err := triggerRemoteUpsample(ctx, rc, assets[0].ID, aspect, vidResolution, seedPtr)
			if err != nil {
				return fmt.Errorf("native video %s ready but upsample failed: %w", assets[0].ID, err)
			}
			assets = upAssets
		}

		outBase := vidOutput
		if outBase == "" {
			outBase = cfg.OutputDir
		}
		var saved []models.Asset
		var saveErrs []string
		for i := range assets {
			a := &assets[i]
			savedPath, err := util.SaveAssetIndexed(ctx, a, outBase, i, len(assets))
			if err != nil {
				saveErrs = append(saveErrs, fmt.Sprintf("asset %d: %v", i+1, err))
				continue
			}
			saved = append(saved, *a)
			absPath, _ := filepath.Abs(savedPath)
			fmt.Fprintf(os.Stderr, "Saved: %s\n", absPath)
			if herr := history.Add(history.Entry{
				ID: a.ID, Type: "video", Prompt: prompt, LocalPath: savedPath, URL: a.URL, Aspect: aspect,
			}); herr != nil {
				fmt.Fprintf(os.Stderr, "Warning: saved %s but history failed: %v\n", savedPath, herr)
			}
		}
		if len(saved) == 0 {
			return fmt.Errorf("no videos saved (errors: %v)", saveErrs)
		}
		if jsonOutput {
			data, _ := json.MarshalIndent(saved, "", "  ")
			fmt.Println(string(data))
		}
		if len(saveErrs) > 0 {
			return fmt.Errorf("partial save failures: %v", saveErrs)
		}
		return nil
	},
}

func init() {
	videoCmd.Flags().StringVarP(&vidAspect, "aspect", "a", "landscape", "Aspect ratio: landscape, portrait, square")
	videoCmd.Flags().IntVarP(&vidDuration, "duration", "d", 10, "Duration in seconds: 4, 6, 8, 10")
	videoCmd.Flags().StringVarP(&vidResolution, "resolution", "r", "720p", "Resolution: 720p, 1080p, 4k")
	videoCmd.Flags().StringVarP(&vidOutput, "output", "o", "", "Output directory or filename")
	videoCmd.Flags().StringVar(&vidStart, "start", "", "Start frame image path or media ID")
	videoCmd.Flags().StringVar(&vidEnd, "end", "", "End frame image path or media ID")
	videoCmd.Flags().Int64Var(&vidSeed, "seed", 0, "Seed for reproducible generation")
}

func resolveFrame(ctx context.Context, rc *remote.Client, val, name string) (string, error) {
	if val == "" {
		return "", nil
	}
	path, mid, err := resolveReference(val)
	if err != nil {
		return "", fmt.Errorf("invalid %s frame: %w", name, err)
	}
	if path != "" {
		mid, err = rc.UploadFile(ctx, path)
		if err != nil {
			return "", fmt.Errorf("failed to upload %s frame: %w", name, err)
		}
	}
	return mid, nil
}

func waitWithProgress(ctx context.Context, rc *remote.Client, jobID string) (models.JobStatus, error) {
	deadline := time.Now().Add(rc.PollTimeout)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return models.JobStatus{}, ctx.Err()
		case <-timer.C:
		}
		if time.Now().After(deadline) {
			return models.JobStatus{}, fmt.Errorf("timed out waiting for %s", jobID)
		}
		st, err := rc.CheckOnce(ctx, jobID)
		if err != nil {
			timer.Reset(rc.PollEvery)
			continue
		}
		if st.Status == "processing" {
			if !jsonOutput {
				fmt.Fprint(os.Stderr, ".")
			}
			timer.Reset(rc.PollEvery)
			continue
		}
		if !jsonOutput {
			fmt.Fprint(os.Stderr, "\n")
		}
		return st, nil
	}
}

func triggerRemoteUpsample(ctx context.Context, rc *remote.Client, mediaID, aspect, resolution string, seed *int64) ([]models.Asset, error) {
	sub, err := rc.Upsample(ctx, mediaID, aspect, resolution, seed)
	if err != nil {
		return nil, err
	}
	st, err := waitWithProgress(ctx, rc, sub.JobID)
	if err != nil {
		return nil, fmt.Errorf("source %s retained; upsample wait failed: %w", mediaID, err)
	}
	if st.Status == "failed" {
		msg := ""
		if st.Error != nil {
			msg = st.Error.Message
		}
		return nil, fmt.Errorf("upsample failed for source %s: %s", mediaID, msg)
	}
	return st.Assets, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
