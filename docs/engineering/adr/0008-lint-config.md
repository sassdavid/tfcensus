# ADR-0008: golangci-lint "golden config" with documented overrides

- Status: accepted (2026-11-09)

## Context
`maratori/golangci-lint-config` is a strict, curated `.golangci.yml`. Its tags mirror golangci-lint releases: v2.15.0, released 2026-10-21, matches golangci-lint v2.15.0. About 287 repos reference it (cashapp/hermit, alecthomas/participle, kubewarden).
- It enables about 85 linters, `govet enable-all` (minus fieldalignment), and formatters goimports + golines (120).
- Big projects (gh, trivy, goreleaser, golangci-lint) hand-pick 18–32 linters instead.
- Strict choices: forbidigo bans `fmt.Print*`, gochecknoglobals, gochecknoinits, sloglint `no-global`, nonamedreturns, testpackage, paralleltest, mnd, godot, nolintlint (requires a reason), depguard (no `math/rand`, no `log` outside main).

## Decision
Adopt **v2.15.0 verbatim**, then apply this override block. It's marked with `# --- local overrides ---` so upstream syncs are an easy 3-way merge.

1. `forbidigo`: allowed. Output goes through an injected `io.Writer` (`fmt.Fprint*`), so the ban helps rather than hurts.
2. `gochecknoglobals`: keep. Cobra commands are built by constructor functions. One narrow exclusion for the ldflags `version`, `commit` and `date` in `internal/buildinfo`.
3. `goimports.local-prefixes: github.com/sassdavid/tfcensus`.
4. Enable **gofumpt** as an extra formatter (goreleaser style). Keep golines at 120.
5. Tests: relax `paralleltest` for testscript files. Add `goconst` and `mnd` to the test exclusions. Keep `testpackage`.
6. `sloglint context: scope`: keep. Relax only if it gets noisy. Record the reason in this ADR if we do.
7. `exhaustruct`: off for MVP (upstream default). Reconsider at 1.0.

## Amendment (2026-11-10)
At M0 the test exclusions from item 5 (`mnd` in tests, `paralleltest` for testscript files) matched
nothing, and golangci-lint warned about unused exclusion rules. They were left out. Add each one back
when a test first needs it. `goconst` in tests is already excluded upstream. The upstream "you may want
to enable" linters stay off, as upstream ships them.

## Keeping it current
- The upstream tag goes in the file's header comment: `# source: maratori/golangci-lint-config v2.15.0`.
- A Renovate `customManagers` regex (`github-tags` datasource) bumps that comment. It is **grouped** with the golangci-lint bump in `mise.toml` and CI, so both move together.
- The resulting PR prompts a diff against upstream at the new tag, after which the override block is re-applied. golangci-lint v2 has no `extends`, so this sync step is manual.
