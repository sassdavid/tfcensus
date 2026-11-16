# tfcensus

A Go CLI that takes a census of Terraform/OpenTofu + Terragrunt fleets on AWS. It covers:
- units and inventory
- unmanaged-resource scan
- state backend audit
- drift, whois/codify and cross-unit moves (later)

## Verify (run before claiming anything works)
- `mise run check` runs the linters (golangci-lint, prose, jactionlint), `go test -race ./...` and govulncheck. It must pass before every commit.
- `mise run test:golden -- -update` regenerates goldens. Review the diff.
- `mise run typemap:gen` regenerates `internal/typemap/data`, and must produce a deterministic diff.
- `mise run docs` regenerates `docs/cli/`. CI fails if it's stale.

## Plan
- Milestones are in `docs/plan/milestones.md`. Do **one milestone per session**, the one named in the prompt.
- Read the milestone's linked spec(s) in `docs/plan/specs/` first. They define done.
- Tick a box only with evidence (the command and its result) in the commit body.
- ADRs live in `docs/plan/adr/` and `docs/engineering/adr/`. Propose a new ADR instead of silently deviating.

## Sibling repo
- relgap (`~/src/github/relgap`) shares this repo's build, lint, test and release setup. Follow `.claude/rules/siblings.md`: reuse its files instead of writing new ones, keep shared files identical (`mise run sibling:diff`), and make shared changes in both repos.

## Writing and comments
- All text (docs, comments, CLI help, commits, PRs) follows `.claude/rules/writing.md`. `mise run lint:prose` checks part of it.
- Go code keeps comments few: package docs in `doc.go`, one-sentence doc comments on exported names, why-comments only. Details in `.claude/rules/go.md`.

## Safety rules (non-negotiable)
- Never call real AWS unless the prompt names a profile and says read-only is allowed. Default to stubs (`testdata/cassettes/aws/`).
- Never print state attribute values, only identifiers. The grep test enforces this.
- Code paths outside `internal/migrate` must not write to S3, DynamoDB or state. Write APIs are only reachable from `move apply|rollback|mark-applied`.
- No mutating tool is exposed over MCP.
- Don't import `hashicorp/terraform`, terragrunt `internal/`, or OpenTofu `internal/`. Shell out through `internal/execx`.

## Layout
- `cmd/tfcensus/main.go` → `internal/cli` (constructor functions, no globals).
- Foundations: `discovery` (F1), `accounts` and `probe` (F2), `tfstate` (F3), `plan` (F4).
- Features: `inventory` (layers), `typemap`, `match`, `ownership`, `scan`, `audit`, `codify`, `migrate`, `suggest` (move suggest), `notify`, `render`, `mcpserver`.
- Fixtures:
  - `testdata/repos/` (Terragrunt fixtures)
  - `testdata/states/`
  - `testdata/cassettes/aws/` (go-vcr cassettes written from AWS API shapes, replayed with `awstest.Replay` or `TEST_CASSETTE` in scripts)
  - `internal/testutil/fakebin` (fake terragrunt), replaying `testdata/terragrunt/` recordings made by `scripts/record-terragrunt.sh`
  - `testdata/tofu/` (an encrypted state and its `tofu show -json` output), recorded by `scripts/record-tofu.sh` and replayed by a fake tofu

## Reference sources (read-only, for inspiration)
- Local clones live in `~/src/github/_reference/<owner>/<repo>` (list and license notes: `docs/plan/reference-repos.md`). Read them to verify formats and behaviour. **Never copy code** from BSL (terraform), AGPL (renovate) or Prosperity (dependabot) repos.
- Dependency sources are already in `$(go env GOMODCACHE)`, so read those instead of cloning.

## Conventions that aren't obvious
- AWS clients come from `accounts` (one `aws.Config` per profile/role, cached). Never call `config.LoadDefaultConfig` elsewhere.
- AWS SDK code follows `.claude/rules/aws-sdk.md`. Check the operation's API reference and the SDK guide through the AWS MCP server first.
- Always tell terragrunt the terraform binary, because it defaults to tofu: `TG_TF_PATH` on every call, plus `--tf-path` for `run` and `render` (`find` rejects the flag).
- Match order is identity → arn → normalized id. Unmapped types are `undetermined`, never managed.
- Every finding carries `reason` and `rule`. Every suppression carries `reason` and optionally `expires`.
- Logs go to stderr, data to stdout through an injected `io.Writer`. The logger comes from `Deps` (sloglint).
- CI workflows and the examples in `docs/ci/` install tools with mise (`jdx/mise-action`, `mise.lock`,
  and `mise.<env>.toml` for a tool only one job needs), not `setup-*` actions, `npm install -g` or
  downloads. Rules in `docs/engineering/ci-and-release.md#github-actions-workflows`.
- Use Conventional Commits. release-please owns versions.
- Renovate runs from `.github/workflows/renovate.yml` with the rules in `renovate.json` (engineering ADR-0015, setup in `docs/engineering/ci-and-release.md#renovate`). Change both files in all repos that share them.
