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
	Short:   "Generate AI images (Imagen 4 / Nano Banana 2)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
		cfg := config.LoadConfig()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()

		if err := daemon.EnsureRunningWithAuth(cfg.Host, cfg.Port, cfg.APIToken); err != nil {
			return fmt.Errorf("background server error: %w", err)
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
			return fmt.Errorf("Chrome extension not connected. Run 'gflow setup' and open https://labs.google/fx/tools/flow")
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

		var seedPtr *int64
		if cmd.Flags().Changed("seed") {
			if imgSeed < 0 || imgSeed > models.MaxSeed {
				return fmt.Errorf("seed must be in [0,%d]", models.MaxSeed)
			}
			seedPtr = &imgSeed
		}

		aspect, err := config.ResolveImageAspect(imgAspect, "")
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Generating %d image(s) [%s, %s]...\n", imgCount, aspect, imgModel)

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
				URL: a.URL, Aspect: aspect, Model: imgModel,
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
	},
}

func init() {
	imageCmd.Flags().StringVarP(&imgAspect, "aspect", "a", "landscape", "Aspect ratio: landscape, square, portrait, 4:3, 3:4")
	imageCmd.Flags().IntVarP(&imgCount, "count", "c", 1, "Number of images (1-4)")
	imageCmd.Flags().StringVarP(&imgModel, "model", "m", "narwhal", "Image model: narwhal (default), harbor_seal (lite), gem_pix_2 (pro)")
	imageCmd.Flags().StringVarP(&imgOutput, "output", "o", "", "Output directory or filename")
	imageCmd.Flags().StringVar(&imgRef, "ref", "", "Reference image path or media ID")
	imageCmd.Flags().Int64Var(&imgSeed, "seed", 0, "Seed for reproducible generation")
}
