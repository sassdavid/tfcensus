---
description: Check drift on a schedule and on pull requests with tfcensus-action, upload SARIF to code scanning and keep one drift comment per pull request.
---

# Check drift in CI with tfcensus-action

This guide sets up two GitHub Actions workflows for a Terragrunt live repository. The first checks
drift on a schedule, uploads the result to code scanning and fails the job when a unit drifted.
The second keeps one drift comment on each pull request. Both use
[tfcensus-action](https://github.com/sassdavid/tfcensus-action), which installs a verified tfcensus
release, and an AWS role with the [read-only policy](/reference/iam).

## How the workflows work

terragrunt plans every unit once, with `plan -refresh-only -lock=false`, and writes the plans to a
directory on the runner. One tfcensus run reads those plans with `--from-json-out-dir`, so
terragrunt runs once per unit and tfcensus starts no plan of its own. That run writes JSON,
Markdown, SARIF and GitHub annotations through repeatable `--out format=path` flags, then gates
the job with `--fail-on`.

The plans hold state values, so they stay on the runner and are never uploaded. The reports name
attribute paths only.

## Before you start

- The live repository's `mise.toml` pins terragrunt and terraform, so the runner uses the same
  versions as your laptop.
- An IAM role trusts GitHub OIDC for this repository and has the read-only policy. Set its ARN as
  the repository variable `DRIFT_ROLE_ARN`.
- `.tfcensus.yaml` in the repository holds your [drift noise rules](/guides/drift-noise), if any.

## Scheduled drift check

Copy this file to `.github/workflows/drift.yml` in the live repository. It is
`docs/ci/drift.yml` in the tfcensus repository, where CI lints it on every change:

<<< @/ci/drift.yml

The action runs `tfcensus drift` in report mode. It passes `from` as `--from-json-out-dir` and
`<from>/report.json` as `--report-file`, so units that failed before writing a plan are reported
as failed. `fail-on: drift,errors` exits 3 when a unit drifted or failed. The action uploads the
SARIF and writes the step summary before the step fails, so code scanning shows what failed the
job.

The alerts are under Security, Code scanning, in the category `tfcensus-drift`. Each alert points
at the unit's `terragrunt.hcl`.

## Pull request comment

Copy this file to `.github/workflows/drift-pr.yml`. It is `docs/ci/drift-pr.yml` in the tfcensus
repository:

<<< @/ci/drift-pr.yml

This workflow calls tfcensus directly, so you see each step. The `drift` step writes annotations,
JSON and Markdown in one run. `tfcensus notify --notify github-pr-comment` then keeps one comment
on the pull request: a re-run edits it only when the report changed, and drift that is gone turns
it into "Nothing to report". [Publish reports with notify](/guides/notify#github-pull-request-comment)
explains the marker and the token.

The action's comment mode does the same in one step:

```yaml
- uses: sassdavid/tfcensus-action@f2cc1dccce5a5de868ff2f3f50dbec2603d197c7 # v1.0.1
  with:
    mode: comment
    from: ${{ runner.temp }}/plans
    sarif: false
```

A pull request from a fork or from Dependabot gets no OIDC token and a read-only `GITHUB_TOKEN`.
Without AWS credentials there is nothing to plan, so `drift-pr.yml` skips such pull requests with
`if: github.event.pull_request.head.repo.full_name == github.repository`. When comment mode does
run without a write token, the action skips the comment, says why in a notice and writes the
report to the step summary.

## Long reports

Markdown is cut at a table row or section once it reaches `--md-max-chars` (60,000 characters by
default, under GitHub's limit of 65,536 characters for a comment), with a footer that says what was
cut. The JSON and SARIF files are never cut.

## Exit codes in CI

| Code | In these workflows |
|---|---|
| 0 | No unit drifted and no unit failed, or nothing matched `--fail-on`. |
| 1 | A unit or a repository failed and no gate matched. The report lists it. |
| 2 | tfcensus couldn't read the plans, or a flag or the config is wrong. |
| 3 | A `--fail-on` gate matched. The files, the SARIF upload and the comment are done first. |
| 4 | Credentials were refused, such as a read-only token for the comment. |

All codes are on the [exit codes page](/reference/exit-codes), and the gate values on the
[drift CLI page](/cli/drift#fail-on-values).

## tfcensus-action inputs

Pin the action by commit SHA. Each `vX.Y.Z` release is immutable, and the `v1` tag moves to the
newest 1.x release. The inputs and outputs below are those of v1.

| Input | Default | Meaning |
|---|---|---|
| `version` | the version the action release pins | tfcensus version to install: `X.Y.Z`, `vX.Y.Z` or `latest`. |
| `verify` | `attestation` | The check after sha256 against `checksums.txt`. `attestation` runs `gh attestation verify` pinned to tfcensus's release workflow, `cosign` runs `cosign verify-blob` on `checksums.txt`, and `checksum` adds nothing. |
| `mode` | `install` | `install` puts tfcensus on `PATH`. `report` runs the command once, writes JSON, Markdown, SARIF and annotations, uploads the SARIF and writes the step summary. `comment` does the same on a `pull_request` event and keeps one comment on the pull request. |
| `command` | `drift` | The command report and comment mode run. Both take `drift` only. |
| `args` | | More arguments for the command, one per line. |
| `from` | | The `--json-out-dir` of an earlier `terragrunt run --all` step, passed as `--from-json-out-dir`, plus `--report-file` for its `report.json` when that file exists and `args` has none. Without it, tfcensus runs terragrunt itself. |
| `fail-on` | | `--fail-on` values, separated by commas or newlines. A match exits 3 and fails the step after the files, the SARIF upload and the comment. |
| `sarif` | `true` | Upload the SARIF file to code scanning. The job needs `security-events: write`. |
| `step-summary` | `true` | Append the Markdown report to the step summary. |
| `github-token` | <code v-pre>${{ github.token }}</code> | Token for `gh attestation verify`, tfcensus's GitHub API calls, the SARIF upload and the comment. |
| `working-directory` | `.` | Directory the command runs in. SARIF locations are relative to it, and the SARIF category ends with `/<directory>` when it isn't the repository root. |

A bad input exits 2 before the install: an unknown mode or command, or `command`, `args`, `from`
or `fail-on` in install mode. The action never takes AWS credentials, so run
`aws-actions/configure-aws-credentials` before it.

## tfcensus-action outputs

| Output | Meaning |
|---|---|
| `version` | The installed tfcensus version, without a leading `v`. |
| `path` | Absolute path of the tfcensus binary. |
| `exit-code` | Exit code of the command in report and comment mode: 0, 1, 2, 3 or 4. |
| `json-file` | Absolute path of the JSON report, empty when the command wrote none. |
| `sarif-file` | Absolute path of the SARIF file, empty when the command writes no SARIF. |
| `md-file` | Absolute path of the Markdown report, empty when the command wrote none. |

The step fails with the command's exit code. In comment mode, a refused comment fails it with
notify's code when the command passed.

## Next steps

- [Hide drift noise](/guides/drift-noise) such as `tags_all` updates before you turn the gate on.
- [Publish reports with notify](/guides/notify) to send drift to Slack or GitHub issues as well.
- [Add an AI summary to CI](/guides/ai-summary) to have a model you pick summarize the drift report.
