package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/jackchuka/postura/internal/collect"
	"github.com/jackchuka/postura/internal/report"
	"github.com/spf13/cobra"
)

var (
	runEnterprise        string
	runConfig, runRules  string
	runFormat, runFailOn string
	runFailOnUnknown     bool
	runOrg, runScope     []string
)

var runCmd = &cobra.Command{
	Use:   "run [owner/repo ...]",
	Short: "Collect facts, evaluate the ruleset, and report findings",
	Long:  "Audit an org (and optionally specific repos), and/or an enterprise via --enterprise. With no repo args, every non-forked repo in the org is audited.",
	RunE:  runRun,
}

func init() {
	f := runCmd.Flags()
	f.StringSliceVar(&runOrg, "org", nil, "organization(s) to audit (repeatable/comma-separated; or infer from owner/repo args)")
	f.StringVar(&runEnterprise, "enterprise", "", "enterprise slug to also audit")
	f.StringVar(&runConfig, "config", "", "policy-config YAML with per-target overrides")
	f.StringVar(&runRules, "rules", "", "ruleset YAML (required; see examples/rules.yaml)")
	f.StringVar(&runFormat, "format", "md", "output format: md | json | sarif")
	f.StringVar(&runFailOn, "fail-on", "error", "exit non-zero if a fail at/above this severity: error | warn | info | none")
	f.BoolVar(&runFailOnUnknown, "fail-on-unknown", false, "also exit non-zero on an unknown finding at/above --fail-on (an under-scoped token can't pass the gate)")
	f.StringSliceVar(&runScope, "scope", nil, "limit to scopes: enterprise|org|repos (repeatable; default: all applicable)")
	_ = runCmd.MarkFlagRequired("rules")
	rootCmd.AddCommand(runCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	specs, err := orgsFromArgs(runOrg, args)
	if err != nil {
		return err
	}
	scopes, err := parseScopes(runScope, runEnterprise != "", len(specs) > 0)
	if err != nil {
		return err
	}
	rs, err := loadRuleset(runRules)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(runConfig)
	if err != nil {
		return err
	}
	token, err := resolveToken()
	if err != nil {
		return err
	}

	ctx := context.Background()
	c := collect.NewClient(ctx, token)
	var facts Facts

	if scopes.enterprise {
		if facts.Enterprise, err = collect.Enterprise(ctx, c, runEnterprise); err != nil {
			return fmt.Errorf("collect enterprise: %w", err)
		}
	}
	if scopes.org {
		for _, s := range specs {
			of, err := collect.Org(ctx, c, s.name)
			if err != nil {
				return fmt.Errorf("collect org %s: %w", s.name, err)
			}
			facts.Orgs = append(facts.Orgs, of)
		}
	}
	if scopes.repos {
		for _, s := range specs {
			rf, err := collect.Repos(ctx, c, s.name, s.repos)
			if err != nil {
				return fmt.Errorf("collect repos for %s: %w", s.name, err)
			}
			facts.Repos = append(facts.Repos, rf...)
		}
	}

	findings, err := evaluate(rs, cfg, facts, runEnterprise)
	if err != nil {
		return err
	}
	if err := writeReport(cmd.OutOrStdout(), findings, runFormat, scopeDesc(facts)); err != nil {
		return err
	}
	if report.ShouldFail(findings, runFailOn, runFailOnUnknown) {
		os.Exit(1)
	}
	return nil
}
