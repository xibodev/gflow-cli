package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xibodev/gflow-cli/pkg/api"
	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/cdp"
	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/util"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "image", "img":
		cmdImage(args)
	case "video", "vid":
		cmdVideo(args)
	case "upsample":
		cmdUpsample(args)
	case "upload":
		cmdUpload(args)
	case "status":
		cmdStatus(args)
	case "serve", "server":
		cmdServe(args)
	case "cdp":
		cmdCDP(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`flow — Lean Google Flow CLI (Imagen 4 & Veo 3.1) in Go

Usage:
  flow <command> [options] [arguments]

Commands:
  image <prompt>       Generate AI images (Imagen 4 / Nano Banana 2)
  video <prompt>       Generate AI videos (Veo 3.1)
  upsample <media_id>  Upsample video to 1080p or 4K
  upload <file>        Upload reference image to Google Flow
  status               Check bridge connection and auth status
  serve                Start bridge server and OpenAI-compatible API
  cdp                  Inspect and test direct Chrome DevTools Protocol connection

Options for image:
  -a, --aspect         Aspect ratio: landscape, square, portrait, 4:3, 3:4 (default: landscape)
  -c, --count          Number of images to generate: 1-4 (default: 1)
  -m, --model          Model: narwhal (default), harbor_seal (lite), gem_pix_2 (pro)
  -o, --output         Output path or directory (default: ./output)
  --ref                Reference image path or media ID
  --json               Output result as JSON

Options for video:
  -a, --aspect         Aspect ratio: landscape, portrait, square (default: landscape)
  -d, --duration       Duration in seconds: 4, 6, 8, 10 (default: 10)
  -r, --resolution     Resolution: 720p, 1080p, 4k (default: 720p)
  -o, --output         Output video file path (default: ./output)
  --start              Start frame image path or media ID
  --end                End frame image path or media ID
  --json               Output result as JSON

Options for serve:
  -p, --port           Port to listen on (default: 8001)
  --host               Host to bind to (default: 127.0.0.1)

Run 'flow <command> --help' for details on specific commands.`)
}

// ensureBackendClient gets an active client either by connecting to running server
// or spinning up a lightweight local bridge.
func ensureBackendClient(cfg *config.Config) (*client.FlowClient, *api.Server, func()) {
	// First check if server is already running on port
	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)
	resp, err := http.Get(healthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		// Server already running, create remote-aware flow client
		br := bridge.NewExtensionBridge()
		fc := client.NewFlowClient(cfg, br)
		return fc, nil, func() {}
	}

	// Server not running, spin up internal bridge
	br := bridge.NewExtensionBridge()
	fc := client.NewFlowClient(cfg, br)
	srv := api.NewServer(fc)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Bridge server error: %v\n", err)
		}
	}()

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}

	return fc, srv, cleanup
}

func waitForReady(fc *client.FlowClient, timeoutSec int) bool {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	br := fc.Bridge()

	fmt.Print("Waiting for Google Flow Chrome extension...")
	for time.Now().Before(deadline) {
		if br.IsConnected() && br.HasFlowKey() {
			fmt.Println(" Ready!")
			return true
		}
		time.Sleep(1 * time.Second)
		fmt.Print(".")
	}
	fmt.Println()
	return br.IsConnected() && br.HasFlowKey()
}

