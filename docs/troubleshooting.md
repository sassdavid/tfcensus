---
description: Fixes for common tfcensus failures, from expired credentials and unreadable states to scan false positives, a move that stopped halfway and notify errors.
---

# Troubleshooting

Start with the exit code and the notes under the table. Every tfcensus report lists what failed
and why, and the [exit codes](/reference/exit-codes) page says what each code means. When the
notes aren't enough, turn on logging.

## Get more detail

Logs go to stderr, so they never mix with the report on stdout.

```sh
tfcensus scan --profile acme-nonprod -vv 2> scan.log
tfcensus scan --profile acme-nonprod -vvv --log-format json --log-file scan.jsonl
```

`-v` logs at info level, `-vv` at debug and `-vvv` at trace, which adds every AWS request and
child process. Request headers are logged with `Authorization` and `x-amz-security-token`
redacted, and command lines hide the values of flags and variables whose names look secret.
`--log-format json` writes one JSON object per line, and `--log-file` writes the logs to a file.

## Credentials

### `SSO token for profile acme-nonprod is expired or missing`

Sign in again with the command the message gives, such as `aws sso login --profile acme-nonprod`,
or `aws sso login --sso-session <name>` when the profile uses an `sso-session` section.

### `cannot assume <role> from <source>: access denied`

The source credentials may not assume the role. With `--org`, check that `--org-role` (default
`OrganizationAccountAccessRole`) exists in the member account and trusts the management
account. The [accounts guide](/guides/accounts) covers OU roles and `accounts.map`.

### `no profile matches "acme-*"`

The `--profile` glob matched no profile in `~/.aws/config`. Quote the glob so the shell doesn't
expand it, and check `AWS_CONFIG_FILE` if your config is elsewhere.

### `no usable credentials for <source>`

Neither a profile nor environment credentials worked. Without `--profile` and `--org`, tfcensus
uses the default AWS credentials: `AWS_PROFILE`, environment variables or an instance role.

A source whose credentials fail is skipped with a warning, and the run goes on with the others. A
command exits 4 only when every source failed.

## Access denied for one call

A credential that works but lacks a permission shows up per layer or per check, such as
`layer config in 111111111111 eu-west-1: denied` in the scan notes, and the command exits 1. The
rest of the report is still correct, but the types that layer lists are missing from it. Attach
the [read-only policy](/reference/iam), which grants every action tfcensus calls outside `move`.

## doctor

### A tool shows `not found`

terragrunt or terraform isn't on `PATH`. tfcensus reports it without failing. A plain Terraform
repository needs neither for `inventory` and `scan`. A Terragrunt repository needs terragrunt to
list its units, and `drift`, `codify` and `move` run terraform. Point to another binary with
`--tg-path` and `--tf-path`.

### A tool shows `too old`

It is below the minimum on the [supported versions](/supported-versions) page, and `doctor`
exits 1. The table also lists the features the found version is too old for.

### `no AWS Config recorder, so the config layer is off here`, `the Resource Explorer index has no view`

Not errors. `doctor` never creates a recorder, index or view. The scan uses the other layers in
that region, and the notes say what an admin could turn on. The
[aggregator guide](/guides/aggregator) explains when that pays off.

`doctor` caches its probe for 24 hours. After an admin turns something on, run it with `--refresh`.

## Units

### A unit shows `unresolved`

tfcensus couldn't find its backend. The note under the table gives the error from the fast path
and from `terragrunt render`. A configuration that calls `run_cmd` or another function only
terragrunt can evaluate needs terragrunt on `PATH`. A command that fails inside `run_cmd` fails
both ways.

### A function such as `get_env("DEPLOY_ENV")` returns an empty value

tfcensus passes only allow-listed environment variables to terragrunt and terraform: the basics,
proxy and certificate variables, and names that start with `AWS_`, `TF_`, `TG_`, `TERRAGRUNT_`,
`TOFU_`, `OPENTOFU_`, `MISE_`, `GIT_` or `XDG_`. Rename the variable to one of those prefixes,
such as `TG_DEPLOY_ENV` ([terragrunt concept](/concepts/terragrunt)).

### `not generated: run with --generate-stacks to list its units`

The row is a Terragrunt stack whose units aren't generated yet. `--generate-stacks` runs
`terragrunt stack generate` first.

### A unit is missing

Check the path you passed, `discovery.roots`, and any `--filter`. A unit that terragrunt
excludes shows as `excluded by terragrunt` in the notes.

## State

### `backend gcs: tfcensus reads state from s3 only`

tfcensus lists the unit but reads state from `s3` backends only
([how state is read](/concepts/state)).

### `state is encrypted with OpenTofu client-side encryption`

Only serial and lineage are readable. `--decrypt-with-tofu` reads the state with `tofu show
-json` in the unit ([encrypted state guide](/guides/encrypted-state)).

