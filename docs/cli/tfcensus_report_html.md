## tfcensus report html

Write a static HTML report from scan, drift and audit JSON

### Synopsis

html writes one HTML file from the JSON that scan, drift and audit print with --format json.
The CSS and JavaScript are inside the file and a Content-Security-Policy blocks every network
request, so it opens offline and can be attached to a CI run.

The newest scan fills the scan section, with filters per account, category and type. With
two or more scan files, every scan becomes a point of a coverage trend. The newest drift and
audit files fill their sections. Drift shows attribute paths only, never values, even when
the drift JSON was written with --show-attributes.

The same input files always give the same bytes.

```
tfcensus report html [flags]
```

### Examples

```
  tfcensus report html --from scan.json -o census.html
  tfcensus report html --from scan-2026-11-01.json --from scan.json --from drift.json --from audit.json -o census.html
```

### Options

```
      --from stringArray    scan, drift or audit JSON file (repeatable; .gz and .zst work)
  -h, --help[=true|false]   help for html
  -o, --output string       file to write, or - for stdout (default "-")
```

### Options inherited from parent commands

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json, csv, sarif or github (default tty when stdout is a terminal, else plain)
      --log-file string         write logs to this file instead of stderr
      --log-format string       log format: text or json (default "text")
      --no-input[=true|false]   never prompt for input
  -q, --quiet[=true|false]      log errors only
      --tf-path string          terraform or tofu binary, passed to terragrunt as --tf-path (config binaries.terraform) (default "terraform")
      --tg-path string          terragrunt binary (config binaries.terragrunt) (default "terragrunt")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus report](tfcensus_report.md)	 - Render saved scan, drift, audit and index files without collecting again

