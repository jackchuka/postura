package cmd

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"os"

	"github.com/jackchuka/postura/internal/engine"
	"github.com/jackchuka/postura/internal/report"
	"github.com/jackchuka/postura/internal/rules"
)

// loadRuleset reads the ruleset from path. postura ships no built-in ruleset, so
// a path is always required (see examples/rules.yaml for a starting point).
func loadRuleset(path string) (*rules.Ruleset, error) {
	if path == "" {
		return nil, fmt.Errorf("no ruleset: pass --rules <file> (see examples/rules.yaml)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rules %s: %w", path, err)
	}
	return rules.Load(data)
}

// loadConfig reads the policy-config from path, or an empty config.
func loadConfig(path string) (*rules.Config, error) {
	if path == "" {
		return rules.LoadConfig(nil)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return rules.LoadConfig(data)
}

// resolveToken finds a GitHub token from the environment, falling back to the
// `gh` CLI's stored credential.
func resolveToken() (string, error) {
	for _, e := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(e); v != "" {
			return v, nil
		}
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if tok := strings.TrimSpace(string(out)); tok != "" {
			return tok, nil
		}
	}
	return "", fmt.Errorf("no GitHub token: set GITHUB_TOKEN/GH_TOKEN or run `gh auth login`")
}

// orgSpec is one org to audit and, optionally, the specific repos within it. An
// empty repos slice means "every non-forked repo in the org".
type orgSpec struct {
	name  string
	repos []string
}

// orgsFromArgs resolves the orgs to audit from the repeatable --org flag and any
// owner/repo arguments. Each distinct owner becomes an org; an owner/repo arg
// scopes that org to the named repo, while an --org with no matching arg audits
// all of its repos. A bare repo name (no owner) attaches to the sole org when
// exactly one is in play and is otherwise ambiguous. Order is stable (flags
// first, then first appearance in args) so output is deterministic. A missing
// org is not an error here — whether one is required depends on the resolved
// scope (see parseScopes).
func orgsFromArgs(orgFlags, args []string) ([]orgSpec, error) {
	specs := map[string]*orgSpec{}
	var order []string
	add := func(name string) *orgSpec {
		s, ok := specs[name]
		if !ok {
			s = &orgSpec{name: name}
			specs[name] = s
			order = append(order, name)
		}
		return s
	}
	for _, o := range orgFlags {
		add(o)
	}
	var bare []string
	for _, a := range args {
		if owner, repo, ok := strings.Cut(a, "/"); ok {
			s := add(owner)
			s.repos = append(s.repos, repo)
		} else {
			bare = append(bare, a)
		}
	}
	if len(bare) > 0 {
		if len(order) != 1 {
			return nil, fmt.Errorf("bare repo name(s) %v are ambiguous: pass them as owner/repo, or name a single --org", bare)
		}
		specs[order[0]].repos = append(specs[order[0]].repos, bare...)
	}
	out := make([]orgSpec, 0, len(order))
	for _, n := range order {
		out = append(out, *specs[n])
	}
	return out, nil
}

// scopeSet is the set of scopes an audit will collect and evaluate.
type scopeSet struct {
	enterprise, org, repos bool
}

// parseScopes resolves which scopes to audit. With no explicit --scope, the
// default is "everything nameable": enterprise when a slug was given, plus org
// and repos when an org is available (orgs are named entities, so they don't
// auto-expand from an enterprise the way repos auto-expand from an org). Each
// scope requires its input — org/repos need an org, enterprise needs a slug —
// and naming nothing auditable is an error.
func parseScopes(values []string, hasEnterprise, hasOrg bool) (scopeSet, error) {
	if len(values) == 0 {
		s := scopeSet{enterprise: hasEnterprise, org: hasOrg, repos: hasOrg}
		if !s.enterprise && !s.org && !s.repos {
			return s, fmt.Errorf("nothing to audit: pass --org, an owner/repo argument, or --enterprise")
		}
		return s, nil
	}
	var s scopeSet
	for _, v := range values {
		switch v {
		case "enterprise":
			s.enterprise = true
		case "org":
			s.org = true
		case "repos":
			s.repos = true
		default:
			return s, fmt.Errorf("unknown scope %q (enterprise|org|repos)", v)
		}
	}
	if s.enterprise && !hasEnterprise {
		return s, fmt.Errorf("--scope enterprise requires --enterprise <slug>")
	}
	if (s.org || s.repos) && !hasOrg {
		return s, fmt.Errorf("--scope org/repos requires --org or an owner/repo argument")
	}
	return s, nil
}

// writeReport renders findings to w in the requested format.
func writeReport(w io.Writer, findings []engine.Finding, format, scope string) error {
	switch format {
	case "md", "markdown":
		_, _ = fmt.Fprint(w, report.Markdown(findings, scope))
	case "json":
		b, err := report.JSON(findings)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, string(b))
	case "sarif":
		b, err := report.SARIF(findings)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, string(b))
	default:
		return fmt.Errorf("unknown format %q (md|json|sarif)", format)
	}
	return nil
}

// scopeDesc describes the audited scope for the report summary line, e.g.
// "enterprise + org + 12 repos".
func scopeDesc(f Facts) string {
	var parts []string
	if f.Enterprise != nil {
		parts = append(parts, "enterprise")
	}
	if n := len(f.Orgs); n == 1 {
		parts = append(parts, "org")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d orgs", n))
	}
	if len(f.Repos) > 0 {
		parts = append(parts, fmt.Sprintf("%d repos", len(f.Repos)))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " + ")
}
