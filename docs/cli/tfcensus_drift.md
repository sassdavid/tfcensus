## tfcensus drift

Find drift between the units' state and AWS

### Synopsis

drift runs a plan in every Terragrunt unit and reports the resources that changed outside
terraform, attribute by attribute. It runs

  terragrunt run --all [--filter ...] --tf-path <bin> --json-out-dir <tmp> --report-file <tmp>/report.json
    --report-format json --log-format json -- plan -refresh-only -lock=false -detailed-exitcode

in each repo, with TG_TF_PATH set, and reads every unit's tfplan.json and the run report. Plain
Terraform root modules are skipped. With --mode full, the plan isn't refresh-only, and drift tells
the drift the plan reverts (the code disagrees) from the drift it keeps.

--from-json-out-dir reads plans an earlier terragrunt run wrote, without running anything. Pass the
same run's --report-file, so units that failed before writing a plan are reported.

Identical changes in several units become one group (--collapse shape compares type, action and
attribute paths; strict also compares values; none keeps one group per change). Noise rules under
drift.noise hide attribute changes or whole changes; the presets aws-tags-all and computed-hashes
hide tags_all updates and computed hashes. A rule past its expires date stops hiding drift and
drift warns. drift.never_hide (default delete and replace) lists actions no rule can hide.

drift prints attribute paths, never values. --show-attributes adds before and after values;
sensitive values show as (sensitive) and unknown ones as (known after apply).

The exit code is 3 when a --fail-on gate counts at least --min-count: drift counts drifted
resources, drift:reverts the ones the plan reverts (--mode full only), errors the failed units. It
is 1 when a unit or a repo failed and no gate matched, and 0 otherwise. terraform's
-detailed-exitcode (2 for changes) is read internally and never returned.

```
tfcensus drift [paths...] [flags]
```

### Examples

```
  tfcensus drift ~/src/infra/acme-live --filter './live/prod/**' --fail-on drift
  tfcensus drift --mode full --fail-on drift:reverts --format md > drift.md
  tfcensus drift --from-json-out-dir out --report-file out/report.json --format github
```

### Options

```
      --collapse string                group identical changes across units: shape, strict or none (config drift.collapse) (default "shape")
      --fail-on strings                exit 3 when these gates match: drift, drift:reverts or errors
      --filter stringArray             terragrunt filter expression, passed to terragrunt find unchanged (repeatable)
      --from-json-out-dir string       read the plans of an earlier terragrunt run --json-out-dir instead of running terragrunt
      --generate-stacks[=true|false]   run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)
  -h, --help[=true|false]              help for drift
      --min-count int                  count a --fail-on gate needs before it exits 3 (default 1)
      --mode string                    plan mode: refresh-only or full; full also tells reverted drift from kept drift (config drift.mode) (default "refresh-only")
      --render string                  when to use terragrunt render: auto, always or never (config discovery.render) (default "auto")
      --report-file string             the JSON --report-file of that run
      --show-attributes[=true|false]   show before and after values; sensitive and unknown values stay masked
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

* [tfcensus](tfcensus.md)	 - Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS

