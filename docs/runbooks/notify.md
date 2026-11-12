# Publish tfcensus reports with notify

This runbook sets up `tfcensus notify` in CI, so that drift, unmanaged resources and backend
audit findings reach Slack, GitHub issues, a Bitbucket pull request or a file. It also covers
what happens on re-runs and how to recover from mistakes.

## How it works

`notify` reads the JSON that `scan`, `drift` and `audit backend` print with `--format json`. It
never collects anything itself, so you run the collecting command first and pass its output with
`--from`.

Each report is split into scopes: one per unit for drift, one per account for scan and backend
audit. Audit findings that name no account go to the `fleet` scope. Every scope the run covered is
listed, including the ones with nothing to report, and those count as resolved.

| Notifier | Writes | Re-run with no change |
|---|---|---|
| `github-issues` | one issue per scope | no request except the list |
| `bitbucket-pr-comment` | one comment per report on one pull request | no request except the list |
| `slack` | one message per report to an incoming webhook | sends again, see below |
| `file` | `tfcensus-<kind>.md` and `tfcensus-<kind>.json` | leaves the files alone |

Issue bodies and comments start with a hidden HTML comment:

```text
<!-- tfcensus:v1 {"kind":"drift","scope":"acme-live/prod/vpc","hash":"sha256:..."} -->
```

`kind` and `scope` let the next run find the issue again, and `hash` covers the rest of the body,
so an unchanged scope gets no write. tfcensus overwrites manual edits to the body on the next
change.

## Secrets

Secrets come from environment variables only. The config names the variables, never the values:

| Notifier | Variable (default) | Config key |
|---|---|---|
| `slack` | `SLACK_WEBHOOK_URL` | `notify.slack.webhook_env` |
| `github-issues` | `GITHUB_TOKEN` | `notify.github_issues.token_env` |
| `bitbucket-pr-comment` | `BITBUCKET_TOKEN`, and the email variable for basic auth | `notify.bitbucket_pr_comment.token_env`, `.email_env` |

tfcensus doesn't log them. With `-vvv` the request headers are logged with `Authorization`
redacted, and the Slack webhook path, which is the secret, shows as `[redacted]`.

## Configure

Add a `notify:` section to `.tfcensus.yaml`. Every key is optional; these are the defaults with
`enabled` filled in:

```yaml
notify:
  enabled: [github-issues, slack]
  categories: [double-managed, unmanaged]   # scan categories to report
  min_severity: low                         # lowest audit severity to report
  top: 10                                   # findings listed in a Slack message
  slack:
    webhook_env: SLACK_WEBHOOK_URL
    when: findings          # or always
    link: ""                # URL of the full report, such as a CI artifact
  github_issues:
    repo: ""                # owner/name; empty means GITHUB_REPOSITORY
    api_url: ""             # empty means GITHUB_API_URL, else https://api.github.com
    token_env: GITHUB_TOKEN
    labels: []              # added to new issues, besides tfcensus and tfcensus:<kind>
    max_open: 10
    close_resolved: true
    summary: false
  bitbucket_pr_comment:
    workspace: ""           # empty means BITBUCKET_WORKSPACE
    repo: ""                # empty means BITBUCKET_REPO_SLUG
    pull_request: 0         # 0 means BITBUCKET_PR_ID
    api_url: https://api.bitbucket.org/2.0
    token_env: BITBUCKET_TOKEN
    email_env: ""           # set to send an API token with basic auth
  file:
    dir: .
    formats: [md, json]
```

`--notify slack,github-issues` replaces `enabled` for one run. `tfcensus config show --sources`
shows the merged values.

## Try it without writing

```sh
tfcensus drift --format json > drift.json
tfcensus notify --from drift.json --notify github-issues --dry-run-notify
```

`--dry-run-notify` lists the open issues and comments with GET requests and prints what would
happen to each scope: `create`, `update`, `close`, `unchanged`, or `skipped` when `max_open` is
reached. Slack and file print what they would send or write. Nothing is written. The dry run
still needs a token that can read issues.

