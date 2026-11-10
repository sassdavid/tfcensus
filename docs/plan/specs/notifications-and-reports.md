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
| `bitbucket-issues` / `bitbucket-pr-comment` | the same lifecycle via Bitbucket Cloud API (the author's primary SCM) |
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

## Acceptance criteria
- [ ] Notifier contract tests with httptest servers (create → update → close lifecycle, idempotency).
- [ ] The HTML report renders offline (headless browser smoke test in CI), and is deterministic.
- [ ] An in-process MCP client test confirms no tool can trigger exec or write (static allowlist test).
