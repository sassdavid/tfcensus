## tfcensus config

Inspect and validate configuration

### Synopsis

Configuration is layered; later layers win:

  defaults < user file < project file < env (TFCENSUS_*) < flags

User file:    $XDG_CONFIG_HOME/tfcensus/config.yaml (~/.config/tfcensus/config.yaml)
Project file: .tfcensus.yaml, found by walking up from the working directory to the git root
Env vars:     TFCENSUS_<KEY> with dots as underscores, e.g. TFCENSUS_CACHE_TTL=30m.
              Use __ to mark nesting explicitly (TFCENSUS_DISCOVERY__GENERATE_STACKS=true).
              Lists are comma-separated.

Unknown keys and wrong types are errors (exit 2), reported with file:line:column.

### Options

```
  -h, --help[=true|false]   help for config
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

* [tfcensus](tfcensus.md)	 - Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS
* [tfcensus config path](tfcensus_config_path.md)	 - Print the config file locations tfcensus reads
* [tfcensus config show](tfcensus_config_show.md)	 - Print the effective configuration
* [tfcensus config validate](tfcensus_config_validate.md)	 - Validate config files against the schema

