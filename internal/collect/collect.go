package collect

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/go-github/v84/github"
	"golang.org/x/oauth2"
)

// NewClient builds a GitHub REST client authenticated with a token. Requests are
// wrapped in retry-on-rate-limit so a large audit rides out a transient throttle
// instead of failing. The retry sits outside the oauth transport so the auth
// header is re-applied on every attempt.
func NewClient(ctx context.Context, token string) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	hc := oauth2.NewClient(ctx, ts)
	hc.Transport = newRetryTransport(hc.Transport)
	return github.NewClient(hc)
}

// setBool sets key only when the value was readable (non-nil pointer). A nil
// pointer means the API omitted the field — usually a missing scope — so the
// key is left absent, which the engine surfaces as unknown (not a false
// `false`) for any rule that reads it.
func setBool(f map[string]any, key string, p *bool) {
	if p != nil {
		f[key] = *p
	}
}

func strOrNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Org collects org-level access/membership facts as one normalized object.
func Org(ctx context.Context, c *github.Client, org string) (map[string]any, error) {
	f := map[string]any{"org": org}

	o, _, err := c.Organizations.Get(ctx, org)
	if err != nil {
		return nil, err
	}
	// Member-privilege / code-security defaults need admin:org; when absent the
	// API omits them and we leave the key out so the rule reports unknown.
	setBool(f, "two_factor_requirement_enabled", o.TwoFactorRequirementEnabled)
	f["default_repository_permission"] = strOrNull(o.GetDefaultRepoPermission())
	setBool(f, "members_can_create_repositories", o.MembersCanCreateRepos)
	setBool(f, "members_can_fork_private_repositories", o.MembersCanForkPrivateRepos)
	setBool(f, "secret_scanning_enabled_for_new_repositories", o.SecretScanningEnabledForNewRepos)
	setBool(f, "secret_scanning_push_protection_enabled_for_new_repositories", o.SecretScanningPushProtectionEnabledForNewRepos)
	setBool(f, "dependabot_alerts_enabled_for_new_repositories", o.DependabotAlertsEnabledForNewRepos)

	admins, err := orgMemberLogins(ctx, c, org, "admin")
	if err != nil {
		return nil, err
	}
	members, err := orgMemberLogins(ctx, c, org, "all")
	if err != nil {
		return nil, err
	}
	oc, err := outsideCollaborators(ctx, c, org)
	if err != nil {
		return nil, err
	}
	f["admins"] = admins
	f["admin_count"] = len(admins)
	f["members"] = members
	f["member_count"] = len(members)
	f["outside_collaborators"] = oc
	f["outside_collaborator_count"] = len(oc)

	teams, err := orgTeams(ctx, c, org)
	if err != nil {
		return nil, err
	}
	f["teams"] = teams

	f["invitations"], err = pendingInvitations(ctx, c, org)
	if err != nil {
		return nil, err
	}

	if roles := organizationRoles(ctx, c, org); roles != nil {
		f["organization_roles"] = roles
	}

	return f, nil
}

func orgMemberLogins(ctx context.Context, c *github.Client, org, role string) ([]any, error) {
	opt := &github.ListMembersOptions{Role: role, ListOptions: github.ListOptions{PerPage: 100}}
	var out []any
	for {
		users, resp, err := c.Organizations.ListMembers(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			out = append(out, u.GetLogin())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return emptyIfNil(out), nil
}

func outsideCollaborators(ctx context.Context, c *github.Client, org string) ([]any, error) {
	opt := &github.ListOutsideCollaboratorsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var out []any
	for {
		users, resp, err := c.Organizations.ListOutsideCollaborators(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			out = append(out, u.GetLogin())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return emptyIfNil(out), nil
}

func orgTeams(ctx context.Context, c *github.Client, org string) ([]any, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []any
	for {
		teams, resp, err := c.Teams.ListTeams(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		for _, t := range teams {
			out = append(out, map[string]any{"slug": t.GetSlug(), "privacy": t.GetPrivacy()})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return emptyIfNil(out), nil
}

func pendingInvitations(ctx context.Context, c *github.Client, org string) ([]any, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []any
	for {
		invs, resp, err := c.Organizations.ListPendingOrgInvitations(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		for _, iv := range invs {
			login := iv.GetLogin()
			if login == "" {
				login = iv.GetEmail()
			}
			row := map[string]any{"login": login, "age_days": nil}
			if t := iv.GetCreatedAt(); !t.IsZero() {
				row["age_days"] = int(time.Since(t.Time).Hours() / 24)
			}
			out = append(out, row)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return emptyIfNil(out), nil
}

// organizationRoles lists each org role with its team and user assignments,
// every holder tagged direct or indirect. Returns nil when the token cannot
// read org roles (needs admin:org), leaving the fact absent rather than empty.
func organizationRoles(ctx context.Context, c *github.Client, org string) any {
	var body struct {
		Roles []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"roles"`
	}
	if _, err := getJSON(ctx, c, "orgs/"+org+"/organization-roles?per_page=100", &body); err != nil {
		return nil
	}
	out := []any{}
	for _, r := range body.Roles {
		teams := roleAssignees(ctx, c, org, r.ID, "teams", "slug")
		users := roleAssignees(ctx, c, org, r.ID, "users", "login")
		if len(teams) == 0 && len(users) == 0 {
			continue
		}
		out = append(out, map[string]any{"role": r.Name, "teams": teams, "users": users})
	}
	return out
}

func roleAssignees(ctx context.Context, c *github.Client, org string, roleID int64, kind, field string) []any {
	base := "orgs/" + org + "/organization-roles/" + itoa(roleID) + "/" + kind
	out := []any{}
	page := 1
	for {
		var raw []map[string]any
		// Paginate to the end: a broadly-granted role can span more assignees than
		// one page, and a truncated list would understate the grant.
		resp, err := getJSON(ctx, c, base+"?per_page=100&page="+itoa(int64(page)), &raw)
		if err != nil {
			return out
		}
		for _, m := range raw {
			row := map[string]any{}
			if v, ok := m[field].(string); ok {
				row[field] = v
			}
			// GitHub tags each holder "direct" (granted on the user/team itself)
			// or "indirect" (inherited through a parent team); carry it through so
			// the fact distinguishes the two. Default to "direct" when unreported
			// so the field is always present.
			if a, ok := m["assignment"].(string); ok {
				row["assignment"] = a
			} else {
				row["assignment"] = "direct"
			}
			out = append(out, row)
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return out
}

// getJSON issues a GET against the REST API and decodes into v, returning the
// response so callers can follow pagination (resp.NextPage). 4xx (e.g. 403 for
// missing scope) returns an error the caller degrades to unknown.
func getJSON(ctx context.Context, c *github.Client, path string, v any) (*github.Response, error) {
	req, err := c.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, req, v)
}

func emptyIfNil(s []any) []any {
	if s == nil {
		return []any{}
	}
	return s
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
