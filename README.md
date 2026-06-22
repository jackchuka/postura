<div align="center">

<h1>postura</h1>

<p>
  <strong>A deterministic CLI that audits your GitHub enterprise, orgs, and repos<br>against a security baseline you own — rules are data, the bar is per-target.</strong>
</p>

<p>
  <a href="https://pkg.go.dev/github.com/jackchuka/postura"><img src="https://pkg.go.dev/badge/github.com/jackchuka/postura.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/jackchuka/postura"><img src="https://goreportcard.com/badge/github.com/jackchuka/postura" alt="Go Report Card"></a>
  <a href="https://github.com/jackchuka/postura/actions/workflows/test.yml"><img src="https://github.com/jackchuka/postura/actions/workflows/test.yml/badge.svg" alt="Test"></a>
  <a href="https://github.com/jackchuka/postura/releases"><img src="https://img.shields.io/github/v/release/jackchuka/postura?sort=semver" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

</div>

---

**postura** audits a GitHub organization's **security posture** — branch protection,
2FA enforcement, secret scanning, outside collaborators, org-role grants, and more —
against a **per-target-configurable** policy baseline. Rules are data
([CEL](https://github.com/google/cel-go) expressions in YAML); the same ruleset can hold
a different bar for each enterprise, org, or repo. It is **read-only** — it never mutates
anything on GitHub — and **deterministic**, so it gates cleanly in CI.

```console
$ postura run --org acme --enterprise acme-inc --rules examples/rules.yaml
```

```markdown
# GitHub audit

_3 fail, 1 unknown across 12 repos + org + enterprise_

### ORG-1 — Two-factor authentication is enforced org-wide (error)

_1 fail, 0 unknown of 1 checked_

| Target | Status  | Notes                                   |
| ------ | ------- | --------------------------------------- |
| acme   | ❌ fail | two_factor_requirement_enabled is false |

**Fix:** Org Settings → Authentication security: require two-factor authentication

### REPO-1 — Default branch requires a pull request before merging (error)

_2 fail, 0 unknown of 12 checked_

| Target   | Status  | Notes                                  |
| -------- | ------- | -------------------------------------- |
| acme/web | ❌ fail | required_approving_review_count is 0   |
| acme/api | ❌ fail | no branch protection on default branch |

**Fix:** Settings → Branches: require a pull request before merging

### ENT-1 — SAML single sign-on is configured for the enterprise (error)

_0 fail, 1 unknown of 1 checked_

| Target   | Status     | Notes                                             |
| -------- | ---------- | ------------------------------------------------- |
| acme-inc | ⚠️ unknown | needs an enterprise-owner token (read:enterprise) |
```

## Why postura

Tools like [legitify](https://github.com/Legit-Labs/legitify) apply a _global_ policy —
you can disable a check everywhere, but not hold a different bar for different targets.
[OSSF Scorecard](https://github.com/ossf/scorecard) does take per-repo config, but it's
_decentralized_ (a `.github/scorecard.yml` each repo owns) and scoped to OSS/supply-chain
repo health — not org/enterprise settings like 2FA enforcement, outside collaborators, or
org-role grants.

postura's difference is **centralized, layered per-target configuration**: one ruleset a
security team operates, where `acme` may allow 8 owners while `acme-labs` allows 3, a
legacy repo can be exempted from a rule, and enterprise + org + repo all audit from the
same policy — no forking, no per-repo file to trust.

Judgment calls (admin-count proportion, acceptable outside collaborators, allowed broad
org-roles) are expressed as **tunable thresholds and allowlists**, so the audit stays
fully deterministic and CI-gateable.

|                       | postura                                                        |
| --------------------- | -------------------------------------------------------------- |
| **Read-only**         | Never writes to GitHub — pure audit                            |
| **Rules are data**    | CEL expressions in YAML, compiled at load (typos fail fast)    |
| **Per-target bar**    | Same ruleset, different thresholds per enterprise / org / repo |
| **Honest about gaps** | A fact it can't read → `unknown`, never a false pass           |
| **CI-native**         | `--fail-on` gate + SARIF output for code scanning              |
| **Three scopes**      | Enterprise account settings, org settings, per-repo posture    |

## Install

```bash
go install github.com/jackchuka/postura@latest
```

Or grab a prebuilt binary from [Releases](https://github.com/jackchuka/postura/releases)
(Linux / macOS / Windows, amd64 + arm64).

### Authentication

postura needs a GitHub token via `GITHUB_TOKEN` / `GH_TOKEN`, or a logged-in
[`gh`](https://cli.github.com/) (`gh auth token` is used as a fallback).

| Scope of audit              | Token requirement                          |
| --------------------------- | ------------------------------------------ |
| Repo settings               | repo read                                  |
| Org settings, owners, roles | `admin:org` read                           |
| Enterprise account settings | enterprise-owner token (`read:enterprise`) |

Without the right scope, the relevant facts report as **unknown** rather than a false pass.

## Usage

```text
postura run --rules R [owner/repo ...]    # collect + evaluate + report (the common case)
postura collect [owner/repo ...]          # write facts as JSON (cache / offline eval)
postura eval --rules R <facts.json>       # evaluate a facts file — pure, no network
postura rules list --rules R              # list every rule
postura rules explain --rules R <id>      # show a rule's CEL, config knobs, and fix hint
```

`--rules` is required for every command except `collect`, which only gathers facts:
`collect` writes facts; `eval` and `run` apply rules to them. postura ships **no
built-in ruleset** — you always pass your own. A generic GitHub security baseline you can
copy and tune lives in [`examples/rules.yaml`](examples/rules.yaml).

With no repo arguments, every non-forked repo in the org is audited. Pass `owner/repo`
tokens to scope to specific repos. `--org` is repeatable, so several orgs can be audited
in one run.

### Flags

Shared by `run` and `eval`:

| Flag        | Default                          | Meaning                                                                          |
| ----------- | -------------------------------- | -------------------------------------------------------------------------------- |
| `--config`  | —                                | policy-config YAML with per-target overrides                                     |
| `--rules`   | — (**required**)                 | ruleset YAML (see [`examples/rules.yaml`](examples/rules.yaml))                  |
| `--format`  | `md`                             | `md` \| `json` \| `sarif`                                                        |
| `--fail-on` | `error` (`run`), `none` (`eval`) | exit non-zero on a fail at/above this severity (`error`\|`warn`\|`info`\|`none`) |

Shared by `run` and `collect` (the commands that talk to GitHub):

| Flag           | Default        | Meaning                                                                                                                 |
| -------------- | -------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `--org`        | —              | org(s) to audit — repeatable/comma-separated, or inferred from `owner/repo` args; optional for an enterprise-only audit |
| `--enterprise` | —              | enterprise slug to also audit (needs an enterprise-owner token)                                                         |
| `--scope`      | all applicable | restrict to scopes: `enterprise` \| `org` \| `repos` (repeatable, e.g. `--scope org,repos`)                             |

`eval` takes its org/enterprise from the facts file, so it has none of the GitHub-facing
flags above. SARIF output is uploadable to GitHub code scanning and usable as audit evidence.

### Examples

```bash
# Enterprise + org + every repo (default scope = all applicable)
postura run --org acme --enterprise acme-inc --rules examples/rules.yaml

# Several orgs in one run (repeatable or comma-separated --org)
postura run --org acme --org acme-labs --rules examples/rules.yaml
postura run --org acme,acme-labs --rules examples/rules.yaml

# Enterprise only — no --org needed (scope defaults to enterprise)
postura run --enterprise acme-inc --rules examples/rules.yaml

# Org-level checks only (skip the per-repo sweep)
postura run --org acme --scope org --rules examples/rules.yaml

# Specific repos (org inferred from the args), audited alongside the enterprise
postura run --enterprise acme-inc acme/web acme/api --rules examples/rules.yaml

# Collect enterprise facts to JSON, then evaluate offline
postura collect --enterprise acme-inc --scope enterprise > facts.json
postura eval --rules examples/rules.yaml facts.json

# CI gate: fail on any warn-or-worse, emit SARIF for code scanning
postura run --org acme --enterprise acme-inc --scope enterprise,org \
  --fail-on warn --format sarif --rules examples/rules.yaml > postura.sarif
```

`--scope` is repeatable and comma-separated (`--scope org,repos` == `--scope org --scope
repos`). With no `--scope`, postura audits everything nameable: org + its repos when an
org is given, plus the enterprise when `--enterprise` is given. Each scope needs its input
— so `--scope enterprise` without `--enterprise` (or `--scope org` with no org) is an error.

An enterprise is audited only for its **account-level settings**; it does not enumerate the
orgs beneath it. Audit those by naming them with `--org` — repos auto-expand from an org,
but orgs are named entities.

## The ruleset

You supply the ruleset with `--rules`; postura embeds none. Each rule's `pass` is a
[CEL](https://github.com/google/cel-go) expression over the scope's fact fields (bound as
top-level variables) plus `cfg` (the rule's merged config):

```yaml
- id: ORG-2
  scope: org
  severity: warn
  title: Owner/admin count is proportionate to org size
  config:
    max_admins: 5 # overridable per enterprise/org/repo
    max_admin_ratio: 0.25
  pass: >-
    admin_count <= cfg.max_admins &&
    (member_count == 0 || double(admin_count) / double(member_count) <= cfg.max_admin_ratio)
  fix_hint: "Org Settings → People: review who holds Owner"
```

- **`severity`** — `error` \| `warn` \| `info`. Drives report ordering, the `--fail-on`
  gate, and the SARIF level (`error` → error, `warn` → warning, `info` → note).
- **`applies_when`** — scope a rule to matching targets (e.g. public repos only).
- **`enabled: false`** — off by default; opt in per target via config.

The whole ruleset is CEL-compiled at load, so a typo'd field name (an undeclared variable)
fails fast there. When a fact a rule needs can't be read (e.g. missing token scope), the
collector omits it and postura reports **unknown** (⚠️) for that target automatically — no
rule annotation needed.

## Per-target configuration

A policy-config file (`--config`) overrides rule settings, merged most-specific-wins:
**rule default → enterprise → org → repo**.

```yaml
# config.yaml
orgs:
  acme:
    ORG-2: { max_admins: 8 } # raise the bar
    ORG-3: { allowed_collaborators: [contractor-x] } # allowlist → deterministic
  acme-labs:
    REPO-8: { enabled: true } # opt into an off-by-default rule
repos:
  acme/legacy-thing:
    REPO-1: { enabled: false } # exempt one repo
```

Within a rule's block, `enabled` (bool) and `severity` (string) tune the rule; every other
key is merged into its `cfg`.

## Facts available to rules

Rules read these fields (bound as top-level CEL variables), grouped by scope. The source of
truth is [`internal/collect/schema.go`](internal/collect/schema.go); a test fails if this
list drifts from it. A field that couldn't be read is omitted (→ the rule reports unknown).

<details>
<summary><strong>Enterprise scope</strong></summary>

| Field                              | Meaning                                                                     |
| ---------------------------------- | --------------------------------------------------------------------------- |
| `enterprise`                       | enterprise slug (the audited target)                                        |
| `saml_sso_url`                     | configured SAML SSO URL, or null                                            |
| `default_repository_permission`    | enterprise base repo permission (`NONE`/`READ`/`WRITE`/`ADMIN`/`NO_POLICY`) |
| `ip_allow_list_enabled`            | IP allow list setting (`ENABLED`/`DISABLED`)                                |
| `allow_private_repository_forking` | private/internal forking policy (`ENABLED`/`DISABLED`/`NO_POLICY`)          |
| `outside_collaborators`            | outside-collaborator logins                                                 |
| `outside_collaborator_count`       | number of outside collaborators                                             |

</details>

<details>
<summary><strong>Org scope</strong></summary>

| Field                                                          | Meaning                                                     |
| -------------------------------------------------------------- | ----------------------------------------------------------- |
| `org`                                                          | org login (the audited target)                              |
| `two_factor_requirement_enabled`                               | whether 2FA is enforced org-wide                            |
| `default_repository_permission`                                | base member repo permission (`none`/`read`/`write`/`admin`) |
| `members_can_create_repositories`                              | whether members may create repos                            |
| `members_can_fork_private_repositories`                        | whether members may fork private/internal repos             |
| `secret_scanning_enabled_for_new_repositories`                 | new-repo default: secret scanning                           |
| `secret_scanning_push_protection_enabled_for_new_repositories` | new-repo default: push protection                           |
| `dependabot_alerts_enabled_for_new_repositories`               | new-repo default: Dependabot alerts                         |
| `admins`                                                       | org owner logins                                            |
| `admin_count`                                                  | number of org owners                                        |
| `members`                                                      | org member logins                                           |
| `member_count`                                                 | number of members                                           |
| `outside_collaborators`                                        | outside-collaborator logins                                 |
| `outside_collaborator_count`                                   | number of outside collaborators                             |
| `teams`                                                        | org teams, each `{slug, privacy}`                           |
| `organization_roles`                                           | org-role assignments, each `{role, teams, users}`           |
| `invitations`                                                  | pending invitations, each `{login, age_days}`               |

</details>

<details>
<summary><strong>Repo scope</strong></summary>

| Field                             | Meaning                                                                                                                                        |
| --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`                            | `owner/repo`                                                                                                                                   |
| `owner`                           | owning org                                                                                                                                     |
| `archived`                        | whether the repo is archived                                                                                                                   |
| `visibility`                      | `public` / `private` / `internal`                                                                                                              |
| `secret_scanning`                 | secret-scanning status (`enabled`/`disabled`)                                                                                                  |
| `secret_scanning_push_protection` | push-protection status (`enabled`/`disabled`)                                                                                                  |
| `vulnerability_alerts`            | whether Dependabot alerts are enabled                                                                                                          |
| `codeowners`                      | whether a CODEOWNERS file exists                                                                                                               |
| `license`                         | SPDX license id, or null                                                                                                                       |
| `teams`                           | teams with a direct grant, each `{slug, permission}`                                                                                           |
| `ruleset_count`                   | active branch rulesets on the default branch                                                                                                   |
| `protection`                      | branch-protection facts: `{required_pull_request_reviews, required_approving_review_count, require_code_owner_reviews, pr_bypass_actor_types}` |

</details>

## How it works

```text
            ┌───────────┐      ┌──────────────┐      ┌───────────────┐
 GitHub ──► │  collect  │ ───► │     eval     │ ───► │    report     │
 REST +     │  (facts)  │ json │ (CEL × cfg)  │      │ md/json/sarif │
 GraphQL    └───────────┘      └──────────────┘      └───────────────┘
                read-only        deterministic          + exit code
```

`run` chains all three. Splitting `collect` (the only networked step) from `eval` lets you
cache facts and re-evaluate offline against different rulesets. Branch protection is read
from **both** classic protection and repository rulesets, folding the stricter outcome.

## License

MIT — see [LICENSE](LICENSE).
