---
description: Register tfcensus mcp in Claude Code or another MCP client, give it saved reports, and ask about units, unmanaged resources, whois, audit findings and drift.
---

# Use tfcensus from Claude Code or another MCP client

`tfcensus mcp` runs a Model Context Protocol server on stdin and stdout. An agent such as Claude
Code then answers questions about your fleet from reports that earlier tfcensus runs saved: which
units exist, what they manage, which state manages an ARN, what is unmanaged, which backend
findings are open and what drifted.

The tools read files only. They never call AWS, start terragrunt or terraform, read a state, run a
plan or write a file, and `move apply`, `move rollback` and `move mark-applied` are not tools. The
answers are as fresh as the files you give the server.

## Collect the data

Run the commands that collect, and save their output. Each file feeds one or more tools:

```sh
tfcensus inventory ~/src/infra/acme-live --profile acme-nonprod --save-index ~/.cache/tfcensus/index.json.zst
tfcensus scan ~/src/infra/acme-live --profile acme-nonprod --index ~/.cache/tfcensus/index.json.zst --format json > ~/.cache/tfcensus/scan.json
tfcensus audit backend ~/src/infra/acme-live --format json > ~/.cache/tfcensus/audit.json
tfcensus drift ~/src/infra/acme-live --format json > ~/.cache/tfcensus/drift.json
```

| Flag of `tfcensus mcp` | File | Tools |
|---|---|---|
| `--index` | the state index from `inventory --save-index` | `units`, `inventory`, `whois` |
| `--scan` | scan JSON | `scan_summary` |
| `--audit` | audit JSON | `audit_findings` |
| `--drift` | drift JSON | `drift_summary` |

Give only the files you have. A tool whose file wasn't given fails with a message that names the
flag. `scan_summary` without `--scan` reads the newest snapshot in the scan cache and applies the
config's ownership rules, with a warning when the snapshot is older than `cache.ttl`. The server
reads the files again on every call, so a new run is picked up without a restart.

## Register the server in Claude Code

Register it in Claude Code's local scope, so only the current project sees it:

```sh
claude mcp add --transport stdio --scope local tfcensus -- \
  tfcensus mcp --index ~/.cache/tfcensus/index.json.zst --scan ~/.cache/tfcensus/scan.json \
  --audit ~/.cache/tfcensus/audit.json --drift ~/.cache/tfcensus/drift.json
claude mcp get tfcensus
```

`claude mcp get` shows `Status: Connected` once Claude Code can start the server. Use
`--scope user` to make it available in every project.

## Other MCP clients

Any client that starts stdio servers works. Most take a JSON entry with the command and its
arguments:

```json
{
  "mcpServers": {
    "tfcensus": {
      "command": "tfcensus",
      "args": ["mcp", "--index", "/home/you/.cache/tfcensus/index.json.zst", "--scan", "/home/you/.cache/tfcensus/scan.json"]
    }
  }
}
```

The server writes its logs to stderr, so they don't mix with the protocol on stdout. Add `-v` to
the arguments for more detail.

## Ask questions

Ask in plain words. The agent picks the tools:

- "How many unmanaged resources does the last scan have, and which types are most common?"
- "Which unit manages arn:aws:s3:::acme-logs, and is it managed twice?"
- "Which high-severity backend findings are open in account 111111111111?"
- "Which units drifted, and what changed in each?"

Every tool returns the JSON envelope that the matching `tfcensus report` command prints with
`--format json`, so you can check an answer in the terminal. `tfcensus report scan --from
scan.json --category unmanaged`, for example, prints what `scan_summary` saw. The
[MCP reference](/reference/mcp) lists each tool's arguments.

To try one question without a chat, run Claude Code headless with only the tfcensus tools allowed:

```sh
claude -p "How many unmanaged resources are there?" --allowedTools mcp__tfcensus__scan_summary
```

## Prompts

The server offers two prompts that tell the agent which tools to call and to cite unit paths,
addresses, rule IDs and ARNs:

- `triage_drift [unit]` sorts drift into revert, adopt and investigate.
- `explain_unmanaged [account] [type]` explains the unmanaged resources of the last scan.

Claude Code offers them as `/mcp__tfcensus__triage_drift` and `/mcp__tfcensus__explain_unmanaged`.
Neither asks for attribute values.

## Large results

A result over `--max-result-bytes` (default 50,000, Claude Code's limit for a result it shows to
the model) is split into pages. The envelope then has a `page` field, and the agent passes
`page.next_cursor` back to the same tool for the rest. Double-managed and unmanaged resources and
high-severity findings come first. A cursor lasts 15 minutes after its last use.

## What the agent sees

The tools return identifiers, never state attribute values: account IDs, ARNs, unit paths,
addresses, types, rule IDs and reasons. Those identifiers still map your organization, and the
agent sends what it reads to its model provider. Decide which files to give the server with that
in mind. [Why there is no built-in LLM](/concepts/no-llm) explains why tfcensus leaves that choice
to you, and [Add an AI summary to CI](/guides/ai-summary) shows the same idea in a workflow.
