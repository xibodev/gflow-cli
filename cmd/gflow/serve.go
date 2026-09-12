package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/api"
	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/embedded"
)

var (
	servePort int
	serveHost string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the gflow bridge server & OpenAI-compatible API in the foreground",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.LoadConfig()
		if cmd.Flags().Changed("port") {
			cfg.Port = servePort
		}
		if cmd.Flags().Changed("host") {
			cfg.Host = serveHost
		}
		if err := config.ValidateBindHost(cfg.Host); err != nil {
			return err
		}
		if err := config.ValidatePort(cfg.Port); err != nil {
			return err
		}
		if cfg.APIToken == "" || cfg.BridgeToken == "" {
			if _, err := config.LoadAuth(); err != nil {
				return fmt.Errorf("failed to initialize local credentials: %w", err)
			}
			cfg = config.LoadConfig()
			if cmd.Flags().Changed("port") {
				cfg.Port = servePort
			}
			if cmd.Flags().Changed("host") {
				cfg.Host = serveHost
			}
		}

		br := bridge.NewExtensionBridgeWithToken(cfg.BridgeToken)
		fc := client.NewFlowClient(cfg, br)
		srv := api.NewServer(fc)

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigChan
			fmt.Println("\nGracefully shutting down gflow server...")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
			os.Exit(0)
		}()

		extDir, _ := embedded.GetDefaultExtensionDir()
		if _, err := os.Stat(filepath.Join(extDir, "manifest.json")); os.IsNotExist(err) {
			_, _ = embedded.ExtractExtension("")
		}

		fmt.Println("=====================================================")
		fmt.Println(" gflow server running")
		fmt.Printf(" HTTP & API: http://%s:%d\n", cfg.Host, cfg.Port)
		fmt.Printf(" Extension:  %s\n", extDir)
		fmt.Println("=====================================================")
		fmt.Println("Press Ctrl+C to stop.")

		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}

func init() {
	serveCmd.Flags().IntVarP(&servePort, "port", "p", 8001, "Port to listen on")
	serveCmd.Flags().StringVar(&serveHost, "host", "127.0.0.1", "Host to bind to")
}
