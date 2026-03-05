package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/docs"
	"github.com/spf13/cobra"
)

var adrCmd = &cobra.Command{
	Use:   "adr",
	Short: "Manage Architecture Decision Records (ADRs)",
	Long: `ADR commands create and list Architecture Decision Records in docs/adr/.

ADR is MANDATORY for every relevant bug fix — document root cause and decision
before writing the fix code.`,
}

var adrNewCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Create a new ADR",
	Long: `Creates a new ADR file in docs/adr/ using the MADR template.

Example:
  ciam adr new "Use UUID as primary key for all models"
  ciam adr new "Switch from DRF to FastAPI"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")

		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		path, err := m.NewADR(title)
		if err != nil {
			return fmt.Errorf("failed to create ADR: %w", err)
		}

		rel, _ := filepath.Rel(projectPath, path)
		fmt.Printf("✓ Created %s\n", rel)
		fmt.Printf("  Edit: %s\n", path)
		fmt.Printf("\nDocument the root cause and decision BEFORE writing code.\n")
		return nil
	},
}

var adrListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all ADRs",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		adrs, err := m.ListADRs()
		if err != nil {
			return err
		}

		if len(adrs) == 0 {
			fmt.Println("No ADRs found. Run: ciam adr new \"<title>\"")
			return nil
		}

		fmt.Printf("%-10s  %-12s  %s\n", "ID", "STATUS", "TITLE")
		fmt.Println(strings.Repeat("-", 70))
		for _, a := range adrs {
			fmt.Printf("ADR-%03d   %-12s  %s\n", a.Number, a.Status, a.Title)
		}
		return nil
	},
}

var adrSupersedeCmd = &cobra.Command{
	Use:   "supersede <number> <new-title>",
	Short: "Mark an ADR as superseded and create a replacement",
	Long: `Marks ADR-N as superseded and creates a new ADR referencing it.

Example:
  ciam adr supersede 3 "Use ULID instead of UUID for sortable PKs"`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var n int
		if _, err := fmt.Sscan(args[0], &n); err != nil {
			return fmt.Errorf("invalid ADR number: %s", args[0])
		}
		newTitle := strings.Join(args[1:], " ")

		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		path, err := m.SupersedeADR(n, newTitle)
		if err != nil {
			return fmt.Errorf("failed to supersede ADR: %w", err)
		}

		rel, _ := filepath.Rel(projectPath, path)
		fmt.Printf("✓ ADR-%03d marked as superseded\n", n)
		fmt.Printf("✓ Created replacement: %s\n", rel)
		return nil
	},
}

// resolveProjectPath returns the --project flag value or the current directory.
func resolveProjectPath(cmd *cobra.Command) (string, error) {
	p, _ := cmd.Flags().GetString("project")
	if p == "" {
		var err error
		p, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(p)
}

func init() {
	adrCmd.AddCommand(adrNewCmd)
	adrCmd.AddCommand(adrListCmd)
	adrCmd.AddCommand(adrSupersedeCmd)

	// --project flag on all sub-commands
	for _, sub := range []*cobra.Command{adrNewCmd, adrListCmd, adrSupersedeCmd} {
		sub.Flags().StringP("project", "p", "", "Project root path (defaults to current directory)")
	}
}
