# ADR-0014: Report outputs, Markdown truncation and the GitHub pull request comment

- Status: accepted (2026-11-13). Amends tfcensus [plan ADR-0012](https://github.com/sassdavid/tfcensus/blob/main/docs/plan/adr/0012-notify-and-mcp-read-saved-reports.md)
  (notifiers) and relgap [output-and-ci](https://github.com/sassdavid/relgap/blob/main/docs/plan/specs/output-and-ci.md)
  (`--md-max-bytes`, `-o, --output`).

## Context

The GitHub Actions of milestone M14 run one command and need json, SARIF, Markdown and
annotations from it, then a pull request comment. Until now a workflow ran the command once per
format: four terragrunt or GitHub passes for one report. GitHub refuses a comment over 65,536
characters, and tfcensus drift capped its Markdown at 60 KB counted in bytes. The comment needs a
marker and hash lifecycle, which tfcensus's Bitbucket notifier already has. The research is in
[roadmap-research.md](../../plan/roadmap-research.md#github-action).

## Decision

- `internal/render` (shared) parses a repeatable `--out <format>=<path>`. A `render.Sink` writes
  the report to stdout in the `--format` format and once more per `--out` file, through a
  temporary file and a rename. The gate runs after all writes, so the exit code doesn't depend
  on `--out`. `tty` isn't an `--out` format. Each report command passes its format list to both
  `--format` and `--out` (`internal/cli/out.go`, shared), so help, completion and errors list
  the same formats.
- `render.TruncateMarkdown` (shared) works on the rendered text, so no Markdown writer changes.
  It splits the text into table rows, list items and sections (a `<details>` element, or a
  heading with the text under it), keeps the longest prefix of whole blocks that fits with the
  footer, and counts characters as Unicode code points. The footer names how many sections,
  table rows and list items were cut, and the first cut section. `--md-max-chars` (default
  60,000, 0 for no limit) has the same name in both CLIs. A property test checks the limit.
- `internal/ghcomment` (shared) keeps one comment per report kind on a pull request, marked
  `<!-- <tool>:v1 {"kind","scope":"pull-request","hash"} -->` (`tfcensus:v1` or `relgap:v1`), the
  marker of the tfcensus notifiers. The hash covers the heading and the Markdown, not the
  footer, so an upgrade doesn't rewrite comments. An unchanged hash means no write. A report with nothing open edits an
  existing comment to "Nothing to report" and creates none. The repository comes from
  `GITHUB_REPOSITORY`, the pull request from the `GITHUB_EVENT_PATH` event (`pull_request`,
  `pull_request_target`, or `issue_comment` on a pull request).
- ghcomment sends plain JSON through an `internal/httpx` client, as the tfcensus notifiers do.
  go-github isn't used for it, in relgap either. httpx never resends a POST after a server
  error, so a retry can't post twice.
- tfcensus exposes it as the `github-pr-comment` notifier (`notify.github_pr_comment`). relgap
  gets `relgap notify --from <json> --notify github-pr-comment`, which reads saved digest,
  outdated and drift envelopes.
- relgap gets `--format github` for `outdated` and `drift`, and tfcensus for `scan` and
  `audit backend`, from `sarif.WriteGitHub` (shared), which turns SARIF results into workflow
  commands.
- Cassettes for create, unchanged, update, resolve and skip are generated from the REST API's
  response shapes, not recorded against github.com, because recording needs a pull request to
  write to.

## Consequences

- `--out` replaces the `-o, --output` flag relgap's `cli.md` planned.
- A drift report cut by the old byte cap and by the new character cap can differ for multi-byte
  text. The footer wording changed.
- A comment whose marker a person edits by hand stops matching, and the next run posts a new one.
- `--md-max-chars` applies to `--format md`, to `--out md=` and, in relgap, to the comment;
  tfcensus notify bodies keep their own 60,000-character cap.
