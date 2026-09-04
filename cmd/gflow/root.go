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

	jsonOutput bool
)

var rootCmd = &cobra.Command{
	Use:   "gflow",
	Short: "gflow — Lean CLI and API for Google Flow (Imagen 4 & Veo 3.1)",
	Long: `gflow is a single-binary CLI and server for Google Flow image and video generation.
Powered by Imagen 4 (Nano Banana 2) and Veo 3.1. Zero external Python/Node dependencies.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")

	rootCmd.AddCommand(imageCmd)
	rootCmd.AddCommand(videoCmd)
	rootCmd.AddCommand(upsampleCmd)
	rootCmd.AddCommand(uploadCmd)
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(mcpCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
