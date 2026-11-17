---
description: How to build and test tfcensus from source, the checks a pull request must pass, and how to work on this docs site.
---

# Contributing

tfcensus is developed on [GitHub](https://github.com/sassdavid/tfcensus). Bug reports, false
positives and feature ideas go to [issues](https://github.com/sassdavid/tfcensus/issues), and
changes come as [pull requests](https://github.com/sassdavid/tfcensus/pulls). A vulnerability goes
through the private form on the [security](/security) page instead.

A good bug report has the version (`tfcensus version`), the command, the exit code and the notes
under the report, with account IDs, ARNs and names replaced. For a scan false positive,
`scan --explain --format json` on the resource gives what a rule sees, with the identifiers you
can redact.

## Set up

The repository pins its toolchain (Go, golangci-lint, govulncheck and the rest) in `mise.toml`, so
one command installs it:

```sh
git clone https://github.com/sassdavid/tfcensus.git
cd tfcensus
mise install
go build ./cmd/tfcensus
```

## Checks

Run the full check before you open a pull request. CI runs the same tasks.

```sh
mise run check                     # linters (Go, prose, workflows, licenses) + go test -race + govulncheck
mise run test:golden -- -update    # regenerate golden files, then review the diff
mise run docs                      # regenerate the CLI and reference pages, fails when one was stale
mise run snapshot                  # local goreleaser build into dist/
```

The tests never call real AWS. They replay recorded AWS API responses and recorded terragrunt and
tofu runs, so they pass offline and without credentials. A few end-to-end tasks, such as
`mise run test:move-e2e`, run real terragrunt, terraform and tofu against an AWS emulator, and a
nightly workflow runs them.

Output changes show up as golden file diffs. Regenerate them with `mise run test:golden -- -update`
and check that the diff is what you meant. A help text change needs `mise run docs`, because the
[CLI reference](/cli/) is generated from the command tree and CI fails when it is stale.

## Pull requests

- Commit messages follow Conventional Commits (`feat:`, `fix:`, `docs:`), in the imperative and
  under 72 characters. release-please builds the changelog and the version from them.
- One change per pull request, with tests. Say in the description how you checked it.
- Text in docs, help and comments is plain and specific. `mise run lint:prose` checks a word list,
  dashes, quotes and emoji.
- No command except `move apply`, `move rollback` and `move mark-applied` may write to AWS, and no
  output may carry a state attribute value. Tests enforce both ([safety model](/concepts/safety)).

## Work on this site

The site lives in `docs/` and is built with VitePress. Its tools come from `mise.docs.toml`:

```sh
MISE_ENV=docs mise install
cd docs && aube install --frozen-lockfile && cd ..
mise run docs:dev      # serve with live reload
mise run docs:build    # stale checks, snippet check, build, card and link checks
mise run docs:a11y     # axe in both themes, needs Chrome (CHROME_PATH)
```

Pages under `docs/cli/`, most pages under `docs/reference/`, and the tables in `docs/parts/` are
generated: a generated file says so in its first lines. Edit the Go code it comes from and run
`mise run docs`. Every other page is written by hand and needs a `description` in its
frontmatter. The build checks every `tfcensus` command in a code block against
the real command tree, and every link.

## License

Contributions are under the [Apache-2.0 license](https://github.com/sassdavid/tfcensus/blob/main/LICENSE),
the license of the project.
