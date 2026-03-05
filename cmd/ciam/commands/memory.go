package commands

import (
	"fmt"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/spf13/cobra"
)

var rememberType string

var rememberCmd = &cobra.Command{
	Use:   "remember <content>",
	Short: "Store an important decision or note in persistent memory",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content := strings.Join(args, " ")
		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		if err := client.Remember(content, rememberType); err != nil {
			return fmt.Errorf("failed to store memory: %w", err)
		}

		fmt.Println("Memory stored.")
		return nil
	},
}

var recallCmd = &cobra.Command{
	Use:   "recall <query>",
	Short: "Search stored memories from previous sessions",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		cfg := config.Load()
		client := api.NewClient(cfg.APIURL)

		memories, err := client.Recall(query)
		if err != nil {
			return fmt.Errorf("recall failed: %w", err)
		}

		if len(memories) == 0 {
			fmt.Println("No memories found.")
			return nil
		}

		fmt.Printf("Found %d memor(ies):\n\n", len(memories))
		for i, m := range memories {
			fmt.Printf("[%d] [%s] %s\n    → %s\n\n", i+1, m.Type, m.CreatedAt, m.Content)
		}
		return nil
	},
}

func init() {
	rememberCmd.Flags().StringVarP(&rememberType, "type", "t", "decision",
		"Memory type: decision, note, context, bug, architecture")
}
