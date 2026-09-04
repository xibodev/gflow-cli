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

		// 1. Ensure server is running
		if err := daemon.EnsureRunning(cfg.Host, cfg.Port); err != nil {
			return fmt.Errorf("background server error: %w", err)
		}

		// 2. Check health
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

		// 3. Handle start / end frame uploads if provided
		startID := vidStart
		if startID != "" && fileExists(startID) {
			if !jsonOutput {
				fmt.Printf("Uploading start frame %s...\n", startID)
			}
			mid, err := uploadLocalFile(cfg, startID)
			if err != nil {
				return fmt.Errorf("failed to upload start frame: %w", err)
			}
			startID = mid
		}

		endID := vidEnd
		if endID != "" && fileExists(endID) {
			if !jsonOutput {
				fmt.Printf("Uploading end frame %s...\n", endID)
			}
			mid, err := uploadLocalFile(cfg, endID)
			if err != nil {
				return fmt.Errorf("failed to upload end frame: %w", err)
			}
			endID = mid
		}

		// 4. Submit video generation
		if !jsonOutput {
			fmt.Printf("Submitting video generation: %q [%s, %ds]...\n", prompt, vidAspect, vidDuration)
		}

		reqBody := map[string]any{
			"prompt":      prompt,
			"aspect":      vidAspect,
			"duration":    vidDuration,
			"resolution":  vidResolution,
			"start_image": startID,
			"end_image":   endID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		url := fmt.Sprintf("http://%s:%d/v1/videos/generations", cfg.Host, cfg.Port)
		submitResp, err := http.Post(url, "application/json", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("failed to submit video job: %w", err)
		}
		defer submitResp.Body.Close()

		if submitResp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(submitResp.Body)
			return fmt.Errorf("server error (%d): %s", submitResp.StatusCode, string(b))
		}

		var submitResult struct {
			JobID  string `json:"job_id"`
			Status string `json:"status"`
		}
		_ = json.NewDecoder(submitResp.Body).Decode(&submitResult)

		if !jsonOutput {
			fmt.Printf("✔ Job submitted! Media ID: %s\n", submitResult.JobID)
			fmt.Print("Rendering video...")
		}

		// 5. Poll for completion
		pollURL := fmt.Sprintf("http://%s:%d/v1/videos/generations/%s", cfg.Host, cfg.Port, submitResult.JobID)
		pollClient := &http.Client{Timeout: 35 * time.Second}
		startTime := time.Now()

		for {
			time.Sleep(6 * time.Second)
			pollResp, err := pollClient.Get(pollURL)
			if err != nil {
				continue
			}

			var pollResult struct {
				JobID  string         `json:"job_id"`
				Status string         `json:"status"`
				Assets []models.Asset `json:"assets"`
			}
			_ = json.NewDecoder(pollResp.Body).Decode(&pollResult)
			pollResp.Body.Close()

			if pollResult.Status == "succeeded" && len(pollResult.Assets) > 0 {
				if !jsonOutput {
					fmt.Printf(" Done! (%ds)\n", int(time.Since(startTime).Seconds()))
				}

				assets := pollResult.Assets

				// Handle upsample to 1080p or 4K if requested
				if vidResolution == "1080p" || vidResolution == "4k" {
					if !jsonOutput {
						fmt.Printf("Upsampling to %s...\n", vidResolution)
					}
					upAssets, err := triggerRemoteUpsample(cfg, assets[0].ID, vidAspect, vidResolution)
					if err == nil && len(upAssets) > 0 {
						assets = upAssets
					}
				}

				outDir := vidOutput
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
							fmt.Printf("✔ Saved: %s\n", absPath)
						}
						_ = history.Add(history.Entry{
							ID:        a.ID,
							Type:      "video",
							Prompt:    prompt,
							LocalPath: savedPath,
							URL:       a.URL,
							Aspect:    vidAspect,
						})
					}
				}

				if jsonOutput {
					data, _ := json.MarshalIndent(assets, "", "  ")
					fmt.Println(string(data))
				}
				return nil
			}

			if !jsonOutput {
				fmt.Print(".")
			}
		}
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

func triggerRemoteUpsample(cfg *config.Config, mediaID, aspect, resolution string) ([]models.Asset, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"media_id":   mediaID,
		"aspect":     aspect,
		"resolution": resolution,
	})
	url := fmt.Sprintf("http://%s:%d/v1/videos/upsample", cfg.Host, cfg.Port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upsample request failed (%d): %s", resp.StatusCode, string(b))
	}

	var res struct {
		JobID string `json:"job_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)

	pollURL := fmt.Sprintf("http://%s:%d/v1/videos/generations/%s", cfg.Host, cfg.Port, res.JobID)
	client := &http.Client{Timeout: 35 * time.Second}

	for {
		time.Sleep(5 * time.Second)
		pollResp, err := client.Get(pollURL)
		if err != nil {
			continue
		}

		var pollResult struct {
			JobID  string         `json:"job_id"`
			Status string         `json:"status"`
			Assets []models.Asset `json:"assets"`
		}
		_ = json.NewDecoder(pollResp.Body).Decode(&pollResult)
		pollResp.Body.Close()

		if pollResult.Status == "succeeded" && len(pollResult.Assets) > 0 {
			return pollResult.Assets, nil
		}
		fmt.Print(".")
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
