package collect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v84/github"
)

// testClient points a github.Client at a local handler so collect.Enterprise's
// GraphQL POST is served without network access.
func testClient(t *testing.T, h http.HandlerFunc) *github.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := github.NewClient(nil)
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = u
	return c
}

func TestEnterpriseMapsOwnerInfoAndPaginates(t *testing.T) {
	// Two pages of outside collaborators; settings appear only on the first page.
	page1 := `{"data":{"enterprise":{"ownerInfo":{
		"defaultRepositoryPermissionSetting":"READ",
		"ipAllowListEnabledSetting":"ENABLED",
		"allowPrivateRepositoryForkingSetting":"DISABLED",
		"samlIdentityProvider":{"ssoUrl":"https://sso.example/saml"},
		"outsideCollaborators":{"nodes":[{"login":"alice"}],
			"pageInfo":{"hasNextPage":true,"endCursor":"C1"}}}}}}`
	page2 := `{"data":{"enterprise":{"ownerInfo":{
		"defaultRepositoryPermissionSetting":"READ",
		"ipAllowListEnabledSetting":"ENABLED",
		"allowPrivateRepositoryForkingSetting":"DISABLED",
		"samlIdentityProvider":{"ssoUrl":"https://sso.example/saml"},
		"outsideCollaborators":{"nodes":[{"login":"bob"}],
			"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`

	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables struct {
				After *string `json:"after"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Variables.After == nil {
			_, _ = io.WriteString(w, page1)
		} else {
			_, _ = io.WriteString(w, page2)
		}
	})

	f, err := Enterprise(context.Background(), c, "acme")
	if err != nil {
		t.Fatalf("Enterprise: %v", err)
	}

	want := map[string]any{
		"enterprise":                       "acme",
		"default_repository_permission":    "READ",
		"ip_allow_list_enabled":            "ENABLED",
		"allow_private_repository_forking": "DISABLED",
		"saml_sso_url":                     "https://sso.example/saml",
		"outside_collaborator_count":       2,
	}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s: got %v, want %v", k, f[k], v)
		}
	}
	oc, ok := f["outside_collaborators"].([]any)
	if !ok || len(oc) != 2 || oc[0] != "alice" || oc[1] != "bob" {
		t.Errorf("outside_collaborators: got %v, want [alice bob]", f["outside_collaborators"])
	}
}

func TestEnterpriseNullOwnerInfoIsUnknown(t *testing.T) {
	// A non-owner token: enterprise resolves but ownerInfo is null. Every settings
	// key must stay absent so the rules report unknown, not false passes.
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"enterprise":{"ownerInfo":null}},`+
			`"errors":[{"message":"requires enterprise owner"}]}`)
	})

	f, err := Enterprise(context.Background(), c, "acme")
	if err != nil {
		t.Fatalf("Enterprise: %v", err)
	}
	if f["enterprise"] != "acme" {
		t.Errorf("enterprise slug missing: %v", f)
	}
	for _, k := range EnterpriseVars {
		if k == "enterprise" {
			continue
		}
		if _, present := f[k]; present {
			t.Errorf("field %q should be absent (unknown) when ownerInfo is null, got %v", k, f[k])
		}
	}
}

func TestEnterpriseNullSamlIsExplicitNull(t *testing.T) {
	// ownerInfo readable but no SAML configured: saml_sso_url is present and null
	// (a real "off"), distinct from the unknown case above.
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"enterprise":{"ownerInfo":{
			"defaultRepositoryPermissionSetting":"NO_POLICY",
			"ipAllowListEnabledSetting":"DISABLED",
			"allowPrivateRepositoryForkingSetting":"NO_POLICY",
			"samlIdentityProvider":null,
			"outsideCollaborators":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`)
	})

	f, err := Enterprise(context.Background(), c, "acme")
	if err != nil {
		t.Fatalf("Enterprise: %v", err)
	}
	if v, present := f["saml_sso_url"]; !present || v != nil {
		t.Errorf("saml_sso_url: got (%v, present=%v), want (nil, true)", v, present)
	}
	if f["outside_collaborator_count"] != 0 {
		t.Errorf("outside_collaborator_count: got %v, want 0", f["outside_collaborator_count"])
	}
	if oc, ok := f["outside_collaborators"].([]any); !ok || len(oc) != 0 {
		t.Errorf("outside_collaborators: got %v, want []", f["outside_collaborators"])
	}
}
