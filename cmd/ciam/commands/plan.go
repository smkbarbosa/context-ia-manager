package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/docs"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Manage implementation plans",
	Long: `Plan commands create and list implementation plans in docs/plans/.

Plans are MANDATORY for features with more than one implementation phase.
Each phase has explicit success criteria and a manual verification checkpoint.`,
}

var planPRDRef string

var planNewCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Create a new implementation plan",
	Long: `Creates a new Plan file in docs/plans/ with the phased template.

Example:
  ciam plan new "Stripe billing integration" --prd PRD-001
  ciam plan new "Multi-tenant isolation"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")

		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		path, err := m.NewPlan(title, planPRDRef)
		if err != nil {
			return fmt.Errorf("failed to create plan: %w", err)
		}

		rel, _ := filepath.Rel(projectPath, path)
		fmt.Printf("✓ Created %s\n", rel)
		fmt.Printf("  Edit: %s\n", path)
		if planPRDRef != "" {
			fmt.Printf("  Linked to: %s\n", planPRDRef)
		}
		fmt.Printf("\nFill in the phases and success criteria, then share with the AI to implement.\n")
		return nil
	},
}

var planListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all plans",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath, err := resolveProjectPath(cmd)
		if err != nil {
			return err
		}

		m := docs.New(projectPath)
		plans, err := m.ListPlans()
		if err != nil {
			return err
		}

		if len(plans) == 0 {
			fmt.Println("No plans found. Run: ciam plan new \"<title>\"")
			return nil
		}

		fmt.Printf("%-12s  %-12s  %s\n", "ID", "STATUS", "TITLE")
		fmt.Println(strings.Repeat("-", 70))
		for _, p := range plans {
			fmt.Printf("Plan-%03d    %-12s  %s\n", p.Number, p.Status, p.Title)
		}
		return nil
	},
}

func init() {
	planCmd.AddCommand(planNewCmd)
	planCmd.AddCommand(planListCmd)

	planNewCmd.Flags().StringVar(&planPRDRef, "prd", "", "PRD reference (e.g. PRD-001)")
	planNewCmd.Flags().StringP("project", "p", "", "Project root path (defaults to current directory)")
	planListCmd.Flags().StringP("project", "p", "", "Project root path (defaults to current directory)")
}
