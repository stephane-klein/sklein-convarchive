package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Display renders a live task list on the terminal, in the spirit of the
// listr2 renderer. On a TTY it redraws the tree in place; on a non-TTY
// (piped output, logs) it falls back to a verbose mode that prints each task
// line once as its status changes.
type Display struct {
	w     io.Writer
	tty   bool
	mu    sync.Mutex
	roots []*Task
	sp    *spinner

	// MaxVisibleRoots caps how many top-level tasks are rendered at once.
	// Since tasks are appended as they are processed, the most recent ones
	// (including the active task) stay visible and the older ones are hidden
	// behind a "N hidden conversations" indicator. Zero means no limit.
	MaxVisibleRoots int

	started     bool
	stopped     bool
	rendered    bool
	lastLines   int
	lastContent []string
	lastStatus  map[*Task]Status
}

// New returns a Display writing to w. Whether w is a terminal is detected
// here, once, via os.ModeCharDevice.
func New(w io.Writer) *Display {
	return &Display{
		w:               w,
		tty:             isTTY(w),
		sp:              newSpinner(),
		MaxVisibleRoots: 15,
		lastStatus:      make(map[*Task]Status),
	}
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Root adds a top-level task and returns it.
func (d *Display) Root(title string) *Task {
	t := &Task{Title: title}
	d.mu.Lock()
	d.roots = append(d.roots, t)
	d.mu.Unlock()
	return t
}

// Start renders the initial state and, on a TTY, starts the spinner ticks.
func (d *Display) Start() {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.mu.Unlock()

	d.Redraw()
	if d.tty {
		d.sp.start(d.Redraw)
	}
}

// Redraw re-renders the tree. Safe to call from any goroutine.
func (d *Display) Redraw() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.renderLocked()
}

// Update applies fn to the task tree and re-renders it, holding the display
// lock for both so a concurrent spinner render never observes a half-mutated
// task. fn must only mutate in-memory state; slow work (network, disk) must
// stay outside of it.
func (d *Display) Update(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn()
	d.renderLocked()
}

func (d *Display) renderLocked() {
	if d.tty {
		d.renderTTY(d.renderLines())
		return
	}
	d.renderVerbose()
}

// renderLines builds the display lines for the current task tree, applying
// the top-level window (only the most recent MaxVisibleRoots tasks are shown).
func (d *Display) renderLines() []string {
	roots := d.roots
	hidden := 0
	if d.MaxVisibleRoots > 0 && len(roots) > d.MaxVisibleRoots {
		hidden = len(roots) - d.MaxVisibleRoots
		roots = roots[len(roots)-d.MaxVisibleRoots:]
	}
	lines := RenderAll(roots, d.sp.frame())
	if hidden > 0 {
		lines = append([]string{fmt.Sprintf("- … %d hidden conversations …", hidden)}, lines...)
	}
	return lines
}

// Stop performs a final render, stops the spinner, and leaves the cursor on a
// fresh line. It is idempotent.
func (d *Display) Stop() {
	d.mu.Lock()
	if !d.started || d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	d.mu.Unlock()

	d.sp.stopWait()
	d.Redraw()
	if d.tty {
		fmt.Fprintln(d.w)
	}
}

// renderTTY redraws the task list incrementally: only the lines whose content
// changed are rewritten, so a spinner tick that changes nothing emits nothing
// and long task lists stay cheap to refresh. The cursor is parked at the end
// of the block after every render so the next one finds the top by moving up
// exactly lastLines.
func (d *Display) renderTTY(lines []string) {
	if !d.rendered {
		for _, l := range lines {
			fmt.Fprintf(d.w, "\x1b[2K%s\n", l)
		}
		d.lastContent = append([]string(nil), lines...)
		d.rendered = true
		d.lastLines = len(lines)
		return
	}

	if equalStrings(lines, d.lastContent) {
		// Nothing changed: leave the cursor where it is (end of block).
		return
	}

	// Move back to the top of the block.
	fmt.Fprintf(d.w, "\x1b[%dA", d.lastLines)

	// Rewrite the changed and new lines.
	at := 0
	for i, l := range lines {
		if i >= len(d.lastContent) || lines[i] != d.lastContent[i] {
			if i > at {
				fmt.Fprintf(d.w, "\x1b[%dE", i-at)
			}
			fmt.Fprintf(d.w, "\x1b[2K%s", l)
			at = i
		}
	}

	// Clear the lines that were removed when the block shrank.
	if len(lines) < len(d.lastContent) {
		if n := len(lines) - at; n > 0 {
			fmt.Fprintf(d.w, "\x1b[%dE", n)
		}
		for i := len(lines); i < len(d.lastContent); i++ {
			fmt.Fprint(d.w, "\x1b[2K\n")
		}
		fmt.Fprintf(d.w, "\x1b[%dF", len(d.lastContent)-len(lines))
	} else if at < len(lines) {
		fmt.Fprintf(d.w, "\x1b[%dE", len(lines)-at)
	}

	d.lastContent = append([]string(nil), lines...)
	d.lastLines = len(lines)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (d *Display) renderVerbose() {
	var walk func(t *Task, depth int)
	walk = func(t *Task, depth int) {
		if d.lastStatus[t] != t.Status {
			d.lastStatus[t] = t.Status
			indent := strings.Repeat("  ", depth)
			line := fmt.Sprintf("%s- %s %s", indent, statusSymbol(t, ""), t.Title)
			if t.StatusText != "" {
				line += " " + t.StatusText
			}
			fmt.Fprintln(d.w, line)
		}
		for _, c := range t.children {
			walk(c, depth+1)
		}
	}
	for _, r := range d.roots {
		walk(r, 0)
	}
}
