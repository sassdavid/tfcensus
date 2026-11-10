# ADR-0011: MCP server mode with the official Go SDK

- Status: accepted (2026-11-09)

## Context
tfcensus produces exactly the kind of structured answer an LLM agent needs: "which units manage this ARN?" /
"what is unmanaged in account X?"

The author works daily in Claude Code with MCP servers (AWS, Atlassian, Bitbucket, context7).

## Decision
- `tfcensus mcp` is a subcommand: a stdio MCP server built with **`github.com/modelcontextprotocol/go-sdk` v1.8.0**. terragrunt's `mcp` command uses this official SDK. mark3labs/mcp-go is more popular, but none of the surveyed CLIs use it.
- MCP tools are thin wrappers over the same domain functions as the CLI, and return the same JSON envelope as `--format json`.
- Read-only. tfcensus exposes **no** state-mutating MCP tool (`move apply` is never available over MCP).
- Tool descriptions are short and specific. Results are paginated and size-capped (the LLM context budget), with a `cursor` for more.

## Consequences
- MCP lands in M10, after the JSON envelope is stable.
- Tests: an in-process client from the SDK calls each tool and compares the output against the same golden files as `--format json`.
