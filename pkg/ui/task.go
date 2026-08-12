package ui

// Status describes the state of a task in the list.
type Status int

const (
	// StatusPending marks a task that has not started yet.
	StatusPending Status = iota
	// StatusRunning marks a task that is currently being processed.
	StatusRunning
	// StatusSuccess marks a task that completed successfully.
	StatusSuccess
	// StatusError marks a task that failed or was interrupted.
	StatusError
)

// Task is a node in the task list. A task may have children, which are
// rendered below it with increasing indentation, like the listr2 renderer.
type Task struct {
	Title      string
	Status     Status
	StatusText string
	children   []*Task
	parent     *Task

	// MaxVisibleChildren caps how many children are rendered at once. When a
	// task has more children than this limit, a sliding window of the given
	// size is shown around the active child (the one running or in error, or
	// the most recent one when none is active), with "N hidden children"
	// indicators above and below. Zero means no limit.
	MaxVisibleChildren int

	// AnchorFirstWhenPending moves the idle window anchor to the first child
	// (instead of the last) when none of the children have started yet. Used
	// for oldest-first task lists: processing starts at the first child, so
	// the initial window should show it.
	AnchorFirstWhenPending bool

	// CollapseWhenInactive collapses the children to a single line whenever the
	// task itself is neither running nor in error. Only the currently active
	// task stays expanded, which keeps long task lists compact (listr2-style).
	CollapseWhenInactive bool

	// CollapsedSummary is appended to the task line when it is rendered
	// collapsed (see CollapseWhenInactive), e.g. "· 8 months".
	CollapsedSummary string
}

// AddChild appends a child task and returns it.
func (t *Task) AddChild(title string) *Task {
	child := &Task{Title: title, parent: t}
	t.children = append(t.children, child)
	return child
}

// Children returns the child tasks of t.
func (t *Task) Children() []*Task {
	return t.children
}
