package rules

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Config holds per-target rule overrides, layered most-specific-wins on top of
// each rule's built-in defaults:
//
//	rule.Config  ->  enterprises[slug]  ->  orgs[name]  ->  repos[owner/repo]
//
// Each override block is keyed by rule id. Within a block the reserved keys
// `enabled` (bool) and `severity` (string) tune the rule itself; every other key
// is merged into the rule's `cfg` map available to its CEL expressions.
type Config struct {
	Enterprises map[string]map[string]map[string]any `yaml:"enterprises"`
	Orgs        map[string]map[string]map[string]any `yaml:"orgs"`
	Repos       map[string]map[string]map[string]any `yaml:"repos"`
}

// LoadConfig parses a policy-config YAML file. An empty/nil input yields a
// zero Config (defaults only), which is valid.
func LoadConfig(data []byte) (*Config, error) {
	c := &Config{}
	if len(data) == 0 {
		return c, nil
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}

// Resolved is a rule's effective settings for one specific target.
type Resolved struct {
	Enabled  bool
	Severity string
	Cfg      map[string]any
}

// Resolve computes the effective settings for rule r against a target located at
// (enterprise, org, repo). Empty location components are skipped. The most
// specific override present wins for each individual key.
func (c *Config) Resolve(r Rule, enterprise, org, repo string) Resolved {
	res := Resolved{Enabled: r.Enabled == nil || *r.Enabled, Severity: r.Severity, Cfg: map[string]any{}}
	for k, v := range r.Config {
		res.Cfg[k] = v
	}

	apply := func(block map[string]any) {
		if block == nil {
			return
		}
		for k, v := range block {
			switch k {
			case "enabled":
				if b, ok := v.(bool); ok {
					res.Enabled = b
				}
			case "severity":
				if s, ok := v.(string); ok {
					res.Severity = s
				}
			default:
				res.Cfg[k] = v
			}
		}
	}

	if enterprise != "" {
		apply(c.Enterprises[enterprise][r.ID])
	}
	if org != "" {
		apply(c.Orgs[org][r.ID])
	}
	if repo != "" {
		apply(c.Repos[repo][r.ID])
	}
	return res
}
