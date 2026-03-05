package commands

import (
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/smkbarbosa/context-ia-manager/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server (stdio transport — used by VSCode / Antigravity)",
	Long: `Starts the MCP (Model Context Protocol) server via stdio.
This is called automatically by VSCode / Antigravity when the workspace opens.
You don't need to run this manually.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		srv := mcp.NewServer(cfg)
		return srv.Serve()
	},
}
