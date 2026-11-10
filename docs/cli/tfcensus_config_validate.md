## tfcensus config validate

Validate config files against the schema

### Synopsis

Without arguments, validates the user and project config files plus TFCENSUS_* env vars and
flags, exactly as other commands would load them. With arguments, validates only those files.

Exits 2 and prints every problem with file:line:column when something is invalid.

```
tfcensus config validate [file...] [flags]
```

### Examples

```
  tfcensus config validate
  tfcensus config validate .tfcensus.yaml ~/.config/tfcensus/config.yaml
```

### Options

```
  -h, --help[=true|false]   help for validate
```

### Options inherited from parent commands

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json, csv or sarif (default tty when stdout is a terminal, else plain)
      --log-file string         write logs to this file instead of stderr
      --log-format string       log format: text or json (default "text")
      --no-input[=true|false]   never prompt for input
  -q, --quiet[=true|false]      log errors only
      --tf-path string          terraform or tofu binary, passed to terragrunt as --tf-path (config binaries.terraform) (default "terraform")
      --tg-path string          terragrunt binary (config binaries.terragrunt) (default "terragrunt")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus config](tfcensus_config.md)	 - Inspect and validate configuration

