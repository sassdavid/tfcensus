# Renovate

Renovate runs self-hosted as the `sassdavid-renovate` GitHub App from
`.github/workflows/renovate.yml`, with its rules in `renovate.json` (engineering ADR-0015 in the
tool repos). The same rules and settings hold in tfcensus, relgap, tfcensus-action,
relgap-action, action-test and advent-of-code-rust; advent-of-code-rust keeps its config in
`.github/renovate.json5`, so its reasons are JSON5 comments. Sources, checked on 2026-11-17: the
Renovate docs pages "Upgrade best practices" (`docs.renovatebot.com/upgrade-best-practices/`) and
"Full config presets" (`docs.renovatebot.com/presets-config/`), and zizmor's audit list
(`docs.zizmor.sh/audits/`).
Before you use a Renovate option, look it up in those docs (or context7 `/websites/renovatebot`),
because options get renamed and `:configMigration` then opens a PR.

## What every Renovate config keeps

- `extends` starts with `config:best-practices`. It brings `config:recommended`,
  `docker:pinDigests`, `helpers:pinGitHubActionDigests`, `:configMigration`,
  `:pinDevDependencies`, `abandonments:recommended`, `security:minimumReleaseAgeNpm` and
  `:maintainLockFilesWeekly`. Turn one off only for a reason you write down: in a rule's
  `description`, a JSON5 comment, or the PR when the option has no description field
  (action-test and advent-of-code-rust turn `lockFileMaintenance` off because they have no
  lockfile to maintain).
- `minimumReleaseAge` is `"3 days"` for every datasource. The docs ask for 14 days when
  third-party updates automerge; these repos don't automerge, so 3 days stays. Only the repos'
  own releases skip the cooldown, each in a rule with a `description`.
- Leave `minimumReleaseAgeBehaviour` (`timestamp-required`) and `internalChecksFilter` (`strict`)
  at their defaults: a version without a release timestamp waits, and no branch opens before the
  cooldown ends. The pending ones show on the Dependency Dashboard.
- Updates for a known vulnerability skip the cooldown by default. That is the current choice;
  setting `vulnerabilityAlerts.minimumReleaseAge` would change it.
- `enabledManagers` limits a repo to the managers whose files it owns. action-test and
  advent-of-code-rust use `github-actions` and `mise` only; advent-of-code-rust's `Cargo.toml`
  and `Cargo.lock` come from its upstream sync, which would overwrite a Renovate change.
- Every `packageRules` entry and every `customManagers` entry has a `description`.
- Versions are exact: actions by full commit SHA with a version comment, images by digest, tools
  in `mise.toml` with their checksums in `mise.lock` (or `mise.<env>.lock`), npm packages with
  exact versions in `package.json`.
- Non-major updates are grouped per manager in one PR. Majors get their own PR and are merged
  one major at a time.
- `vulnerabilityAlerts` stays on, with commit type `fix` in the tool repos so a security update
  ships in a release. `osvVulnerabilityAlerts` is on in every repo.
- The Dependency Dashboard stays on. Ask for a major or an urgent update there instead of
  lowering the cooldown.
- Commit messages are Conventional Commits (`:semanticCommits`) wherever release-please reads
  them.
- `renovate-config.yml` runs `renovate-config-validator --strict`, with the Renovate version that
  `renovate.yml` runs, on every PR that changes the config.

## The self-hosted run

- The app token asks only for the permissions listed in `renovate.yml`. The key lives in the
  `renovate` environment, whose deployment branch is `main`. advent-of-code-rust is a private
  repo on GitHub Free, which has no environments, so its key is a repository secret and the
  zizmor `secrets-outside-env` finding is ignored on that line.
- `RENOVATE_PLATFORM_COMMIT` is on, so commits go through the API and are signed.
- `RENOVATE_ALLOWED_UNSAFE_EXECUTIONS` allows `mise` only, so `mise lock` follows `mise.toml`.
  advent-of-code-rust has no `mise.lock` and leaves it unset.
- `OSV_OFFLINE_DATABASE_FORMAT: split` keeps the OSV download to the ecosystems the repo uses.
- Branches start with `renovate-self-hosted/`, so they can't clash with the hosted Mend app.
- `renovate-version` is pinned, and Renovate updates it, aged by the npm release of the same
  version.

## Lockfiles Renovate can't update yet

- When a package manager has no Renovate manager, a workflow on `renovate-self-hosted/**`
  refreshes its lockfile and commits it through the API with an app token from its own
  environment. Today that is `aube-lock.yml` for `docs/aube-lock.yaml` in the tool repos.
- Renovate doesn't check the release age of what such a workflow or lock file maintenance
  installs, so the package manager must: `docs/aube-workspace.yaml` sets aube's
  `minimumReleaseAge` to the same 3 days.
- Don't gate such a workflow on `github.actor`: zizmor's `bot-conditions` audit flags it as
  spoofable. The branch filter and the environment's deployment branches limit it.
- When Renovate releases an aube manager (renovatebot/renovate#44428) and `renovate-version`
  reaches that release, move to the standard way in both tool repos: let Renovate update
  `docs/aube-lock.yaml`, delete `aube-lock.yml` and the `aube-lock` environment, drop the
  workflow from `scripts/sibling-diff.sh`, and update engineering ADR-0016 and
  `docs/engineering/ci-and-release.md`. Check that PR whenever you review a `renovate-version`
  bump.

## Release notes in pull requests

GitHub notifies every user an issue, pull request, comment or commit mentions, and adds a
backlink to every issue it references (GitHub docs, "Autolinked references and URLs",
`docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/autolinked-references-and-urls`).
Release notes are full of both, so quoted notes must be silenced.

- Renovate silences the notes in its pull request bodies: a zero-width space follows each `@`
  and `#123`, and issue, pull request and discussion links go through `redirect.github.com`
  (`lib/util/markdown.ts` and `lib/modules/platform/github/massage-markdown-links.ts` in
  renovatebot/renovate).
- Keep the notes out of commit messages. Don't put `releaseNotes` in `commitBody`, and keep
  squash merges at the pull request title with an empty message (`squash_merge_commit_title`
  `PR_TITLE` and `squash_merge_commit_message` `BLANK` in the API), as the five squash-merge repos
  have them. Renovate rewrites links in commit messages only since renovatebot/renovate#47858
  (November 2026), and never breaks a bare `#123` there. advent-of-code-rust uses merge commits,
  because its `upstream:commit` puts an `Upstream:` trailer on the branch commit; there
  Renovate's own commits, which carry no notes, land on `main`.
- Anything else that quotes release notes on a pull request silences them the same way.
  relgap's digest Markdown does (relgap's `docs/plan/specs/output-and-ci.md`), and relgap-action
  posts it as a comment on Renovate pull requests. relgap 0.4.0 and older quote the notes as they
  are: a comment on sassdavid/action-test#1 notified about 50 jq authors
  (sassdavid/relgap-action#13). 0.5.0 to 0.13.0 still let some through, such as a mention after a
  non-ASCII letter or one written as a character reference (sassdavid/relgap#124). relgap-action
  refuses comment mode below 0.13.1 (`TOOL_COMMENT_MIN_VERSION` in its `scripts/tool.env`); raise
  that minimum when a relgap release closes another case.

## Changing the config

- `renovate.json` and the Renovate workflows are shared: identical between the tool repos and
  between the action repos (`mise run sibling:diff`). action-test and advent-of-code-rust have no
  sibling check, so keep their settings and this file in step by hand. Make a change in every
  repo, in the same session.
- After a change, read the next Dependency Dashboard for config warnings, and look at the first
  PR the new rule opens.
