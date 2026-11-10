## tfcensus doctor

Check AWS credentials and which inventory layers each account and region supports

### Synopsis

doctor loads the credentials of every selected account source and probes each account and
region for the inventory layers a scan can use:

  re      Resource Explorer: an index in the region and a view to search it with
  config  AWS Config: a recorder that is recording
  cc      Cloud Control: always available, but slow and throttled
  native  tfcensus's own enumerators: always available

Without --profile or --org, doctor uses the default AWS credentials (AWS_PROFILE, environment
credentials or an instance role). Without --region, it probes the regions that have a Resource
Explorer index and the profile's own region.

doctor is read-only: it never creates indexes, views or recorders. Its notes say what an admin
could turn on. Results are cached for 24 hours under $XDG_CACHE_HOME/tfcensus/probe; --refresh
probes again.

A source whose credentials fail, such as a profile with an expired SSO token, is skipped with a
warning. doctor exits 4 when every source fails.

```
tfcensus doctor [flags]
```

### Examples

```
  tfcensus doctor --profile 'acme-*'
  tfcensus doctor --profile acme-nonprod --region eu-west-1 --region us-east-1
  tfcensus doctor --org --management-profile acme-mgmt --format json
```

### Options

```
  -h, --help[=true|false]           help for doctor
      --management-profile string   profile that lists the organization's accounts, else the default AWS credentials (config accounts.org.management_profile)
      --org[=true|false]            list the accounts of the AWS organization and assume --org-role in each (config accounts.org.enabled)
      --org-role string             role assumed in each member account (config accounts.org.role) (default "OrganizationAccountAccessRole")
      --profile stringArray         AWS profile from ~/.aws/config, or a glob such as 'acme-*' (repeatable; config accounts.profiles)
      --refresh[=true|false]        probe again even when the cached result is fresh
      --region stringArray          AWS region (repeatable; config regions)
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

