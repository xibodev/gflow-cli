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
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/remote"
	"github.com/xibodev/gflow-cli/pkg/util"
)

var (
	imgAspect string
	imgCount  int
	imgModel  string
	imgOutput string
	imgRef    string
	imgSeed   int64
)

var imageCmd = &cobra.Command{
	Use:     "image <prompt>",
	Aliases: []string{"img"},
	Short:   "Generate AI images (Gemini Imagen 3 or Flow)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
		prov := getProvider()
		if prov == "gemini" {
			return runGeminiImage(cmd, prompt)
		}
		return runFlowImage(cmd, prompt)
	},
}

func runGeminiImage(cmd *cobra.Command, prompt string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cli, err := gemini.NewClient(ctx, false)
	if err != nil {
		return fmt.Errorf("gemini client error: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Generating image via Gemini (Imagen 3): %q...\n", prompt)
	res, err := cli.Generate(ctx, "Generate an image of: "+prompt)
	if err != nil {
		return err
	}

	if len(res.ImageURLs) == 0 {
		if res.Text != "" {
			return fmt.Errorf("gemini response: %s", res.Text)
		}
		return errors.New("no image URL returned by Gemini")
	}

	cfg := config.LoadConfig()
	outBase := imgOutput
	if outBase == "" {
		outBase = cfg.OutputDir
	}

	var saved []models.Asset
	for i, u := range res.ImageURLs {
		savedPath, err := cli.DownloadMedia(ctx, u, outBase)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed downloading image %d: %v\n", i+1, err)
			continue
		}
		abs, _ := filepath.Abs(savedPath)
		fmt.Fprintf(os.Stderr, "Saved: %s\n", abs)
		asset := models.Asset{
			ID:        fmt.Sprintf("gemini_img_%d", i+1),
			Type:      "image",
			Prompt:    prompt,
			LocalPath: savedPath,
			URL:       u,
			MimeType:  "image/jpeg",
		}
		saved = append(saved, asset)
		_ = history.Add(history.Entry{
			ID:        asset.ID,
			Type:      "image",
			Prompt:    prompt,
			LocalPath: savedPath,
			URL:       u,
			Model:     "Imagen 3",
		})
	}

	if len(saved) == 0 {
		return errors.New("failed to save any generated images")
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(saved, "", "  ")
		fmt.Println(string(data))
	}

	return nil
}

func runFlowImage(cmd *cobra.Command, prompt string) error {
	cfg := config.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	aspect, err := config.ResolveImageAspect(imgAspect, "")
	if err != nil {
		return err
	}

	var seedPtr *int64
	if cmd.Flags().Changed("seed") {
		if imgSeed < 0 || imgSeed > models.MaxSeed {
			return fmt.Errorf("seed must be in [0,%d]", models.MaxSeed)
		}
		seedPtr = &imgSeed
	}

	// 1. Try extension-free direct CDP connection first
	flowBridge := cdp.NewFlowBridge(cfg.CDPPort)
	if _, err := flowBridge.EnsureConnected(ctx); err == nil {
		fmt.Fprintf(os.Stderr, "Connected to Google Flow tab via CDP (extension-free, port %d)...\n", cfg.CDPPort)
		defer flowBridge.Close()
		fc := client.NewFlowClientWithExecutor(cfg, flowBridge)

		fmt.Fprintf(os.Stderr, "Generating %d image(s) via Google Flow [%s, %s]...\n", imgCount, aspect, imgModel)
		assets, err := fc.GenerateImages(ctx, prompt, aspect, imgCount, imgModel, nil, seedPtr)
		if err != nil {
			return fmt.Errorf("flow generation failed: %w", err)
		}

		outBase := imgOutput
		if outBase == "" {
			outBase = cfg.OutputDir
		}
		return saveFlowAssets(ctx, assets, prompt, aspect, imgModel, outBase)
	}

	// 2. Fall back to daemon/bridge route if running
	if err := daemon.EnsureRunningWithAuth(cfg.Host, cfg.Port, cfg.APIToken); err != nil {
		return fmt.Errorf("could not connect to Flow. Either start Chrome with '--remote-debugging-port=9222' on flow.google.com (extension-free), or start the background server: %w", err)
	}
	rc := remote.New(cfg.Host, cfg.Port, cfg.APIToken)
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

	var refMediaIDs []string
	if imgRef != "" {
		path, mid, err := resolveReference(imgRef)
		if err != nil {
			return err
		}
		if path != "" {
			fmt.Fprintf(os.Stderr, "Uploading reference image %s...\n", imgRef)
			mid, err = rc.UploadFile(ctx, path)
			if err != nil {
				return fmt.Errorf("failed to upload reference image: %w", err)
			}
		}
		refMediaIDs = append(refMediaIDs, mid)
	}

	fmt.Fprintf(os.Stderr, "Generating %d image(s) via Flow daemon [%s, %s]...\n", imgCount, aspect, imgModel)
	assets, err := rc.GenerateImages(ctx, models.ImageRequest{
		Prompt: prompt, N: imgCount, Aspect: aspect, Model: imgModel,
		ReferenceMediaIDs: refMediaIDs, Seed: seedPtr,
	})
	if err != nil {
		return err
	}

	outBase := imgOutput
	if outBase == "" {
		outBase = cfg.OutputDir
	}
	return saveFlowAssets(ctx, assets, prompt, aspect, imgModel, outBase)
}

