# AI summary of drift in CI

tfcensus has no built-in LLM call ([plan ADR-0013](../plan/adr/0013-no-built-in-llm.md)). Its
findings are already exact, and its identifiers map your organization, so sending them to a model
is your decision. [ai-summary.yml](ai-summary.yml) is the drift workflow from
[drift.yml](drift.yml) with two opt-in jobs. One of them sends `tfcensus report drift --format json`
to a model your organization picks and appends the answer to the job summary.

Copy `ai-summary.yml` to `.github/workflows/drift.yml` in your live repository, in place of
`drift.yml`. Nothing calls a model until you set the repository variable `AI_SUMMARY`:

| `AI_SUMMARY` | Job that runs | Model |
|---|---|---|
| unset | none | none |
| `bedrock` | `ai-summary-bedrock` | Amazon Bedrock in your own AWS account |
| `copilot` | `ai-summary-copilot` | GitHub Copilot, billed to your organization |

For an agent on your laptop, use the MCP prompts `triage_drift` and `explain_unmanaged` of
`tfcensus mcp` instead. They need no workflow at all.

## What leaves the runner

The drift job writes `tfcensus report drift --format json`, cuts it at 100,000 bytes and hands it
to the AI job as a one-day artifact. That JSON contains:

- unit paths, which name your repositories, environments, accounts and regions in the directory
  layout
- AWS account IDs and regions of the units
- resource addresses, including module names and `for_each` or `count` keys
- TF resource types and the drift action (update, delete)
- attribute paths, including map keys such as tag names
- names of the outputs that changed
- the reason a unit failed, as terragrunt reported it, which can quote an ARN, a bucket or a role
- counts per status, the warnings, the tfcensus version and the time of the run

It never contains attribute values (before and after), state, plan JSON, terragrunt or terraform
logs, source code or credentials. `report drift` drops `before` and `after` even when the drift
run used `--show-attributes`. The plans stay on the drift runner.

## Bedrock

The request goes to Bedrock in an AWS account you choose, through the Converse API. AWS documents
that Bedrock doesn't store prompts or answers and doesn't share them with model providers
([Bedrock data protection](https://docs.aws.amazon.com/bedrock/latest/userguide/data-protection.html)).
Some models require a data retention mode; check
[data retention](https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html) for the
model you pick.

1. Pick a model and its inference profile ID in your region:
   `aws bedrock list-inference-profiles --region eu-west-1`. Claude Haiku 5.5 is a good default
   for this size of input. Amazon Nova Micro costs a third as much but summarizes long JSON less
   well.
2. Create an IAM role for GitHub OIDC that trusts only this repository's default branch
   ([GitHub's guide](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-aws)).
   Its only permission is `bedrock:InvokeModel` on that profile and on the model it routes to:

   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": "bedrock:InvokeModel",
         "Resource": "arn:aws:bedrock:eu-west-1:111111111111:inference-profile/PROFILE_ID"
       },
       {
         "Effect": "Allow",
         "Action": "bedrock:InvokeModel",
         "Resource": "arn:aws:bedrock:*::foundation-model/MODEL_ID",
         "Condition": {
           "StringLike": {
             "bedrock:InferenceProfileArn": "arn:aws:bedrock:eu-west-1:111111111111:inference-profile/PROFILE_ID"
           }
         }
       }
     ]
   }
   ```

   A cross-region profile routes to the model in other regions, which is why the second statement
   has a wildcard region and the profile condition
   ([inference profile prerequisites](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-profiles-prereq.html)).
3. Set the repository variables `AI_SUMMARY=bedrock`, `AI_ROLE_ARN`, `AI_REGION` and
   `AI_MODEL_ID` (the profile ID).

Use a separate role from `DRIFT_ROLE_ARN`. The drift role reads state; the AI role can only call
the model.

## Copilot

The job runs `copilot -p` with the job's `GITHUB_TOKEN` and `copilot-requests: write`, so it needs
no personal access token
([Copilot CLI in Actions](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli-in-actions)).
Requests are billed to the organization under its Copilot plan and data terms. mise installs the
CLI from a [config environment](https://mise.jdx.dev/configuration/environments.html), so only
jobs with `MISE_ENV=ai` load it. The drift job and your laptop never install it.

1. In the organization's Copilot policies, check that "Allow use of Copilot CLI billed to the
   organization" is on. It is on by default when the Copilot CLI is enabled.
2. In the live repository, add the CLI to `mise.ai.toml` and lock it for the runner:

   ```sh
   mise use --env ai copilot@1.0.105
   MISE_ENV=ai mise lock --platform linux-x64
   git add mise.ai.toml mise.ai.lock
   ```

   `mise.ai.lock` records the download URL and sha256 of the release asset, and the job installs
   with `mise install --locked`. Renovate's mise manager reads `mise.ai.toml` and updates
   `mise.ai.lock` with it.
3. Set the repository variable `AI_SUMMARY=copilot`. `AI_MODEL_ID` is optional; without it the
   CLI picks the model (`auto`).

The job checks out only `mise.toml`, `mise.lock`, `mise.ai.toml` and `mise.ai.lock`, and sets
`MISE_ENABLE_TOOLS=copilot`, so it installs nothing else from them.

The CLI is an agent that can read files and run commands. The job turns every tool off:
`--available-tools=ask_user` leaves one tool and `--no-ask-user` disables it. It also skips
`AGENTS.md` and other custom instructions, disables the built-in MCP servers and runs in an empty
directory outside the checkout. `--max-ai-credits=30` caps the cost of the answer.

`actions/ai-inference` v3 calls the same CLI, but it can't turn its tools off or pass a credit cap,
and its default model `gpt-4.1` is retired. Version 2 used GitHub Models, which GitHub retired on
2026-07-30. That's why the job calls the CLI directly.

## Cost

One run sends about 10,000 input tokens for a mid-sized fleet and gets up to 1,024 back. Prices
checked on 2026-11-15:

| Model | Price per 1M tokens (input / output) | One run |
|---|---|---|
| Bedrock, Amazon Nova Micro | $0.035 / $0.14 (us-east-1) | about $0.0005 |
| Bedrock, Claude Haiku 5.5 | $0.10 / $0.50 (global profile; regional about 10% more) | about $0.0015 |
| Copilot, Claude Haiku 5.5 | $0.10 / $0.50 in AI credits of $0.01 | about 0.15 credits, plus the CLI's own system prompt |
| Copilot, Claude Sonnet 5.5 | $2.00 / $10.00 in AI credits | about 3 credits, plus the CLI's own system prompt |

Bedrock figures are from the AWS Price List API, Copilot figures from GitHub's Copilot models and
pricing table. Bedrock has no permanent free tier. A Copilot plan stops serving requests when its
monthly credits run out, unless the organization has set a budget for more.

## Guardrails

- The answer is advisory. The drift job gates the run with `fail-on`, and the AI jobs have
  `continue-on-error`, a 5-minute timeout and no write permission, so a model error, timeout or
  odd answer changes no status, SARIF result or comment.
- The report goes in `<drift_report>` tags, and the instructions say to treat it as untrusted data.
  Unit paths and failure reasons come from your repository and from terragrunt, so a crafted
  directory name could try to steer the model.
- The model gets no tools, `temperature` 0 and at most 1,024 output tokens on Bedrock. The summary
  shows at most 20,000 bytes of the answer, under a heading that says it is LLM-generated.
- The instructions ask the model to cite unit paths and resource addresses as the report writes
  them. Check a claim against the drift report before you act on it.
