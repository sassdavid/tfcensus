# Working on the tfcensus docs site

This folder is the source of `https://tfcensus.sasskovacs.dev`, a VitePress site. The site
describes the latest release: a release deploys it, and `docs.yml` can redeploy a ref by hand.
This file is not published (`srcExclude` in `.vitepress/config.mts`). The spec is
[docs-site](plan/specs/docs-site.md), the design is [docs-design](plan/specs/docs-design.md), and
the page list is [docs-content](plan/specs/docs-content.md). The plan and the engineering docs
sit next to the site in [plan/](plan/milestones.md) and [engineering/](engineering/README.md),
and the site never publishes them.

## Tasks

The site's tools (node, aube, lychee, oxfmt) come from `mise.docs.toml`, so install them once
with `MISE_ENV=docs`. Run the tasks from the repo root.

```sh
MISE_ENV=docs mise install
cd docs && aube install --frozen-lockfile && cd ..
mise run docs:dev        # live reload on localhost
mise run docs            # regenerate the generated pages and data; exits 1 when one changed
mise run docs:build      # every check CI runs, then the build in docs/.vitepress/dist
mise run docs:a11y       # axe in both themes on the built site; needs Chrome
mise run docs:preview    # serve docs/.vitepress/dist
mise run lint:docs       # oxfmt --check and oxlint on .vitepress
mise run lint:prose      # the writing rules, on every published page
```

`docs:build` runs, in order: the generators' stale check, the `llms.txt` and icon stale
checks, the snippet check, the node unit tests, `vitepress build` (it renders one social card per
page), the social card check, the third-party request check, the docs URL check, the
never-on-the-site check and `lychee --offline`. `docs:a11y` takes Chrome from `CHROME_PATH`, or
`google-chrome` or `chromium` on `PATH`. It also checks reduced motion and fails on any request
that leaves the site. CI runs both in `docs.yml`.

`mise run docs` exits 1 after it rewrote a stale file. Review the diff, commit it, and run it
again to see exit 0.

## Where pages go

| Path | What goes there |
|---|---|
| `index.md` | the home page: frontmatter for `HomeHero` and `AnswerRows` |
| `getting-started.md`, `install.md`, `supported-versions.md` | the Start section |
| `guides/` | one task per page, named for what the reader wants to do |
| `concepts/` | how tfcensus works: safety, units, state, layers, matching, categories, rules |
| `reference/` | generated pages plus hand-written ones (`cel`, `iam`, `move-file`, `output`, `type-coverage`) |
| `cli/` | generated, one page per command |
| `troubleshooting.md`, `faq.md`, `accuracy.md`, `security.md`, `contributing.md` | the Help section |
| `ci/` | the GitHub Actions examples; guides include them with `<<<` |
| `runbooks/` | one-line pages that keep old links working |
| `public/` | files served as they are: glyph, favicons, manifest, IAM policy, `llms.txt`, schemas |
| `.vitepress/` | config, sidebar, theme, the build scripts and their tests |
| `plan/`, `engineering/`, `validation/`, `dogfood.md` | internal, never published |

A new page needs frontmatter with `title` and `description` (the build fails without a
description, because search, social cards and `llms.txt` use it) and an entry in
`.vitepress/sidebar.mts`. A guide or concept page also needs its row in docs-content.md, which
`TestContentPages` reads. Command pages link guides through `cmd.Annotations["docs.related"]`, and
help text links the site through `internal/docsurl`, never through a repo path.

## Generated files

The table lists every generated file. The Markdown ones say so in their first lines. Change the
code a file comes from, then run `mise run docs`. Don't edit the file.

| Files | Written by | From |
|---|---|---|
| `cli/**`, `cli/commands.json` | `scripts/gendocs` | the cobra tree, flags, annotations |
| `reference/` config, environment, exit codes, fail-on, schemas, MCP | `scripts/gendocs` | the config schema and defaults, `run.go`, `schemas/`, the MCP server |
| `reference/` audit checks, drift noise, rule IDs, rule packs; `parts/*.md` | `scripts/gensite` | the audit, drift, match and ownership packages |
| `snippets/*.txt` | `scripts/gensite` | test script goldens |
| `.vitepress/theme/data/*.json` | `scripts/gensite` | the scan golden and the typemap |
| `public/schemas/*.json` | `scripts/gendocs` | `schemas/` |
| `public/llms.txt` | `.vitepress/llms.mts` | the sidebar and each page's description |
| `public/favicon.ico`, `public/apple-touch-icon.png`, `public/icon-192.png`, `public/icon-512.png` | `.vitepress/social.mjs` | `public/glyph.svg` |

Output on a page is never typed by hand. Include a golden with `<<< @/../testdata/golden/...` or
use a snippet, so `mise run test:golden -- -update` refreshes the site too.

Social cards are not committed. `vitepress build` renders them into `dist/social/` with
`@napi-rs/canvas`, the variable font file of `@fontsource-variable/public-sans` (the package carries its
OFL text) and the tally from `theme/card.mjs`. A card's file name is the hash of its PNG.

## Voice

Pages follow [.claude/rules/writing.md](../.claude/rules/writing.md), and `mise run lint:prose`
checks the word list, dashes, quotes and emoji. In short:

- Write for an engineer who runs Terragrunt across many accounts: give the command, the flag or
  the file, and say what it does.
- Use "you", active voice and the present tense. Use sentence-case headings.
- Every `tfcensus` command in a `sh` block must parse: the snippet check runs each one against the
  command tree and checks flags and values.
- Say what is read-only. A page about `move apply`, `move rollback` or `move mark-applied` shows
  the `WritesState` note.
- Never show a state attribute value. Use identifiers only.
- Mark a scan category with `<CategoryMark kind="unmanaged" />` and its text label, never with
  colour alone.
