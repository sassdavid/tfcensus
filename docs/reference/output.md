---
description: The output formats of each tfcensus command, the --out flag, and the JSON envelope that every --format json output shares.
---

# Output formats and the JSON envelope

tfcensus writes data to stdout and logs to stderr, so `tfcensus scan --format json > scan.json`
captures the report and nothing else. `--format` picks the format of stdout. Without it, tfcensus
prints `tty` when stdout is a terminal and `plain` otherwise.

| Format | Output |
|---|---|
| `tty` | a table with color, for a terminal |
| `plain` | the same table without color |
| `md` | Markdown, cut at a table row or section once it reaches `--md-max-chars` (60,000 characters by default, under GitHub's comment limit) |
| `json` | the JSON envelope below, for scripts |
| `csv` | comma-separated values, one row per finding or unit |
| `sarif` | SARIF 2.1.0, for GitHub code scanning and other SARIF viewers |
| `github` | GitHub Actions workflow commands, which show as annotations |

A command rejects a format it doesn't have with exit 2.

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
