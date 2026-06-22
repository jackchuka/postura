package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jackchuka/postura/internal/report"
	"github.com/spf13/cobra"
)

var (
	evalConfig, evalRules  string
	evalFormat, evalFailOn string
	evalFailOnUnknown      bool
)

var evalCmd = &cobra.Command{
	Use:   "eval <facts.json>",
	Short: "Evaluate the ruleset against a collected-facts JSON file",
	Args:  cobra.ExactArgs(1),
	RunE:  runEval,
}

func init() {
	f := evalCmd.Flags()
	f.StringVar(&evalConfig, "config", "", "policy-config YAML with per-target overrides")
	f.StringVar(&evalRules, "rules", "", "ruleset YAML (required; see examples/rules.yaml)")
	f.StringVar(&evalFormat, "format", "md", "output format: md | json | sarif")
	f.StringVar(&evalFailOn, "fail-on", "none", "exit non-zero if a fail at/above this severity: error | warn | info | none (eval default: none)")
	f.BoolVar(&evalFailOnUnknown, "fail-on-unknown", false, "also exit non-zero on an unknown finding at/above --fail-on")
	_ = evalCmd.MarkFlagRequired("rules")
	rootCmd.AddCommand(evalCmd)
}

func runEval(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var f Facts
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse facts: %w", err)
	}
	f.Enterprise = asMap(normalizeNumbers(f.Enterprise))
	for i := range f.Orgs {
		f.Orgs[i] = asMap(normalizeNumbers(f.Orgs[i]))
	}
	for i := range f.Repos {
		f.Repos[i] = asMap(normalizeNumbers(f.Repos[i]))
	}

	rs, err := loadRuleset(evalRules)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(evalConfig)
	if err != nil {
		return err
	}
	ent := strField(orMap(f.Enterprise), "enterprise", "")
	findings, err := evaluate(rs, cfg, f, ent)
	if err != nil {
		return err
	}
	if err := writeReport(cmd.OutOrStdout(), findings, evalFormat, scopeDesc(f)); err != nil {
		return err
	}
	if report.ShouldFail(findings, evalFailOn, evalFailOnUnknown) {
		os.Exit(1)
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
