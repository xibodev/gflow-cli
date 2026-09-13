package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/cdp"
	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/gemini"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/minimax"
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/remote"
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
	Short:   "Generate AI videos (Gemini Veo, MiniMax H3, or Flow)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
		prov := getProvider()
		switch prov {
		case "minimax":
			return runMiniMaxVideo(cmd, prompt)
		case "flow":
			return runFlowVideo(cmd, prompt)
		default:
			return runGeminiVideo(cmd, prompt)
		}
	},
}

func runGeminiVideo(cmd *cobra.Command, prompt string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	cfg := config.LoadConfig()
	outBase := vidOutput
	if outBase == "" {
		outBase = cfg.OutputDir
	}

	fmt.Fprintf(os.Stderr, "Generating video via Gemini (Veo): %q...\n", prompt)
	savedPath, err := gemini.GenerateVideoCDP(ctx, prompt, outBase)
	if err == nil && savedPath != "" {
		absPath, _ := filepath.Abs(savedPath)
		fmt.Fprintf(os.Stderr, "Saved: %s\n", absPath)
		return nil
	}

	cli, err := gemini.NewClient(ctx, false)
	if err != nil {
		return fmt.Errorf("gemini client error: %w", err)
	}

	res, err := cli.Generate(ctx, "Generate a video of: "+prompt)
	if err != nil {
		return err
	}

	if res.VideoURL == "" {
		if res.Text != "" {
			return fmt.Errorf("gemini response: %s", res.Text)
		}
		return errors.New("no video URL returned by Gemini")
	}

	fmt.Fprintf(os.Stderr, "Downloading generated video...\n")
	savedPath, err = cli.DownloadMedia(ctx, res.VideoURL, outBase)
	if err != nil {
		return fmt.Errorf("failed downloading video: %w", err)
	}

	absPath, _ := filepath.Abs(savedPath)
	fmt.Fprintf(os.Stderr, "Saved: %s\n", absPath)

	asset := models.Asset{
		ID:        fmt.Sprintf("gemini_vid_%s", time.Now().Format("150405")),
		Type:      "video",
		Prompt:    prompt,
		LocalPath: savedPath,
		URL:       res.VideoURL,
		MimeType:  "video/mp4",
	}

	_ = history.Add(history.Entry{
		ID:        asset.ID,
		Type:      "video",
		Prompt:    prompt,
		LocalPath: savedPath,
		URL:       res.VideoURL,
		Aspect:    vidAspect,
		Model:     "Veo",
	})

	if jsonOutput {
		data, _ := json.MarshalIndent([]models.Asset{asset}, "", "  ")
		fmt.Println(string(data))
	}

	return nil
}

func runMiniMaxVideo(cmd *cobra.Command, prompt string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	client, err := minimax.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("minimax client error: %w", err)
	}

	res := vidResolution
	if res == "" || res == "720p" {
		res = "768P"
	} else if res == "1080p" {
		res = "2K"
	}

	aspect := vidAspect
	if aspect == "" || aspect == "landscape" {
		aspect = "16:9"
	} else if aspect == "portrait" {
		aspect = "9:16"
	} else if aspect == "square" {
		aspect = "1:1"
	}

	dur := vidDuration
	if dur <= 0 {
		dur = 4
	}

	fmt.Fprintf(os.Stderr, "Submitting video to MiniMax H3 [%s, %ds, %s]...\n", aspect, dur, res)
	taskID, err := client.GenerateVideo(ctx, prompt, res, dur, aspect)
	if err != nil {
		return fmt.Errorf("minimax submit failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Job submitted: %s\nRendering video...\n", taskID)
	dlURL, err := client.WaitForVideo(ctx, taskID, 12*time.Minute)
	if err != nil {
		return err
	}

	cfg := config.LoadConfig()
	outBase := vidOutput
	if outBase == "" {
		outBase = cfg.OutputDir
	}

	savedPath, err := client.DownloadFile(ctx, dlURL, outBase)
	if err != nil {
		return fmt.Errorf("failed downloading video from %s: %w", dlURL, err)
	}

	absPath, _ := filepath.Abs(savedPath)
	fmt.Fprintf(os.Stderr, "Saved: %s\n", absPath)

	asset := models.Asset{
		ID:        taskID,
		Type:      "video",
		Prompt:    prompt,
		LocalPath: savedPath,
		URL:       dlURL,
		MimeType:  "video/mp4",
	}

	_ = history.Add(history.Entry{
		ID:        taskID,
		Type:      "video",
		Prompt:    prompt,
		LocalPath: savedPath,
		URL:       dlURL,
		Aspect:    aspect,
		Model:     "MiniMax H3",
	})

	if jsonOutput {
		data, _ := json.MarshalIndent([]models.Asset{asset}, "", "  ")
		fmt.Println(string(data))
	}

	return nil
}

