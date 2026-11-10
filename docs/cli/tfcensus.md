## tfcensus

Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS

### Synopsis

tfcensus lists Terraform/OpenTofu and Terragrunt units and their state, finds AWS
resources that no unit manages, and audits state backends. It is read-only by default.

stdout carries data, stderr carries logs. Use --format json for scripting.

### Options

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json, csv or sarif (default tty when stdout is a terminal, else plain)
  -h, --help[=true|false]       help for tfcensus
      --log-file string         write logs to this file instead of stderr
      --log-format string       log format: text or json (default "text")
      --no-input[=true|false]   never prompt for input
  -q, --quiet[=true|false]      log errors only
      --tf-path string          terraform or tofu binary, passed to terragrunt as --tf-path (config binaries.terraform) (default "terraform")
      --tg-path string          terragrunt binary (config binaries.terragrunt) (default "terragrunt")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus completion](tfcensus_completion.md)	 - Generate the autocompletion script for the specified shell
* [tfcensus config](tfcensus_config.md)	 - Inspect and validate configuration
* [tfcensus doctor](tfcensus_doctor.md)	 - Check AWS credentials and which inventory layers each account and region supports
* [tfcensus inventory](tfcensus_inventory.md)	 - Read each unit's state: versions, providers, resource counts, serial and lineage
* [tfcensus units](tfcensus_units.md)	 - List units with their state backend, target account and region
* [tfcensus version](tfcensus_version.md)	 - Print version and build information

