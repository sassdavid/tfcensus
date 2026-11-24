# ADR-0017: Terminal output with lipgloss v2 and one color decision per stream

- Status: accepted (2026-11-24)

## Context
Tables used hand-written ANSI codes and an aligner that counted runes (relgap) or `text/tabwriter`
(tfcensus), which misaligns wide characters and colored cells. tfcensus tables had no color.
`--color always` and `CLICOLOR_FORCE` gave no color when stdout was a pipe, `TERM=dumb` was
ignored, Windows VT processing was enabled for stderr only, release-note and tag text reached the
terminal with its control characters, and `| head` killed the process before `--out` files were
written. Checked on 2026-11-24:

- gh decides once per process (`go-gh/pkg/term/env.go`), pads plain text and colors it afterwards
  (`go-gh/pkg/tableprinter`), and sanitizes third-party text (`go-gh/pkg/asciisanitizer`).
- lipgloss v2.1.1 (`charm.land/lipgloss/v2`, stable since 2026-02-24) measures display cells with
  `clipperhouse/displaywidth`. `Style.Render` always emits the full style; a `colorprofile.Writer`
  downsamples or strips it when printing. `lipgloss/table` draws a borderless table. Charm's log v2
  moved to it.
- colorprofile reads `NO_COLOR` with `strconv.ParseBool` and lets `CLICOLOR_FORCE` win off a
  terminal, which differs from no-color.org and bixense.com/clicolors.
- Importing lipgloss detects stdout at package init and runs `tmux info` inside tmux on a
  terminal (charmbracelet/lipgloss#749; the fix, #759, is open).
- Rendering 10,000 rows takes 244 ms with `lipgloss/table` and 6 ms with a hand-written aligner.
- None of gh, golangci-lint, goreleaser, cargo or npm turns color on from `CI` or `GITHUB_ACTIONS`.

## Decision
- `internal/term` (shared, identical in both repos) detects each stream once at startup:
  terminal, color and width. Color rules, first match wins: `--color always|never`, a non-empty
  `NO_COLOR` (off), `CLICOLOR_FORCE` or `FORCE_COLOR` other than empty, `0` or `false` (on),
  `CLICOLOR=0` or `TERM=dumb` (off), then whether the stream is a terminal on which VT processing
  could be enabled. Width comes from `COLUMNS`, then the terminal.
- Styles are tones on the 16 basic ANSI colors, so they follow the user's theme. The text carries
  the meaning too (WCAG 1.4.1).
- Renderers always style. The sink writes the tty format through a `colorprofile.Writer` with
  `ANSI` when stdout has color and `NoTTY` otherwise; plain and `--out` files are always `NoTTY`.
  We pass the profile ourselves and never call lipgloss's `Print` functions or `colorprofile.Detect`.
- Tables use `lipgloss/table` with no borders, two spaces between columns, no trailing spaces, and
  never cut a cell. Prose (notes, legends, release-note entries) wraps at the width on a terminal.
- Auto format is tty when stdout is a terminal or has color, else plain.
- Third-party text goes through `term.Sanitize` (caret notation for C0, DEL and C1).
- SIGPIPE is caught: a closed stdout drops the rest of stdout, `--out` files are still written and
  the exit code stays. A log file never gets color.
- Every report goes to stdout in one write.

## Consequences
- Adds lipgloss v2, colorprofile, `charmbracelet/x/ansi` and `charmbracelet/x/term` (MIT) and their
  indirect modules. go-isatty stays for Cygwin and MSYS ptys.
- The init-time tmux query stays until lipgloss#759 ships; Renovate brings the release.
- CI step logs stay plain unless the workflow sets `--color always` or `CLICOLOR_FORCE=1`.
- OSC 8 links, a pager and colored help output (fang, which queries the terminal background) are
  left out.
