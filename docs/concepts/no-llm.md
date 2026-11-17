---
description: Why tfcensus makes no call to a language model, and the two ways to use one with its reports anyway, an MCP agent or an opt-in CI job, with you choosing the provider.
---

# Why there is no built-in LLM

tfcensus has no command, flag or config key that sends data to a language model, and the binary
holds no model client. The [safety model](/concepts/safety#no-built-in-model) states that rule.
This page gives the reasons and shows how to bring your own model when you want one.

## The reports are already summaries

Every `scan` finding and every `audit backend` finding carries a `rule` and a `reason`, such as
`pack/aws-defaults/default-vpc` and a sentence that says why a default VPC isn't a finding. `drift`
groups identical changes across units and lists the attribute paths that changed. The same input
always gives the same report, so a CI gate or a diff between two runs means something. A model
would restate these fields in other words, and could get them wrong.

## The useful questions need values tfcensus never prints

The questions a model could help with are "why did this security group drift?" or "what is this
bucket for?". They need attribute values: the rule that changed, the tag that names an owner. State
files hold secrets next to those values, so tfcensus prints identifiers and never values. A model
that only gets tfcensus output gets the same identifiers you see.

## Identifiers still map your organization

The identifiers tfcensus does print are account IDs, ARNs, bucket and role names, unit paths and
resource addresses. Together they describe your accounts and how you deploy. Sending them to a model
provider is a decision about the provider, the region where it processes data and how long it keeps
it. That decision belongs to your organization, so tfcensus offers no switch for it. An air-gapped
or regulated environment has nothing to turn off.

relgap, the sibling tool, does have an opt-in `--summarize`. It sends extracted release-note
entries, which are public text from the projects' release pages, not data about your accounts.

## Bring your own model

Two setups send tfcensus output to a model you choose. In both, you pick the provider and see what
leaves your machine or runner.

### An agent over MCP

`tfcensus mcp` serves saved reports to an agent such as Claude Code. Its tools read the files
earlier runs wrote. They never call AWS, run terraform or read a state, and none of them writes.
The agent's model belongs to you.

The server also offers two prompts, which clients that support MCP prompts show as commands.
Claude Code shows them as `/mcp__tfcensus__triage_drift` and `/mcp__tfcensus__explain_unmanaged`:

| Prompt | Arguments | What it asks the agent to do |
|---|---|---|
| `triage_drift` | `unit` (optional glob) | sort drifted and failed units into revert, adopt and investigate |
| `explain_unmanaged` | `account`, `type` (both optional) | group unmanaged resources by rule and propose an import, an ownership rule or a deletion review |

Each prompt names the tools the agent should call, tells it to cite unit paths, resource addresses,
rule IDs and ARNs as the tools return them, and tells it never to ask you for attribute values,
state or plan output. The tests fetch every prompt and fail if a fixture secret appears in its
text.

[Use tfcensus from an MCP client](/guides/mcp) sets this up, and the
[MCP reference](/reference/mcp) lists every tool and prompt.

### A summary job in CI

[Add an AI summary to CI](/guides/ai-summary) shows an opt-in workflow job that sends the drift
report JSON to Amazon Bedrock in your own AWS account, or to GitHub Copilot, and appends the answer
to the job summary.
The page lists every identifier that leaves the runner, so you can decide before you turn it on.
