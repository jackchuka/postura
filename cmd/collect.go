package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackchuka/postura/internal/collect"
	"github.com/spf13/cobra"
)

var (
	collectEnterprise string
	collectOrg        []string
	collectScope      []string
)

var collectCmd = &cobra.Command{
	Use:   "collect [owner/repo ...]",
	Short: "Collect facts and write them as JSON (for caching / offline eval)",
	RunE:  runCollect,
}

func init() {
	f := collectCmd.Flags()
	f.StringSliceVar(&collectOrg, "org", nil, "organization(s) to collect (repeatable/comma-separated; or infer from owner/repo args)")
	f.StringVar(&collectEnterprise, "enterprise", "", "enterprise slug to also collect")
	f.StringSliceVar(&collectScope, "scope", nil, "limit to scopes: enterprise|org|repos (repeatable; default: all applicable)")
	rootCmd.AddCommand(collectCmd)
}

func runCollect(cmd *cobra.Command, args []string) error {
	specs, err := orgsFromArgs(collectOrg, args)
	if err != nil {
		return err
	}
	scopes, err := parseScopes(collectScope, collectEnterprise != "", len(specs) > 0)
	if err != nil {
		return err
	}
	token, err := resolveToken()
	if err != nil {
		return err
	}
	ctx := context.Background()
	c := collect.NewClient(ctx, token)

	var f Facts
	if scopes.enterprise {
		if f.Enterprise, err = collect.Enterprise(ctx, c, collectEnterprise); err != nil {
			return fmt.Errorf("collect enterprise: %w", err)
		}
	}
	if scopes.org {
		for _, s := range specs {
			of, err := collect.Org(ctx, c, s.name)
			if err != nil {
				return fmt.Errorf("collect org %s: %w", s.name, err)
			}
			f.Orgs = append(f.Orgs, of)
		}
	}
	if scopes.repos {
		for _, s := range specs {
			rf, err := collect.Repos(ctx, c, s.name, s.repos)
			if err != nil {
				return fmt.Errorf("collect repos for %s: %w", s.name, err)
			}
			f.Repos = append(f.Repos, rf...)
		}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}
