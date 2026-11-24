---
description: The output formats of each tfcensus command, the --out flag, and the JSON envelope that every --format json output shares.
---

# Output formats and the JSON envelope

tfcensus writes data to stdout and logs to stderr, so `tfcensus scan --format json > scan.json`
captures the report and nothing else. `--format` picks the format of stdout. Without it, tfcensus
prints `tty` when stdout is a terminal or gets color anyway, and `plain` otherwise.

| Format | Output |
|---|---|
| `tty` | a table with color, notes wrapped at the terminal width |
| `plain` | the same table without color or wrapping |
| `md` | Markdown, cut at a table row or section once it reaches `--md-max-chars` (60,000 characters by default, under GitHub's comment limit) |
| `json` | the JSON envelope below, for scripts |
| `csv` | comma-separated values, one row per finding or unit |
| `sarif` | SARIF 2.1.0, for GitHub code scanning and other SARIF viewers |
| `github` | GitHub Actions workflow commands, which show as annotations |

A command rejects a format it doesn't have with exit 2.

## Color

tfcensus decides color once for stdout and once for stderr (the logs). The first rule that
applies wins:

1. `--color always` or `--color never`.
2. `NO_COLOR` set to anything but an empty string turns color off.
3. `CLICOLOR_FORCE` or `FORCE_COLOR` set to anything but an empty string, `0` or `false` turns it
   on, also when the output is a pipe or a file.
4. `CLICOLOR=0` or `TERM=dumb` turns it off.
5. Otherwise the stream gets color when it is a terminal.

The tty format uses the 16 basic terminal colors, so it follows your terminal theme, and every
colored cell also says what it means in text: unmanaged counts are red, double-managed bold red,
undetermined yellow, drifted units yellow and failed ones red, high audit findings red. `plain`,
`md`, `json`, `csv` and the `--out` files never contain color codes, and neither does a
`--log-file`. `COLUMNS` sets the wrap width of notes and warnings; tables are never cut.

CI logs are not terminals, so they stay plain, and so do the logs of tfcensus-action. GitHub
Actions shows colors in step logs: add `--color always` or set `CLICOLOR_FORCE: "1"` on a step
whose tty output you read in the log.

AWS resource names, tag values, ARNs, drift values and error messages from AWS, terraform or tofu
can contain control characters. The tty and plain formats show them in caret notation (`^[` for
escape), so text from outside can't move the cursor, change the window title or write the
clipboard.

When stdout is a pipe that closes early, as in `tfcensus scan | head -3`, tfcensus stops writing
stdout, still writes the `--out` files and exits with the code it would have used.

## Formats per command

The report commands also take a repeatable `--out format=path`, so one run writes several formats,
such as `--out json=scan.json --out sarif=scan.sarif`, and still prints to stdout. `--out` takes
every format of the command except `tty`.

<!--@include: ../parts/output-formats.md-->

## The JSON envelope

Every `--format json` output, every `--out json=` file and every MCP tool result is one JSON object
with the same fields around the command's data:

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | string | The envelope version, `"1"`. A change that breaks readers increases it. |
| `tool` | string | `"tfcensus"`. |
| `version` | string | The tfcensus version that wrote the output. |
| `generated_at` | string | When the output was written, in RFC 3339 UTC to the second. |
| `data` | object | The command's result. Its shape is in the command's schema. |
| `errors` | array | Problems with one target that didn't stop the run, each with `target` (such as a unit or account) and `message`. Empty when there were none. |
| `page` | object | Only in MCP results that are split into pages: `items`, `returned`, `total` and `next_cursor`. |

[`report-v1.json`](/schemas/report-v1.json) is the envelope's schema, and each command with its own
schema in the table above extends it. The [JSON schemas](/reference/schemas) page lists them all.

A change that would break a reader, such as renaming or removing a field, increases
`schema_version`. New fields can appear without it, so read the output with a parser that ignores
unknown fields. tfcensus is before 1.0, and the [changelog](/changelog) lists each change.

The data holds identifiers, never state attribute values ([safety model](/concepts/safety)).

## Saved reports

The JSON output of `scan`, `drift` and `audit backend` is also the input of `report`, `notify`,
`report html` and `mcp`, and `inventory --save-index` writes the index that `whois --index` and
`report units` read. None of these commands calls AWS.
