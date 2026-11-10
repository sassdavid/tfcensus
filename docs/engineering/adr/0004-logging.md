# ADR-0004: log/slog + tint (not zap)

- Status: accepted (2026-11-09)

## Context
The author asked us to double-check **uber-go/zap**:
- Status: v1.28.0 (2026-04-28), 24.7k★, "Stable", slow small releases.
- Who imports it directly: only long-running servers (atlantis, temporal). trivy, goreleaser and golangci-lint have it only as an indirect dependency.
- What the CLIs we surveyed use instead:
  - log/slog: k9s and aqua (both with tint), helm.
  - go-hclog: terraform, tofu, terragrunt.
  - charm log: gum, crush.
- **No surveyed CLI picked zap for diagnostics.**
- `zapslog` lives in the experimental `go.uber.org/zap/exp` module, last released 2024-10.

Benchmarks (zap README, message + 10 fields): zerolog 380 ns, zap 656 ns, slog ≈2,480 ns.
A CLI logs a few hundred lines per run, so slog costs under 1 ms. Next to network latency that's irrelevant.

## Decision
Use **`log/slog`** (stdlib). Human output goes through **`github.com/lmittmann/tint` v1.2.1**; JSON output uses `slog.NewJSONHandler`. Logs go to stderr.

## Reasons
- Zero extra dependencies; the ecosystem direction since Go 1.21.
- The golden lint config enables `sloglint` (`no-global: all`, `context: scope`) and depguard prefers slog.
- `-v/-vv/-vvv/-q` maps to a `slog.LevelVar`. `--log-format json` swaps the handler.

## Consequences
- The logger is injected through `Deps`, never `slog.Default()`. sloglint enforces this.
- Use `slog.*Context` variants so we can attach request-scoped attributes later (account, region, unit).
- We'd revisit if a long-running server mode (e.g. an MCP HTTP server under load) ever shows logging as a hotspot in a profile.
