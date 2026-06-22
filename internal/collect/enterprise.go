package collect

import (
	"context"
	"net/http"

	"github.com/google/go-github/v84/github"
)

// enterpriseOwnerInfoQuery reads every enterprise-scope fact in one shot.
// enterprise(slug).ownerInfo is the only API surface that exposes these settings
// (there is no REST equivalent), and ownerInfo is readable only by enterprise
// owners. The outsideCollaborators connection is paginated via $after.
const enterpriseOwnerInfoQuery = `
query($slug: String!, $after: String) {
  enterprise(slug: $slug) {
    ownerInfo {
      defaultRepositoryPermissionSetting
      ipAllowListEnabledSetting
      allowPrivateRepositoryForkingSetting
      samlIdentityProvider { ssoUrl }
      outsideCollaborators(first: 100, after: $after) {
        nodes { login }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

type entOwnerInfo struct {
	DefaultRepositoryPermissionSetting   *string `json:"defaultRepositoryPermissionSetting"`
	IPAllowListEnabledSetting            *string `json:"ipAllowListEnabledSetting"`
	AllowPrivateRepositoryForkingSetting *string `json:"allowPrivateRepositoryForkingSetting"`
	SamlIdentityProvider                 *struct {
		SSOURL string `json:"ssoUrl"`
	} `json:"samlIdentityProvider"`
	OutsideCollaborators struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
	} `json:"outsideCollaborators"`
}

type entResponse struct {
	Data struct {
		Enterprise *struct {
			OwnerInfo *entOwnerInfo `json:"ownerInfo"`
		} `json:"enterprise"`
	} `json:"data"`
}

// Enterprise collects enterprise-account facts via a single GraphQL query. When
// the token is not an enterprise owner (or the slug is unknown) ownerInfo comes
// back null; we then leave every settings key absent so each enterprise rule
// reports unknown rather than a false pass — the same contract org/repo collection
// uses for fields a missing scope hides.
func Enterprise(ctx context.Context, c *github.Client, slug string) (map[string]any, error) {
	f := map[string]any{"enterprise": slug}

	var collaborators []any
	var after *string
	for {
		vars := map[string]any{"slug": slug, "after": after}
		var resp entResponse
		if err := graphQL(ctx, c, enterpriseOwnerInfoQuery, vars, &resp); err != nil {
			return nil, err
		}
		if resp.Data.Enterprise == nil || resp.Data.Enterprise.OwnerInfo == nil {
			return f, nil // unreadable: everything is unknown
		}
		oi := resp.Data.Enterprise.OwnerInfo

		// Settings are identical across pages; record them once on the first page.
		if after == nil {
			if oi.DefaultRepositoryPermissionSetting != nil {
				f["default_repository_permission"] = *oi.DefaultRepositoryPermissionSetting
			}
			if oi.IPAllowListEnabledSetting != nil {
				f["ip_allow_list_enabled"] = *oi.IPAllowListEnabledSetting
			}
			if oi.AllowPrivateRepositoryForkingSetting != nil {
				f["allow_private_repository_forking"] = *oi.AllowPrivateRepositoryForkingSetting
			}
			f["saml_sso_url"] = nil
			if oi.SamlIdentityProvider != nil {
				f["saml_sso_url"] = strOrNull(oi.SamlIdentityProvider.SSOURL)
			}
		}

		for _, n := range oi.OutsideCollaborators.Nodes {
			collaborators = append(collaborators, n.Login)
		}
		if !oi.OutsideCollaborators.PageInfo.HasNextPage {
			break
		}
		cursor := oi.OutsideCollaborators.PageInfo.EndCursor
		after = &cursor
	}

	f["outside_collaborators"] = emptyIfNil(collaborators)
	f["outside_collaborator_count"] = len(collaborators)
	return f, nil
}

// graphQL issues a POST against the GraphQL endpoint and decodes data/errors into
// v. GraphQL returns HTTP 200 even for query-level errors (e.g. a non-owner token
// yields a null ownerInfo plus an errors array), so callers degrade a null result
// to unknown; only transport-level failures surface as an error here.
func graphQL(ctx context.Context, c *github.Client, query string, vars map[string]any, v any) error {
	body := map[string]any{"query": query, "variables": vars}
	req, err := c.NewRequest(http.MethodPost, "graphql", body)
	if err != nil {
		return err
	}
	_, err = c.Do(ctx, req, v)
	return err
}
