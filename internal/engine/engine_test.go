package engine

import (
	"testing"

	"github.com/jackchuka/postura/internal/rules"
)

func mustEngine(t *testing.T, scope rules.Scope, rs []rules.Rule, vars []string) *Engine {
	t.Helper()
	e, err := New(scope, rs, vars)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func findingByID(fs []Finding, id string) (Finding, bool) {
	for _, f := range fs {
		if f.ID == id {
			return f, true
		}
	}
	return Finding{}, false
}

// ORG-1: a plain boolean port. An unreadable fact (the collector omits the key
// entirely) must surface as unknown, not a false fail. This is the core of the
// unknown mechanism: a declared CEL variable absent from the activation makes
// pass error out, which maps to unknown.
func TestUnreadableFactUnknown(t *testing.T) {
	rs := []rules.Rule{{
		ID: "ORG-1", Scope: rules.ScopeOrg, Severity: "error", Title: "2FA enforced",
		Pass: "two_factor_requirement_enabled == true",
	}}
	e := mustEngine(t, rules.ScopeOrg, rs, []string{"two_factor_requirement_enabled"})
	cfg := &rules.Config{}

	cases := map[string]struct {
		facts map[string]any
		want  Status
	}{
		"enforced":   {map[string]any{"two_factor_requirement_enabled": true}, StatusPass},
		"off":        {map[string]any{"two_factor_requirement_enabled": false}, StatusFail},
		"unreadable": {map[string]any{}, StatusUnknown}, // key omitted by the collector
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := e.Evaluate([]Target{{Name: "acme", Facts: tc.facts}}, cfg, "")
			f, ok := findingByID(got, "ORG-1")
			if !ok {
				t.Fatal("no ORG-1 finding")
			}
			if f.Status != tc.want {
				t.Fatalf("got %s, want %s (notes: %q)", f.Status, tc.want, f.Notes)
			}
		})
	}
}

// REPO-11: jq list predicate -> CEL all(). applies_when skips archived repos.
func TestListPredicateAndAppliesWhen(t *testing.T) {
	rs := []rules.Rule{{
		ID: "REPO-11", Scope: rules.ScopeRepo, Severity: "warn", Title: "push cap",
		Applies: "archived != true",
		Pass:    `teams.all(t, t.permission != "admin" && t.permission != "maintain")`,
	}}
	e := mustEngine(t, rules.ScopeRepo, rs, []string{"archived", "teams"})
	cfg := &rules.Config{}

	targets := []Target{
		{Name: "o/clean", Facts: map[string]any{"archived": false, "teams": []any{
			map[string]any{"slug": "dev", "permission": "push"},
		}}},
		{Name: "o/bad", Facts: map[string]any{"archived": false, "teams": []any{
			map[string]any{"slug": "ops", "permission": "admin"},
		}}},
		{Name: "o/archived", Facts: map[string]any{"archived": true, "teams": []any{
			map[string]any{"slug": "ops", "permission": "admin"},
		}}},
	}
	got := e.Evaluate(targets, cfg, "")

	if len(got) != 2 {
		t.Fatalf("archived repo should be skipped; got %d findings: %+v", len(got), got)
	}
	for _, f := range got {
		want := StatusPass
		if f.Target == "o/bad" {
			want = StatusFail
		}
		if f.Status != want {
			t.Errorf("%s: got %s want %s", f.Target, f.Status, want)
		}
	}
}

// An applies_when that can't be scored (a fact it needs is unreadable) must
// surface as unknown, not silently drop the check — otherwise a missing fact would
// make the rule vanish unnoticed. A false applies_when still skips silently.
func TestUnscorableAppliesWhenUnknown(t *testing.T) {
	rs := []rules.Rule{{
		ID: "REPO-3", Scope: rules.ScopeRepo, Severity: "error", Title: "secret scanning",
		Applies: `archived != true && visibility == "public"`,
		Pass:    `secret_scanning == "enabled"`,
	}}
	e := mustEngine(t, rules.ScopeRepo, rs, []string{"archived", "visibility", "secret_scanning"})
	cfg := &rules.Config{}

	targets := []Target{
		// visibility unreadable: applies_when is unscorable -> unknown.
		{Name: "o/unknown", Facts: map[string]any{"archived": false}},
		// applies_when false (private): skipped, no finding.
		{Name: "o/private", Facts: map[string]any{"archived": false, "visibility": "private"}},
	}
	got := e.Evaluate(targets, cfg, "")

	if len(got) != 1 {
		t.Fatalf("want 1 finding (unknown for o/unknown only), got %d: %+v", len(got), got)
	}
	if got[0].Target != "o/unknown" || got[0].Status != StatusUnknown {
		t.Fatalf("want o/unknown unknown, got %s %s", got[0].Target, got[0].Status)
	}
}

// ORG-2: the judgment rule, now deterministic with config knobs that an org can
// override. Same facts flip pass/fail depending on the org's max_admins.
func TestConfigOverridableThreshold(t *testing.T) {
	rs := []rules.Rule{{
		ID: "ORG-2", Scope: rules.ScopeOrg, Severity: "warn", Title: "admin count",
		Config: map[string]any{"max_admins": 5, "max_admin_ratio": 0.25},
		Pass: "admin_count <= cfg.max_admins && " +
			"(member_count == 0 || double(admin_count) / double(member_count) <= cfg.max_admin_ratio)",
	}}
	e := mustEngine(t, rules.ScopeOrg, rs, []string{"admin_count", "member_count"})
	facts := map[string]any{"admin_count": 7, "member_count": 100}

	// Default max_admins=5 -> 7 admins fails.
	def := &rules.Config{}
	got := e.Evaluate([]Target{{Name: "acme", Facts: facts}}, def, "")
	if f, _ := findingByID(got, "ORG-2"); f.Status != StatusFail {
		t.Fatalf("default: want fail, got %s", f.Status)
	}

	// acme raises its bar to 8 -> same facts now pass.
	override := &rules.Config{Orgs: map[string]map[string]map[string]any{
		"acme": {"ORG-2": {"max_admins": 8}},
	}}
	got = e.Evaluate([]Target{{Name: "acme", Facts: facts}}, override, "")
	if f, _ := findingByID(got, "ORG-2"); f.Status != StatusPass {
		t.Fatalf("override: want pass, got %s", f.Status)
	}
}

// An override can also disable a rule for a specific target.
func TestDisableRulePerTarget(t *testing.T) {
	rs := []rules.Rule{{
		ID: "REPO-1", Scope: rules.ScopeRepo, Severity: "error", Title: "PR required",
		Pass: "protection.required_pull_request_reviews == true",
	}}
	e := mustEngine(t, rules.ScopeRepo, rs, []string{"protection"})
	cfg := &rules.Config{Repos: map[string]map[string]map[string]any{
		"o/legacy": {"REPO-1": {"enabled": false}},
	}}
	facts := map[string]any{"protection": map[string]any{"required_pull_request_reviews": false}}
	got := e.Evaluate([]Target{{Name: "o/legacy", Facts: facts}}, cfg, "")
	if len(got) != 0 {
		t.Fatalf("disabled rule should emit no finding, got %+v", got)
	}
}
