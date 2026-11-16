## tfcensus move

Move resources between the states of Terragrunt units

### Synopsis

move runs migration files: migration "units" blocks that move resources from one unit's state
to others, or run tfmigrate-style mv, xmv and rm actions inside one state. See
docs/runbooks/move.md for the file format and a full walkthrough.

suggest drafts a migration file from plans. plan and status only read. apply, rollback and mark-applied are the only tfcensus commands that
write to a backend; each asks you to type the block's destination unit path first, or takes
--yes. Without a terminal and without --yes they exit 2 and change nothing.

Unit paths in the file are relative to --repo (default: the current directory) and name units
with an s3 backend. Write path:workspace for a workspace other than default, such as
live/app:blue; the state is then read from <workspace_key_prefix>/<workspace>/<key> (env: when
the backend sets no prefix), and terragrunt runs with TF_WORKSPACE. The workspace must exist.
The native strategy works on the default workspace only.

### Options

```
  -h, --help[=true|false]   help for move
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
      --tofu-path string        tofu binary that --decrypt-with-tofu runs (config binaries.tofu) (default "tofu")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus](tfcensus.md)	 - Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS
* [tfcensus move apply](tfcensus_move_apply.md)	 - Apply a migration file to the units' states
* [tfcensus move mark-applied](tfcensus_move_mark-applied.md)	 - Record migration blocks as applied without changing state
* [tfcensus move plan](tfcensus_move_plan.md)	 - Verify a migration file with plans, without writing anything
* [tfcensus move rollback](tfcensus_move_rollback.md)	 - Restore the states a migration file changed
* [tfcensus move status](tfcensus_move_status.md)	 - Show whether each migration block is applied in each unit
* [tfcensus move suggest](tfcensus_move_suggest.md)	 - Draft a migration file from the moves that plans show

