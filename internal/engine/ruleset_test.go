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

// REPO-15 is the rule that catches enforcement that only looks enabled: branch
// protection requires code-owner review, but GitHub ignores the CODEOWNERS lines
// it can't resolve, so the owned paths end up requiring nobody. It reads
// protection.require_code_owner_reviews as the collector nests it, which
// compilation can't check (fact fields are dyn-typed) — hence an evaluation test
// against collector-shaped facts, as for ORG-10.
func TestExampleREPO15MatchesCollectorShape(t *testing.T) {
	data, err := os.ReadFile("../../examples/rules.yaml")
	if err != nil {
		t.Fatalf("read example ruleset: %v", err)
	}
	rs, err := rules.Load(data)
	if err != nil {
		t.Fatalf("load example ruleset: %v", err)
	}
	e, err := engine.New(rules.ScopeRepo, rs.ByScope(rules.ScopeRepo), collect.RepoVars)
	if err != nil {
		t.Fatalf("build repo engine: %v", err)
	}

	// protection shaped exactly as collect.protection emits it.
	protection := func(requireCodeOwner bool) map[string]any {
		return map[string]any{
			"required_pull_request_reviews":   true,
			"required_approving_review_count": 1,
			"require_code_owner_reviews":      requireCodeOwner,
			"pr_bypass_actor_types":           []any{},
		}
	}

	// A repo that doesn't require code-owner review has nothing for a broken
	// CODEOWNERS to void, so the rule is out of scope and emits no finding at all
	// — scoring it as a pass would pad the pass count with repos never checked.
	//
	// codeowners nil is the collector's unreadable case: it omits the presence fact
	// and, with it, the error count, since a count it cannot trust is no count.
	cases := map[string]struct {
		codeowners       any
		requireCodeOwner bool
		errorCount       int
		archived         bool
		want             engine.Status
		wantSkipped      bool
	}{
		"broken CODEOWNERS while code-owner review is required": {
			codeowners: true, requireCodeOwner: true, errorCount: 2, want: engine.StatusFail,
		},
		"clean CODEOWNERS with code-owner review required": {
			codeowners: true, requireCodeOwner: true, errorCount: 0, want: engine.StatusPass,
		},
		// The zero error count here is vacuous, not a clean bill: with no file to
		// own anything, every path merges on an ordinary approval, which is the
		// broken-CODEOWNERS defect taken to its limit rather than an exemption.
		"no CODEOWNERS at all is the worst case, not an exemption": {
			codeowners: false, requireCodeOwner: true, errorCount: 0, want: engine.StatusFail,
		},
		"an unreadable CODEOWNERS presence cannot be scored": {
			codeowners: nil, requireCodeOwner: true, want: engine.StatusUnknown,
		},
		"broken CODEOWNERS with no code-owner review to void": {
			codeowners: true, requireCodeOwner: false, errorCount: 2, wantSkipped: true,
		},
		"archived repos are out of scope": {
			codeowners: true, requireCodeOwner: true, errorCount: 2, archived: true, wantSkipped: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			facts := map[string]any{
				"archived":   tc.archived,
				"protection": protection(tc.requireCodeOwner),
			}
			if tc.codeowners != nil {
				facts["codeowners"] = tc.codeowners
				facts["codeowners_error_count"] = tc.errorCount
			}
			target := engine.Target{Name: "acme/web", Facts: facts}
			got, ok := findingByID(e.Evaluate([]engine.Target{target}, &rules.Config{}, ""), "REPO-15")
			if tc.wantSkipped {
				if ok {
					t.Fatalf("REPO-15 should not apply here, got status %s (notes: %q)", got.Status, got.Notes)
				}
				return
			}
			if !ok {
				t.Fatal("no REPO-15 finding emitted")
			}
			if got.Status != tc.want {
				t.Fatalf("REPO-15 status = %s, want %s (notes: %q)", got.Status, tc.want, got.Notes)
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