func cmdImage(args []string) {
	fs := flag.NewFlagSet("image", flag.ExitOnError)
	aspect := fs.String("aspect", "landscape", "Aspect ratio")
	fs.StringVar(aspect, "a", "landscape", "Aspect ratio (shorthand)")
	count := fs.Int("count", 1, "Number of images (1-4)")
	fs.IntVar(count, "c", 1, "Number of images (shorthand)")
	model := fs.String("model", "narwhal", "Image model")
	fs.StringVar(model, "m", "narwhal", "Image model (shorthand)")
	output := fs.String("output", "", "Output file or directory")
	fs.StringVar(output, "o", "", "Output file or directory (shorthand)")
	ref := fs.String("ref", "", "Reference image path or media ID")
	jsonOut := fs.Bool("json", false, "Output JSON")

	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: prompt required (e.g. flow image \"a cyberpunk city\")")
		os.Exit(1)
	}
	prompt := fs.Arg(0)

	cfg := config.LoadConfig()

	// If remote server is listening, we can query it via /v1/images/generations
	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)
	resp, err := http.Get(healthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		callRemoteImageGen(cfg, prompt, *aspect, *count, *model, *output, *jsonOut)
		return
	}

	// Otherwise run locally
	fc, _, cleanup := ensureBackendClient(cfg)
	defer cleanup()

	if !waitForReady(fc, 30) {
		fmt.Fprintln(os.Stderr, "\n[!] Extension not connected or not logged into Google Flow.")
		fmt.Fprintln(os.Stderr, "Please load the extension in Chrome and open: https://labs.google/fx/tools/flow")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var refMediaIDs []string
	if *ref != "" {
		if _, err := os.Stat(*ref); err == nil {
			fmt.Printf("Uploading reference image %s...\n", *ref)
			mid, err := fc.UploadImage(ctx, *ref)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to upload reference: %v\n", err)
				os.Exit(1)
			}
			refMediaIDs = append(refMediaIDs, mid)
		} else {
			refMediaIDs = append(refMediaIDs, *ref)
		}
	}

	fmt.Printf("Generating image: %q [%s, %s]...\n", prompt, *aspect, *model)
	assets, err := fc.GenerateImages(ctx, prompt, *aspect, *count, *model, refMediaIDs, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Image generation error: %v\n", err)
		os.Exit(1)
	}

	outDir := *output
	if outDir == "" {
		outDir = cfg.OutputDir
	}

	for i := range assets {
		a := &assets[i]
		savedPath, err := util.SaveAsset(ctx, a, outDir)
		if err == nil {
			a.LocalPath = savedPath
			if !*jsonOut {
				fmt.Printf("Saved image: %s\n", savedPath)
			}
		}
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(assets, "", "  ")
		fmt.Println(string(data))
	}
}

func callRemoteImageGen(cfg *config.Config, prompt string, aspect string, count int, model string, output string, jsonOut bool) {
	reqBody := map[string]any{
		"prompt": prompt,
		"n":      count,
		"size":   aspect,
		"model":  model,
	}
	bodyData, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("http://%s:%d/v1/images/generations", cfg.Host, cfg.Port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyData))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to reach server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Server error (%d): %s\n", resp.StatusCode, string(b))
		os.Exit(1)
	}

	var res struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)

	outDir := output
	if outDir == "" {
		outDir = cfg.OutputDir
	}

	var assets []models.Asset
	ts := time.Now().Format("150405")
	for i, d := range res.Data {
		a := models.Asset{
			ID:       fmt.Sprintf("img_%s_%d", ts, i+1),
			Type:     "image",
			URL:      d.URL,
			Prompt:   prompt,
			MimeType: "image/png",
		}
		saved, err := util.SaveAsset(context.Background(), &a, outDir)
		if err == nil {
			a.LocalPath = saved
			if !jsonOut {
				fmt.Printf("Saved image: %s\n", saved)
			}
		}
		assets = append(assets, a)
	}

	if jsonOut {
		d, _ := json.MarshalIndent(assets, "", "  ")
		fmt.Println(string(d))
	}
}

