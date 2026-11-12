## tfcensus report inventory

Count the managed resources of a saved state index per unit and type

### Synopsis

inventory counts the managed resource instances in an index written by tfcensus inventory
--save-index, per unit and TF type, with the instances another state also manages. It is
what the inventory tool of tfcensus mcp returns.

```
tfcensus report inventory [flags]
```

### Examples

```
  tfcensus report inventory --index index.json.zst --account 111111111111 --type 'aws_iam_*'
```

### Options

```
      --account string      AWS account ID
  -h, --help[=true|false]   help for inventory
      --index string        state index from tfcensus inventory --save-index
      --region string       AWS region
      --type string         TF resource type glob, such as aws_iam_*
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

