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

	started    bool
	stopped    bool
	rendered   bool
	lastLines  int
	lastStatus map[*Task]Status
}

// New returns a Display writing to w. Whether w is a terminal is detected
// here, once, via os.ModeCharDevice.
func New(w io.Writer) *Display {
	return &Display{
		w:          w,
		tty:        isTTY(w),
		sp:         newSpinner(),
		lastStatus: make(map[*Task]Status),
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
	if d.tty {
		d.renderTTY(RenderAll(d.roots, d.sp.frame()))
		return
	}
	d.renderVerbose()
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

func (d *Display) renderTTY(lines []string) {
	if d.rendered && d.lastLines > 0 {
		fmt.Fprintf(d.w, "\x1b[%dA", d.lastLines)
	}
	for _, l := range lines {
		fmt.Fprintf(d.w, "\x1b[2K%s\n", l)
	}
	for i := len(lines); i < d.lastLines; i++ {
		fmt.Fprint(d.w, "\x1b[2K\n")
	}
	// When the block shrank, the clear loop left the cursor at the bottom of
	// the previous block. Park it at the end of the new block so the next
	// render moves up exactly lastLines and finds the top of the block again
	// — otherwise the cursor drifts down and stale lines survive on screen.
	if len(lines) < d.lastLines {
		fmt.Fprintf(d.w, "\x1b[%dA", d.lastLines-len(lines))
	}
	d.rendered = true
	d.lastLines = len(lines)
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
