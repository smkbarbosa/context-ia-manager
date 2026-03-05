package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initConfigCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate .vscode/mcp.json for the current workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		vscodeDir := ".vscode"
		if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
			return fmt.Errorf("could not create .vscode directory: %w", err)
		}

		mcpPath := filepath.Join(vscodeDir, "mcp.json")
		if _, err := os.Stat(mcpPath); err == nil {
			fmt.Printf(".vscode/mcp.json already exists, skipping.\n")
			return nil
		}

		mcpConfig := map[string]any{
			"servers": map[string]any{
				"ciam": map[string]any{
					"command": "ciam",
					"args":    []string{"mcp"},
					"env": map[string]string{
						"CIAM_API_URL":      "http://localhost:8080",
						"CIAM_OLLAMA_URL":   "http://localhost:11434",
						"CIAM_PROJECT_PATH": "${workspaceFolder}",
					},
				},
			},
		}

		data, err := json.MarshalIndent(mcpConfig, "", "  ")
		if err != nil {
			return err
		}

		if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
			return fmt.Errorf("could not write mcp.json: %w", err)
		}

		fmt.Printf("Created .vscode/mcp.json\n")
		fmt.Println("VSCode and Antigravity will now pick up ciam automatically when this workspace opens.")
		return nil
	},
}
