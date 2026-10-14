<div align="center">

# postura

**Audit your GitHub enterprise, orgs, and repos against a security baseline you own.**

Rules are CEL expressions in YAML, and the bar can differ per enterprise, org, or repo.<br>
Read-only and deterministic, so it gates cleanly in CI.

[![Test](https://github.com/jackchuka/postura/actions/workflows/test.yml/badge.svg)](https://github.com/jackchuka/postura/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/jackchuka/postura?sort=semver)](https://github.com/jackchuka/postura/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/jackchuka/postura)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

<img src="assets/demo.gif" alt="postura listing rules, showing a per-org config, and evaluating collected facts into a report" width="800">

[Installation](#installation) · [Quick Start](#quick-start) · [Commands](#commands) · [Ruleset](#the-ruleset) · [Per-target configuration](#per-target-configuration) · [Facts](#facts-available-to-rules)

</div>

---

Tools like [legitify](https://github.com/Legit-Labs/legitify) apply one _global_ policy: you can turn a check off everywhere, but you can't hold different targets to different bars. [OSSF Scorecard](https://github.com/ossf/scorecard) accepts per-repo config, but each repo owns its own `.github/scorecard.yml`, and it focuses on OSS supply-chain health rather than org and enterprise settings such as 2FA enforcement, outside collaborators, or org-role grants.

**postura** keeps one centrally owned ruleset with **layered per-target overrides**: `acme` may allow 8 owners while `acme-labs` allows 5, a legacy mirror can be exempted from a rule, and the enterprise, its orgs, and their repos are all audited from the same policy. Judgment calls (owner ratio, expected outside collaborators, allowed broad org roles) become tunable thresholds and allowlists, so the result stays deterministic.

## Features

- **Read-only** — never writes to GitHub
- **Rules are data** — CEL expressions in YAML, compiled at load so a typo'd field name fails fast
- **Per-target bar** — one ruleset, with overrides per enterprise, org, and repo (most specific wins)
- **Honest about gaps** — a fact the token can't read reports `unknown`, never a false pass
- **Three scopes** — enterprise account settings, org settings, and per-repo posture
- **Offline evaluation** — `collect` facts to JSON once, then `eval` against any ruleset without network access
- **CI-native** — `--fail-on` severity gate, Markdown / JSON / SARIF output for code scanning
- **Classic protection and rulesets** — branch protection is read from both classic protection and repository rulesets, keeping the stricter outcome

## Installation

### Homebrew

```bash
brew install jackchuka/tap/postura
```

### Go

```bash
go install github.com/jackchuka/postura@latest
```

### Binary

Download a prebuilt binary (Linux / macOS / Windows, amd64 and arm64) from the [releases page](https://github.com/jackchuka/postura/releases).

### Authentication

postura reads a GitHub token from `GITHUB_TOKEN` or `GH_TOKEN`, and falls back to `gh auth token` from a logged-in [`gh`](https://cli.github.com/).

| Scope of audit              | Token requirement                          |
| --------------------------- | ------------------------------------------ |
| Repo settings               | repo read                                  |
| Org settings, owners, roles | `admin:org` read                           |
| Enterprise account settings | enterprise-owner token (`read:enterprise`) |
| Dependabot alerts           | `security_events`                          |

Facts the token can't read are reported as **unknown**. The org-wide Dependabot alert sweep also needs org-owner or security-manager standing; without it, alerts are read per repo.

## Quick Start

1. **Copy the example ruleset.** postura ships no built-in rules; [`examples/rules.yaml`](examples/rules.yaml) is a generic GitHub security baseline to start from.

   ```bash
   curl -sLO https://raw.githubusercontent.com/jackchuka/postura/main/examples/rules.yaml
   postura rules list --rules rules.yaml
   ```

2. **Audit an org.** With no repo arguments, every non-forked repo in the org is included.

   ```bash
   postura run --org acme --rules rules.yaml
   ```

3. **Tune the bar per target** with a policy config (see [`examples/config.yaml`](examples/config.yaml)), then gate CI on it:

   ```bash
   postura run --org acme --rules rules.yaml --config config.yaml --fail-on warn
   ```

An excerpt of a report (from the fabricated facts in [`assets/demo/`](assets/demo)):

```markdown
# GitHub audit

_3 fail, 1 unknown across 2 orgs + 4 repos_

### ORG-1 — Two-factor authentication is enforced org-wide (error)

_1 fail, 0 unknown of 2 checked_

| Target | Status | Notes |
|--------|--------|-------|
| acme-labs | ❌ fail |  |

**Fix:** Org Settings -> Authentication security -> Require 2FA

### REPO-3 — Secret scanning is enabled (public repos) (error)

_0 fail, 1 unknown of 2 checked_

| Target | Status | Notes |
|--------|--------|-------|
| acme-labs/sandbox | ⚠️ unknown | secret_scanning not readable (missing scope?) |

**Fix:** Repo Settings -> Code security -> enable Secret scanning
```

Rules whose targets all pass are listed as `✅ all N pass`.

## Commands

```text
postura run --rules R [owner/repo ...]    # collect + evaluate + report (the common case)
postura collect [owner/repo ...]          # write facts as JSON (cache / offline eval)
postura eval --rules R <facts.json>       # evaluate a facts file — pure, no network
postura rules list --rules R              # list every rule
postura rules explain --rules R <id>      # show a rule's CEL, config knobs, and fix hint
```

`--rules` is required by every command except `collect`, which only gathers facts.

Pass `owner/repo` arguments to audit specific repos; their owner becomes the org. A bare repo name is accepted when exactly one `--org` is given.

### Flags

Shared by `run` and `eval`:

| Flag                | Default                          | Meaning                                                                          |
| ------------------- | -------------------------------- | -------------------------------------------------------------------------------- |
| `--rules`           | — (**required**)                 | ruleset YAML                                                                     |
| `--config`          | —                                | policy-config YAML with per-target overrides                                     |
| `--format`          | `md`                             | `md` \| `json` \| `sarif`                                                        |
| `--fail-on`         | `error` (`run`), `none` (`eval`) | exit non-zero on a fail at/above this severity: `error` \| `warn` \| `info` \| `none` |
| `--fail-on-unknown` | `false`                          | also exit non-zero on an unknown at/above `--fail-on`, so an under-scoped token can't pass the gate |

Shared by `run` and `collect` (the commands that call GitHub):

| Flag           | Default        | Meaning                                                                                                     |
| -------------- | -------------- | ----------------------------------------------------------------------------------------------------------- |
| `--org`        | —              | org(s) to audit; repeatable or comma-separated, or inferred from `owner/repo` args                          |
| `--enterprise` | —              | enterprise slug to also audit (needs an enterprise-owner token)                                             |
| `--scope`      | all applicable | limit to `enterprise` \| `org` \| `repos`; repeatable or comma-separated (`--scope org,repos`)              |

`eval` reads the org and enterprise from the facts file, so it takes none of the GitHub-facing flags.

With no `--scope`, postura audits everything you named: the org and its repos when an org is given, plus the enterprise when `--enterprise` is given. Each scope needs its input, so `--scope enterprise` without `--enterprise`, or `--scope org` without an org, is an error.

An enterprise is audited only for its **account-level settings**; postura does not enumerate the orgs beneath it. Name those with `--org`.

### Exit codes

| Code | Meaning                                                   |
| ---- | --------------------------------------------------------- |
| `0`  | no fail at or above `--fail-on`                           |
| `1`  | a fail (or, with `--fail-on-unknown`, an unknown) at or above `--fail-on` |
| `2`  | usage, config, or API error                               |

### Examples

```bash
# Enterprise + org + every repo
postura run --org acme --enterprise acme-inc --rules rules.yaml

# Several orgs in one run
postura run --org acme,acme-labs --rules rules.yaml

# Enterprise only (no --org needed)
postura run --enterprise acme-inc --rules rules.yaml

# Org-level checks only, skipping the per-repo sweep
postura run --org acme --scope org --rules rules.yaml

# Specific repos (org inferred), alongside the enterprise
postura run --enterprise acme-inc acme/web acme/api --rules rules.yaml

# Collect once, evaluate offline
postura collect --org acme > facts.json
postura eval --rules rules.yaml facts.json

# CI gate: fail on warn or worse, emit SARIF for code scanning
postura run --org acme --enterprise acme-inc --scope enterprise,org \
  --fail-on warn --format sarif --rules rules.yaml > postura.sarif
```

## The ruleset

You pass the ruleset with `--rules`; postura embeds none. Each rule's `pass` is a [CEL](https://github.com/google/cel-go) expression over the scope's [fact fields](#facts-available-to-rules), bound as top-level variables, plus `cfg` (the rule's merged config):

```yaml
rules:
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
    fix_hint: "Org Settings -> People: review who holds Owner"
```

- **`scope`** — `enterprise` \| `org` \| `repo`.
- **`severity`** — `error` \| `warn` \| `info`. Drives report order, the `--fail-on` gate, and the SARIF level (`error` → error, `warn` → warning, `info` → note; unknowns are always note).
- **`applies_when`** — CEL predicate that limits a rule to matching targets (e.g. public repos only).
- **`enabled: false`** — off by default; opt in per target via config.

When a fact a rule needs can't be read, the collector omits it and the finding becomes **unknown**, with no extra annotation in the rule.

## Per-target configuration

A policy config (`--config`) overrides rule settings, merged most-specific-wins: **rule default → enterprise → org → repo**.

```yaml
enterprises:
  acme-inc:
    ENT-3: { enabled: true } # opt into an off-by-default rule
orgs:
  acme:
    ORG-2: { max_admins: 8 } # raise the bar
    ORG-3: { allowed_collaborators: [contractor-x] } # allowlist
  acme-labs:
    REPO-8: { enabled: true }
repos:
  acme/legacy-thing:
    REPO-1: { enabled: false } # exempt one repo
```

Within a rule's block, `enabled` (bool) and `severity` (string) tune the rule itself; every other key is merged into its `cfg`.

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
| `dependabot_alerts`               | open Dependabot alerts: `{total, critical, high, medium, low}`, each severity `{count, oldest_open_days}` |
| `dependabot_config`               | whether a Dependabot version-updates config exists (`.github/dependabot.yml`)                                                                  |
| `renovate_config`                 | whether a Renovate config exists (`renovate.json`, `.renovaterc*`, or under `.github/`)                                                        |
| `codeowners`                      | whether a CODEOWNERS file exists                                                                                                               |
| `codeowners_error_count`          | errors GitHub's CODEOWNERS linter reports (unknown owners, bad syntax); `0` when no file exists                                                |
| `license`                         | SPDX license id, or null                                                                                                                       |
| `teams`                           | teams with a direct grant, each `{slug, permission}`                                                                                           |
| `ruleset_count`                   | active branch rulesets on the default branch                                                                                                   |
| `protection`                      | branch-protection facts: `{required_pull_request_reviews, required_approving_review_count, require_code_owner_reviews, pr_bypass_actor_types}` |

</details>

> **Note:** `dependabot_alerts` is absent when Dependabot alerts are known to be
> turned off for the repo, and when no alert listing could be read (a token
> without `security_events`), so a rule over it reports unknown rather than a
> false "no alerts". The org sweep reports only repos that have alerts enabled,
> so a rule over this fact should still gate on `vulnerability_alerts == true`
> for the case where that flag itself is unreadable — see REPO-16 in
> [`examples/rules.yaml`](examples/rules.yaml). Ages are computed at collect time
> and frozen into the facts, so re-running `eval` on cached facts stays
> reproducible; they count from the alert's `created_at`, which GitHub never
> resets, so a reopened alert carries its full original age. A severity bucket
> holding an alert whose age GitHub does not report omits `oldest_open_days`
> entirely, since an undatable alert is of unknown age, not a fresh one.

> **Note:** `dependabot_config` / `renovate_config` detect only *committed* config.
> A repo onboarded to Renovate via a shared org preset (in the org's `.github`
> repo) or the Renovate App's default, with no repo-level file, is not detected.

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
