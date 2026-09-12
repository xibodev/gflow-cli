package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/gemini"
)

var audioCmd = &cobra.Command{
	Use:     "audio <prompt>",
	Aliases: []string{"music"},
	Short:   "Generate audio or music track via Gemini",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()

		client, err := gemini.NewClient(ctx, false)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "Generating audio via Gemini: %q...\n", prompt)
		res, err := client.Generate(ctx, "Generate an audio or music track: "+prompt)
		if err != nil {
			return err
		}

		if res.Text != "" {
			fmt.Println(res.Text)
		}
		if res.VideoURL != "" {
			fmt.Printf("Audio track/clip URL: %s\n", res.VideoURL)
		}

		return nil
	},
}
