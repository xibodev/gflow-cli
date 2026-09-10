package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/xibodev/gflow-cli/pkg/cdp"
)

// flow is the legacy binary. Generation and serve paths are retired in favor
// of the supported `gflow` executable and its authenticated daemon. The CDP
// diagnostic command is preserved.

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}
	command := os.Args[1]
	args := os.Args[2:]
	switch command {
	case "cdp":
		cmdCDP(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "The 'flow' binary is retired for %q. Use 'gflow %s' instead.\n\n", command, command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`flow (legacy) — use 'gflow' instead.

Usage:
  gflow image <prompt>       Generate AI images
  gflow video <prompt>       Generate AI videos
  gflow upsample <media_id>  Upsample video to 1080p or 4K
  gflow upload <file>        Upload reference image
  gflow status               Check bridge connection and auth status
  gflow serve                Start bridge server and API
  flow cdp                   Inspect direct Chrome DevTools Protocol connection`)
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
	preview := token
	if len(preview) > 30 {
		preview = preview[:30]
	}
	fmt.Printf("reCAPTCHA Enterprise token successfully generated! (%d chars)\n", len(token))
	fmt.Printf("Token preview: %s...\n", preview)
}
