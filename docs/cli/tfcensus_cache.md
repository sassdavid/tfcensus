## tfcensus cache

Inspect or clear the probe and scan caches

### Synopsis

tfcensus keeps two caches under $XDG_CACHE_HOME/tfcensus:

  probe  doctor's and scan's capability probe, one file per account and region, fresh for 24h
  scan   scan's inventory and state index, one file per set of inputs, fresh for cache.ttl (1h)

They hold identifiers, tags and probe results, never state attribute values. doctor --refresh
and scan --refresh ignore them for one run.

### Options

```
  -h, --help[=true|false]   help for cache
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
* [tfcensus cache clear](tfcensus_cache_clear.md)	 - Delete every entry of both caches
* [tfcensus cache info](tfcensus_cache_info.md)	 - Show each cache's directory, entry count, size and freshness

