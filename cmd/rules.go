package cmd

import (
	"fmt"

	"github.com/jackchuka/postura/internal/rules"
	"github.com/spf13/cobra"
)

var rulesPath string

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Inspect the ruleset",
}

var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all rules (id, scope, severity, title)",
	RunE:  runRulesList,
}

var rulesExplainCmd = &cobra.Command{
	Use:   "explain <id>",
	Short: "Show a rule's CEL expressions, config knobs, and fix hint",
	Args:  cobra.ExactArgs(1),
	RunE:  runRulesExplain,
}

func init() {
	rulesCmd.PersistentFlags().StringVar(&rulesPath, "rules", "", "ruleset YAML (required; see examples/rules.yaml)")
	_ = rulesCmd.MarkPersistentFlagRequired("rules")
	rulesCmd.AddCommand(rulesListCmd, rulesExplainCmd)
	rootCmd.AddCommand(rulesCmd)
}

func runRulesList(cmd *cobra.Command, _ []string) error {
	rs, err := loadRuleset(rulesPath)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, r := range rs.Rules {
		state := ""
		if r.Enabled != nil && !*r.Enabled {
			state = " [off by default]"
		}
		_, _ = fmt.Fprintf(out, "%-8s %-10s %-7s %s%s\n", r.ID, r.Scope, r.Severity, r.Title, state)
	}
	return nil
}

func runRulesExplain(cmd *cobra.Command, args []string) error {
	rs, err := loadRuleset(rulesPath)
	if err != nil {
		return err
	}
	for _, r := range rs.Rules {
		if r.ID == args[0] {
			return explainRule(cmd, r)
		}
	}
	return fmt.Errorf("no such rule: %s", args[0])
}

func explainRule(cmd *cobra.Command, r rules.Rule) error {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%s — %s\n", r.ID, r.Title)
	_, _ = fmt.Fprintf(out, "  scope:    %s\n", r.Scope)
	_, _ = fmt.Fprintf(out, "  severity: %s\n", r.Severity)
	if r.Applies != "" {
		_, _ = fmt.Fprintf(out, "  applies:  %s\n", r.Applies)
	}
	_, _ = fmt.Fprintf(out, "  pass:     %s\n", r.Pass)
	if len(r.Config) > 0 {
		_, _ = fmt.Fprintln(out, "  config (overridable per enterprise/org/repo):")
		for k, v := range r.Config {
			_, _ = fmt.Fprintf(out, "    %s: %v\n", k, v)
		}
	}
	if r.FixHint != "" {
		_, _ = fmt.Fprintf(out, "  fix:      %s\n", r.FixHint)
	}
	return nil
}
