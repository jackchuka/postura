package engine_test

import (
	"os"
	"testing"

	"github.com/jackchuka/postura/internal/collect"
	"github.com/jackchuka/postura/internal/engine"
	"github.com/jackchuka/postura/internal/rules"
)

// Every rule in the shipped example ruleset must compile under CEL against its
// scope's declared fact variables. This is the lint that guards the manifest.
func TestExampleRulesetCompiles(t *testing.T) {
	data, err := os.ReadFile("../../examples/rules.yaml")
	if err != nil {
		t.Fatalf("read example ruleset: %v", err)
	}
	rs, err := rules.Load(data)
	if err != nil {
		t.Fatalf("load example ruleset: %v", err)
	}

	scopes := []struct {
		scope rules.Scope
		vars  []string
	}{
		{rules.ScopeEnterprise, collect.EnterpriseVars},
		{rules.ScopeOrg, collect.OrgVars},
		{rules.ScopeRepo, collect.RepoVars},
	}
	for _, s := range scopes {
		got := rs.ByScope(s.scope)
		if len(got) == 0 {
			t.Errorf("scope %s: no rules", s.scope)
		}
		if _, err := engine.New(s.scope, got, s.vars); err != nil {
			t.Errorf("scope %s: %v", s.scope, err)
		}
	}
}

// ORG-10 reads organization_roles.teams as the collector actually shapes it:
// a list of {slug, assignment} maps, not bare strings. Compilation alone can't
// catch a mismatch (fact fields are dyn-typed), so this evaluates the shipped
// rule against collector-shaped facts. It guards against the rule and the
// collector drifting apart — an allowlisted broad team must pass, an
// un-allowlisted one must fail.
func TestExampleORG10MatchesCollectorShape(t *testing.T) {
	data, err := os.ReadFile("../../examples/rules.yaml")
	if err != nil {
		t.Fatalf("read example ruleset: %v", err)
	}
	rs, err := rules.Load(data)
	if err != nil {
		t.Fatalf("load example ruleset: %v", err)
	}
	e, err := engine.New(rules.ScopeOrg, rs.ByScope(rules.ScopeOrg), collect.OrgVars)
	if err != nil {
		t.Fatalf("build org engine: %v", err)
	}

	// One broad org role ("admin" marker) held only by team "sre", no users —
	// teams shaped exactly as collect.organizationRoles emits them.
	role := func(teamSlug string) map[string]any {
		return map[string]any{
			"role":  "all-repo-admin",
			"users": []any{},
			"teams": []any{map[string]any{"slug": teamSlug, "assignment": "direct"}},
		}
	}
	cfg := &rules.Config{Orgs: map[string]map[string]map[string]any{
		"acme": {"ORG-10": {"allowed_broad_teams": []any{"sre"}}},
	}}

	cases := map[string]struct {
		team string
		want engine.Status
	}{
		"allowlisted broad team passes":   {"sre", engine.StatusPass},
		"un-allowlisted broad team fails": {"random-team", engine.StatusFail},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			target := engine.Target{Name: "acme", Facts: map[string]any{
				"organization_roles": []any{role(tc.team)},
			}}
			got, ok := findingByID(e.Evaluate([]engine.Target{target}, cfg, ""), "ORG-10")
			if !ok {
				t.Fatal("no ORG-10 finding emitted")
			}
			if got.Status != tc.want {
				t.Fatalf("ORG-10 status = %s, want %s (notes: %q)", got.Status, tc.want, got.Notes)
			}
		})
	}
}

func findingByID(fs []engine.Finding, id string) (engine.Finding, bool) {
	for _, f := range fs {
		if f.ID == id {
			return f, true
		}
	}
	return engine.Finding{}, false
}
