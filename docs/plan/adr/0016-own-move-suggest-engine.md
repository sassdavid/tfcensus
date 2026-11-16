# tfcensus ADR-0016: move suggest uses its own strict 1:1 engine instead of importing tfautomv

- Status: accepted (2026-11-16). Amends [ADR-0006](0006-tfmigrate-compatible-move.md), which
  allowed importing the tfautomv engine for `move suggest`.

## Context
- tfautomv (busser/tfautomv, Apache-2.0) has a small engine in `pkg/engine`: it flattens the
  planned value of every pure delete and create, compares pairs of the same type and keeps a pair
  when each side matches nothing else. Its only outside import is `hashicorp/terraform-json`.
- Its maintenance, checked on 2026-11-16 with `gh` against the repository: the last release is
  v0.7.0 (2025-06-06). Since then the maintainer committed a README change only. Ten renovate PRs
  are open, the oldest from 2025-08 (#121, the `terraform-json` v0.29.0 bump tfcensus already
  uses), and two outside PRs have no review: #126 (a json rule, 2025-09) and #137 (2026-11). It
  has one maintainer.
- Open engine issues that matter for Terragrunt fleets:
  - #84 and PR #85: a replaced resource (delete and create at one address) is paired with
    another replaced resource, and the generated moves swap them.
  - #53 and #86: a rule names one flat key, so a whole map such as `tags` or a list count can't
    be ignored in one rule.
  - #101: only the keys of the planned create are compared, so an attribute removed from the
    configuration still matches. The maintainer calls this intended.
  - #38: nothing tells the user why a delete found no move.
- `flatmap.Flatten` logs the whole value with `log.Printf("%#v", ...)` when it isn't a map. That
  would print attribute values, which tfcensus never does.
- terragrunt isn't supported (#105, #127); the maintainer declines it. tfcensus runs the plans
  itself, so this doesn't block an import, but no upstream fix will come for it either.
- The other tool found, smford/tf-move-gen, has no license and scores pairs by weighted
  similarity, which can pair resources that aren't the same.

## Decision
- `internal/suggest` holds tfcensus's own engine, written from tfautomv's documented behaviour
  and the issues above. No code is copied and no module is added.
- Pairs: pure deletes of `--from` units against pure creates of `--to` units, of the same type.
  A pair is a move when no attribute differs and each side has exactly one full match. Replaced
  resources (both delete and create in one change) and data sources never take part.
- Comparison: values are flattened to tfautomv's keys (`a.b`, `list.0`, `list.#`). Keys under an
  `after_unknown` mark of the create are skipped. Every other key of either side is compared, so
  an attribute set on the old resource and dropped from the configuration differs. Null, an empty
  string and an empty collection count as equal.
- `--ignore` keeps tfautomv's syntax (`everything`, `whitespace`, `prefix`), so its rules work
  unchanged. A rule also covers the keys below its ATTR, ATTR may hold `*` segments, TYPE may be
  `*`, and a `json` kind compares JSON strings by value.
- Every unmatched delete and create is reported with its full matches when it has several, or
  with the closest candidate and the top-level names of the attributes that differ. Values are
  never printed; the engine rejects a planned value that isn't an object without echoing it.
- The output is a draft migration file for `move plan` to verify, never a state write.

## Consequences
- tfcensus owns about 300 lines of matching code and its tests, and can change the rules without
  an upstream release.
- Results differ from tfautomv where the issues above apply: no swapped replacements, fewer moves
  that change attributes, and more resources reported as unmatched with a reason.
- tfautomv stays in `docs/plan/reference-repos.md` as a behaviour reference, not a dependency.
