package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/util"
	"github.com/spf13/cobra"
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

		// 1. Ensure server is running
		if err := daemon.EnsureRunning(cfg.Host, cfg.Port); err != nil {
			return fmt.Errorf("background server error: %w", err)
		}

		// 2. Check server health
		healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(healthURL)
		if err != nil {
			return fmt.Errorf("could not connect to gflow agent on %s: %w", healthURL, err)
		}
		defer resp.Body.Close()

		var health map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&health)

		if connected, _ := health["extension_connected"].(bool); !connected {
			return fmt.Errorf("Chrome extension not connected. Run 'gflow setup' and open https://labs.google/fx/tools/flow")
		}

		if hasToken, _ := health["has_flow_key"].(bool); !hasToken {
			return fmt.Errorf("Google Flow session not ready. Open https://labs.google/fx/tools/flow in Chrome")
		}

		// 3. Handle reference image upload if specified
		var refMediaIDs []string
		if imgRef != "" {
			if _, err := os.Stat(imgRef); err == nil {
				if !jsonOutput {
					fmt.Printf("Uploading reference image %s...\n", imgRef)
				}
				mid, err := uploadLocalFile(cfg, imgRef)
				if err != nil {
					return fmt.Errorf("failed to upload reference image: %w", err)
				}
				refMediaIDs = append(refMediaIDs, mid)
			} else {
				refMediaIDs = append(refMediaIDs, imgRef)
			}
		}

		// 4. Send generation request
		if !jsonOutput {
			fmt.Printf("Generating %d image(s): %q [%s, %s]...\n", imgCount, prompt, imgAspect, imgModel)
		}

		reqBody := map[string]any{
			"prompt": prompt,
			"n":      imgCount,
			"size":   imgAspect,
			"model":  imgModel,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		genURL := fmt.Sprintf("http://%s:%d/v1/images/generations", cfg.Host, cfg.Port)
		genResp, err := http.Post(genURL, "application/json", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("generation request failed: %w", err)
		}
		defer genResp.Body.Close()

		if genResp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(genResp.Body)
			return fmt.Errorf("generation failed (%d): %s", genResp.StatusCode, string(b))
		}

		var openAIResp struct {
			Data []struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := json.NewDecoder(genResp.Body).Decode(&openAIResp); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		outDir := imgOutput
		if outDir == "" {
			outDir = cfg.OutputDir
		}
		_ = os.MkdirAll(outDir, 0755)

		var savedAssets []models.Asset
		ts := time.Now().Format("150405")

		for i, d := range openAIResp.Data {
			a := models.Asset{
				ID:       fmt.Sprintf("img_%s_%d", ts, i+1),
				Type:     "image",
				URL:      d.URL,
				Prompt:   prompt,
				MimeType: "image/png",
			}
			savedPath, err := util.SaveAsset(context.Background(), &a, outDir)
			if err == nil {
				a.LocalPath = savedPath
				savedAssets = append(savedAssets, a)
				if !jsonOutput {
					absPath, _ := filepath.Abs(savedPath)
					fmt.Printf("✔ Saved: %s\n", absPath)
				}
				_ = history.Add(history.Entry{
					ID:        a.ID,
					Type:      "image",
					Prompt:    prompt,
					LocalPath: savedPath,
					URL:       d.URL,
					Aspect:    imgAspect,
					Model:     imgModel,
				})
			}
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(savedAssets, "", "  ")
			fmt.Println(string(data))
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

func uploadLocalFile(cfg *config.Config, filePath string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"file_path": filePath})
	url := fmt.Sprintf("http://%s:%d/v1/upload", cfg.Host, cfg.Port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload error (%d): %s", resp.StatusCode, string(b))
	}

	var res struct {
		MediaID string `json:"media_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.MediaID, nil
}
