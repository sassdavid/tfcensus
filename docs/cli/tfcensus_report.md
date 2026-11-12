## tfcensus report

Render saved scan, drift, audit and index files without collecting again

### Synopsis

report reads JSON files that earlier runs wrote and renders them, offline: no AWS call, no
terragrunt and no state read. report html builds one static HTML page. The other subcommands
print the same summaries as the tools of tfcensus mcp, with the same filters.

### Options

```
  -h, --help[=true|false]   help for report
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
* [tfcensus report audit](tfcensus_report_audit.md)	 - List the findings of a saved backend audit, highest severity first
* [tfcensus report drift](tfcensus_report_drift.md)	 - Summarize a saved drift run: unit statuses and drifted resources
* [tfcensus report html](tfcensus_report_html.md)	 - Write a static HTML report from scan, drift and audit JSON
* [tfcensus report inventory](tfcensus_report_inventory.md)	 - Count the managed resources of a saved state index per unit and type
* [tfcensus report scan](tfcensus_report_scan.md)	 - Summarize a saved scan: counts and the top unmanaged resources
* [tfcensus report units](tfcensus_report_units.md)	 - List the units of a saved state index

