## tfcensus move mark-applied

Record migration blocks as applied without changing state

### Synopsis

mark-applied writes a history record for each block in every unit it touches and changes no
state. Use it after a native block's apply, or for a migration done by hand. Such records can't be
rolled back by tfcensus.

```
tfcensus move mark-applied <migration-file> [flags]
```

### Examples

```
  tfcensus move mark-applied migrations/split_network.hcl
  tfcensus move mark-applied migrations/split_network.hcl --block split_network --var env=prod
```

### Options

```
      --block strings       run only these blocks (default: all, in file order)
  -h, --help[=true|false]   help for mark-applied
      --repo string         repository root that the file's unit paths are relative to (default ".")
      --var stringArray     set var.NAME in the migration file, as NAME=VALUE
      --yes[=true|false]    don't ask for confirmation
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

* [tfcensus move](tfcensus_move.md)	 - Move resources between the states of Terragrunt units

