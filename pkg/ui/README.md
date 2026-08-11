# pkg/ui — a minimal listr2-style task list renderer

`pkg/ui` is a small, dependency-free renderer for hierarchical task lists in
the spirit of [listr2](https://github.com/listr2/listr2), the Node.js task
list library that inspired this package. It powers the live display of the
`sklein-convarchive mattermost archive` command.

## Inspiration

listr2 renders a live, hierarchical list of tasks with spinner frames,
nesting and per-task state (`[ ]` pending, spinner running, `[x]` done,
`[!]` error). Its output style is familiar from modern CLI tools. The archive
command wants exactly this: one task per conversation, with one child task
per archived month, and a final task for the object storage upload.

## Why not an existing Go library?

As of 2026, we found **no Go equivalent of listr2**. The closest candidates
were reviewed and rejected:

- **bubbletea + bubbles + lipgloss** (charmbracelet) — a full TUI framework
  (Elm architecture, raw-mode input, terminal takeover). Powerful, but a
  framework-sized dependency for a CLI that only prints a status tree and
  exits.
- **pterm** — provides spinners, trees and progress bars, but no live nested
  task list; the redraw loop would still need to be written by hand.
- **yacspin / briandowns/spinner** — single-line spinners only, no tree.

Given that a bare renderer is roughly 250 lines and the terminal redraw is a
handful of ANSI escape sequences, we chose to write it ourselves with **zero
dependencies**, consistent with the project's dependency doctrine.

## Could it become a library?

The `Task` model and the pure `RenderAll` function are intentionally generic:
extracting this into a standalone Go library would be straightforward
(remove the Mattermost-specific wiring, add options for symbols, colors, and
state transitions). We deliberately do not plan to do so — maintaining a
general-purpose library is a rabbit hole we prefer not to enter. The code
stays here, small and focused, easy to read and to adapt.

## Design

- `task.go` — the `Task` tree model and its states (pending, running,
  success, error). A task can cap its rendered children with
  `MaxVisibleChildren`: longer subtrees render as a sliding window of that
  size around the active child, with "N hidden children" indicators above and
  below — used to keep long month lists compact.
- `render.go` — pure rendering of a task tree into display lines
  (`RenderAll`), unit-testable without a terminal.
- `spinner.go` — the braille frame set and its ticker.
- `display.go` — the live renderer: on a TTY it redraws the tree in place;
  on a non-TTY it degrades to a verbose mode that prints each line once as
  its status changes, keeping logs and piped output readable. Verbose output
  is not windowed: it always lists every task.
