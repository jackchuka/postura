package collect

import (
	"context"
	"strings"
	"time"

	"github.com/google/go-github/v84/github"
)

// alertSeverities are the severity buckets a Dependabot alert can land in, in
// GitHub's own ordering. An advisory carrying anything else counts toward the
// repo's total but no bucket, so a rule over a named bucket is never skewed by
// a severity postura does not model.
var alertSeverities = []string{"critical", "high", "medium", "low"}

// alertBucket is one severity's open-alert tally. dated counts the alerts whose
// age could actually be established, which is what separates "nothing older
// than oldestDays" from "nothing we could date".
type alertBucket struct {
	count, dated, oldestDays int
}

// repoTally accumulates one repo's open alerts across pages.
type repoTally struct {
	total int
	sev   map[string]*alertBucket
}

func newRepoTally() *repoTally { return &repoTally{sev: map[string]*alertBucket{}} }

func (t *repoTally) add(now time.Time, a *github.DependabotAlert) {
	t.total++
	// security_vulnerability.severity is the rating for the package and version
	// range this alert is actually about — the severity GitHub's own UI puts on
	// the alert. The advisory-wide severity is the max across every package the
	// GHSA covers and can differ, which would score the alert against the wrong
	// SLA window; it is only the fallback for a response missing the field.
	sev := strings.ToLower(a.GetSecurityVulnerability().GetSeverity())
	if sev == "" {
		sev = strings.ToLower(a.GetSecurityAdvisory().GetSeverity())
	}
	b := t.sev[sev]
	if b == nil {
		b = &alertBucket{}
		t.sev[sev] = b
	}
	b.count++
	created := a.GetCreatedAt().Time
	if created.IsZero() {
		return
	}
	b.dated++
	if d := ageDays(now, created); d > b.oldestDays {
		b.oldestDays = d
	}
}

// fact renders the tally as the `dependabot_alerts` fact.
func (t *repoTally) fact() map[string]any {
	f := map[string]any{"total": t.total}
	for _, s := range alertSeverities {
		b := t.sev[s]
		if b == nil {
			b = &alertBucket{}
		}
		bf := map[string]any{"count": b.count}
		// An alert carrying no usable created_at has an unknown age, not a fresh
		// one. Any such alert in the bucket makes the datable maximum a mere
		// lower bound, so oldest_open_days is only emitted when every counted
		// alert was dated; otherwise the field is left absent and an age rule
		// reports unknown instead of trusting a "worst case" that is not one.
		if b.count == b.dated {
			bf["oldest_open_days"] = b.oldestDays
		}
		f[s] = bf
	}
	return f
}

// alertLister is one page-fetching call — the org-wide or the repo-scoped
// endpoint — bound to its target.
type alertLister func(context.Context, *github.ListAlertsOptions) ([]*github.DependabotAlert, *github.Response, error)

// listOpenAlerts walks every page of an open-alert listing, folding each alert
// into the tally its key names. Returns ok=false if any page could not be read,
// so a partial listing is never mistaken for a complete one.
func listOpenAlerts(ctx context.Context, list alertLister, key func(*github.DependabotAlert) string) (map[string]*repoTally, bool) {
	tally := map[string]*repoTally{}
	opt := &github.ListAlertsOptions{State: github.Ptr("open")}
	opt.ListCursorOptions.PerPage = 100
	now := time.Now()
	for {
		alerts, resp, err := list(ctx, opt)
		if err != nil {
			return nil, false
		}
		for _, a := range alerts {
			k := key(a)
			if k == "" {
				continue
			}
			t := tally[k]
			if t == nil {
				t = newRepoTally()
				tally[k] = t
			}
			t.add(now, a)
		}
		if resp.After == "" {
			break
		}
		// A cursor that does not advance would re-request the same page forever,
		// and the page it just served was already a duplicate. That is a
		// malformed listing, not a complete one: report it unreadable rather
		// than pass off a truncated, double-counted tally as authoritative.
		if resp.After == opt.After {
			return nil, false
		}
		opt.After = resp.After
	}
	return tally, true
}

// dependabotAlerts sweeps the org's open Dependabot alerts once and folds them
// into per-repo severity buckets, keyed "org/repo". One org-wide read covers
// every repo, instead of a paginated listing per repo.
//
// Returns ok=false when the sweep could not be completed (a token without the
// security_events scope or without org-owner / security-manager standing, a
// mid-pagination error), so the caller falls back to the repo-scoped listing.
// A completed sweep that mentions no repo is a proven zero, not an unknown: it
// returns an empty map with ok=true, and alertsFor zero-fills each repo.
//
// The endpoint only reports repos where Dependabot alerts are enabled, so a
// repo with them turned off is never mentioned. repoFacts therefore skips the
// fact entirely when `vulnerability_alerts` is known false — zero-filling there
// would be a false clean — and rules over it must gate on
// `vulnerability_alerts == true` for the case where that flag is unreadable.
func dependabotAlerts(ctx context.Context, c *github.Client, org string) (map[string]any, bool) {
	tally, ok := listOpenAlerts(ctx,
		func(ctx context.Context, opt *github.ListAlertsOptions) ([]*github.DependabotAlert, *github.Response, error) {
			return c.Dependabot.ListOrgAlerts(ctx, org, opt)
		},
		func(a *github.DependabotAlert) string { return a.GetRepository().GetName() })
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(tally))
	for repo, t := range tally {
		out[org+"/"+repo] = t.fact()
	}
	return out, true
}

// repoAlerts reads one repo's open alerts directly. It is the fallback for when
// the org-wide sweep was skipped (an explicit repo subset, where sweeping the
// whole org to use a handful of it is pure waste) or refused (a repo-scoped
// token, which cannot read the org endpoint but can read this one). Returns
// ok=false when this listing is refused too — including on a repo with
// Dependabot alerts disabled, which GitHub answers with a 403 — so the fact
// stays absent rather than becoming a false clean.
func repoAlerts(ctx context.Context, c *github.Client, org, repo string) (any, bool) {
	tally, ok := listOpenAlerts(ctx,
		func(ctx context.Context, opt *github.ListAlertsOptions) ([]*github.DependabotAlert, *github.Response, error) {
			return c.Dependabot.ListRepoAlerts(ctx, org, repo, opt)
		},
		func(*github.DependabotAlert) string { return repo })
	if !ok {
		return nil, false
	}
	if t := tally[repo]; t != nil {
		return t.fact(), true
	}
	return zeroAlertsFact(), true
}

// zeroAlertsFact is the fact for a repo a completed listing proved clean: every
// bucket at zero. The one rendering of "no open alerts", shared by both the
// org-sweep and repo-listing paths so the two can never drift apart.
func zeroAlertsFact() map[string]any { return newRepoTally().fact() }

// alertsFor returns one repo's alert fact from a completed org sweep. A repo the
// sweep never mentioned carries a fully zeroed fact, so an SLA rule reads a real
// zero rather than needing a guard for the no-alerts case.
func alertsFor(byRepo map[string]any, org, name string) any {
	if f, ok := byRepo[org+"/"+name]; ok {
		return f
	}
	return zeroAlertsFact()
}

// ageDays is whole days elapsed since t, floored at zero. Computed at collect
// time and frozen into the facts, so a later eval over cached facts is stable.
func ageDays(now, t time.Time) int {
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}
