package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "1.0.0"
	commit  = "none"
	date    = "unknown"

	jsonOutput   bool
	providerFlag string
)

var rootCmd = &cobra.Command{
	Use:   "gflow",
	Short: "gflow — Lean multi-provider CLI for AI images, video, and audio",
	Long: `gflow is a single-binary CLI and server for AI image, video, and audio generation.
Supports extension-free Gemini (Imagen 3 & Veo), MiniMax Design (H3), and Google Flow.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")
	rootCmd.PersistentFlags().StringVarP(&providerFlag, "provider", "P", "", "Provider backend: gemini (default), minimax, flow")

	rootCmd.AddCommand(imageCmd)
	rootCmd.AddCommand(videoCmd)
	rootCmd.AddCommand(audioCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(upsampleCmd)
	rootCmd.AddCommand(uploadCmd)
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(mcpCmd)
}

func getProvider() string {
	if providerFlag != "" {
		return providerFlag
	}
	if p := os.Getenv("GFLOW_PROVIDER"); p != "" {
		return p
	}
	return "gemini"
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
