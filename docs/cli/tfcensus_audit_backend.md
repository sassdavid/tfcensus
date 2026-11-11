## tfcensus audit backend

Check state buckets, lock tables and the fleet of states

### Synopsis

audit backend discovers units the way units does, reads their state the way inventory
does, and checks:

  - every s3 bucket the units' backends name (versioning, encryption and key policy, public
    access block, TLS-only bucket policy, object ownership, replication, lifecycle, logging),
  - every DynamoDB lock table (point-in-time recovery, KMS, stale locks, orphaned digests),
  - the fleet: states no unit resolves to, units with no state, empty states, copied lineages,
    double-managed resources, old terraform and provider versions, large states, and attribute
    names that look like secrets.

Each finding names its check (rule), severity, target and reason. Output never contains attribute
values: FL-SECRET lists attribute names only. Every call is a read. audit takes no lock.

--discover also lists the buckets of each account (s3:ListAllMyBuckets) and audits those matching
--discover-pattern that no unit names; their state keys show up as FL-ORPHAN.

Suppress accepted findings in config with audit.suppress entries: check, bucket, unit and key
are globs, reason is required, and after expires (YYYY-MM-DD) the entry stops matching and a
warning names it.

The exit code is 3 when an unsuppressed finding is at or above --fail-on, 1 when a check couldn't
run, and 4 when every failure was a credential problem.

```
tfcensus audit backend [paths...] [flags]
```

### Examples

```
  tfcensus audit backend ~/src/infra/acme-live
  tfcensus audit backend --fail-on high --format sarif > audit.sarif
  tfcensus audit backend --profile 'acme-*' --discover --format json
```

### Options

```
      --concurrency int                states read at the same time (default 16)
      --discover[=true|false]          also audit buckets of each account that match --discover-pattern and that no unit names
      --discover-pattern stringArray   bucket name glob for --discover (repeatable; config audit.discover_patterns)
      --fail-on string                 exit 3 when a finding is at least this severe: info, low, medium or high
      --filter stringArray             terragrunt filter expression, passed to terragrunt find unchanged (repeatable)
      --generate-stacks[=true|false]   run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)
  -h, --help[=true|false]              help for backend
      --management-profile string      profile that lists the organization's accounts, else the default AWS credentials (config accounts.org.management_profile)
      --org[=true|false]               list the accounts of the AWS organization and assume --org-role in each (config accounts.org.enabled)
      --org-role string                role assumed in each member account (config accounts.org.role) (default "OrganizationAccountAccessRole")
      --profile stringArray            AWS profile from ~/.aws/config, or a glob such as 'acme-*' (repeatable; config accounts.profiles)
      --render string                  when to use terragrunt render: auto, always or never (config discovery.render) (default "auto")
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

* [tfcensus audit](tfcensus_audit.md)	 - Audit state backends

