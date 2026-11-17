---
description: Short answers about what tfcensus changes, which tools and backends it needs, how it treats secrets, and how it fits into CI and agents.
---

# FAQ

## Does tfcensus change anything in AWS?

No, except for three commands you run on purpose. `move apply`, `move rollback` and
`move mark-applied` write state, the migration history and the lock table. Every other command
works with the [read-only IAM policy](/reference/iam), takes no state lock and creates nothing,
not even a Resource Explorer view or a Config recorder. The [safety model](/concepts/safety) has
the details.

## Will it print my secrets?

No. State files hold passwords and keys, so tfcensus prints only identifiers: account IDs, ARNs,
resource IDs, addresses, types, tags and attribute names. No output format, log, cache or MCP answer
carries a state attribute value. The one opt-in exception is `drift --show-attributes`, and even
then sensitive values show as `(sensitive)`.

## Does it need terraform or terragrunt?

It depends on the command. `inventory` and `scan` on a plain Terraform repository read state
directly from S3 and need neither. A Terragrunt repository needs terragrunt to list its units.
`drift`, `codify` and `move` run terraform or OpenTofu in the units. `doctor` checks what is
installed, and the [supported versions](/supported-versions) page lists the minimums.

## Does it work with OpenTofu?

Yes. Pass `--tf-path tofu` or set `binaries.terraform`. tfcensus always tells terragrunt which
binary to run, because terragrunt picks tofu by default. A state that OpenTofu encrypts on the
client side needs `--decrypt-with-tofu` ([encrypted state](/guides/encrypted-state)).

## Which state backends does it read?

`s3` only. Units with another backend still show up in `units`, but their state isn't read, and
the report says so. tfcensus reads each state with the credentials of that unit's backend
([how state is read](/concepts/state)).

## Does it work for Azure or Google Cloud?

No. tfcensus lists live resources in AWS only, and resources of other providers in a state are
counted but not matched.

## Does it take the state lock?

No. Reading needs no lock, so a run never blocks a `terraform apply`. Only the `move` commands
that write take the lock.

## Why is a resource `undetermined`?

Its type has no mapping to a Terraform type, so tfcensus can't tell whether a state manages it,
and it doesn't guess. The [type mapping coverage](/reference/type-coverage) page lists the mapped
types. Undetermined resources don't count against coverage.

## How do I hide resources I already know about?

Write an ownership rule with category `ignored`, or `other-iac` or `aws-managed` when someone else
owns them. Give it a `reason`, and an `expires` date for a temporary exception
([rules guide](/guides/rules)).

## Why does a scan take long, and how do I speed it up?

Most of the time goes into listing resources, region by region. An AWS Config aggregator lists
every account and region in one query ([aggregator guide](/guides/aggregator)). A scan also caches
its inventory for an hour, so a second run with other rules or another output format makes no AWS
calls ([caches](/concepts/caches)).

## Can I run it in CI?

Yes. Report commands take `--out format=path` more than once, so one run writes JSON, SARIF,
Markdown and GitHub annotations. `--fail-on` sets exit code 3 when the report has findings.
[tfcensus-action](/guides/drift-ci) runs it in GitHub Actions, and [notify](/guides/notify)
posts the results to Slack, GitHub or Bitbucket.

## Does it send data anywhere?

Only to the AWS APIs of the accounts you select and to the notify targets you configure. It sends
no telemetry, and it has no built-in language model ([why](/concepts/no-llm)). The HTML report
loads nothing from the network.

## Can an agent use it?

Yes. `tfcensus mcp` serves saved reports to Claude Code or another MCP client. Its tools only read
files that earlier runs wrote, and the writing commands aren't exposed ([MCP guide](/guides/mcp)).

## Is it stable?

tfcensus is before 1.0, so commands and output can still change. The JSON output carries a
`schema_version`, and the [changelog](/changelog) lists every change.

## Can relgap read its inventory?

Yes. relgap's `drift --from-tfcensus` reads the JSON of `tfcensus inventory --format json` and
compares the Terraform and provider versions across units. relgap's
[guide](https://relgap.sasskovacs.dev/guides/from-tfcensus) shows how.
