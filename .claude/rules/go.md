---
paths:
  - "**/*.go"
---

# Go code

## Comments

Keep comments few. Names and code should carry the meaning. A comment has to say something the
code can't.

- Each package has one package comment, in `doc.go`, of one or two sentences. Libraries start
  with "Package x"; commands start with the binary name ("Tfcensus prints ...").
- Every exported identifier gets a doc comment, as engineering-standards.md requires. Keep it to
  one sentence, two at most. Start with the identifier's name, say what it does or returns, not
  how. Functions returning bool use "reports whether".
- Unexported identifiers get a comment only when the name and signature leave something out.
- Inside a function, comment only the why: a workaround, an ordering constraint, why an error is
  ignored. Never narrate the next line.
- No comments that repeat a field or constant name. A short end-of-line comment on a field is fine
  for units, allowed values or where the value comes from.
- No milestone numbers, dates or ticket links in Go comments. `//nolint` needs a reason
  (nolintlint), and that reason is the exception.
- Link identifiers as `[Name]` or `[pkg.Name]`. No commented-out code.
- Comment text follows `.claude/rules/writing.md`.

## Code

- Wrap errors as `fmt.Errorf("verb object: %w", err)`. Error strings are lowercase with no trailing
  punctuation. Sentinels are `ErrX`; match with `errors.Is` and `errors.AsType`.
- Short receiver names, the same for every method of a type. No `Get` prefix. No stutter
  (`config.Load`, not `config.LoadConfig`). Initialisms keep one case (`URL`, `ID`).
- A function named like a predicate returns bool. One that returns an error is named for the
  action (`Check`, `Validate`).
- No package-level mutable state. Dependencies, including the logger, come through `app.Deps`.
- Define interfaces where they're used, with 1-3 methods. Return concrete types.
- `context.Context` is the first parameter of anything that does I/O.
- Tests live in the external `_test` package, are table-driven and call `t.Parallel()`. Use
  `t.TempDir()` and `t.Context()`. Inject env vars instead of `t.Setenv`. No sleeps.
- `mise run check` must pass before you say something works.
