package ui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestRenderAll(t *testing.T) {
	root := &Task{Title: "@zack", Status: StatusRunning}
	jan := root.AddChild("@zack 2026-01")
	jan.Status = StatusSuccess
	jan.StatusText = "Ok"
	feb := root.AddChild("@zack 2026-02")
	feb.Status = StatusPending

	lines := RenderAll([]*Task{root}, "⠋")
	want := []string{
		"- [⠋] @zack",
		"  - [x] @zack 2026-01 Ok",
		"  - [ ] @zack 2026-02",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestRenderAllMultipleRoots(t *testing.T) {
	a := &Task{Title: "a", Status: StatusSuccess, StatusText: "Ok"}
	b := &Task{Title: "b", Status: StatusError, StatusText: "boom"}
	lines := RenderAll([]*Task{a, b}, "⠋")
	want := []string{
		"- [x] a Ok",
		"- [!] b boom",
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestStatusSymbolRunningEmptyFrame(t *testing.T) {
	root := &Task{Title: "c", Status: StatusRunning}
	if got := statusSymbol(root, ""); got != "[.]" {
		t.Errorf("got %q, want [.]", got)
	}
}

func TestSpinnerFramesNonEmpty(t *testing.T) {
	if len(Frames) == 0 {
		t.Fatal("Frames is empty")
	}
}

func TestDisplayVerbose(t *testing.T) {
	var buf strings.Builder
	d := New(&buf)
	root := d.Root("c")
	root.Status = StatusRunning
	root.StatusText = "12 posts"
	d.Redraw()
	root.Status = StatusSuccess
	root.StatusText = "Ok"
	d.Redraw()

	want := "- [.] c 12 posts\n- [x] c Ok\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDisplayVerboseNoReprintOnSameStatus(t *testing.T) {
	var buf strings.Builder
	d := New(&buf)
	root := d.Root("c")
	root.Status = StatusRunning
	d.Redraw()
	d.Redraw()

	if got := strings.Count(buf.String(), "- [.] c"); got != 1 {
		t.Errorf("got %d printed lines, want 1", got)
	}
}

func monthTask(title string) *Task {
	return &Task{Title: title}
}

func monthRoot(t *testing.T, active int, done []int, max int) (*Task, []*Task) {
	t.Helper()
	root := &Task{Title: "@zack", Status: StatusSuccess, MaxVisibleChildren: max}
	children := make([]*Task, 0, 30)
	for i := 0; i < 30; i++ {
		c := root.AddChild(monthKey(i))
		children = append(children, c)
	}
	doneSet := map[int]bool{}
	for _, i := range done {
		doneSet[i] = true
	}
	for i := 0; i < 30; i++ {
		switch {
		case i == active:
			children[i].Status = StatusRunning
			children[i].StatusText = "… in progress …"
		case doneSet[i]:
			children[i].Status = StatusSuccess
			children[i].StatusText = "Ok"
		default:
			children[i].Status = StatusPending
		}
	}
	return root, children
}

func monthKey(i int) string {
	return fmt.Sprintf("m%02d", i)
}

func TestVisibleWindowCenteredOnActive(t *testing.T) {
	root, children := monthRoot(t, 15, nil, 10)
	start, end := visibleWindow(root.children, root.MaxVisibleChildren, false)
	if start != 11 || end != 21 {
		t.Fatalf("window = [%d, %d), want [11, 21)", start, end)
	}
	if children[15].Status != StatusRunning {
		t.Fatal("active child lost its status")
	}
}

func TestVisibleWindowClampStart(t *testing.T) {
	root, _ := monthRoot(t, 2, nil, 10)
	start, end := visibleWindow(root.children, root.MaxVisibleChildren, false)
	if start != 0 || end != 10 {
		t.Fatalf("window = [%d, %d), want [0, 10)", start, end)
	}
}

func TestVisibleWindowClampEnd(t *testing.T) {
	root, _ := monthRoot(t, 28, nil, 10)
	start, end := visibleWindow(root.children, root.MaxVisibleChildren, false)
	if start != 20 || end != 30 {
		t.Fatalf("window = [%d, %d), want [20, 30)", start, end)
	}
}

func TestVisibleWindowNoActiveShowsNewest(t *testing.T) {
	// Nothing running and nothing done (initial pending state) and all-done
	// state both anchor the window on the newest children.
	root, children := monthRoot(t, -1, nil, 10)
	start, end := visibleWindow(root.children, root.MaxVisibleChildren, false)
	if start != 20 || end != 30 {
		t.Fatalf("pending window = [%d, %d), want [20, 30)", start, end)
	}
	done := []int{}
	for i := range children {
		done = append(done, i)
	}
	root2, _ := monthRoot(t, -1, done, 10)
	start2, end2 := visibleWindow(root2.children, root2.MaxVisibleChildren, false)
	if start2 != 20 || end2 != 30 {
		t.Fatalf("done window = [%d, %d), want [20, 30)", start2, end2)
	}
}

func TestVisibleWindowAnchorFirstWhenPending(t *testing.T) {
	// In an oldest-first task list, the initial all-pending state anchors the
	// window on the first child (the oldest), where processing starts.
	root, _ := monthRoot(t, -1, nil, 10)
	root.AnchorFirstWhenPending = true
	start, end := visibleWindow(root.children, root.MaxVisibleChildren, true)
	if start != 0 || end != 10 {
		t.Fatalf("pending window = [%d, %d), want [0, 10)", start, end)
	}

	// Once done, the window still shows the newest children.
	for _, c := range root.children {
		c.Status = StatusSuccess
	}
	start, end = visibleWindow(root.children, root.MaxVisibleChildren, true)
	if start != 20 || end != 30 {
		t.Fatalf("done window = [%d, %d), want [20, 30)", start, end)
	}
}

func TestRenderWindowed(t *testing.T) {
	root, _ := monthRoot(t, 5, []int{6, 7, 8, 9}, 10)
	lines := RenderAll([]*Task{root}, "⠋")

	// 1 parent line + 1 leading indicator + 10 children + 1 trailing indicator.
	if len(lines) != 13 {
		t.Fatalf("got %d lines, want 13: %q", len(lines), lines)
	}
	// anchor 5 → window [1, 11): one hidden child before, 19 after.
	if lines[1] != "  - … 1 hidden month …" {
		t.Errorf("leading indicator = %q", lines[1])
	}
	if lines[12] != "  - … 19 hidden months …" {
		t.Errorf("trailing indicator = %q", lines[12])
	}
	// The active child must be visible with its spinner frame.
	found := false
	for _, l := range lines {
		if strings.Contains(l, "- [⠋] m05 … in progress …") {
			found = true
		}
	}
	if !found {
		t.Errorf("active child not rendered: %q", lines)
	}
}

func TestRenderWindowedNoLimit(t *testing.T) {
	root, _ := monthRoot(t, -1, nil, 0)
	lines := RenderAll([]*Task{root}, "⠋")
	if len(lines) != 31 {
		t.Fatalf("got %d lines, want 31", len(lines))
	}
}

func TestHiddenIndicatorSingular(t *testing.T) {
	if got := hiddenIndicator("", 1); got != "  - … 1 hidden month …" {
		t.Errorf("got %q", got)
	}
	if got := hiddenIndicator("  ", 3); got != "    - … 3 hidden months …" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCollapsedWhenInactive(t *testing.T) {
	root := &Task{Title: "team-nimbus/chan-delta", Status: StatusSuccess, StatusText: "Ok", CollapseWhenInactive: true, CollapsedSummary: "· 8 months"}
	for i := 0; i < 8; i++ {
		c := root.AddChild(monthKey(i))
		c.Status = StatusSuccess
		c.StatusText = "Ok (uploaded)"
	}
	lines := RenderAll([]*Task{root}, "⠋")
	want := []string{"- [x] team-nimbus/chan-delta Ok · 8 months"}
	if len(lines) != 1 || lines[0] != want[0] {
		t.Fatalf("got %q, want %q", lines, want)
	}
}

func TestRenderActiveTaskStaysExpanded(t *testing.T) {
	root := &Task{Title: "team-nimbus/chan-delta", Status: StatusRunning, StatusText: "3200 posts", CollapseWhenInactive: true, MaxVisibleChildren: 10}
	for i := 0; i < 8; i++ {
		root.AddChild(monthKey(i))
	}
	lines := RenderAll([]*Task{root}, "⠋")
	if len(lines) != 9 { // parent + 8 children
		t.Fatalf("got %d lines, want 9: %q", len(lines), lines)
	}
}

func TestRenderCollapsedWithoutSummary(t *testing.T) {
	root := &Task{Title: "c", Status: StatusSuccess, CollapseWhenInactive: true}
	root.AddChild("x")
	lines := RenderAll([]*Task{root}, "⠋")
	if len(lines) != 1 || lines[0] != "- [x] c" {
		t.Fatalf("got %q", lines)
	}
}

// vt is a minimal virtual terminal that applies the ANSI sequences emitted by
// renderTTY (\x1b[nA cursor up, \x1b[nE cursor next line, \x1b[nF cursor
// preceding line, \x1b[2K clear line, \n newline) so tests can assert the
// final on-screen lines, not just the raw byte stream.
type vt struct {
	screen []string
	row    int
}

func (v *vt) write(s string) {
	for len(s) > 0 {
		i := strings.IndexByte(s, '\x1b')
		if i < 0 {
			v.putText(s)
			return
		}
		if i > 0 {
			v.putText(s[:i])
		}
		rest := s[i+1:]
		if !strings.HasPrefix(rest, "[") {
			return
		}
		rest = rest[1:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		n := 1
		if j > 0 {
			n, _ = strconv.Atoi(rest[:j])
		}
		switch rest[j] {
		case 'A', 'F':
			v.row -= n
			if v.row < 0 {
				v.row = 0
			}
		case 'E':
			v.row += n
		case 'K':
			v.ensure(v.row)
			v.screen[v.row] = ""
		}
		s = rest[j+1:]
	}
}

func (v *vt) ensure(row int) {
	for len(v.screen) <= row {
		v.screen = append(v.screen, "")
	}
}

func (v *vt) putText(s string) {
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			v.ensure(v.row)
			v.screen[v.row] += s
			return
		}
		v.ensure(v.row)
		v.screen[v.row] += s[:i]
		v.row++
		s = s[i+1:]
	}
}

type vtWriter struct{ v *vt }

func (w vtWriter) Write(p []byte) (int, error) {
	w.v.write(string(p))
	return len(p), nil
}

func TestRenderTTYNoGhostAfterShrink(t *testing.T) {
	v := &vt{}
	d := &Display{w: vtWriter{v}, tty: true, sp: newSpinner(), lastStatus: make(map[*Task]Status)}

	conv := &Task{Title: "@zack", Status: StatusRunning, StatusText: "25200 posts", MaxVisibleChildren: 10}
	for i := 0; i < 30; i++ {
		conv.AddChild(monthKey(i))
	}
	d.roots = []*Task{conv}

	// 13 lines: parent + leading + 10 children + trailing (active in the middle).
	conv.Children()[20].Status = StatusRunning
	for i := 21; i < 30; i++ {
		conv.Children()[i].Status = StatusSuccess
		conv.Children()[i].StatusText = "Ok"
	}
	d.Redraw()
	if got := RenderAll(d.roots, d.sp.frame()); len(got) != 13 {
		t.Fatalf("expected 13 lines, got %d", len(got))
	}

	// 12 lines: all done, window anchored on the newest (no trailing indicator).
	conv.Status = StatusSuccess
	conv.StatusText = "Ok"
	for _, c := range conv.Children() {
		c.Status = StatusSuccess
		c.StatusText = "Ok"
	}
	d.Redraw()

	// Back to 13 lines: the upload task is added.
	upload := &Task{Title: "Upload to Object Storage", Status: StatusRunning}
	d.roots = append(d.roots, upload)
	d.Redraw()

	// Final render: upload done.
	upload.Status = StatusSuccess
	upload.StatusText = "234 objects"
	d.Redraw()

	want := RenderAll(d.roots, "")
	if len(want) != 13 {
		t.Fatalf("expected final 13 lines, got %d: %q", len(want), want)
	}
	for i, wl := range want {
		if v.screen[i] != wl {
			t.Errorf("screen line %d = %q, want %q", i, v.screen[i], wl)
		}
	}
	for i := len(want); i < len(v.screen); i++ {
		if v.screen[i] != "" {
			t.Errorf("stale line at row %d: %q", i, v.screen[i])
		}
	}
}

func TestRenderTTYGrowAfterCollapse(t *testing.T) {
	v := &vt{}
	d := &Display{w: vtWriter{v}, tty: true, sp: newSpinner(), lastStatus: make(map[*Task]Status)}

	mkConv := func(title string, n int) *Task {
		conv := &Task{Title: title, Status: StatusRunning, MaxVisibleChildren: 10, CollapseWhenInactive: true}
		for i := 0; i < n; i++ {
			c := conv.AddChild(title + "-" + string(rune('a'+i)))
			c.Status = StatusSuccess
			c.StatusText = "Ok (uploaded)"
		}
		conv.StatusText = "3200 posts"
		d.roots = append(d.roots, conv)
		d.Redraw()
		return conv
	}
	finish := func(conv *Task) {
		conv.Status = StatusSuccess
		conv.StatusText = "Ok"
		conv.CollapsedSummary = "· 12 months"
		d.Redraw()
	}

	c1 := mkConv("team-nimbus/chan-delta", 12)
	finish(c1)
	c2 := mkConv("team-nimbus/chan-gamma", 12)
	finish(c2)
	c3 := mkConv("team-nimbus/chan-zeta", 12)
	finish(c3)
	mkConv("team-nimbus/chan-eta", 12) // active, expanded

	want := RenderAll(d.roots, d.sp.frame())
	// 3 collapsed + 1 expanded (parent + hidden indicator + 10 window children).
	if len(want) != 15 {
		t.Fatalf("expected 15 lines, got %d: %q", len(want), want)
	}
	for i, wl := range want {
		if got := v.screen[i]; got != wl {
			t.Errorf("screen line %d = %q, want %q", i, got, wl)
		}
	}
}

func TestRenderTTYRootWindow(t *testing.T) {
	v := &vt{}
	d := &Display{w: vtWriter{v}, tty: true, sp: newSpinner(), lastStatus: make(map[*Task]Status), MaxVisibleRoots: 3}
	for i := 0; i < 5; i++ {
		r := d.Root(fmt.Sprintf("conv-%d", i))
		r.Status = StatusSuccess
		r.StatusText = "Ok"
	}
	d.Redraw()
	lines := d.renderLines()
	// 5 roots, 3 visible → 1 indicator + 3 roots.
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), lines)
	}
	if lines[0] != "- … 2 hidden conversations …" {
		t.Errorf("indicator = %q", lines[0])
	}
	for i, wl := range lines {
		if v.screen[i] != wl {
			t.Errorf("screen line %d = %q, want %q", i, v.screen[i], wl)
		}
	}
}
