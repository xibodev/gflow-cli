package main

import (
	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the Model Context Protocol (MCP) server over stdio",
	Long: `Starts an MCP stdio server compatible with Claude Desktop, Cursor, OpenCode, Cline, and Windsurf.
Provides native generation tools for AI assistants.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.LoadConfig()
		br := bridge.NewExtensionBridge()
		fc := client.NewFlowClient(cfg, br)

		srv := mcp.NewServer(fc)
		return srv.Run()
	},
}
