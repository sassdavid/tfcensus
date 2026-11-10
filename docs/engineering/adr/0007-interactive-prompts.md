# ADR-0007: Interactive prompts. None in MVP; huh later behind an interface

- Status: accepted (2026-11-09)

## Context
The author asked us to check **charmbracelet/huh**:
- v2.0.3 (`charm.land/huh/v2`), 7.2k★, active. Used by gh (accessible prompter only), gh-aw, woodpecker, changie.
- It does **not** detect a non-TTY itself: without a terminal, `Run()` errors or hangs (issue #718).
- Alternatives: survey (archived 2023) and promptui (dead since 2021).

Where tfcensus might prompt:
- tfcensus `move apply` confirmation: essential.
- `init` wizards: nice to have.
- Interactive pickers for unit, profile or account: nice to have.

## Decision
- **MVP:** no prompt library. A small `internal/prompt.Prompter` interface with a `bufio` implementation handles yes/no and gh-style "type the name to confirm".
- **Later** (`init` wizard, pickers): add `charm.land/huh/v2` as a second `Prompter` implementation. `TFCENSUS_ACCESSIBLE=1` turns on `WithAccessible(true)`.

## Rules
- Prompt only if stdin **and** stdout are TTYs, `CI` is unset, and `--no-input` is not given.
- A destructive command with no TTY and no `--yes` exits code 2 with "refusing to modify state without --yes". It never blocks.
- `move apply` confirmation asks you to type the destination unit path, not just `y`.
- testscript covers every non-interactive path.