## GitHub Actions

The token needs `issues: write`. The workflow's `GITHUB_TOKEN` works for the same repository:

```yaml
permissions:
  contents: read
  issues: write
steps:
  # drift exits 1 when a plan failed; the JSON still lists every unit.
  - run: tfcensus drift --format json > drift.json || true
  - run: tfcensus notify --from drift.json --notify github-issues
    env:
      GITHUB_TOKEN: ${{ github.token }}
```

`GITHUB_REPOSITORY` and `GITHUB_API_URL` come from the runner, so no repository setting is needed.
[docs/ci/drift.yml](../ci/drift.yml) is a full drift workflow.

## GitHub issue lifecycle

On each run, `github-issues` lists the open issues with the `tfcensus` label, 100 per page, and
reads their markers. It ignores pull requests and issues without a marker. Then, for every scope
in the report:

1. A scope with findings and no issue gets a new issue, unless `max_open` open issues of that
   report kind already exist. Then the scope is listed as `skipped` in the run output, and in the
   summary issue when `summary: true`.
2. A scope with findings and an issue gets its title and body updated when the hash changed.
3. A scope without findings and with an issue gets the comment "Resolved: ..." and is closed with
   `state_reason: completed`, when `close_resolved` is on.
4. A second open issue with the same marker, which a failed create can leave behind, is closed as
   a duplicate of the oldest one.

Issues for scopes the run didn't cover stay as they are. A drift run filtered to production
doesn't close staging issues, and a unit whose plan was skipped (early exit or excluded) keeps
its issue. A unit whose plan failed gets an issue that says so, because its drift is unknown.

tfcensus creates the labels it needs the first time it opens an issue. Writes are spaced one
second apart, as GitHub's REST API guidance asks, and a 429 or a secondary rate limit is retried
after `Retry-After`. A create that fails with a server error is not retried, so it can't open
two issues.

## Bitbucket pull request comment

Bitbucket Cloud removed its issue tracker API on 2026-08-20, so `bitbucket-pr-comment` writes one
comment per report kind on a pull request instead. In Bitbucket Pipelines the workspace, repository
and pull request ID come from `BITBUCKET_WORKSPACE`, `BITBUCKET_REPO_SLUG` and `BITBUCKET_PR_ID`.

Use a repository access token with pull request write access as `BITBUCKET_TOKEN`; tfcensus sends
it as a bearer token. For a personal API token, also set `email_env` to a variable that holds the
Atlassian account email, and tfcensus uses basic auth.

The comment is edited in place on later runs. When nothing is left to report, it is edited to say
so; a comment can't be closed.

## Slack

An incoming webhook can't edit or delete a message, so Slack has no lifecycle. With `when:
findings` (the default) a message goes out only when some scope has findings; with `always` every
run sends one. The message has a header, counts, the first `top` findings and a link to
`notify.slack.link` when set. Point the link at the HTML report from `tfcensus report html`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | every notifier succeeded |
| 1 | a notifier failed, such as a network error or an HTTP error from the API |
| 2 | a notifier isn't configured (no repository, pull request or token), or a `--from` file isn't a scan, drift or audit report |
| 4 | every failure was a refused token (HTTP 401 or 403) |

Notifiers run one after the other; a failure in one doesn't stop the next.

## Recover

- An issue was closed by mistake: reopen it. The next run that finds the scope open updates it;
  one that finds it resolved closes it again.
- Labels were removed from an issue: tfcensus no longer sees it and opens a new one. Close the
  old one by hand.
- Too many issues at once: lower `max_open`, or set `summary: true` to get one overview issue,
  and close the extra issues by hand. tfcensus doesn't close issues just because of `max_open`.
- Stop all notifications: remove the notifier from `enabled`, or drop the `notify` step. Existing
  issues stay open until you close them.
