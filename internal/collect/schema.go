// Package collect gathers facts from the GitHub API into fully-normalized JSON
// objects. It is deterministic and rule-unaware: it only fetches facts. Every
// documented field is always present (null/false/0/[] when unknown) so CEL
// rules need no defensive guards — except fields the token could not read,
// which stay absent so a rule reading them reports unknown, not a false pass.
package collect

// Canonical fact-field names per scope. The engine declares exactly these as
// CEL variables, so a rule referencing an unknown field fails to compile (the
// lint), and collect guarantees each is populated on every object.
var (
	EnterpriseVars = []string{
		"enterprise",
		"saml_sso_url",
		"default_repository_permission",
		"ip_allow_list_enabled",
		"allow_private_repository_forking",
		"outside_collaborator_count",
		"outside_collaborators",
	}

	OrgVars = []string{
		"org",
		"two_factor_requirement_enabled",
		"default_repository_permission",
		"members_can_create_repositories",
		"members_can_fork_private_repositories",
		"secret_scanning_enabled_for_new_repositories",
		"secret_scanning_push_protection_enabled_for_new_repositories",
		"dependabot_alerts_enabled_for_new_repositories",
		"admins", "admin_count",
		"members", "member_count",
		"outside_collaborators", "outside_collaborator_count",
		"teams",
		"organization_roles",
		"invitations",
	}

	RepoVars = []string{
		"name", "owner", "archived", "visibility",
		"secret_scanning", "secret_scanning_push_protection",
		"vulnerability_alerts", "codeowners", "license",
		"teams", "ruleset_count", "protection",
	}
)
