package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ciam",
	Short: "Context IA Manager — semantic search & memory for Django projects",
	Long: `ciam indexes your project, stores memories across sessions, and exposes
everything to your AI assistant via MCP — reducing context usage by up to 98%.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(rememberCmd)
	rootCmd.AddCommand(recallCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(initConfigCmd)
	rootCmd.AddCommand(adrCmd)
	rootCmd.AddCommand(prdCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(watchCmd)
}
