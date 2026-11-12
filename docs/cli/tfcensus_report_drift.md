## tfcensus report drift

Summarize a saved drift run: unit statuses and drifted resources

### Synopsis

drift lists the drifted and failed units of a file from tfcensus drift --format json, and
their drifted resources with attribute paths, never values. It runs no plan. It is what the
drift_summary tool of tfcensus mcp returns.

```
tfcensus report drift [flags]
```

### Examples

```
  tfcensus report drift --from drift.json --unit 'acme-live/prod/*'
```

### Options

```
      --from string         drift JSON from tfcensus drift --format json
  -h, --help[=true|false]   help for drift
      --status string       unit status: drifted, failed, clean, early-exit or excluded (default drifted and failed)
      --unit string         unit path glob, * matches / too
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

