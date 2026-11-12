package cmd

import (
	"context"
	"encoding/json"
	"fmt"

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
	f, err := collectFacts(context.Background(), token, scopes, specs, collectEnterprise, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}
