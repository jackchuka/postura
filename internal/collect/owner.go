package collect

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v84/github"
)

// Owner is the account that owns the audited repos: an organization, or a
// user account. Org-scope collection applies to organizations only.
type Owner struct {
	Login string
	User  bool // a user account rather than an organization
	Self  bool // the user account of the token itself
}

// ResolveOwner looks up whether login is an organization or a user account.
func ResolveOwner(ctx context.Context, c *github.Client, login string) (Owner, error) {
	u, _, err := c.Users.Get(ctx, login)
	if err != nil {
		return Owner{}, fmt.Errorf("look up owner %s: %w", login, err)
	}
	o := Owner{Login: login, User: u.GetType() != "Organization"}
	if o.User {
		if me, _, err := c.Users.Get(ctx, ""); err == nil {
			o.Self = strings.EqualFold(me.GetLogin(), login)
		}
	}
	return o, nil
}

// userRepos lists the non-forked repos a user account owns. For the token's own
// account it uses /user/repos so private repos are included; /users/{u}/repos
// lists public repos only.
func userRepos(ctx context.Context, c *github.Client, owner Owner) ([]*github.Repository, error) {
	var out []*github.Repository
	keep := func(repos []*github.Repository) {
		for _, r := range repos {
			if !r.GetFork() && strings.EqualFold(r.GetOwner().GetLogin(), owner.Login) {
				out = append(out, r)
			}
		}
	}
	if owner.Self {
		opt := &github.RepositoryListByAuthenticatedUserOptions{Affiliation: "owner", ListOptions: github.ListOptions{PerPage: 100}}
		for {
			repos, resp, err := c.Repositories.ListByAuthenticatedUser(ctx, opt)
			if err != nil {
				return nil, err
			}
			keep(repos)
			if resp.NextPage == 0 {
				return out, nil
			}
			opt.Page = resp.NextPage
		}
	}
	opt := &github.RepositoryListByUserOptions{Type: "owner", ListOptions: github.ListOptions{PerPage: 100}}
	for {
		repos, resp, err := c.Repositories.ListByUser(ctx, owner.Login, opt)
		if err != nil {
			return nil, err
		}
		keep(repos)
		if resp.NextPage == 0 {
			return out, nil
		}
		opt.Page = resp.NextPage
	}
}