### `unsupported state format version`

The state file isn't version 4, the format terraform 0.12 and later and OpenTofu write.

### A state read fails with access denied

tfcensus reads each state with the credentials of that unit's backend, from the profile or role
the backend configuration names. Check that those credentials can call `s3:GetObject` on the
state key.

## Scan

### Many resources show as `unmanaged` that a state manages

Look at the notes first. A state that couldn't be read leaves every resource it manages
unmatched, and those then show as unmanaged. Fix the read and scan again with `--refresh`. A
warning that a state is encrypted means the same for that state
([encrypted state](/guides/encrypted-state)).

### A resource you know about shows as `unmanaged`

Run `scan --explain` on its ARN. It prints the match result and every rule's outcome. If another
tool or AWS owns it, write a rule ([rules guide](/guides/rules)).

### Many resources show as `undetermined`

Their types have no Terraform mapping ([type coverage](/reference/type-coverage)). They don't
count against coverage. `--types` narrows the scan to the types you care about.

### A resource is missing from the report

No layer listed its type in that region. `--explain-coverage` shows which layer covered each
type ([inventory layers](/concepts/inventory-layers)).

### The scan shows old results

`scan` caches the inventory and the state index for `cache.ttl` (one hour by default). `--refresh`
lists again, and `tfcensus cache clear` deletes the caches ([caches](/concepts/caches)).

### `rule "<name>" is past its expires date and no longer matches`

A user rule expired. Renew the date or delete the rule.

## move

### `override file left from an earlier run`

An interrupted run left `_tfcensus_override.tf` in a unit. Make sure no other run is active,
delete the file, and start again. tfcensus never deletes it for you. While the file is there,
terraform in that unit sees a local backend at its next init and asks whether to migrate the
state. Answer no, then delete the file.

### `the unit's remote_state must use generate`

Add a `generate` block to the `remote_state` in your root configuration, so the local override
can replace the backend.

### `state changed since it was read`

Someone applied a unit during the run. Plan and apply again.

### `state is locked`

terraform or another tfcensus run holds the lock. Wait for it, or remove a stale lock with
`terraform force-unlock` in the unit.

### `has no versioning, so a push couldn't be rolled back`

`move apply` refuses a unit whose state bucket has no versioning (rule MV-VERSIONING). Turn on
versioning for the bucket first.

### `move apply` exits 2 in CI

It doesn't prompt without a terminal, with `CI` set or with `--no-input`. Pass `--yes`.

### The apply stopped halfway

Apply prints the units it already pushed. The resources are then in both the destination and the
source states, `move status` shows the block as `partial`, and apply refuses to run it again.
Run `move rollback` for the block, or finish the remaining pushes by hand and run
`move mark-applied` so the history matches.

### Rollback stops because a state changed after the push

Restoring it would drop that change. Look at what changed in the S3 object versions, then pass
`--force` if the old version is still what you want. Revert the code branch as well.

### Verification passed, but the destination plans changes after the merge

The plan ran on a branch whose configuration differs from what was merged. Plan the units again
and fix the code.

The [move guide](/guides/move) explains each step.

## notify

| Code | Meaning |
|---|---|
| 0 | Every notifier succeeded. |
| 1 | A notifier failed, such as a network error or an HTTP error from the API. |
| 2 | A notifier isn't configured (no repository, pull request or token), or a `--from` file isn't a scan, drift or audit report. |
| 4 | Every failure was a refused token (HTTP 401 or 403). |

Notifiers run one after the other, and a failure in one doesn't stop the next. Behind a TLS proxy,
set `SSL_CERT_FILE` to a PEM file with the proxy's CA.

### An issue was closed by mistake

Reopen it. The next run that finds the scope open updates it, and one that finds it resolved
closes it again.

### Labels were removed from an issue

tfcensus no longer sees it and opens a new one. Close the old one by hand.

### Too many issues at once

Lower `max_open`, or set `summary: true` to get one overview issue, and close the extra issues by
hand. tfcensus doesn't close issues because of `max_open`.

### Stop all notifications

Remove the notifier from `notify.enabled`, or drop the `notify` step. Existing issues stay open
until you close them.

The [notify guide](/guides/notify) explains the lifecycle.

## Configuration

An unknown key in a config file is an error, and the command exits 2. `tfcensus config validate`
checks a file, and `tfcensus config show --sources` prints every effective setting and the layer
it came from. An unknown `TFCENSUS_*` environment variable only warns. Maps and lists of objects
can't be set from the environment ([environment variables](/reference/environment)).

## Interrupted runs

Ctrl-C stops a run and exits 130. A read-only command leaves nothing behind. `move` stops between
steps, and an interrupted push is a halfway apply as described above.
