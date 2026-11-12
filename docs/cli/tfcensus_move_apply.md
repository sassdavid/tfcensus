## tfcensus move apply

Apply a migration file to the units' states

### Synopsis

apply plans and verifies each pending block like plan, then asks you to type the block's last
destination unit path (its from path for an in-place block). It checks that no state changed since
it was read, pushes the destinations and then the source with terragrunt run -- state push (so
terraform takes the lock and checks lineage and serial), and records the S3 version IDs in each
unit's <key>.tfcensus-migrations.json. Blocks already applied are skipped.

A native block writes moves_tfcensus_<block>.tf into each unit (import blocks in destinations,
removed blocks in the source), then plans each unit to check the files. It changes no state and
no history; run mark-applied after your normal apply.

apply needs bucket versioning (S3-VER). The exit code is 3 when a block is rejected.

```
tfcensus move apply <migration-file> [flags]
```

### Examples

```
  tfcensus move apply migrations/split_network.hcl
  tfcensus move apply migrations/split_network.hcl --block split_network --var env=prod
```

### Options

```
      --block strings        run only these blocks (default: all, in file order)
      --force[=true|false]   overwrite moves_tfcensus_<block>.tf files of native blocks
  -h, --help[=true|false]    help for apply
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

