# Spec: notifications, HTML report, MCP (v0.5)

## Notifiers (`internal/notify`, one interface)

```go
type Notifier interface {
    Name() string
    Publish(ctx context.Context, r Report) (Result, error) // idempotent per (report kind, scope key)
}
```

| Notifier | Behaviour |
|---|---|
| `slack` | webhook, summary + top N findings + link to an artifact or HTML report |
| `github-issues` | one issue per scope (unit for drift, account for scan); updates the body on re-run; closes when resolved; `max_open`, labels, summary issue (driftive-style) |
| `bitbucket-pr-comment` | one comment per report on a Bitbucket Cloud pull request, edited in place (the author's primary SCM). `bitbucket-issues` is dropped: Atlassian removed the issue tracker API on 2026-08-20 |
| `file` | writes md/json for any other system |

Configured in `.tfcensus.yaml` → `notify:`. Secrets come from env only. `--notify slack,github-issues` overrides per run. `--dry-run-notify` prints what would change.

## HTML report
`tfcensus report html --from scan.json [--from drift.json --from audit.json] -o census.html`:
a single static file (embedded CSS/JS, no external requests), with filters per account, category and type,
and a coverage trend when given multiple scan JSONs over time.

## MCP (read-only)

| Tool | Returns |
|---|---|
| `units` | discovered units (filtered) |
| `inventory` | per-unit summary |
| `whois` | owners of ARNs/IDs |
| `scan_summary` | counts + top unmanaged (cursor for more), from cache when fresh |
| `audit_findings` | backend audit results |
| `drift_summary` | from the last drift JSON (no plan runs over MCP) |

No tool mutates state or runs plans. Size caps and cursors follow [engineering ADR-0011](../../engineering/adr/0011-mcp-go-sdk.md).

## Decisions (M10)
Recorded with the owner before coding; [ADR-0012](../adr/0012-notify-and-mcp-read-saved-reports.md)
has the reasons.

- Notifiers: `slack`, `github-issues`, `bitbucket-pr-comment`, `file`. `tfcensus notify --from
  <json>...` publishes saved scan, drift and audit envelopes. Scopes: unit for drift, account for
  scan and audit (`fleet` for audit findings without an account).
- Marker: `<!-- tfcensus:v1 {"kind":"drift","scope":"<unit>","hash":"sha256:<24 hex>"} -->` on the
  first line of every issue body and comment. `--` in a scope is written as `-\u002d`, so it can't
  end the comment.
- GitHub lifecycle: list open issues with the `tfcensus` label (100 per page, `Link` header), skip
  pull requests, parse markers. Same hash and title: no write. Changed: PATCH title and body.
  Covered scope with nothing to report: comment, then PATCH `state: closed`,
  `state_reason: completed` (`close_resolved`, default true). Uncovered scopes: untouched.
  Duplicates: the oldest stays, the rest close with `state_reason: duplicate`. `max_open` default
  10 per kind; over it the scope is `skipped` and listed in the summary issue (`summary`, default
  false). New issues get `tfcensus`, `tfcensus:<kind>` and `labels`; missing labels are created.
  Writes are 1 s apart. A failed unit plan opens or updates its issue with "plan failed"; early-exit
  and excluded units count as uncovered.
- Clients: GitHub and Bitbucket go through `internal/httpx` with plain JSON, no go-github. A POST is
  never resent after a 5xx or a connection error, only after 429 or `Retry-After`.
- Bitbucket: one comment per kind per pull request (`BITBUCKET_WORKSPACE`, `BITBUCKET_REPO_SLUG`,
  `BITBUCKET_PR_ID`), bearer token, or basic auth with `email_env` for API tokens. A resolved report
  edits the comment to "Nothing to report" (op `close`).
- Slack: webhook URL from `webhook_env`; header, counts, top `notify.top` items, context with
  `link`. `when: findings` (default) skips a report with nothing open. The webhook URL never appears
  in logs or errors.
- `--dry-run-notify`: GET requests only, prints `create`, `update`, `close`, `unchanged`, `skipped`,
  `send` or `write` per scope, in tty, plain or json.
- HTML: `html/template`, CSS, JS and the template embedded with `go:embed`, a CSP meta tag with
  `default-src 'none'`, filters as JS over `data-` attributes, the coverage trend as inline SVG
  built in Go from every scan input. The generated time is the newest input's `generated_at`;
  inputs are sorted, so the same files give the same bytes. Managed findings are counted, not
  listed.
- Browser smoke test: `TestSpec_notifications_and_reports_2` runs the local Chrome
  (`--headless=new --dump-dom`, every host unresolvable) and checks the `data-ready="ok"` marker
  the page script sets. GitHub's runners have Chrome; the test fails on a Linux CI runner without
  it and is skipped elsewhere. No Go dependency, no workflow change.
- MCP reads saved files only. Paths are fixed at startup (`--index`, `--scan`, `--audit`,
  `--drift`), tools take filters. Inputs: `units {path, status}`, `inventory {unit, account, region,
  type}`, `whois {queries, types}`, `scan_summary {account, region, category, type, top}`,
  `audit_findings {rule, severity, account, include_suppressed}`, `drift_summary {unit, status}`.
  Every tool but whois pages with mcpkit's cursor, ranked double-managed and unmanaged first, high
  severity first. `tfcensus report units|inventory|scan|audit|drift` are the CLI twins.
- `scan_summary` also returns `types`, the matching findings per type, most first (at most 25).
  The dogfood run ([docs/dogfood.md](../../dogfood.md)) showed that the top list alone can't say
  which types dominate.

## Acceptance criteria
- [x] Notifier contract tests with httptest servers (create → update → close lifecycle, idempotency).
  `TestSpec_notifications_and_reports_1`, `TestGitHubMaxOpenAndSummary`, `TestGitHubClosesDuplicates`,
  `TestBitbucketLifecycle`, `TestSlack`, `TestFile` in `internal/notify`; `TestNotify*` in
  `internal/cli`.
- [ ] The HTML report renders offline (headless browser smoke test in CI), and is deterministic.
  `TestSpec_notifications_and_reports_2`: two runs give the same bytes and match
  `testdata/golden/report/census.html`, and no `src` or `href` leaves the file (passing). The
  Chrome check hasn't run yet: the development machine has no Chrome, and a local
  `chromedp/headless-shell` container hung even on a data URL. Tick this after the first CI run
  on ubuntu-latest passes it.
- [x] An in-process MCP client test confirms no tool can trigger exec or write (static allowlist test).
  `TestSpec_notifications_and_reports_3` calls every tool against the `report` goldens with no
  program on PATH and a transport that fails on any request; `TestMCP_staticNoExecNoWrite` checks
  imports and calls of the MCP code.