func saveFlowAssets(ctx context.Context, assets []models.Asset, prompt, aspect, model, outBase string) error {
	var savedAssets []models.Asset
	var saveErrs []string
	ts := time.Now().Format("150405")
	for i := range assets {
		a := &assets[i]
		a.ID = fmt.Sprintf("img_%s_%d", ts, i+1)
		a.Type = "image"
		a.Prompt = prompt
		if a.MimeType == "" {
			a.MimeType = "image/png"
		}
		savedPath, err := util.SaveAssetIndexed(ctx, a, outBase, i, len(assets))
		if err != nil {
			saveErrs = append(saveErrs, fmt.Sprintf("asset %d: %v", i+1, err))
			continue
		}
		savedAssets = append(savedAssets, *a)
		absPath, _ := filepath.Abs(savedPath)
		fmt.Fprintf(os.Stderr, "Saved: %s\n", absPath)
		if herr := history.Add(history.Entry{
			ID: a.ID, Type: "image", Prompt: prompt, LocalPath: savedPath,
			URL: a.URL, Aspect: aspect, Model: model,
		}); herr != nil {
			fmt.Fprintf(os.Stderr, "Warning: saved %s but history failed: %v\n", savedPath, herr)
		}
	}
	if len(savedAssets) == 0 {
		return fmt.Errorf("no images saved (errors: %v)", saveErrs)
	}
	if jsonOutput {
		data, _ := json.MarshalIndent(savedAssets, "", "  ")
		fmt.Println(string(data))
	}
	if len(saveErrs) > 0 {
		return fmt.Errorf("partial save failures: %v", saveErrs)
	}
	return nil
}

func init() {
	imageCmd.Flags().StringVarP(&imgAspect, "aspect", "a", "landscape", "Aspect ratio: landscape, square, portrait, 4:3, 3:4")
	imageCmd.Flags().IntVarP(&imgCount, "count", "c", 1, "Number of images (1-4)")
	imageCmd.Flags().StringVarP(&imgModel, "model", "m", "narwhal", "Image model: narwhal (default), harbor_seal (lite), gem_pix_2 (pro)")
	imageCmd.Flags().StringVarP(&imgOutput, "output", "o", "", "Output directory or filename")
	imageCmd.Flags().StringVar(&imgRef, "ref", "", "Reference image path or media ID")
	imageCmd.Flags().Int64Var(&imgSeed, "seed", 0, "Seed for reproducible generation")
}
