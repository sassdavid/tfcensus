# Engineering foundation

Language, stack, conventions and release pipeline for tfcensus. The sibling tool
[relgap](https://github.com/sassdavid/relgap) keeps its own copy of this foundation; the two are aligned by
convention, not by shared code ([integration](../plan/integration.md)). Docs index: [../plan/README.md](../plan/README.md).

| File | What it covers |
|---|---|
| [engineering-standards.md](engineering-standards.md) | layout, control flow, errors and exit codes, output contract, config, logging, security |
| [stack.md](stack.md) | every chosen module with version and reasoning |
| [ci-and-release.md](ci-and-release.md) | mise tasks, GitHub Actions, goreleaser, distribution |
| [version-policy.md](version-policy.md) | supported versions of Go, terragrunt, terraform, opentofu, aws provider; feature gating |

## Decisions (ADRs)

| ADR | Decision |
|---|---|
| [0001](adr/0001-language-go.md) | Go (Rust considered) |
| [0002](adr/0002-cli-cobra.md) | cobra, commands built by constructor functions |
| [0003](adr/0003-config-koanf.md) | koanf layered config + JSON Schema validation |
| [0004](adr/0004-logging.md) | log/slog + tint; zap evaluated and rejected |
| [0005](adr/0005-http-and-api-clients.md) | retryablehttp + per-host rate limits for notifier APIs, aws-sdk-go-v2 adaptive retry |
| [0006](adr/0006-testing-strategy.md) | unit / recorded component / testscript CLI / nightly live |
| [0007](adr/0007-interactive-prompts.md) | no prompt library in MVP; huh later behind an interface |
| [0008](adr/0008-lint-config.md) | maratori golden golangci config v2.15.0 + documented overrides |
| [0009](adr/0009-release-goreleaser.md) | release-please + goreleaser, cosign, SBOM, attestations |
| [0010](adr/0010-license-apache2.md) | Apache-2.0; MPL dependencies imported unmodified; no BSL code |
| [0011](adr/0011-mcp-go-sdk.md) | `tfcensus mcp` stdio server via the official Go SDK, read-only |
| [0012](adr/0012-third-party-notices.md) | third-party licenses and notices in every release archive |
| [0013](adr/0013-shell-completion-and-man-pages.md) | cobra completion for 4 shells, embedded carapace for 6 more, man pages in the archives |

ADR format (Nygard): Status, Context, Decision, Reasons/Consequences, at most 60 lines.
A superseded ADR stays in place with `Status: superseded by NNNN`.
