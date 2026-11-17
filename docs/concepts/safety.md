---
description: What tfcensus reads, the three commands that write, why it prints identifiers and never state values, and what reaches child processes, agents and models.
---

# Read-only by default

tfcensus is built so that a security team can run it with a read-only role. This page says what it
reads, what it writes, what it prints and what leaves your machine.

## Three commands write

Every command except `move apply`, `move rollback` and `move mark-applied` works with the
[read-only IAM policy](/reference/iam) and calls no AWS API that writes. Those three write to S3
(state and the migration history next to it) and to the DynamoDB lock table, and their CLI pages
say so at the top. They don't create, change or delete any other AWS resource.

The writing commands guard each write:

- `move apply` refuses a unit whose state bucket has no versioning (MV-VERSIONING), because a push
  couldn't be rolled back.
- It asks you to type a confirmation, or needs `--yes`. It doesn't prompt without a terminal, with
  `CI` set or with `--no-input`, and then it exits 2 unless `--yes` is given.
- It records the S3 version of every state before it pushes, so `move rollback` can restore it.
- It checks that no state changed between its read and its push, and stops if one did.
- It logs every write at info level.

## Reading takes no lock

tfcensus reads state objects from S3 directly, with the credentials of each unit's backend. It
takes no state lock, so it never blocks a `terraform apply`, and it never writes a state back.
`doctor` never creates a Resource Explorer index, a view or a Config recorder. Its notes say what an
admin could turn on.

## Identifiers, never values

State files hold secrets: passwords, keys, connection strings. tfcensus prints only identifiers:
account IDs, ARNs, resource IDs, unit paths, resource addresses, types, tags and attribute names. No
output format, log line, cache file, saved index or MCP answer carries a state attribute value.

`drift` shows the paths of the attributes that changed. The one exception is
`drift --show-attributes`, which you turn on: it adds the before and after values from the plan,
and even then sensitive values show as `(sensitive)`. The tests run
`inventory`, `scan`, `audit backend` and `drift` in every format over fixture states that hold
marker secrets, and fail if a marker shows up in any output, debug log or saved file.

## Child processes

tfcensus runs terragrunt, terraform and OpenTofu for discovery, `drift`, `move` and `codify`. It
passes them an allowlist of environment variables: `PATH`, `HOME` and the other basics, the
locale, proxy and certificate variables, and every variable that starts with `AWS_`, `TF_`,
`TG_`, `TERRAGRUNT_`, `TOFU_`, `OPENTOFU_`, `MISE_`, `GIT_` or `XDG_`. A terragrunt configuration
that reads another variable, such as `get_env("DEPLOY_ENV")`, gets an empty value. Logged command
lines hide the values of flags and variables whose names look secret, such as `--token` or
`db_password=`.

## Network

tfcensus calls the AWS APIs of the accounts you select and, for `notify`, the Slack, GitHub or
Bitbucket endpoints you configure. It sends no telemetry. The HTML report from `report html` loads
nothing: its Content-Security-Policy blocks every request.

## Agents

`tfcensus mcp` serves saved reports to an agent such as Claude Code. Its tools read files that
earlier runs wrote. They never call AWS, run terragrunt or terraform, read a state or write a file,
and the writing move commands are not tools.

## No built-in model

tfcensus has no command, flag or config key that sends data to a language model. Its findings
already carry a rule and a reason, so a model adds little, and the questions a model could answer
better need the state values tfcensus never prints. Its identifiers also map your organization:
account IDs, ARNs, bucket and role names. Sending them to a model provider is your organization's
decision, so tfcensus doesn't make it for you.

An agent can use the MCP prompts `triage_drift` and `explain_unmanaged`. To summarize drift in CI,
[AI summary of drift in CI](/ci/ai-summary) shows an opt-in job and lists what leaves the runner.
