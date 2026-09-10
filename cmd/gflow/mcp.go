package main

import (
	"github.com/spf13/cobra"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/daemon"
	"github.com/xibodev/gflow-cli/pkg/mcp"
	"github.com/xibodev/gflow-cli/pkg/remote"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the Model Context Protocol (MCP) server over stdio",
	Long: `Starts an MCP stdio server compatible with Claude Desktop, Cursor, OpenCode, Cline, and Windsurf.
The MCP server is a client of the local gflow daemon; it never owns extension sessions directly.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.LoadConfig()
		// Stdout must carry only JSON-RPC; EnsureRunning is silent on success.
		if err := daemon.EnsureRunningWithAuth(cfg.Host, cfg.Port, cfg.APIToken); err != nil {
			return err
		}
		rc := remote.New(cfg.Host, cfg.Port, cfg.APIToken)
		srv := mcp.NewServerRemote(rc, cfg)
		return srv.Run()
	},
}
