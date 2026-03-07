package commands

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/smkbarbosa/context-ia-manager/internal/config"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start Ollama and the ciam API via Docker Compose",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Starting services (Ollama + ciam-api)...")

		compose := exec.Command("docker", "compose", "up", "-d")
		compose.Stdout = os.Stdout
		compose.Stderr = os.Stderr
		if err := compose.Run(); err != nil {
			return fmt.Errorf("docker compose up failed: %w", err)
		}

		fmt.Print("Waiting for Ollama to be ready")
		if err := waitForOllama(60); err != nil {
			return err
		}
		fmt.Println(" ready.")

		fmt.Println("Pulling embedding model (nomic-embed-text)...")
		pull := exec.Command("docker", "compose", "exec", "-T", "ollama",
			"ollama", "pull", "nomic-embed-text")
		pull.Stdout = os.Stdout
		pull.Stderr = os.Stderr
		if err := pull.Run(); err != nil {
			return fmt.Errorf("model pull failed: %w", err)
		}

		cfg := config.Load()
		if cfg.CodeModel != "" {
			fmt.Printf("Pulling code model (%s)...\n", cfg.CodeModel)
			pull2 := exec.Command("docker", "compose", "exec", "-T", "ollama",
				"ollama", "pull", cfg.CodeModel)
			pull2.Stdout = os.Stdout
			pull2.Stderr = os.Stderr
			if err := pull2.Run(); err != nil {
				// non-fatal: embedding still works without code model
				fmt.Printf("warning: code model pull failed (%v) — ciam_draft will be unavailable\n", err)
			}
		}

		fmt.Println("\nAll services ready.")
		fmt.Println("  API:    http://localhost:8080")
		fmt.Println("  Ollama: http://localhost:11434")
		return nil
	},
}

// waitForOllama polls `ollama list` until it succeeds or timeout is reached.
func waitForOllama(maxSeconds int) error {
	for i := 0; i < maxSeconds; i++ {
		check := exec.Command("docker", "compose", "exec", "-T", "ollama", "ollama", "list")
		if check.Run() == nil {
			return nil
		}
		fmt.Print(".")
		time.Sleep(time.Second)
	}
	return fmt.Errorf("ollama did not become ready after %d seconds", maxSeconds)
}
