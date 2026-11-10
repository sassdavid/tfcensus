# ADR-0002: cobra for CLI parsing

- Status: accepted (2026-11-09)

## Context
- **cobra v1.10.2** is used by 12 of the 20 surveyed Go CLIs (gh, trivy, syft, golangci-lint, k9s, goreleaser, chezmoi, atlantis, steampipe, pluto, glow, crush).
- **urfave/cli v3:** aqua uses it, and OpenTofu (experimental, PR #4419) chose it because pflag parses `-json` as `-j -s -o -n`.
- **urfave/cli v2:** terragrunt wraps it, so unknown flags pass through to terraform.
- **kong:** used by gum and terramate.

## Decision
Use **cobra**, constructed through functions (`newRootCmd(deps) *cobra.Command`), never package-level vars.

## Reasons
- It's the community default, with the most contributor familiarity.
- Built-in completions for bash, zsh, fish and PowerShell. `cobra/doc` generates man pages and Markdown, which we check in and diff in CI.
- tfcensus doesn't need single-dash long flags.
- Constructor functions make commands testable and satisfy `gochecknoglobals` in the golden lint config.

## Consequences
- Passthrough to terraform/terragrunt (tfcensus `--` args) uses `cmd.Flags().SetInterspersed(false)` or a `--` separator. Document it.
- Each command lives in its own file under `internal/cli/`. Its `RunE` does only three things: parse options, call a domain function, render.
