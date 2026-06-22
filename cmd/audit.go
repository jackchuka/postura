package cmd

import (
	"github.com/jackchuka/postura/internal/collect"
	"github.com/jackchuka/postura/internal/engine"
	"github.com/jackchuka/postura/internal/rules"
)

// Facts is the collected-facts document: the unit passed between `collect` and
// `eval`, and produced in-memory by `run`. Orgs and their repos are flat lists
// so a single run can audit several orgs; each repo fact carries its own owner.
type Facts struct {
	Enterprise map[string]any   `json:"enterprise,omitempty"`
	Orgs       []map[string]any `json:"orgs,omitempty"`
	Repos      []map[string]any `json:"repos,omitempty"`
}

// evaluate compiles each scope's rules against its declared fact variables and
// runs them over the collected facts, returning all findings. Per-target config
// (including the owning org) is resolved inside the engine, so multiple orgs and
// their repos can be evaluated in one pass.
func evaluate(rs *rules.Ruleset, cfg *rules.Config, f Facts, enterprise string) ([]engine.Finding, error) {
	var out []engine.Finding

	if f.Enterprise != nil {
		eng, err := engine.New(rules.ScopeEnterprise, rs.ByScope(rules.ScopeEnterprise), collect.EnterpriseVars)
		if err != nil {
			return nil, err
		}
		name := strField(f.Enterprise, "enterprise", enterprise)
		out = append(out, eng.Evaluate([]engine.Target{{Name: name, Facts: f.Enterprise}}, cfg, enterprise)...)
	}

	if len(f.Orgs) > 0 {
		eng, err := engine.New(rules.ScopeOrg, rs.ByScope(rules.ScopeOrg), collect.OrgVars)
		if err != nil {
			return nil, err
		}
		targets := make([]engine.Target, 0, len(f.Orgs))
		for _, o := range f.Orgs {
			targets = append(targets, engine.Target{Name: strField(o, "org", ""), Facts: o})
		}
		out = append(out, eng.Evaluate(targets, cfg, enterprise)...)
	}

	if len(f.Repos) > 0 {
		eng, err := engine.New(rules.ScopeRepo, rs.ByScope(rules.ScopeRepo), collect.RepoVars)
		if err != nil {
			return nil, err
		}
		targets := make([]engine.Target, 0, len(f.Repos))
		for _, r := range f.Repos {
			targets = append(targets, engine.Target{Name: strField(r, "name", ""), Facts: r})
		}
		out = append(out, eng.Evaluate(targets, cfg, enterprise)...)
	}

	return out, nil
}

func strField(m map[string]any, key, fallback string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

// normalizeNumbers converts whole float64 values (as produced by encoding/json)
// to int64 so CEL integer comparisons hold after a collect->JSON->eval round
// trip. Genuine fractional values (ratios in config, not facts) are untouched.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case float64:
		if t == float64(int64(t)) {
			return int64(t)
		}
		return t
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeNumbers(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = normalizeNumbers(val)
		}
		return t
	}
	return v
}
