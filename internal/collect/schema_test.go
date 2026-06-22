package collect

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestReadmeFactsMatchSchema keeps the README's "Facts available to rules"
// section in sync with the schema. It fails if a field exists in the *Vars
// slices but isn't documented, or is documented but no longer exists — so
// adding a fact field forces a README update (and removing one forbids a stale
// entry).
func TestReadmeFactsMatchSchema(t *testing.T) {
	known := map[string]bool{}
	for _, vs := range [][]string{EnterpriseVars, OrgVars, RepoVars} {
		for _, v := range vs {
			known[v] = true
		}
	}

	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	section := factsSection(string(data))
	if section == "" {
		t.Fatal(`README has no "## Facts" section`)
	}

	// First backticked token of each table row is the documented field name.
	rowField := regexp.MustCompile("(?m)^\\|\\s*`([a-z0-9_]+)`")
	documented := map[string]bool{}
	for _, m := range rowField.FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}

	if missing := diff(known, documented); len(missing) > 0 {
		t.Errorf("fields in schema.go but not in README Facts section: %v", missing)
	}
	if stale := diff(documented, known); len(stale) > 0 {
		t.Errorf("fields in README Facts section but not in schema.go: %v", stale)
	}
}

// diff returns keys present in a but absent from b, sorted.
func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// factsSection returns the body of the "## Facts" section, up to the next "## ".
func factsSection(md string) string {
	lines := strings.Split(md, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") && strings.Contains(strings.ToLower(l), "facts") {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "## ") {
			break
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}