func runFlowVideo(cmd *cobra.Command, prompt string) error {
	cfg := config.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	aspect, err := config.ResolveVideoAspect(vidAspect)
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

	dur := vidDuration
	if dur <= 0 {
		dur = 10
	}

	outBase := vidOutput
	if outBase == "" {
		outBase = cfg.OutputDir
	}

	// 1. Try extension-free direct CDP connection first
	flowBridge := cdp.NewFlowBridge(cfg.CDPPort)
	if _, err := flowBridge.EnsureConnected(ctx); err == nil {
		fmt.Fprintf(os.Stderr, "Connected to Google Flow tab via CDP (extension-free, port %d)...\n", cfg.CDPPort)
		defer flowBridge.Close()
		fc := client.NewFlowClientWithExecutor(cfg, flowBridge)

		fmt.Fprintf(os.Stderr, "Submitting video to Google Flow [%s, %ds]...\n", aspect, dur)
		mediaIDs, err := fc.GenerateVideo(ctx, prompt, aspect, dur, "", vidStart, vidEnd, seedPtr)
		if err != nil {
			return fmt.Errorf("flow submit failed: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Job submitted: %s\nRendering video...\n", mediaIDs[0])
		assets, err := fc.WaitForVideo(ctx, mediaIDs, 15*time.Minute)
		if err != nil {
			return fmt.Errorf("flow rendering failed: %w", err)
		}

		if vidResolution == "1080p" || vidResolution == "4k" {
			fmt.Fprintf(os.Stderr, "Upsampling video to %s...\n", vidResolution)
			upIDs, err := fc.UpsampleVideo(ctx, assets[0].ID, aspect, vidResolution, seedPtr)
			if err == nil && len(upIDs) > 0 {
				upAssets, err := fc.WaitForVideo(ctx, upIDs, 10*time.Minute)
				if err == nil && len(upAssets) > 0 {
					assets = upAssets
				}
			}
		}

		return saveFlowAssets(ctx, assets, prompt, aspect, "Veo 3.1", outBase)
	}

	// 2. Fall back to daemon/bridge route if running
	if err := daemon.EnsureRunningWithAuth(cfg.Host, cfg.Port, cfg.APIToken); err != nil {
		return fmt.Errorf("could not connect to Flow. Either start Chrome with '--remote-debugging-port=9222' on flow.google.com (extension-free), or start the background server: %w", err)
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
		return fmt.Errorf("Chrome extension not connected. Run 'gflow setup' and open https://labs.google/fx/tools/flow, or launch Chrome with '--remote-debugging-port=9222'")
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

	fmt.Fprintf(os.Stderr, "Submitting video generation to Flow daemon [%s, %ds]...\n", aspect, dur)
	sub, err := rc.SubmitVideo(ctx, models.VideoSubmitRequest{
		Prompt: prompt, Aspect: aspect, Duration: dur,
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

	return saveFlowAssets(ctx, assets, prompt, aspect, "Veo 3.1", outBase)
}

func init() {
	videoCmd.Flags().StringVarP(&vidAspect, "aspect", "a", "landscape", "Aspect ratio: landscape, portrait, square")
	videoCmd.Flags().IntVarP(&vidDuration, "duration", "d", 10, "Duration in seconds: 4, 6, 8, 10")
	videoCmd.Flags().StringVarP(&vidResolution, "resolution", "r", "720p", "Resolution: 720p, 1080p, 4k (Flow); 768P, 2K (MiniMax)")
	videoCmd.Flags().StringVarP(&vidOutput, "output", "o", "", "Output directory or filename")
	videoCmd.Flags().StringVar(&vidStart, "start", "", "Start frame image path or media ID (Flow)")
	videoCmd.Flags().StringVar(&vidEnd, "end", "", "End frame image path or media ID (Flow)")
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
