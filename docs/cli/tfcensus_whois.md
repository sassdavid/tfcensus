## tfcensus whois

Show which units manage an AWS resource

### Synopsis

whois looks each ARN or TF resource id up in the state of every discovered unit, with the
match keys scan uses (identity, then ARN, then id), and prints the units, addresses, accounts and
regions that manage it, and the key that matched. More than one state means double-managed.

An ARN whose type the typemap maps is matched like a live resource; any other ARN, and a bare id,
is looked up across every type. --type narrows the lookup to TF types.

When no state manages the resource, the ownership rules explain why (other-iac, aws-managed,
ignored or unmanaged). Without --index, whois lists the resource's type in its account and region
through the scan inventory layers, so the rules see its tags and relations. With --index the run
stays offline and the rules see only what the ARN gives; --live lists anyway.

--index reads an index written by tfcensus inventory --save-index instead of reading every state.

The exit code is 3 when --fail-on finds at least --min-count answers in its categories, 1 when a
state read or a layer failed, 4 when every failure was a credential problem, and 0 otherwise.

```
tfcensus whois <arn|id>... [flags]
```

### Examples

```
  tfcensus whois arn:aws:iam::111111111111:role/app --repo ~/src/infra/acme-live
  tfcensus whois --index index.json.zst sg-0123456789abcdef0 --type aws_security_group
  jq -r '.data.findings[].arn // empty' scan.json | tfcensus whois --stdin --index index.json.zst
  tfcensus whois --index index.json.zst --fail-on double-managed arn:aws:s3:::acme-assets
```

### Options

```
      --concurrency int                states read at the same time (default 16)
      --fail-on strings                exit 3 when answers are in these categories: unmanaged or double-managed
      --filter stringArray             terragrunt filter expression, passed to terragrunt find unchanged (repeatable)
      --generate-stacks[=true|false]   run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)
  -h, --help[=true|false]              help for whois
      --index string                   read the state index from this file (from inventory --save-index) instead of reading states
      --live[=true|false]              list an unmanaged resource's type live so the rules see its tags (default true, false with --index) (default true)
      --management-profile string      profile that lists the organization's accounts, else the default AWS credentials (config accounts.org.management_profile)
      --md-max-chars int               cut Markdown output to this many characters at a row or section boundary; 0 means no limit (default 60000)
      --min-count int                  answers --fail-on needs before it exits 3 (default 1)
      --org[=true|false]               list the accounts of the AWS organization and assume --org-role in each (config accounts.org.enabled)
      --org-role string                role assumed in each member account (config accounts.org.role) (default "OrganizationAccountAccessRole")
      --out stringArray                also write the report to a file as format=path (repeatable); format is one of plain, md, json or sarif
      --profile stringArray            AWS profile from ~/.aws/config, or a glob such as 'acme-*' (repeatable; config accounts.profiles)
      --region stringArray             AWS region (repeatable; config regions)
      --render string                  when to use terragrunt render: auto, always or never (config discovery.render) (default "auto")
      --repo stringArray               repository to discover units in (repeatable; config discovery.roots, else the working directory)
      --rules stringArray              YAML file with more ownership rules under rules: (repeatable)
      --stdin[=true|false]             read more ARNs or ids from stdin, one per line
      --type strings                   TF resource types to look in, such as aws_security_group
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

