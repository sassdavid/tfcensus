## tfcensus move plan

Verify a migration file with plans, without writing anything

### Synopsis

plan reads each involved unit's state from S3, applies the block in memory and plans every
unit against its new state through a local backend override (_tfcensus_override.tf), with its
own credentials. A surgery block passes when every resource change is no-op or read; output
changes don't count. Blocks run in order, so a later block sees an earlier one's result.

A native block only lists the files apply would write.

The exit code is 3 when a block is rejected (MV-VERIFY, MV-PLAN, MV-ACCOUNT or MV-VERSIONING).

```
tfcensus move plan <migration-file> [flags]
```

### Examples

```
  tfcensus move plan migrations/split_network.hcl
  tfcensus move plan migrations/split_network.hcl --block split_network --var env=prod
```

### Options

```
      --block strings       run only these blocks (default: all, in file order)
  -h, --help[=true|false]   help for plan
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

