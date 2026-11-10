## tfcensus units

List units with their state backend, target account and region

### Synopsis

units lists the Terragrunt units and plain Terraform/OpenTofu root modules under each path,
with where each one keeps its state and which AWS account and region it manages.

A path with a terragrunt.hcl, root.hcl or terragrunt.stack.hcl anywhere below it is a Terragrunt
repo: terragrunt find lists its units. Any other path is searched for root modules, which are
directories whose .tf files have a backend or cloud block.

tfcensus evaluates remote_state itself (the fast path). get_aws_account_id there returns the
account of the default AWS credentials (AWS_PROFILE, environment credentials or an instance role),
looked up once with sts:GetCallerIdentity and only when state needs it. With --render auto, units
the fast path can't evaluate, for example because they call run_cmd or set iam_role, go to
terragrunt render --json. Units that neither method resolves are listed with the reason. Without paths, units uses discovery.roots from
the config, or the working directory.

```
tfcensus units [paths...] [flags]
```

### Examples

```
  tfcensus units ~/src/infra/acme-live
  tfcensus units --filter './live/prod/**' --format json
  tfcensus units --render never --generate-stacks
```

### Options

```
      --filter stringArray             terragrunt filter expression, passed to terragrunt find unchanged (repeatable)
      --generate-stacks[=true|false]   run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)
  -h, --help[=true|false]              help for units
      --render string                  when to use terragrunt render: auto, always or never (config discovery.render) (default "auto")
```

### Options inherited from parent commands

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json or sarif (default tty when stdout is a terminal, else plain)
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

