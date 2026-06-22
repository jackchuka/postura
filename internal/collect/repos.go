package collect

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/google/go-github/v84/github"
)

// repoWorkers bounds how many repos are collected concurrently. Each repo makes
// several API calls, so a small pool cuts wall-clock on large orgs while keeping
// secondary-rate-limit pressure modest (the client also retries on a throttle).
const repoWorkers = 8

var codeownersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// Repos collects per-repo facts. With no names it audits every non-forked repo
// in the org. Each returned object is fully normalized against RepoVars.
func Repos(ctx context.Context, c *github.Client, org string, names []string) ([]map[string]any, error) {
	repos, err := resolveRepos(ctx, c, org, names)
	if err != nil {
		return nil, err
	}
	// Direct per-repo team grants for the whole org, read once from the team side
	// (org-role access does not appear there). grantsOK is false if the listing
	// could not be completed, so each repo leaves its teams fact absent (unknown).
	grants, grantsOK := directRepoGrants(ctx, c, org)
	// Collect each repo's facts concurrently, writing into its own slot so the
	// output order matches the (already deterministic) repo list.
	out := make([]map[string]any, len(repos))
	sem := make(chan struct{}, repoWorkers)
	var wg sync.WaitGroup
	for i, r := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, r *github.Repository) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = repoFacts(ctx, c, org, r, grants, grantsOK)
		}(i, r)
	}
	wg.Wait()
	return out, nil
}

