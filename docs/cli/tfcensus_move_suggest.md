## tfcensus move suggest

Draft a migration file from the moves that plans show

### Synopsis

suggest plans each --from and --to unit through terragrunt (one at a time, against its backend,
without the state lock) and pairs every resource a --from unit plans to delete with the resource a
--to unit plans to create. A pair is a move when the type is the same, every attribute known before
apply is equal, and neither resource matches anything else. Null equals an empty string, list or
map. Replaced resources and data sources never take part. Name a unit in both --from and --to to
find renames inside it.

--ignore lets one attribute differ, with tfautomv's syntax: everything:TYPE:ATTR,
whitespace:TYPE:ATTR, prefix:TYPE:ATTR:PREFIX, plus json:TYPE:ATTR, which compares JSON strings by
value. TYPE may be *. ATTR is a flat key such as tags.Name or ingress.0.cidr_blocks.#; a * segment
matches any one segment, and a rule covers the keys below its ATTR.

The draft goes to stdout, or to --draft. It holds one migration "units" block per source unit and
one block of mv actions per unit whose addresses change. suggest writes no state; check the draft
with tfcensus move plan. --plan-dir reads <dir>/<unit>/tfplan.json instead of running terragrunt,
the layout of terragrunt run --all --json-out-dir.

```
tfcensus move suggest --from <unit> --to <unit> [flags]
```

### Examples

```
  tfcensus move suggest --from live/network --to live/network-sg --to live/network-endpoints
  tfcensus move suggest --from live/app --to live/app --ignore json:aws_iam_policy:policy --draft migrations/app.hcl
```

### Options

```
      --draft string         write the draft migration file here instead of stdout
      --force[=true|false]   overwrite an existing --draft file
      --from stringArray     unit whose planned deletes can be moved, as path or path:workspace (repeatable)
  -h, --help[=true|false]    help for suggest
      --ignore stringArray   let an attribute differ: KIND:TYPE:ATTR[:PREFIX] (repeatable)
      --plan-dir string      read plans from <dir>/<unit>/tfplan.json instead of running terragrunt
      --repo string          repository root that unit paths are relative to (default ".")
      --to stringArray       unit whose planned creates can receive a move, as path or path:workspace (repeatable)
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

* [tfcensus move](tfcensus_move.md)	 - Move resources between the states of Terragrunt units

