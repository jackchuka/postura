// Package engine evaluates a ruleset against collected facts using CEL and
// produces findings. It is pure and deterministic: same facts + same rules +
// same config always yield the same findings. No network, no model.
package engine

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"github.com/jackchuka/postura/internal/rules"
)

// Status is the outcome of evaluating one rule against one target.
type Status string

const (
	StatusPass    Status = "pass"    // compliant
	StatusFail    Status = "fail"    // confirmed violation
	StatusUnknown Status = "unknown" // could not score (missing fact / scope) — never a pass
)

// Finding is the result of one rule against one target.
type Finding struct {
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Target   string `json:"target"`
	Status   Status `json:"status"`
	Notes    string `json:"notes,omitempty"`
	FixHint  string `json:"fix_hint,omitempty"`
}

// Target is one fact-bearing entity to evaluate rules against.
type Target struct {
	Name  string         // display id (org name, enterprise slug, owner/repo)
	Facts map[string]any // fully-normalized facts; fields bind as top-level CEL vars
}

// compiled is a rule with its CEL programs pre-compiled against a scope env.
type compiled struct {
	rule    rules.Rule
	pass    cel.Program
	applies cel.Program // nil if rule has no applies_when
}

// Engine evaluates a single scope's rules. Build one per scope via New.
type Engine struct {
	scope    rules.Scope
	compiled []compiled
}

// New compiles the given rules against a CEL environment whose top-level
// variables are `varNames` (the union of fact-field names for the scope) plus
// `cfg`. A compile error in any expression is returned — this is the lint that
// catches a typo'd field name or a non-boolean rule before evaluation.
func New(scope rules.Scope, rs []rules.Rule, varNames []string) (*Engine, error) {
	env, err := newEnv(varNames)
	if err != nil {
		return nil, err
	}
	e := &Engine{scope: scope}
	for _, r := range rs {
		c := compiled{rule: r}
		if c.pass, err = compileBool(env, r.Pass); err != nil {
			return nil, fmt.Errorf("rule %s pass: %w", r.ID, err)
		}
		if r.Applies != "" {
			if c.applies, err = compileBool(env, r.Applies); err != nil {
				return nil, fmt.Errorf("rule %s applies_when: %w", r.ID, err)
			}
		}
		e.compiled = append(e.compiled, c)
	}
	return e, nil
}

// Evaluate runs every compiled rule against every target, resolving per-target
// config via cfg layered enterprise→org→repo. The org and repo keys are derived
// from each target: for org scope the org is the target's own name; for repo
// scope (Name "owner/repo") the org is the owner. This lets one Evaluate span
// multiple orgs, each resolving its own overrides. Disabled rules and
// not-applicable targets emit no finding.
func (e *Engine) Evaluate(targets []Target, cfg *rules.Config, enterprise string) []Finding {
	var out []Finding
	for _, c := range e.compiled {
		for _, t := range targets {
			org, repoKey := "", ""
			switch e.scope {
			case rules.ScopeOrg:
				org = t.Name
			case rules.ScopeRepo:
				repoKey = t.Name
				if owner, _, ok := strings.Cut(t.Name, "/"); ok {
					org = owner
				}
			}
			res := cfg.Resolve(c.rule, enterprise, org, repoKey)
			if !res.Enabled {
				continue
			}
			act := activation(t.Facts, res.Cfg)

			f := Finding{
				ID: c.rule.ID, Scope: string(e.scope), Severity: res.Severity,
				Title: c.rule.Title, Target: t.Name, FixHint: c.rule.FixHint,
			}

			// applies_when: a false result skips the target silently (no row). An
			// unscorable one — a fact the predicate needs is missing — is reported
			// as unknown, never silently dropped, so the check can't vanish unnoticed.
			if c.applies != nil {
				ok, err := evalBool(c.applies, act)
				switch {
				case err != nil:
					f.Status = StatusUnknown
					f.Notes = unknownNote(err)
					out = append(out, f)
					continue
				case !ok:
					continue
				}
			}

			// A pass-evaluation error means a fact the rule needs is missing from
			// the activation — the collector could not read it (usually a missing
			// token scope). Report that as unknown, never a false fail.
			ok, err := evalBool(c.pass, act)
			switch {
			case err != nil:
				f.Status = StatusUnknown
				f.Notes = unknownNote(err)
			case ok:
				f.Status = StatusPass
			default:
				f.Status = StatusFail
			}
			out = append(out, f)
		}
	}
	return out
}

func newEnv(varNames []string) (*cel.Env, error) {
	opts := []cel.EnvOption{
		ext.Strings(),
		cel.Variable("cfg", cel.MapType(cel.StringType, cel.DynType)),
	}
	for _, v := range varNames {
		opts = append(opts, cel.Variable(v, cel.DynType))
	}
	return cel.NewEnv(opts...)
}

func compileBool(env *cel.Env, expr string) (cel.Program, error) {
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, iss.Err()
	}
	ot := ast.OutputType()
	if !ot.IsExactType(cel.BoolType) && !ot.IsExactType(cel.DynType) {
		return nil, fmt.Errorf("expression must be boolean, got %s", ot)
	}
	return env.Program(ast)
}

func evalBool(p cel.Program, act map[string]any) (bool, error) {
	out, _, err := p.Eval(act)
	if err != nil {
		return false, err
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("non-boolean result %v", out.Value())
	}
	return b, nil
}

// activation builds the CEL variable bindings: every fact field at top level,
// plus `cfg`.
func activation(facts, cfg map[string]any) map[string]any {
	act := make(map[string]any, len(facts)+1)
	for k, v := range facts {
		act[k] = v
	}
	act["cfg"] = cfg
	return act
}

// unknownNote turns a pass-evaluation error into a human note. A missing fact (a
// declared CEL variable absent from the activation — i.e. the collector could
// not read it) is the common case and names the unreadable field.
func unknownNote(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "no such attribute") || strings.Contains(msg, "undeclared reference") {
		if field := lastIdent(msg); field != "" {
			return field + " not readable (missing scope?)"
		}
		return "fact not readable (missing scope?)"
	}
	return "could not evaluate: " + msg
}

// lastIdent returns the final identifier-like token in s (letters, digits, _).
func lastIdent(s string) string {
	end := -1
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		isIdent := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if isIdent {
			if end == -1 {
				end = i + 1
			}
		} else if end != -1 {
			return s[i+1 : end]
		}
	}
	if end != -1 {
		return s[:end]
	}
	return ""
}