func resolveRepos(ctx context.Context, c *github.Client, org string, names []string) ([]*github.Repository, error) {
	if len(names) > 0 {
		var out []*github.Repository
		for _, n := range names {
			r, _, err := c.Repositories.Get(ctx, org, n)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	}
	opt := &github.RepositoryListByOrgOptions{Type: "all", ListOptions: github.ListOptions{PerPage: 100}}
	var out []*github.Repository
	for {
		repos, resp, err := c.Repositories.ListByOrg(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if r.GetFork() {
				continue
			}
			out = append(out, r)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

func repoFacts(ctx context.Context, c *github.Client, org string, r *github.Repository, grants map[string][]any, grantsOK bool) map[string]any {
	name := r.GetName()
	f := map[string]any{
		"name":       org + "/" + name,
		"owner":      org,
		"archived":   r.GetArchived(),
		"visibility": r.GetVisibility(),
	}

	// Code security & analysis: readable on public repos / with admin. When the
	// block is absent we leave the keys out so REPO-3/REPO-9 report unknown rather
	// than a false "disabled".
	if sa := r.GetSecurityAndAnalysis(); sa != nil {
		if ss := sa.GetSecretScanning(); ss != nil {
			f["secret_scanning"] = ss.GetStatus()
		}
		if pp := sa.GetSecretScanningPushProtection(); pp != nil {
			f["secret_scanning_push_protection"] = pp.GetStatus()
		}
	}

	// License: SPDX id or null.
	f["license"] = nil
	if lic := r.GetLicense(); lic != nil {
		f["license"] = strOrNull(lic.GetSPDXID())
	}

	// Dependabot / vulnerability alerts. Left absent (→ unknown) when unreadable
	// (missing scope / no admin), never defaulted to a value that would read as a
	// false "disabled" for a rule over this fact.
	if enabled, _, err := c.Repositories.GetVulnerabilityAlerts(ctx, org, name); err == nil {
		f["vulnerability_alerts"] = enabled
	}

	// Omit on uncertainty so the rule reports unknown rather than a false answer.
	if present, known := codeownersStatus(ctx, c, org, name); known {
		f["codeowners"] = present
	}

	// Teams holding a direct per-repo grant on this repo. A repo absent from the
	// org's grant map definitively has none (empty list). Left absent (unknown)
	// only when the org grants could not be enumerated, so a rule over `teams`
	// never sees a false empty.
	if grantsOK {
		if g, ok := grants[strings.ToLower(org+"/"+name)]; ok {
			f["teams"] = g
		} else {
			f["teams"] = []any{}
		}
	}

	prot, rulesetCount := protection(ctx, c, org, name, r.GetDefaultBranch())
	f["protection"] = prot
	f["ruleset_count"] = rulesetCount

	return f
}

// codeownersStatus reports whether a CODEOWNERS file exists and whether that
// answer is trustworthy. `known` is false when a non-404 error (permission or
// transient) leaves absence unproven; the caller then omits the fact so the rule
// reports unknown instead of a false "missing".
func codeownersStatus(ctx context.Context, c *github.Client, org, repo string) (present, known bool) {
	uncertain := false
	for _, p := range codeownersPaths {
		_, _, resp, err := c.Repositories.GetContents(ctx, org, repo, p, nil)
		if err == nil {
			return true, true
		}
		if resp == nil || resp.StatusCode != http.StatusNotFound {
			uncertain = true // not a clean 404: can't prove absence from this path
		}
	}
	if uncertain {
		return false, false
	}
	return false, true // every path returned a clean 404: definitively absent
}

// directRepoGrants maps "owner/repo" to the teams that hold a direct
// (github_team_repository) grant on it, each with that grant's permission. It is
// built from the team->repos endpoint, which lists only explicit per-repo grants
// — access a team receives through an organization role is not returned there
// (that lives in organization_roles). ok is false if any team's grants could not
// be read, so the caller leaves the teams fact absent rather than risk a false
// "no direct grant".
func directRepoGrants(ctx context.Context, c *github.Client, org string) (grants map[string][]any, ok bool) {
	slugs, err := orgTeamSlugs(ctx, c, org)
	if err != nil {
		return nil, false
	}
	grants = map[string][]any{}
	for _, slug := range slugs {
		opt := &github.ListOptions{PerPage: 100}
		for {
			repos, resp, err := c.Teams.ListTeamReposBySlug(ctx, org, slug, opt)
			if err != nil {
				return nil, false
			}
			for _, r := range repos {
				// Key on a lowercased "owner/repo": the lookup side derives the key
				// from the user-supplied --org string, whose casing may differ from
				// GitHub's canonical full name. A mismatch would yield a false empty
				// teams list (a silent REPO-11 pass), so normalize both sides.
				full := strings.ToLower(r.GetFullName())
				grants[full] = append(grants[full], map[string]any{
					"slug":       slug,
					"permission": highestPerm(r.GetPermissions()),
				})
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
	}
	return grants, true
}

func orgTeamSlugs(ctx context.Context, c *github.Client, org string) ([]string, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []string
	for {
		teams, resp, err := c.Teams.ListTeams(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		for _, t := range teams {
			out = append(out, t.GetSlug())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// highestPerm collapses GitHub's cumulative permission set to the single role
// name used elsewhere (admin > maintain > push > triage > pull), matching the
// legacy `permission` vocabulary REPO-11 reads.
func highestPerm(p *github.RepositoryPermissions) string {
	switch {
	case p.GetAdmin():
		return "admin"
	case p.GetMaintain():
		return "maintain"
	case p.GetPush():
		return "push"
	case p.GetTriage():
		return "triage"
	default:
		return "pull"
	}
}

// protection folds classic branch protection and repository rulesets into one
// normalized object. Many orgs protect the default branch with rulesets,
// which the classic protection endpoint does not return, so both are read and
// the stricter outcome wins for each field. Returns the protection facts and
// the count of active, default-branch-targeting rulesets.
func protection(ctx context.Context, c *github.Client, org, repo, branch string) (map[string]any, int) {
	p := map[string]any{
		"required_pull_request_reviews":   false,
		"required_approving_review_count": 0,
		"require_code_owner_reviews":      false,
		"pr_bypass_actor_types":           []any{},
	}
	if branch == "" {
		return p, 0
	}

	// Classic branch protection (404 when none / rulesets-only).
	if bp, _, err := c.Repositories.GetBranchProtection(ctx, org, repo, branch); err == nil && bp != nil {
		if rev := bp.GetRequiredPullRequestReviews(); rev != nil {
			p["required_pull_request_reviews"] = true
			p["required_approving_review_count"] = rev.RequiredApprovingReviewCount
			p["require_code_owner_reviews"] = rev.RequireCodeOwnerReviews
		}
	}

	count := foldRulesets(ctx, c, org, repo, branch, p)
	return p, count
}

type rulesetSummary struct {
	ID          int64  `json:"id"`
	Target      string `json:"target"`
	Enforcement string `json:"enforcement"`
}

type rulesetDetail struct {
	Conditions struct {
		RefName struct {
			Include []string `json:"include"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
			RequireCodeOwnerReview       bool `json:"require_code_owner_review"`
		} `json:"parameters"`
	} `json:"rules"`
	BypassActors []struct {
		ActorType string `json:"actor_type"`
	} `json:"bypass_actors"`
}

// foldRulesets reads repository rulesets via the stable REST endpoints (go-github's
// typed ruleset model churns across versions), folds any pull_request rule that
// targets the default branch into the protection facts (stricter wins), and
// collects bypass-actor types from PR-requiring rulesets for REPO-12. Returns the
// number of active, default-branch-targeting rulesets.
func foldRulesets(ctx context.Context, c *github.Client, org, repo, branch string, p map[string]any) int {
	var summaries []rulesetSummary
	if _, err := getJSON(ctx, c, "repos/"+org+"/"+repo+"/rulesets?includes_parents=false", &summaries); err != nil {
		return 0
	}
	bypass := map[string]bool{}
	count := 0
	for _, s := range summaries {
		if s.Enforcement != "active" || s.Target != "branch" {
			continue
		}
		var d rulesetDetail
		if _, err := getJSON(ctx, c, "repos/"+org+"/"+repo+"/rulesets/"+itoa(s.ID), &d); err != nil {
			continue
		}
		if !targetsBranch(d.Conditions.RefName.Include, branch) {
			continue
		}
		count++
		for _, r := range d.Rules {
			if r.Type != "pull_request" {
				continue
			}
			p["required_pull_request_reviews"] = true
			if r.Parameters.RequiredApprovingReviewCount > toInt(p["required_approving_review_count"]) {
				p["required_approving_review_count"] = r.Parameters.RequiredApprovingReviewCount
			}
			if r.Parameters.RequireCodeOwnerReview {
				p["require_code_owner_reviews"] = true
			}
			for _, a := range d.BypassActors {
				bypass[a.ActorType] = true
			}
		}
	}
	if len(bypass) > 0 {
		keys := make([]string, 0, len(bypass))
		for t := range bypass {
			keys = append(keys, t)
		}
		sort.Strings(keys) // deterministic order: facts are an artifact (cache/diff)
		types := make([]any, len(keys))
		for i, k := range keys {
			types[i] = k
		}
		p["pr_bypass_actor_types"] = types
	}
	return count
}

// targetsBranch reports whether a ruleset's ref_name include list applies to the
// default branch (~DEFAULT_BRANCH / ~ALL wildcards or an explicit ref).
func targetsBranch(include []string, branch string) bool {
	for _, ref := range include {
		switch ref {
		case "~DEFAULT_BRANCH", "~ALL", "refs/heads/" + branch:
			return true
		}
	}
	return false
}

func toInt(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}
