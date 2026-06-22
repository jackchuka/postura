package cmd

import "testing"

func TestParseScopes(t *testing.T) {
	tests := []struct {
		name          string
		values        []string
		hasEnterprise bool
		hasOrg        bool
		want          scopeSet
		wantErr       bool
	}{
		{
			name:   "default with org: org+repos",
			hasOrg: true,
			want:   scopeSet{org: true, repos: true},
		},
		{
			name:          "default with org+enterprise: all three",
			hasEnterprise: true,
			hasOrg:        true,
			want:          scopeSet{enterprise: true, org: true, repos: true},
		},
		{
			name:          "default enterprise-only (no org): enterprise, no error",
			hasEnterprise: true,
			want:          scopeSet{enterprise: true},
		},
		{
			name:    "default with nothing nameable errors",
			wantErr: true,
		},
		{
			name:   "explicit subset with org",
			values: []string{"org", "repos"},
			hasOrg: true,
			want:   scopeSet{org: true, repos: true},
		},
		{
			name:          "explicit enterprise with slug",
			values:        []string{"enterprise"},
			hasEnterprise: true,
			want:          scopeSet{enterprise: true},
		},
		{
			name:    "explicit enterprise without slug errors",
			values:  []string{"enterprise"},
			wantErr: true,
		},
		{
			name:    "explicit org without an org errors",
			values:  []string{"org"},
			wantErr: true,
		},
		{
			name:    "unknown scope errors",
			values:  []string{"team"},
			hasOrg:  true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScopes(tt.values, tt.hasEnterprise, tt.hasOrg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (scopes %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOrgsFromArgs(t *testing.T) {
	tests := []struct {
		name     string
		orgFlags []string
		args     []string
		want     []orgSpec
		wantErr  bool
	}{
		{
			name:     "single org flag, no args",
			orgFlags: []string{"acme"},
			want:     []orgSpec{{name: "acme"}},
		},
		{
			name: "org inferred from owner/repo",
			args: []string{"acme/web", "acme/api"},
			want: []orgSpec{{name: "acme", repos: []string{"web", "api"}}},
		},
		{
			name:     "bare repo names with single org flag",
			orgFlags: []string{"acme"},
			args:     []string{"web", "api"},
			want:     []orgSpec{{name: "acme", repos: []string{"web", "api"}}},
		},
		{
			name:     "multiple orgs via flags, order preserved",
			orgFlags: []string{"acme", "acme-labs"},
			want:     []orgSpec{{name: "acme"}, {name: "acme-labs"}},
		},
		{
			name: "owner/repo across multiple owners stays attributed",
			args: []string{"acme/web", "other/api"},
			want: []orgSpec{{name: "acme", repos: []string{"web"}}, {name: "other", repos: []string{"api"}}},
		},
		{
			name:     "flag org plus owner/repo for a different org",
			orgFlags: []string{"acme"},
			args:     []string{"other/web"},
			want:     []orgSpec{{name: "acme"}, {name: "other", repos: []string{"web"}}},
		},
		{
			name:    "bare repo name with no org is ambiguous",
			args:    []string{"web"},
			wantErr: true,
		},
		{
			name:     "bare repo name with multiple orgs is ambiguous",
			orgFlags: []string{"acme", "labs"},
			args:     []string{"web"},
			wantErr:  true,
		},
		{
			name: "no org and no args yields nothing",
			want: []orgSpec{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := orgsFromArgs(tt.orgFlags, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equalSpecs(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func equalSpecs(a, b []orgSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || len(a[i].repos) != len(b[i].repos) {
			return false
		}
		for j := range a[i].repos {
			if a[i].repos[j] != b[i].repos[j] {
				return false
			}
		}
	}
	return true
}