func remoteUpload(cfg *config.Config, filePath string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"file_path": filePath})
	url := fmt.Sprintf("http://%s:%d/v1/upload", cfg.Host, cfg.Port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(b))
	}

	var res struct {
		MediaID string `json:"media_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.MediaID, nil
}

func callRemoteUpsample(cfg *config.Config, mediaID, aspect, resolution string, jsonOut bool) ([]models.Asset, error) {
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

		if !jsonOut {
			fmt.Print(".")
		}
	}
}

func callRemoteVideoGen(
	cfg *config.Config,
	prompt string,
	aspect string,
	duration int,
	resolution string,
	output string,
	start string,
	end string,
	jsonOut bool,
) {
	// If start or end are local files, upload them via /v1/upload first
	startID := start
	if startID != "" && fileExists(startID) {
		if !jsonOut {
			fmt.Printf("Uploading start image %s...\n", startID)
		}
		mid, err := remoteUpload(cfg, startID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to upload start image: %v\n", err)
			os.Exit(1)
		}
		startID = mid
	}

	endID := end
	if endID != "" && fileExists(endID) {
		if !jsonOut {
			fmt.Printf("Uploading end image %s...\n", endID)
		}
		mid, err := remoteUpload(cfg, endID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to upload end image: %v\n", err)
			os.Exit(1)
		}
		endID = mid
	}

	reqBody := map[string]any{
		"prompt":      prompt,
		"aspect":      aspect,
		"duration":    duration,
		"resolution":  resolution,
		"start_image": startID,
		"end_image":   endID,
	}
	bodyData, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("http://%s:%d/v1/videos/generations", cfg.Host, cfg.Port)
	if !jsonOut {
		fmt.Printf("Submitting video generation: %q [%s, %ds]...\n", prompt, aspect, duration)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyData))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to reach server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Server error (%d): %s\n", resp.StatusCode, string(b))
		os.Exit(1)
	}

	var res struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)

	if !jsonOut {
		fmt.Printf("Job submitted! Media ID: %s. Rendering video...\n", res.JobID)
	}

	// Poll until completed
	pollURL := fmt.Sprintf("http://%s:%d/v1/videos/generations/%s", cfg.Host, cfg.Port, res.JobID)
	client := &http.Client{Timeout: 35 * time.Second}
	startTime := time.Now()

	for {
		time.Sleep(6 * time.Second)
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
			if !jsonOut {
				fmt.Printf("\nRendering finished! (%ds)\n", int(time.Since(startTime).Seconds()))
			}

			assets := pollResult.Assets
			if resolution == "1080p" || resolution == "4k" {
				if !jsonOut {
					fmt.Printf("Video 720p completed. Upsampling to %s...\n", resolution)
				}
				upAssets, err := callRemoteUpsample(cfg, assets[0].ID, aspect, resolution, jsonOut)
				if err == nil && len(upAssets) > 0 {
					assets = upAssets
				}
			}

			outDir := output
			if outDir == "" {
				outDir = cfg.OutputDir
			}

			for i := range assets {
				a := &assets[i]
				saved, err := util.SaveAsset(context.Background(), a, outDir)
				if err == nil {
					a.LocalPath = saved
					if !jsonOut {
						fmt.Printf("Saved video: %s\n", saved)
					}
				}
			}

			if jsonOut {
				d, _ := json.MarshalIndent(assets, "", "  ")
				fmt.Println(string(d))
			}
			return
		}

		if !jsonOut {
			fmt.Printf("Rendering... (%ds)\n", int(time.Since(startTime).Seconds()))
		}
	}
}

func cmdVideo(args []string) {
	fs := flag.NewFlagSet("video", flag.ExitOnError)
	aspect := fs.String("aspect", "landscape", "Aspect ratio: landscape, portrait, square")
	fs.StringVar(aspect, "a", "landscape", "Aspect ratio (shorthand)")
	duration := fs.Int("duration", 10, "Duration in seconds: 4, 6, 8, 10")
	fs.IntVar(duration, "d", 10, "Duration (shorthand)")
	resolution := fs.String("resolution", "720p", "Resolution: 720p, 1080p, 4k")
	fs.StringVar(resolution, "r", "720p", "Resolution (shorthand)")
	output := fs.String("output", "", "Output video file or directory")
	fs.StringVar(output, "o", "", "Output file (shorthand)")
	start := fs.String("start", "", "Start frame image path or media ID")
	end := fs.String("end", "", "End frame image path or media ID")
	jsonOut := fs.Bool("json", false, "Output JSON")

	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: prompt required (e.g. flow video \"a dragon soaring\")")
		os.Exit(1)
	}
	prompt := fs.Arg(0)

	cfg := config.LoadConfig()

	// If remote server is listening, route through remote server!
	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)
	resp, err := http.Get(healthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		callRemoteVideoGen(cfg, prompt, *aspect, *duration, *resolution, *output, *start, *end, *jsonOut)
		return
	}

	fc, _, cleanup := ensureBackendClient(cfg)
	defer cleanup()

	if !waitForReady(fc, 30) {
		fmt.Fprintln(os.Stderr, "\n[!] Extension not ready. Please make sure Google Flow is open.")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	startID := *start
	if startID != "" && fileExists(startID) {
		fmt.Printf("Uploading start image %s...\n", startID)
		mid, err := fc.UploadImage(ctx, startID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to upload start image: %v\n", err)
			os.Exit(1)
		}
		startID = mid
	}

	endID := *end
	if endID != "" && fileExists(endID) {
		fmt.Printf("Uploading end image %s...\n", endID)
		mid, err := fc.UploadImage(ctx, endID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to upload end image: %v\n", err)
			os.Exit(1)
		}
		endID = mid
	}

	fmt.Printf("Submitting video generation: %q [%s, %ds]...\n", prompt, *aspect, *duration)
	mediaIDs, err := fc.GenerateVideo(ctx, prompt, *aspect, *duration, "", startID, endID, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Video submit error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Job submitted! Media ID: %s. Rendering in background...\n", mediaIDs[0])
	assets, err := fc.WaitForVideo(ctx, mediaIDs, 12*time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Video render error: %v\n", err)
		os.Exit(1)
	}

	// Check if upsampling is requested (1080p or 4k)
	if *resolution == "1080p" || *resolution == "4k" {
		fmt.Printf("Video 720p completed. Upsampling to %s...\n", *resolution)
		upIDs, err := fc.UpsampleVideo(ctx, assets[0].ID, *aspect, *resolution, 0)
		if err == nil && len(upIDs) > 0 {
			upAssets, err := fc.WaitForVideo(ctx, upIDs, 10*time.Minute)
			if err == nil && len(upAssets) > 0 {
				assets = upAssets
			}
		}
	}

	outDir := *output
	if outDir == "" {
		outDir = cfg.OutputDir
	}

	for i := range assets {
		a := &assets[i]
		savedPath, err := util.SaveAsset(ctx, a, outDir)
		if err == nil {
			a.LocalPath = savedPath
			if !*jsonOut {
				fmt.Printf("Saved video: %s\n", savedPath)
			}
		}
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(assets, "", "  ")
		fmt.Println(string(data))
	}
}

func cmdUpsample(args []string) {
	fs := flag.NewFlagSet("upsample", flag.ExitOnError)
	resolution := fs.String("resolution", "1080p", "Resolution: 1080p or 4k")
	fs.StringVar(resolution, "r", "1080p", "Resolution (shorthand)")
	aspect := fs.String("aspect", "landscape", "Aspect ratio")
	fs.StringVar(aspect, "a", "landscape", "Aspect ratio (shorthand)")
	output := fs.String("output", "", "Output path")
	fs.StringVar(output, "o", "", "Output path (shorthand)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: media_id required (e.g. flow upsample 01234567-89ab-...)")
		os.Exit(1)
	}
	mediaID := fs.Arg(0)

	cfg := config.LoadConfig()

	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)
	resp, err := http.Get(healthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		if !*jsonOut {
			fmt.Printf("Upsampling %s to %s...\n", mediaID, *resolution)
		}
		assets, err := callRemoteUpsample(cfg, mediaID, *aspect, *resolution, *jsonOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Upsample failed: %v\n", err)
			os.Exit(1)
		}
		outDir := *output
		if outDir == "" {
			outDir = cfg.OutputDir
		}
		for i := range assets {
			a := &assets[i]
			savedPath, err := util.SaveAsset(context.Background(), a, outDir)
			if err == nil {
				a.LocalPath = savedPath
				if !*jsonOut {
					fmt.Printf("Saved upsampled video: %s\n", savedPath)
				}
			}
		}
		if *jsonOut {
			data, _ := json.MarshalIndent(assets, "", "  ")
			fmt.Println(string(data))
		}
		return
	}

	fc, _, cleanup := ensureBackendClient(cfg)
	defer cleanup()

	if !waitForReady(fc, 30) {
		fmt.Fprintln(os.Stderr, "\n[!] Extension not ready. Open Google Flow in Chrome.")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	fmt.Printf("Upsampling %s to %s...\n", mediaID, *resolution)
	upIDs, err := fc.UpsampleVideo(ctx, mediaID, *aspect, *resolution, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Upsample failed: %v\n", err)
		os.Exit(1)
	}

	assets, err := fc.WaitForVideo(ctx, upIDs, 10*time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Upsample polling failed: %v\n", err)
		os.Exit(1)
	}

	outDir := *output
	if outDir == "" {
		outDir = cfg.OutputDir
	}

	for i := range assets {
		a := &assets[i]
		savedPath, err := util.SaveAsset(ctx, a, outDir)
		if err == nil {
			a.LocalPath = savedPath
			if !*jsonOut {
				fmt.Printf("Saved upsampled video: %s\n", savedPath)
			}
		}
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(assets, "", "  ")
		fmt.Println(string(data))
	}
}

func cmdUpload(args []string) {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: file path required (e.g. flow upload character.png)")
		os.Exit(1)
	}
	filePath := fs.Arg(0)

	cfg := config.LoadConfig()

	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)
	resp, err := http.Get(healthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		mid, err := remoteUpload(cfg, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Upload error: %v\n", err)
			os.Exit(1)
		}
		if *jsonOut {
			data, _ := json.Marshal(map[string]string{"media_id": mid, "file": filePath})
			fmt.Println(string(data))
		} else {
			fmt.Printf("File uploaded successfully!\nMedia ID: %s\n", mid)
		}
		return
	}

	fc, _, cleanup := ensureBackendClient(cfg)
	defer cleanup()

	if !waitForReady(fc, 30) {
		fmt.Fprintln(os.Stderr, "\n[!] Extension not ready.")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mid, err := fc.UploadImage(ctx, filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Upload error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		data, _ := json.Marshal(map[string]string{"media_id": mid, "file": filePath})
		fmt.Println(string(data))
	} else {
		fmt.Printf("File uploaded successfully!\nMedia ID: %s\n", mid)
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")
	_ = fs.Parse(args)

	cfg := config.LoadConfig()
	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Host, cfg.Port)

	resp, err := http.Get(healthURL)
	if err != nil {
		if *jsonOut {
			data, _ := json.Marshal(map[string]any{"status": "server_not_running", "error": err.Error()})
			fmt.Println(string(data))
		} else {
			fmt.Println("Flow server is not currently running.")
			fmt.Printf("Run 'flow serve' or start a generation command to launch.\n")
		}
		return
	}
	defer resp.Body.Close()

	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)

	if *jsonOut {
		data, _ := json.MarshalIndent(health, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Server: Running at http://%s:%d\n", cfg.Host, cfg.Port)
		fmt.Printf("Extension Connected: %v\n", health["extension_connected"])
		fmt.Printf("Flow Auth Token:     %v\n", health["has_flow_key"])
		fmt.Printf("Status:              %v\n", health["status"])
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8001, "Port to listen on")
	fs.IntVar(port, "p", 8001, "Port (shorthand)")
	host := fs.String("host", "127.0.0.1", "Host to bind")
	_ = fs.Parse(args)

	cfg := config.LoadConfig()
	cfg.Port = *port
	cfg.Host = *host

	br := bridge.NewExtensionBridge()
	fc := client.NewFlowClient(cfg, br)
	srv := api.NewServer(fc)

	// Catch interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		os.Exit(0)
	}()

	fmt.Println("=====================================================")
	fmt.Printf(" Google Flow Bridge & API Server (Go)\n")
	fmt.Printf(" Listening on: http://%s:%d\n", cfg.Host, cfg.Port)
	fmt.Printf(" Extension directory: %s\n", filepath.Join(".", "extension"))
	fmt.Println("=====================================================")
	fmt.Println("1. Open chrome://extensions, enable Developer mode.")
	fmt.Println("2. Click 'Load unpacked' and select the 'extension' directory.")
	fmt.Println("3. Open https://labs.google/fx/tools/flow in Chrome.")
	fmt.Println("4. Use CLI or OpenAI API (/v1/images/generations) to generate!")
	fmt.Println()

	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdCDP(args []string) {
	fs := flag.NewFlagSet("cdp", flag.ExitOnError)
	port := fs.Int("port", 9222, "Chrome remote debugging port")
	_ = fs.Parse(args)

	fmt.Printf("Searching for Chrome tabs on port %d...\n", *port)
	target, err := cdp.FindFlowTarget(*port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CDP target error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Ensure Chrome was launched with: chrome.exe --remote-debugging-port=9222")
		os.Exit(1)
	}

	fmt.Printf("Found target: %s (%s)\n", target.Title, target.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := cdp.Connect(ctx, target.WebSocketDebuggerURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to CDP: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Println("Connected to Chrome via CDP! Testing reCAPTCHA generation...")
	token, err := client.GetRecaptchaToken(ctx, "IMAGE_GENERATION")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reCAPTCHA evaluation error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("reCAPTCHA Enterprise token successfully generated! (%d chars)\n", len(token))
	fmt.Printf("Token preview: %s...\n", token[:min(30, len(token))])
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
