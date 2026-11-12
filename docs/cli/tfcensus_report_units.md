## tfcensus report units

List the units of a saved state index

### Synopsis

units lists the states in an index written by tfcensus inventory --save-index, with the
accounts and regions of their resources. It is what the units tool of tfcensus mcp returns.

```
tfcensus report units [flags]
```

### Examples

```
  tfcensus report units --index index.json.zst --path 'acme-live/prod/*'
```

### Options

```
  -h, --help[=true|false]   help for units
      --index string        state index from tfcensus inventory --save-index
      --path string         unit path glob, * matches / too
      --status string       state status: ok, absent, encrypted, unsupported or error
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

