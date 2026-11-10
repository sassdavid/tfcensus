# ADR-0006: Testing strategy. Golden files, recorded I/O, testscript

- Status: accepted (2026-11-09)

## Decision
Tests come in four layers, and every milestone's "done when" refers to them.

| Layer | Tooling | Runs in |
|---|---|---|
| Unit | stdlib `testing` + testify + go-cmp; table-driven; `testing.F` fuzzing for parsers and version schemes | every PR |
| Component | domain packages driven through fakes. HTTP (notifier APIs **and AWS**) stubbed **at the HTTP layer**: go-vcr v4 cassettes or httptest, so the SDK deserializers and paginators really run. Plus in-process gofakes3/kumo and containerized moto/Floci ([testing plan](../../plan/testing.md)) | every PR |
| CLI | `rogpeppe/go-internal/testscript` (`testdata/script/*.txtar`): real binary, real flags, golden stdout and stderr, exit codes, `--format json` checked against `schemas/` | every PR |
| Live | terraform/terragrunt e2e against **moto** (no AWS account). Real-AWS validation is optional and manual | nightly + manual; failures open an issue but don't block PRs |

## Rules
- Golden files update only via `mise run test:golden -- -update`, and the diff is reviewed in the PR.
- Cassettes are recorded with a scrubber that removes `Authorization` headers, tokens and account IDs (account IDs replaced with `111111111111`).
- The `-race` flag is always on. No `time.Sleep` in tests: inject a clock.
- Filesystem tests use `t.TempDir()`. Exec tests use a fake `terragrunt` binary written into a temp `PATH` (the testscript `exec` pattern).
- Coverage is reported but not gated. The gates are: every spec acceptance criterion maps to at least one test (named `TestSpec_<spec>_<criterion>`).

## Why HTTP-level stubs, not SDK middleware stubs
A smithy `Finalize` stub returns a typed output directly, which skips the SDK's deserializers, error mapping and
paginator token handling, exactly where real bugs hide. HTTP-level fixtures exercise all of it.

## Why not mocks everywhere
Recorded real responses catch upstream format quirks (AWS pagination tokens, error-code shapes) that hand-written mocks never contain. `go.uber.org/mock` is used only for wide interfaces where a fake would be larger than the test.
