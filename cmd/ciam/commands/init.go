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
		editor := cmd.Flag("editor").Value.String()
		if editor == "auto" {
			editor = detectEditor()
		}

		err := generateMCPConfig(editor)
		if err != nil {
			return err
		}

		fmt.Printf("VSCode, Antigravity and other editors will now pick up ciam automatically when this workspace opens.\n")
		return nil
	},
}

func init() {
	initConfigCmd.Flags().StringP("editor", "e", "auto", "Target editor for MCP config (auto, vscode, cursor, cline, windsurf, opencode, antigravity)")
}

func detectEditor() string {
	termProgram := os.Getenv("TERM_PROGRAM")
	if termProgram != "" {
		switch termProgram {
		case "vscode":
			return "vscode"
		case "cursor":
			return "cursor"
		case "windsurf":
			return "windsurf"
		}
	}

	// Fallback to checking Antigravity globals
	homeDir, err := os.UserHomeDir()
	if err == nil {
		antigravityPath := filepath.Join(homeDir, ".gemini", "antigravity")
		if _, err := os.Stat(antigravityPath); err == nil {
			return "antigravity"
		}
	}
	return "vscode" // Default fallback
}

func generateMCPConfig(editor string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("could not get current working directory: %w", err)
	}
	projectName := filepath.Base(cwd)

	// Base ciam server config
	serverConfig := map[string]any{
		"command": "ciam",
		"args":    []string{"mcp"},
		"env": map[string]string{
			"CIAM_API_URL":      "http://localhost:8080",
			"CIAM_OLLAMA_URL":   "http://localhost:11434",
			"CIAM_PROJECT_PATH": "${workspaceFolder}",
		},
	}
	// For global configs like Antigravity, use absolute path instead of ${workspaceFolder}
	serverConfigAbsolute := map[string]any{
		"command": "ciam",
		"args":    []string{"mcp"},
		"env": map[string]string{
			"CIAM_API_URL":      "http://localhost:8080",
			"CIAM_OLLAMA_URL":   "http://localhost:11434",
			"CIAM_PROJECT_PATH": cwd,
		},
	}

	switch editor {
	case "vscode":
		return writeLocalConfig(".vscode", "mcp.json", "servers", "ciam", serverConfig)
	case "cursor":
		return writeLocalConfig(".cursor", "mcp.json", "mcpServers", "ciam", serverConfig)
	case "cline":
		return writeLocalConfig(".vscode", "cline_mcp_settings.json", "mcpServers", "ciam", serverConfig)
	case "windsurf":
		return writeLocalConfig(".windsurf", "mcp.json", "mcpServers", "ciam", serverConfig)
	case "opencode":
		return writeLocalConfig(".", ".opencode.json", "mcp", "ciam", map[string]any{
			"type":        "local",
			"command":     []string{"ciam", "mcp"},
			"environment": serverConfig["env"],
		})
	case "antigravity":
		return appendGlobalConfig("antigravity", fmt.Sprintf("ciam-%s", projectName), serverConfigAbsolute)
	default:
		return fmt.Errorf("unsupported editor: %s", editor)
	}
}

func writeLocalConfig(dirName, fileName, serversKey, serverName string, serverConfig interface{}) error {
	mcpPath := fileName
	if dirName != "." {
		if err := os.MkdirAll(dirName, 0o755); err != nil {
			return fmt.Errorf("could not create directory %s: %w", dirName, err)
		}
		mcpPath = filepath.Join(dirName, fileName)
	}

	var configMap map[string]any
	if _, err := os.Stat(mcpPath); err == nil {
		data, err := os.ReadFile(mcpPath)
		if err == nil {
			_ = json.Unmarshal(data, &configMap)
		}
	}
	if configMap == nil {
		configMap = make(map[string]any)
	}

	serversObj, ok := configMap[serversKey].(map[string]any)
	if !ok {
		serversObj = make(map[string]any)
		configMap[serversKey] = serversObj
	}

	serversObj[serverName] = serverConfig

	data, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
		return fmt.Errorf("could not write %s: %w", mcpPath, err)
	}

	fmt.Printf("Created/Updated %s for local workspace\n", mcpPath)
	return nil
}

func appendGlobalConfig(editor, serverName string, serverConfig interface{}) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get home directory: %w", err)
	}

	var mcpPath string
	var serversKey string

	if editor == "antigravity" {
		mcpPath = filepath.Join(homeDir, ".gemini", "antigravity", "mcp_config.json")
		serversKey = "mcpServers"
	} else {
		return fmt.Errorf("unsupported global editor: %s", editor)
	}

	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		return fmt.Errorf("could not create specific editor directory: %w", err)
	}

	var configMap map[string]any
	if _, err := os.Stat(mcpPath); err == nil {
		data, err := os.ReadFile(mcpPath)
		if err == nil {
			_ = json.Unmarshal(data, &configMap)
		}
	}
	if configMap == nil {
		configMap = make(map[string]any)
	}

	serversObj, ok := configMap[serversKey].(map[string]any)
	if !ok {
		serversObj = make(map[string]any)
		configMap[serversKey] = serversObj
	}

	serversObj[serverName] = serverConfig

	data, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
		return fmt.Errorf("could not write global config to %s: %w", mcpPath, err)
	}

	fmt.Printf("Created/Updated global config for %s at %s\n", editor, mcpPath)
	return nil
}
