## tfcensus move rollback

Restore the states a migration file changed

### Synopsis

rollback restores every unit of each applied block to the S3 object version it had before
apply pushed it, byte for byte, newest block first and the source before its destinations. It takes
the backend lock, copies the old version over the current one, fixes the DynamoDB digest, and marks
the history records as rolled back. A unit that didn't exist before is deleted again.

rollback stops when a state changed after the push, unless --force is set.

```
tfcensus move rollback <migration-file> [flags]
```

### Examples

```
  tfcensus move rollback migrations/split_network.hcl
  tfcensus move rollback migrations/split_network.hcl --block split_network --var env=prod
```

### Options

```
      --block strings        run only these blocks (default: all, in file order)
      --force[=true|false]   restore even when a state changed after the push
  -h, --help[=true|false]    help for rollback
      --repo string          repository root that the file's unit paths are relative to (default ".")
      --var stringArray      set var.NAME in the migration file, as NAME=VALUE
      --yes[=true|false]     don't ask for confirmation
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

