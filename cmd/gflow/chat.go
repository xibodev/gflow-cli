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

var chatCmd = &cobra.Command{
	Use:     "chat <prompt>",
	Aliases: []string{"ask"},
	Short:   "Chat with Gemini in terminal",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := args[0]
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		client, err := gemini.NewClient(ctx, false)
		if err != nil {
			return err
		}

		res, err := client.Generate(ctx, prompt)
		if err != nil {
			return err
		}

		fmt.Println(res.Text)
		return nil
	},
}
