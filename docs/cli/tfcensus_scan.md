## tfcensus scan

Find AWS resources that no unit manages

### Synopsis

scan lists the live resources of every selected account and region, matches them against
the state of the discovered units, and puts each resource in one category:

  managed         matched to one state resource
  double-managed  matched in more than one state
  unmanaged       no state manages it and no ownership rule matches
  other-iac       owned by CloudFormation, CDK, Pulumi or a Kubernetes controller
  aws-managed     created by AWS, such as the default VPC
  derived         a child of another resource, such as a NAT gateway's network interface
  ignored         a user rule hides it
  undetermined    its type has no mapping, so tfcensus can't tell

The flow is: capability probe, discovery, state index, inventory layers, matching, then the
ownership rules (the built-in packs, then rules from the config and --rules files, first match
wins). scan --explain <arn> prints how one resource was categorized; --explain-coverage prints
which layer listed each type in each account and region.

Coverage is (managed + double-managed) / (managed + double-managed + unmanaged) per account.
aws-managed, derived, other-iac, ignored and undetermined resources don't count.

scan caches the inventory and state index under $XDG_CACHE_HOME/tfcensus/scan for cache.ttl
(default 1h), keyed by the accounts, regions, layers, types and units. A re-run with other rules
makes no AWS calls. --refresh lists again.

The exit code is 3 when --fail-on finds at least --min-count findings, 1 when a layer, a state
read or discovery failed, 4 when every failure was a credential problem, and 0 otherwise.

```
tfcensus scan [paths...] [flags]
```

### Examples

```
  tfcensus scan ~/src/infra/acme-live --profile acme-nonprod --region eu-west-1
  tfcensus scan --types 'AWS::IAM::*' --fail-on unmanaged
  tfcensus scan --rules extra-rules.yaml --format md > scan.md
  tfcensus scan --explain arn:aws:ec2:eu-west-1:111111111111:security-group/sg-0123456789abcdef0
```

### Options

```
      --concurrency int                 states read at the same time (default 16)
      --explain string                  print how the resource with this ARN or ID was categorized
      --explain-coverage[=true|false]   print which layer listed each type in each account and region
      --fail-on strings                 exit 3 when findings are in these categories: unmanaged, double-managed
      --filter stringArray              terragrunt filter expression, passed to terragrunt find unchanged (repeatable)
      --generate-stacks[=true|false]    run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)
  -h, --help[=true|false]               help for scan
      --index string                    read the state index from this file (from inventory --save-index) instead of reading states
      --layers string                   inventory layers: auto, or a comma-separated list of config, re, cc and native (config scan.layers) (default "auto")
      --management-profile string       profile that lists the organization's accounts, else the default AWS credentials (config accounts.org.management_profile)
      --min-count int                   findings --fail-on needs before it exits 3 (default 1)
      --org[=true|false]                list the accounts of the AWS organization and assume --org-role in each (config accounts.org.enabled)
      --org-role string                 role assumed in each member account (config accounts.org.role) (default "OrganizationAccountAccessRole")
      --profile stringArray             AWS profile from ~/.aws/config, or a glob such as 'acme-*' (repeatable; config accounts.profiles)
      --refresh[=true|false]            list and read states again even when the cache is fresh
      --region stringArray              AWS region (repeatable; config regions)
      --render string                   when to use terragrunt render: auto, always or never (config discovery.render) (default "auto")
      --rules stringArray               YAML file with more ownership rules under rules: (repeatable)
      --types strings                   resource types to scan, as CloudFormation or TF type globs such as 'AWS::IAM::*' or 'aws_security_group*'
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

