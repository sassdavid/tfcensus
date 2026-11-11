## tfcensus rules test

Compile ownership rules and evaluate them against sample resources, offline

### Synopsis

rules test compiles the built-in packs and the rules in a YAML file (a list under rules:, the
same shape as the config key) and reports the first error with the rule name and column.

With --resource, it evaluates every rule against each resource in a JSON file (one object or an
array) and prints the trace: each rule's outcome and the category of the first match. A resource
has the fields a rule sees as resource: account, region, type, source_type, tf_types, arn, id,
name, tags, attrs, layer, sources, parents, relationships and created. scan --explain --format
json prints resources in this shape.

Packs, never_hide and the config's rules come from the loaded config; the file's rules run after
the config's. rules test makes no AWS calls. Expired rules show as expired.

```
tfcensus rules test <rules.yaml> [flags]
```

### Examples

```
  tfcensus rules test rules.yaml
  tfcensus rules test rules.yaml --resource sample.json
```

### Options

```
  -h, --help[=true|false]   help for test
      --resource string     JSON file with a resource or an array of resources to evaluate
```

### Options inherited from parent commands

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json, csv or sarif (default tty when stdout is a terminal, else plain)
      --log-file string         write logs to this file instead of stderr
      --log-format string       log format: text or json (default "text")
      --no-input[=true|false]   never prompt for input
  -q, --quiet[=true|false]      log errors only
      --tf-path string          terraform or tofu binary, passed to terragrunt as --tf-path (config binaries.terraform) (default "terraform")
      --tg-path string          terragrunt binary (config binaries.terragrunt) (default "terragrunt")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus rules](tfcensus_rules.md)	 - Work with ownership rules

