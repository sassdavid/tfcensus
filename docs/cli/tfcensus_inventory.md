## tfcensus inventory

Read each unit's state: versions, providers, resource counts, serial and lineage

### Synopsis

inventory discovers units the way units does, then reads each unit's state from its s3
backend with s3:GetObject. It takes no lock, so it is safe to run during an apply. Other
workspaces of a unit are found with s3:ListBucket under workspace_key_prefix (default env:) and
listed as their own rows.

inventory prints identifiers and counts only: never attribute values. Raw state records which
providers a unit uses but not their versions, so provider versions come from the unit's
.terraform.lock.hcl when the repo has one.

State is read with the backend's credentials: the backend's profile when it names one, else the
profile that owns the bucket's account (accounts.map, then the --profile and --org accounts, then
the default credentials), with the backend's role_arn assumed from it. The bucket's account is
the role's account, or the unit's target account when there is no role.

A state that can't be read (access denied, a parse error) is reported for its unit and the run
continues; inventory then exits 1. An OpenTofu state with client-side encryption shows only its
serial and lineage. --save-index writes the identifier index that whois and scan read, compressed
with zstd for a .zst name and gzip for .gz.

```
tfcensus inventory [paths...] [flags]
```

### Examples

```
  tfcensus inventory ~/src/infra/acme-live
  tfcensus inventory --profile 'acme-*' --format json > inventory.json
  tfcensus inventory --format csv --save-index index.json.zst
```

### Options

```
      --concurrency int                states read at the same time (default 16)
      --filter stringArray             terragrunt filter expression, passed to terragrunt find unchanged (repeatable)
      --generate-stacks[=true|false]   run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)
  -h, --help[=true|false]              help for inventory
      --management-profile string      profile that lists the organization's accounts, else the default AWS credentials (config accounts.org.management_profile)
      --org[=true|false]               list the accounts of the AWS organization and assume --org-role in each (config accounts.org.enabled)
      --org-role string                role assumed in each member account (config accounts.org.role) (default "OrganizationAccountAccessRole")
      --profile stringArray            AWS profile from ~/.aws/config, or a glob such as 'acme-*' (repeatable; config accounts.profiles)
      --render string                  when to use terragrunt render: auto, always or never (config discovery.render) (default "auto")
      --save-index string              write the identifier index to this file (.zst: zstd, .gz: gzip, else plain JSON)
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

* [tfcensus](tfcensus.md)	 - Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS

