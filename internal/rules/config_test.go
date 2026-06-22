package rules

import "testing"

func boolPtr(b bool) *bool { return &b }

func TestResolvePrecedence(t *testing.T) {
	r := Rule{ID: "ORG-2", Severity: "warn", Config: map[string]any{"max_admins": 5, "max_admin_ratio": 0.25}}
	c := &Config{
		Enterprises: map[string]map[string]map[string]any{
			"acme": {"ORG-2": {"max_admins": 6}},
		},
		Orgs: map[string]map[string]map[string]any{
			"acme-inc": {"ORG-2": {"max_admins": 8, "severity": "error"}},
		},
	}

	// org override beats enterprise beats rule default; untouched knob keeps default.
	got := c.Resolve(r, "acme", "acme-inc", "")
	if got.Cfg["max_admins"] != 8 {
		t.Errorf("max_admins: got %v, want 8", got.Cfg["max_admins"])
	}
	if got.Cfg["max_admin_ratio"] != 0.25 {
		t.Errorf("max_admin_ratio: got %v, want 0.25 (default)", got.Cfg["max_admin_ratio"])
	}
	if got.Severity != "error" {
		t.Errorf("severity: got %q, want error", got.Severity)
	}

	// enterprise-only override applies when no org layer present.
	got = c.Resolve(r, "acme", "other-org", "")
	if got.Cfg["max_admins"] != 6 {
		t.Errorf("enterprise layer: got %v, want 6", got.Cfg["max_admins"])
	}
}

func TestResolveEnabled(t *testing.T) {
	off := Rule{ID: "REPO-8", Severity: "info", Enabled: boolPtr(false), Pass: "true"}
	// off by default
	if c := (&Config{}); c.Resolve(off, "", "", "").Enabled {
		t.Error("REPO-8 should be disabled by default")
	}
	// re-enabled per repo
	c := &Config{Repos: map[string]map[string]map[string]any{"o/r": {"REPO-8": {"enabled": true}}}}
	if !c.Resolve(off, "", "", "o/r").Enabled {
		t.Error("REPO-8 should be enabled for o/r")
	}
}
