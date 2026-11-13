# tfcensus ADR-0012: Notifiers, the HTML report and MCP read saved reports

- Status: accepted (2026-11-12). Amends the [notifications spec](../specs/notifications-and-reports.md)
  and [engineering ADR-0011](../../engineering/adr/0011-mcp-go-sdk.md). Amended by
  [engineering ADR-0014](../../engineering/adr/0014-report-outputs-and-pr-comment.md), which adds
  the `github-pr-comment` notifier on the shared `internal/ghcomment`.

## Context
- Discovery always runs `terragrunt find`, and the scan cache key hashes the discovered units. An
  MCP tool that discovers units, reads states or checks the cache key would start a process and
  call AWS. The owner wants MCP tools that never exec, write or call AWS.
- Atlassian removed the Bitbucket Cloud issue tracker API on 2026-08-20 (Bitbucket Cloud changelog,
  "Removal of native Bitbucket Cloud Issue tracker"). The spec's `bitbucket-issues` notifier has
  no API left. Pull request comments remain.
- driftive (Apache-2.0) finds its GitHub issues by exact title and leaves `close_resolved` off by
  default. A renamed unit or an edited title then opens a second issue.

## Decision
- `notify`, `report html` and `mcp` read the JSON envelopes that `scan`, `drift`, `audit backend`
  and `inventory --save-index` write, through `internal/report`. They never collect anything.
  Reading a drift envelope drops `before` and `after`, so no output built from it can show an
  attribute value.
- `tfcensus report units|inventory|scan|audit|drift` print, with `--format json`, exactly what the
  MCP tools return. `whois` over MCP equals `whois --index` without accounts or `--live`. The MCP
  golden test compares each tool with these goldens.
- MCP file paths are fixed at startup (`--index`, `--scan`, `--audit`, `--drift`). Tools take
  filters, never paths. `scan_summary` without `--scan` reads the newest scan cache snapshot and
  applies the config's rules; a snapshot older than `cache.ttl` comes with a warning and is never
  collected again.
- Notifiers ship as `slack`, `github-issues`, `bitbucket-pr-comment` and `file`. `bitbucket-issues`
  is dropped.
- Issues and comments carry `<!-- tfcensus:v1 {"kind","scope","hash"} -->`. `github-issues` finds
  them by listing open issues with the `tfcensus` label and parsing markers, never by title. An
  unchanged hash means no write. A scope the run covered with nothing to report is closed with
  `state_reason: completed` (`close_resolved`, default on). Scopes the run didn't cover are left
  alone. `max_open` (default 10) caps new issues per kind; the summary issue is off by default.
- GitHub and Bitbucket go through `internal/httpx` with plain JSON requests. go-github isn't a
  dependency.

## Consequences
- MCP answers are only as fresh as the files. The server instructions and every `scan_summary`
  result say where the data came from (`source.file` or `source.cache`, `created`, `stale`).
- `report` adds five read-only subcommands, so humans and scripts get the MCP answers too.
- A user who removes the `tfcensus` label from an issue gets a new issue on the next run.
- Slack has no lifecycle: a webhook can't edit or delete messages.
