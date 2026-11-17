---
description: Turn saved scan, drift and audit JSON into one self-contained HTML file with tfcensus report html, with a coverage trend from several scans.
---

# Build the HTML report

`tfcensus report html` turns the JSON that `scan`, `drift` and `audit backend` print with
`--format json` into one HTML file. The CSS and JavaScript are inside the file, and its
Content-Security-Policy blocks every network request, so it opens offline and can be kept as a CI
artifact or attached to a ticket.

## Build it

Save each report as JSON, then pass the files with `--from`:

```sh
tfcensus scan --format json > scan.json
tfcensus drift --format json > drift.json
tfcensus audit backend --format json > audit.json
tfcensus report html --from scan.json --from drift.json --from audit.json -o census.html
```

`report html` reads files only: it calls no AWS API, starts no terragrunt and reads no state. The
input files may be compressed (`.gz` or `.zst`). Without `-o`, the HTML goes to stdout.

You can also write the JSON in the same run as another format, with `--out`:

```sh
tfcensus scan --format md --out json=scan.json > scan.md
```

## What it shows

- The scan section has per-account counts and a findings table that you can filter by account,
  category and type. It comes from the newest scan file.
- The drift section has counts, each unit's status and every drifted resource with the
  attribute paths that changed, never values, even when the drift JSON was written with
  `--show-attributes`.
- The backend audit section has counts per severity and each finding with its check, target,
  reason and remediation.

When you pass several files of one kind, the one generated last fills its section.

## Show a coverage trend

Pass two or more scan files from different days, and every scan becomes a point of a coverage
trend:

```sh
tfcensus report html --from scan-2026-11-01.json --from scan-2026-11-08.json --from scan.json -o census.html
```

Keep the scan JSON of each scheduled run, for example as a CI artifact, to build the trend later.

## Compare and commit

The same input files always give the same bytes, so you can diff two reports or commit the file.
A link to the report fits in a Slack message: set `notify.slack.link` to the artifact URL, as
[Publish reports with notify](/guides/notify#slack) describes.

The other `report` subcommands print the same summaries in the terminal. `tfcensus report scan
--from scan.json --category unmanaged` lists the unmanaged resources of a saved scan, for example.
The [report CLI page](/cli/report/) lists them.
