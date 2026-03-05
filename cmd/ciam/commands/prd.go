package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/docs"
	"github.com/spf13/cobra"
)

var prdCmd = &cobra.Command{
	Use:   "prd",
	Short: "Manage Product Requirement Documents (PRDs)",
	Long: `PRD commands create and list Product Requirement Documents in docs/prd/.

PRD is MANDATORY before starting any new feature — document what and why
before asking the AI to implement anything.`,
}

var prdNewCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Create a new PRD",
	Long: `Creates a new PRD file in docs/prd/ with the standard template.

Example:
  ciam prd new "Subscription billing via Stripe"
  ciam prd new "Multi-tenant user isolation"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")

		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		path, err := m.NewPRD(title)
		if err != nil {
			return fmt.Errorf("failed to create PRD: %w", err)
		}

		rel, _ := filepath.Rel(projectPath, path)
		fmt.Printf("✓ Created %s\n", rel)
		fmt.Printf("  Edit: %s\n", path)
		fmt.Printf("\nFill in the problem, objective, and acceptance criteria BEFORE implementation.\n")
		fmt.Printf("Next: ciam plan new \"%s\" --prd PRD-...\n", title)
		return nil
	},
}

var prdListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all PRDs",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		prds, err := m.ListPRDs()
		if err != nil {
			return err
		}

		if len(prds) == 0 {
			fmt.Println("No PRDs found. Run: ciam prd new \"<title>\"")
			return nil
		}

		fmt.Printf("%-10s  %-12s  %s\n", "ID", "STATUS", "TITLE")
		fmt.Println(strings.Repeat("-", 70))
		for _, p := range prds {
			fmt.Printf("PRD-%03d   %-12s  %s\n", p.Number, p.Status, p.Title)
		}
		return nil
	},
}

func init() {
	prdCmd.AddCommand(prdNewCmd)
	prdCmd.AddCommand(prdListCmd)

	for _, sub := range []*cobra.Command{prdNewCmd, prdListCmd} {
		sub.Flags().StringP("project", "p", "", "Project root path (defaults to current directory)")
	}
}
