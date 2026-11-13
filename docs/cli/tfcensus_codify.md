## tfcensus codify

Write import blocks for unmanaged resources from a scan report

### Synopsis

codify reads the unmanaged findings of a scan report (tfcensus scan --format json) and writes
import blocks into --into as imports_tfcensus_<YYYYMMDD>.tf, dated by the UTC clock. The blocks are
a starting point for adoption: plan with -generate-config-out, review the generated config, and
move it into your code. codify never runs plan or apply and writes nothing outside --into.

Each block imports by identity when the target can: terraform or OpenTofu 1.12 or later, an aws
provider of 6.0 or later in the directory's .terraform.lock.hcl (or no lock file), and a type
whose identity attributes the scan gives. Otherwise it imports by id, from the typemap's id
template. A finding with neither is listed as skipped with the reason.

The target engine comes from the lock file's registry (registry.opentofu.org means OpenTofu),
else from the --tf-path binary's name; its version from '<tf-path> version -json'. --target
terraform@1.12.2 or opentofu@1.11.0 skips the detection. When detection fails, codify writes id
imports.

Names come from the Name tag, then the resource's name, then the last part of its ID, as
lowercase letters, digits and underscores. A name that is taken, in this run or by a resource or
import block already in --into, gets _2, _3 and so on. A resource that an import block in --into
already imports is skipped.

--query also writes query_tfcensus_<YYYYMMDD>.tfquery.hcl with one list block per type and
region, for types that have a provider list resource without required list config. terraform
query -generate-config-out turns it into config (terraform 1.14 or later; OpenTofu has no query).

--select takes a CEL expression over the same resource variable as ownership rules.

codify refuses to overwrite a file unless --force is set. The exit code is 2 for bad flags or
input and 1 when a file can't be written.

```
tfcensus codify --from scan.json --into <dir> [flags]
```

### Examples

```
  tfcensus scan --format json > scan.json
  tfcensus codify --from scan.json --into live/prod/eu-west-1/network --types 'AWS::EC2::*'
  tfcensus codify --from scan.json --into live/prod/iam --select 'resource.tags["team"] == "platform"'
  tfcensus codify --from scan.json --into iam --target terraform@1.14.0 --query
```

### Options

```
      --force[=true|false]   overwrite files codify wrote before
      --from string          scan report from tfcensus scan --format json, or - for stdin
  -h, --help[=true|false]    help for codify
      --import-by string     import by auto or id; auto uses identity when the target can (default "auto")
      --into string          directory of the unit to write the files into; it must exist
      --query[=true|false]   also write a .tfquery.hcl file with list blocks for terraform query
      --select string        CEL expression over resource that picks the findings to codify
      --target string        engine and version the blocks are for, such as terraform@1.12.2 or opentofu@1.11.0 (default: detect)
      --types strings        resource types to codify, as CloudFormation or TF type globs such as 'AWS::IAM::*' or 'aws_iam_role'
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

