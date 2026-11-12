## tfcensus report audit

List the findings of a saved backend audit, highest severity first

### Synopsis

audit lists the findings in a file from tfcensus audit --format json, highest severity
first. It is what the audit_findings tool of tfcensus mcp returns.

```
tfcensus report audit [flags]
```

### Examples

```
  tfcensus report audit --from audit.json --severity medium
```

### Options

```
      --account string                    AWS account ID
      --from string                       audit JSON from tfcensus audit --format json
  -h, --help[=true|false]                 help for audit
      --include-suppressed[=true|false]   list suppressed findings too
      --rule string                       check ID glob, such as S3-*
      --severity string                   lowest severity: info, low, medium or high
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

