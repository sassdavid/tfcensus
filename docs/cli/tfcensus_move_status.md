## tfcensus move status

Show whether each migration block is applied in each unit

### Synopsis

status reads the history object next to each involved unit's state and shows each block as
applied, pending or partial (applied in some units only).

```
tfcensus move status <migration-file> [flags]
```

### Examples

```
  tfcensus move status migrations/split_network.hcl
  tfcensus move status migrations/split_network.hcl --block split_network --var env=prod
```

### Options

```
      --block strings       run only these blocks (default: all, in file order)
  -h, --help[=true|false]   help for status
      --repo string         repository root that the file's unit paths are relative to (default ".")
      --var stringArray     set var.NAME in the migration file, as NAME=VALUE
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

