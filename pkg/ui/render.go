package ui

import (
	"fmt"
	"strings"
)

const (
	symbolPending = "[ ]"
	symbolDone    = "[x]"
	symbolError   = "[!]"
)

// statusSymbol returns the bracket symbol for a task. Running tasks embed the
// current spinner frame; an empty frame yields the static "[.]" form used by
// the verbose renderer.
func statusSymbol(t *Task, frame string) string {
	switch t.Status {
	case StatusRunning:
		if frame == "" {
			return "[.]"
		}
		return "[" + frame + "]"
	case StatusSuccess:
		return symbolDone
	case StatusError:
		return symbolError
	default:
		return symbolPending
	}
}

// RenderAll renders the given root tasks, each with its full subtree, as the
// display lines of a listr2-style task list. It is a pure function so it can
// be unit-tested without a terminal.
func RenderAll(roots []*Task, frame string) []string {
	var lines []string
	for _, r := range roots {
		lines = append(lines, renderNode(r, 0, frame)...)
	}
	return lines
}

func renderNode(t *Task, depth int, frame string) []string {
	indent := strings.Repeat("  ", depth)
	line := fmt.Sprintf("%s- %s %s", indent, statusSymbol(t, frame), t.Title)
	if t.StatusText != "" {
		line += " " + t.StatusText
	}

	// Inactive collapsible tasks hide their children behind a summary line,
	// keeping the tree compact (only the active task stays expanded).
	if t.CollapseWhenInactive && !taskActive(t) {
		if t.CollapsedSummary != "" {
			line += " " + t.CollapsedSummary
		}
		return []string{line}
	}

	lines := []string{line}

	if t.MaxVisibleChildren > 0 && len(t.children) > t.MaxVisibleChildren {
		start, end := visibleWindow(t.children, t.MaxVisibleChildren, t.AnchorFirstWhenPending)
		if start > 0 {
			lines = append(lines, hiddenIndicator(indent, start))
		}
		for _, c := range t.children[start:end] {
			lines = append(lines, renderNode(c, depth+1, frame)...)
		}
		if end < len(t.children) {
			lines = append(lines, hiddenIndicator(indent, len(t.children)-end))
		}
		return lines
	}

	for _, c := range t.children {
		lines = append(lines, renderNode(c, depth+1, frame)...)
	}
	return lines
}

// visibleWindow returns the half-open [start, end) range of children to
// render when a task has more children than the max. The window is anchored
// on the first active child (running or error). When nothing is active it is
// anchored on the most recent child (the last one), so the final done state
// shows the newest children; when nothing has started yet and the parent
// anchors the window on its first child, it shows the oldest children first.
func visibleWindow(children []*Task, max int, anchorFirstWhenPending bool) (start, end int) {
	n := len(children)
	anchor := n - 1
	if anchorFirstWhenPending && allPending(children) {
		anchor = 0
	}
	for i, c := range children {
		if c.Status == StatusRunning || c.Status == StatusError {
			anchor = i
			break
		}
	}
	start = anchor - (max-1)/2
	if start < 0 {
		start = 0
	}
	if start > n-max {
		start = n - max
	}
	return start, start + max
}

func allPending(children []*Task) bool {
	for _, c := range children {
		if c.Status != StatusPending {
			return false
		}
	}
	return true
}

// taskActive reports whether a task is currently being processed or failed.
func taskActive(t *Task) bool {
	return t.Status == StatusRunning || t.Status == StatusError
}

// hiddenIndicator renders the "… N hidden children …" line for a parent task
// at the given indentation. The line does not carry a task status symbol.
func hiddenIndicator(indent string, n int) string {
	label := "months"
	if n == 1 {
		label = "month"
	}
	return fmt.Sprintf("%s  - … %d hidden %s …", indent, n, label)
}
