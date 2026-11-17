---
description: "Check state buckets, lock tables and the fleet of states with audit backend, set the policy thresholds, suppress accepted findings with a reason and an expiry date, and gate CI on severity."
---

# Audit state backends and suppress findings

`tfcensus audit backend` checks where your state lives. It finds the S3 buckets and lock tables
that your units' backends name, checks their settings, and checks the states themselves: orphans,
copies, old versions and attribute names that look like secrets. Every call is a read, and it
takes no lock. This guide runs an audit, tunes its thresholds, and handles the findings you accept.

## Run the audit

`audit backend` discovers units the way `units` does and reads their state the way `inventory`
does, so it takes the same paths and account flags:

```sh
tfcensus audit backend ~/src/infra/acme-live --profile 'acme-*'
```

This is the output for the `tf-audit` fixture fleet, which has a good, a bad and a legacy bucket:

<<< @/../testdata/golden/audit/tf-audit.txt

The first table has one row per finding, with the check, the severity, the target and the reason.
The bucket and lock table tables summarize what the audit read. The lines at the end list the
suppressed findings and a suppression that expired. The [audit checks](/reference/audit-checks)
page explains each check, what passes and how to fix a finding.

Output never holds a state attribute value. `FL-SECRET` lists attribute names, such as `password`,
and the resource address they are in.

## Find buckets no unit names

A state bucket that no unit uses anymore still holds state, and often secrets. `--discover` lists
every bucket of each account (`s3:ListAllMyBuckets`) and audits those that match a pattern and that
no unit names. Their state keys show up as `FL-ORPHAN`:

```sh
tfcensus audit backend --profile 'acme-*' --discover
tfcensus audit backend --profile 'acme-*' --discover --discover-pattern 'acme-tfstate-*'
```

The patterns come from `--discover-pattern` or `audit.discover_patterns`, and default to
`*tfstate*`, `*terraform-state*` and `*tf-state*`.

## Set the policy

Five checks compare against thresholds under `audit.policy`:

```yaml
audit:
  policy:
    min_terraform_version: "1.10"
    min_provider_versions:
      hashicorp/aws: "5.0"
    noncurrent_days: 30
    stale_lock_hours: 24
    max_state_mb: 20
```

| Key | Check | Default |
|---|---|---|
| `audit.policy.min_terraform_version` | `FL-TFVER` flags states written by an older terraform or OpenTofu. | `1.10` |
| `audit.policy.min_provider_versions` | `FL-PROV` flags a provider in `.terraform.lock.hcl` older than its entry. | none |
| `audit.policy.noncurrent_days` | `S3-LC` wants noncurrent state versions kept at least this many days. | `30` |
| `audit.policy.stale_lock_hours` | `LK-STALE` flags locks older than this. | `24` |
| `audit.policy.max_state_mb` | `FL-SIZE` flags states larger than this many MiB. | `20` |

`min_provider_versions` maps a provider source to a version. It is a map, so it has no environment
variable; the other four have one each, such as `TFCENSUS_AUDIT_POLICY_STALE_LOCK_HOURS`
([configuration](/reference/config#audit-policy-min-terraform-version)).

## Suppress accepted findings

Some findings are known and accepted. `S3-MFA` is a common one, because MFA delete needs the root
user's device. List them under `audit.suppress`:

```yaml
audit:
  suppress:
    - check: S3-MFA
      bucket: acme-audit-*
      reason: MFA delete needs the root user's device
      expires: "2027-06-30"
    - check: "LK-*"
      unit: acme-live/sandbox/*
      reason: sandbox units share a lock table on purpose
```

A finding is suppressed when it matches every field an entry sets:

| Field | Matches |
|---|---|
| `check` | The check ID, or a glob such as `LK-*`. |
| `bucket` | The bucket name, as a glob. |
| `unit` | The unit, as a `repo/path` glob. |
| `key` | The state key or the lock table, as a glob. |
| `reason` | Required. Why the finding is accepted. |
| `expires` | Optional. The last day the entry applies, as a quoted `"YYYY-MM-DD"` date in UTC. |

A suppressed finding is not dropped. Text and Markdown list it after the tables with the
suppression's reason, JSON puts it in a `suppressed` list, and SARIF marks the result with a
suppression, so a reviewer can see what was accepted. After
`expires`, the entry stops matching, the finding comes back, and a warning names the entry:

```text
warning: audit.suppress[1] (check S3-LOG) expired on 2026-10-31 and no longer applies: 1 finding(s) reported again
```

Suppressed findings don't count for `--fail-on`.

## Gate CI on severity

`--fail-on` takes one severity, `info`, `low`, `medium` or `high`. The command exits 3 when an
unsuppressed finding is at or above it. SARIF output goes to GitHub code scanning:

```sh
tfcensus audit backend --profile 'acme-*' --fail-on high --out sarif=reports/audit.sarif --out json=reports/audit.json
```

The exit code is 1 when a check couldn't run, such as a call that was denied, and 4 when every
failure was a credential problem. The summary line says how many checks couldn't run. A denied
call names the action, and the [read-only IAM policy](/reference/iam) grants every one the audit
makes.

`tfcensus notify` posts the JSON report to Slack, an issue or a pull request
([Publish reports](/guides/notify)), and `tfcensus report html` adds it to the
[HTML report](/guides/html-report).
