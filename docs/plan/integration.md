# How relgap and tfcensus connect

They are two independent tools, each useful alone, built on one shared foundation ([engineering/](../engineering/README.md)). relgap lives at
[sassdavid/relgap](https://github.com/sassdavid/relgap).
They connect through **stable JSON**, not shared code. A shared library comes later, and only if both actually
need it (rule of three).

## Data touchpoints

| From | To | What flows | Use case |
|---|---|---|---|
| `tfcensus inventory --format json` | `relgap drift --from-tfcensus inventory.json` | terraform_version + provider versions **per unit, read from real state** (what was actually applied, not what the code says) | "Which units still run aws provider 5.x, and what breaks between 5.100 and 6.72?" → `relgap digest tfprovider:hashicorp/aws 5.100.0..6.72.0` |
| relgap pin discovery (`required_version`, `.terraform.lock.hcl`, terragrunt version constraints) | — | the same data from code, offline | drift between code pins and applied state (code says 1.17, state written by 1.15) |
| `tfcensus audit backend` FL-TFVER / FL-PROV | relgap | units below the version policy | relgap explains the upgrade path for exactly those versions |
| Both MCP servers | an agent (Claude Code) | read-only structured answers | "List units on terraform < 1.15 (tfcensus), then summarize the breaking changes to 1.17 (relgap)" in one conversation |

`relgap` reads the tfcensus inventory schema (`schemas/inventory-v1.json`, published by tfcensus) as one more pin
source (detector `tfcensus`). The coupling is one-way and versioned.

## Shared conventions (copied, not linked)
- The same JSON envelope (`schema_version`, `tool`, `version`, `generated_at`, `data`, `errors`).
- The same exit codes, `--format`/`--fail-on` semantics, config precedence, XDG paths and logging flags.
- The same CI/release pipeline (goreleaser, cosign, attestations, Homebrew tap `sassdavid/homebrew-tap`).
- The same rule style: CEL in tfcensus, while relgap's ignore and policy blocks use the same `reason` + `expires` vocabulary.

## Possible later extraction (not planned)
If both tools end up with near-identical `httpx`, `render` (envelope, sarif) and `config` packages, extract a small
module `github.com/sassdavid/clikit`, but only after both have shipped v0.2 and the duplication is real.

## Build order
1. **relgap v0.1 first.** Smaller scope, no cloud credentials needed, immediate daily value, and it proves the shared template.
2. **tfcensus v0.1** reuses the template; its first real users are the author's fleet.
3. Integration (`--from-tfcensus`) lands in relgap once tfcensus v0.1's inventory schema is stable.
