## tfcensus notify

Publish saved scan, drift and audit reports to Slack, GitHub, Bitbucket or files

### Synopsis

notify reads the JSON that scan, drift and audit print with --format json and publishes it.
It never collects anything itself.

  slack                 one message per report to an incoming webhook, with counts and the top
                        findings; sent only when there is something to report (notify.slack.when)
  github-issues         one issue per scope: per unit for drift, per account for scan and audit.
                        A re-run updates the body only when it changed, and closes the issue when
                        the run covered the scope and found nothing. notify.github_issues.max_open
                        caps new issues
  github-pr-comment     one comment per report on a GitHub pull request, edited in place. The
                        pull request comes from GITHUB_EVENT_PATH; a report with nothing open
                        turns the comment into "Nothing to report"
  bitbucket-pr-comment  one comment per report on a Bitbucket Cloud pull request, edited in place
  file                  tfcensus-<kind>.md and .json in notify.file.dir, rewritten only on change

Every issue body and comment starts with a hidden marker that names the report kind, the scope
and a hash of the body, so runs find their own issues and skip unchanged ones. Only issues
with the tfcensus label are read.

Secrets come from the environment only: the variables named by notify.slack.webhook_env,
notify.github_issues.token_env, notify.github_pr_comment.token_env and
notify.bitbucket_pr_comment.token_env. They are never
logged; -vvv shows request headers with Authorization redacted.

--dry-run-notify reads the existing issues and comments with GET requests and prints what
would change, writing nothing.

Exit codes: 0 when every notifier succeeded, 4 when every failure was a refused token, and 1
for any other failure.

```
tfcensus notify [flags]
```

### Examples

```
  tfcensus drift --format json > drift.json; tfcensus notify --from drift.json --notify github-issues
  tfcensus notify --from scan.json --from audit.json --dry-run-notify
  tfcensus notify --from drift.json --notify github-pr-comment   # in a pull_request workflow
  GITHUB_TOKEN=... tfcensus notify --from drift.json --notify github-issues,slack
```

### Options

```
      --dry-run-notify[=true|false]   print what would change; read with GET only and write nothing
      --from stringArray              scan, drift or audit JSON file (repeatable)
  -h, --help[=true|false]             help for notify
      --notify strings                notifiers to run instead of notify.enabled: slack, github-issues, github-pr-comment, bitbucket-pr-comment or file
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

