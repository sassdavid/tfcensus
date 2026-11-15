# tfcensus ADR-0013: No built-in LLM call, agents use MCP

- Status: accepted (2026-11-13, recorded 2026-11-15). Builds on
  [ADR-0012](0012-notify-and-mcp-read-saved-reports.md), whose MCP tools read saved reports.
  The research is in [roadmap-research.md](../roadmap-research.md#llm-features).

## Context
- relgap gets an opt-in `--summarize` that sends extracted release-note entries to a model the
  user configures. The two repos share their build, so the question came up for tfcensus too.
- tfcensus output is already a deterministic summary. Every finding has a `rule` and a `reason`,
  and drift lists attribute paths per resource address.
- The questions a model could add to need attribute values: why a security group drifted, what a
  tag says. tfcensus never prints them, because state holds secrets (risk R7).
- tfcensus identifiers map the organization: account IDs, ARNs, bucket and role names, unit
  paths. Sending them to a provider is a decision for each organization, not a default.
- Trivy, Snyk, Steampipe and Infracost ship an MCP server and no built-in model call. Steampipe
  adds MCP prompts. Prowler, which calls a model, documents that resource names and account IDs
  may reach the provider.
- MCP sampling is deprecated in the 2026-07-28 spec, and Claude Code never supported it. MCP
  prompts are not deprecated.

## Decision
- tfcensus has no command, flag or config key that calls a model, and no LLM client dependency.
- `tfcensus mcp` ships two prompts through the shared `internal/mcpkit`: `triage_drift [unit]`
  and `explain_unmanaged [account] [type]`. Each tells the agent which tools to call, to cite unit
  paths, resource addresses, rule IDs and ARNs as the tools return them, and never to ask the user
  for attribute values, state or plan output.
- `TestMCPPrompts` fetches every prompt and greps its text for the fixture attribute values, as
  the other output tests do. It also checks that every tool a prompt names exists.
- An organization that wants a summary in CI uses [docs/ci/ai-summary.md](../../ci/ai-summary.md):
  an opt-in workflow that sends `tfcensus report drift --format json` to a model the organization
  picks, and lists the identifiers that leave the runner. It offers Bedrock in the organization's
  own account and GitHub Copilot.
- The Copilot job runs the Copilot CLI directly, not through `actions/ai-inference`. v3 of that
  action runs the same CLI but can't turn its tools off or cap AI credits. v2 used GitHub Models,
  which GitHub retired on 2026-07-30. mise installs the CLI from `mise.ai.toml` with the checksum
  in `mise.ai.lock`, as every other workflow installs its tools. The job turns every tool off,
  runs in an empty directory and authenticates with `GITHUB_TOKEN` and `copilot-requests: write`.
- `.claude/rules/siblings.md` lists this as an intended difference from relgap.

## Consequences
- The binary makes no request to a model provider, so an air-gapped or regulated user has nothing
  to turn off.
- The agent and its model belong to the user. tfcensus doesn't choose the provider, the data
  residency or the retention.
- A prompt reaches only clients that support MCP prompts. Claude Code offers them as
  `/mcp__tfcensus__triage_drift` and `/mcp__tfcensus__explain_unmanaged`.
- relgap and tfcensus differ here on purpose. A shared change to `internal/mcpkit` must keep
  working for both.
