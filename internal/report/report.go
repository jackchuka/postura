// Package report renders engine findings as Markdown, JSON, or SARIF, and
// decides the process exit code from a --fail-on severity threshold.
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackchuka/postura/internal/engine"
)

var sevRank = map[string]int{"error": 0, "warn": 1, "info": 2}

// Counts summarizes findings by status.
type Counts struct {
	Fail, Unknown, Pass int
}

func Count(fs []engine.Finding) Counts {
	var c Counts
	for _, f := range fs {
		switch f.Status {
		case engine.StatusFail:
			c.Fail++
		case engine.StatusUnknown:
			c.Unknown++
		case engine.StatusPass:
			c.Pass++
		}
	}
	return c
}

// ShouldFail reports whether any finding fails at or above the threshold
// severity. threshold "none" never fails; "" defaults to "error". When
// failOnUnknown is set, an unknown finding at/above the threshold also fails —
// for the "the audit must be able to read everything" posture, so an
// under-scoped token can't slip a green build past the gate.
func ShouldFail(fs []engine.Finding, threshold string, failOnUnknown bool) bool {
	if threshold == "none" {
		return false
	}
	if threshold == "" {
		threshold = "error"
	}
	limit, ok := sevRank[threshold]
	if !ok {
		limit = sevRank["error"]
	}
	for _, f := range fs {
		if sevRank[f.Severity] > limit {
			continue
		}
		switch f.Status {
		case engine.StatusFail:
			return true
		case engine.StatusUnknown:
			if failOnUnknown {
				return true
			}
		}
	}
	return false
}

// JSON renders findings as an indented JSON array.
func JSON(fs []engine.Finding) ([]byte, error) {
	if fs == nil {
		fs = []engine.Finding{}
	}
	return json.MarshalIndent(fs, "", "  ")
}

type ruleGroup struct {
	id, title, severity, fixHint string
	findings                     []engine.Finding
}

func groupByRule(fs []engine.Finding) []ruleGroup {
	idx := map[string]int{}
	var groups []ruleGroup
	for _, f := range fs {
		i, ok := idx[f.ID]
		if !ok {
			i = len(groups)
			idx[f.ID] = i
			groups = append(groups, ruleGroup{id: f.ID, title: f.Title, severity: f.Severity, fixHint: f.FixHint})
		}
		groups[i].findings = append(groups[i].findings, f)
	}
	sort.SliceStable(groups, func(a, b int) bool {
		if sevRank[groups[a].severity] != sevRank[groups[b].severity] {
			return sevRank[groups[a].severity] < sevRank[groups[b].severity]
		}
		return groups[a].id < groups[b].id
	})
	return groups
}

// Markdown renders the findings report: one table per rule, ordered by severity
// then id, listing only fail and unknown rows. scopeDesc describes the run (e.g.
// "12 repos + org") for the summary line.
func Markdown(fs []engine.Finding, scopeDesc string) string {
	c := Count(fs)
	var b strings.Builder
	fmt.Fprintf(&b, "# GitHub audit\n\n")
	fmt.Fprintf(&b, "_%d fail, %d unknown across %s_\n\n", c.Fail, c.Unknown, scopeDesc)

	for _, g := range groupByRule(fs) {
		var fail, unknown []engine.Finding
		for _, f := range g.findings {
			switch f.Status {
			case engine.StatusFail:
				fail = append(fail, f)
			case engine.StatusUnknown:
				unknown = append(unknown, f)
			}
		}
		fmt.Fprintf(&b, "### %s — %s (%s)\n\n", g.id, g.title, g.severity)
		total := len(g.findings)
		if len(fail) == 0 && len(unknown) == 0 {
			fmt.Fprintf(&b, "✅ all %d pass\n\n", total)
			continue
		}
		fmt.Fprintf(&b, "_%d fail, %d unknown of %d checked_\n\n", len(fail), len(unknown), total)
		b.WriteString("| Target | Status | Notes |\n|--------|--------|-------|\n")
		for _, f := range fail {
			fmt.Fprintf(&b, "| %s | ❌ fail | %s |\n", f.Target, f.Notes)
		}
		for _, f := range unknown {
			fmt.Fprintf(&b, "| %s | ⚠️ unknown | %s |\n", f.Target, f.Notes)
		}
		if g.fixHint != "" {
			fmt.Fprintf(&b, "\n**Fix:** %s\n", g.fixHint)
		}
		b.WriteString("\n")
	}
	return b.String()
}
