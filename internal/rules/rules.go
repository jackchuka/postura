// Package rules loads and validates the postura policy ruleset. Rules are data:
// each rule carries CEL expressions that are evaluated against collected facts by
// the engine package. Nothing here touches GitHub or makes decisions on its own.
package rules

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Scope identifies which facts object a rule runs against.
type Scope string

const (
	ScopeEnterprise Scope = "enterprise"
	ScopeOrg        Scope = "org"
	ScopeRepo       Scope = "repo"
)

// Rule is one policy check. The CEL expressions all evaluate to bool and read
// fact fields as top-level variables plus `cfg` (the rule's merged config).
type Rule struct {
	ID       string         `yaml:"id"`
	Scope    Scope          `yaml:"scope"`
	Severity string         `yaml:"severity"`
	Title    string         `yaml:"title"`
	Pass     string         `yaml:"pass"`         // true => compliant
	Applies  string         `yaml:"applies_when"` // optional: false => skip (no row)
	Config   map[string]any `yaml:"config"`       // default knobs, overridable per target
	Enabled  *bool          `yaml:"enabled"`      // baseline on/off; nil => on. Config can override per target.
	FixHint  string         `yaml:"fix_hint"`
}

// Ruleset is the full ordered list of rules.
type Ruleset struct {
	Rules []Rule `yaml:"rules"`
}

// Load parses a ruleset from YAML and validates it structurally. CEL
// compilation happens in the engine, against a scope-specific environment.
func Load(data []byte) (*Ruleset, error) {
	var rs Ruleset
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("parse ruleset: %w", err)
	}
	if err := rs.validate(); err != nil {
		return nil, err
	}
	return &rs, nil
}

var validSeverity = map[string]bool{"error": true, "warn": true, "info": true}

func (rs *Ruleset) validate() error {
	seen := map[string]bool{}
	for i, r := range rs.Rules {
		if r.ID == "" {
			return fmt.Errorf("rule[%d]: missing id", i)
		}
		if seen[r.ID] {
			return fmt.Errorf("rule %s: duplicate id", r.ID)
		}
		seen[r.ID] = true
		switch r.Scope {
		case ScopeEnterprise, ScopeOrg, ScopeRepo:
		default:
			return fmt.Errorf("rule %s: invalid scope %q", r.ID, r.Scope)
		}
		if !validSeverity[r.Severity] {
			return fmt.Errorf("rule %s: invalid severity %q", r.ID, r.Severity)
		}
		if r.Pass == "" {
			return fmt.Errorf("rule %s: missing pass expression", r.ID)
		}
	}
	return nil
}

// ByScope returns the rules for one scope, in manifest order.
func (rs *Ruleset) ByScope(s Scope) []Rule {
	var out []Rule
	for _, r := range rs.Rules {
		if r.Scope == s {
			out = append(out, r)
		}
	}
	return out
}
