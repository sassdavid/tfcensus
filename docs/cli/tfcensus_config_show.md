## tfcensus config show

Print the effective configuration

```
tfcensus config show [flags]
```

### Examples

```
  tfcensus config show
  tfcensus config show --sources
  TFCENSUS_CACHE_TTL=30m tfcensus config show --sources --format json
```

### Options

```
  -h, --help[=true|false]      help for show
      --sources[=true|false]   show where each value comes from
```

### Options inherited from parent commands

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json or sarif (default tty when stdout is a terminal, else plain)
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

