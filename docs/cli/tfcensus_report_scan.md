## tfcensus report scan

Summarize a saved scan: counts and the top unmanaged resources

### Synopsis

scan prints the per-account counts of a scan and its top findings, double-managed first,
then unmanaged. --category lists another category instead.

With --from it reads a file from tfcensus scan --format json. Without it, it reads the newest
snapshot in the scan cache and applies the current ownership rules to it, with no AWS call.
A snapshot older than cache.ttl is used anyway, with a warning. It is what the scan_summary
tool of tfcensus mcp returns.

```
tfcensus report scan [flags]
```

### Examples

```
  tfcensus report scan --from scan.json --account 111111111111 --top 20
  tfcensus report scan --category undetermined
```

### Options

```
      --account string      AWS account ID
      --category string     list this category: managed, double-managed, unmanaged, other-iac, aws-managed, derived, ignored or undetermined (default: double-managed and unmanaged)
      --from string         scan JSON (default: the newest scan cache snapshot)
  -h, --help[=true|false]   help for scan
      --region string       AWS region, or global
      --top int             findings to list, 1 to 500 (default 50)
      --type string         CloudFormation or layer type glob, such as AWS::EC2::*
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

