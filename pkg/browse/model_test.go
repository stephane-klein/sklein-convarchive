package browse

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func testTree() *Tree {
	keys := []string{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl",
		"markdown/mattermost/-/chan-alpha/2017/2017-08.md.zst",
	}
	tree, err := BuildTree(keys)
	if err != nil {
		panic(err)
	}
	return tree
}

func TestModelLoadingTree(t *testing.T) {
	m := NewModel(&Reader{}, nil)
	if !m.loadingTree {
		t.Fatal("model should start in loadingTree state when no tree is provided")
	}
	if view := m.View(); !strings.Contains(view, "Loading the tree") {
		t.Errorf("loading view should mention tree loading:\n%s", view)
	}

	// Advancing a spinner tick keeps the loading view and returns a tick command.
	m2, cmd := m.Update(spinner.TickMsg{})
	if cmd == nil {
		t.Fatal("expected spinner tick command while loading")
	}
	m = m2.(Model)
	if !m.loadingTree {
		t.Fatal("still loading tree")
	}

	// The initial listing completes: tree is built and the list is shown.
	m2, _ = m.Update(refreshedMsg{keys: []string{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl",
		"markdown/mattermost/-/chan-alpha/2017/2017-08.md.zst",
	}})
	m = m2.(Model)
	if m.loadingTree {
		t.Fatal("loadingTree should be false after listing completes")
	}
	if view := m.View(); strings.Contains(view, "Loading the tree") {
		t.Errorf("view should not show loading after listing completes:\n%s", view)
	}
}

func TestModelLoadingTreeError(t *testing.T) {
	m := NewModel(&Reader{}, nil)
	m2, _ := m.Update(refreshedMsg{err: errors.New("connection refused")})
	m = m2.(Model)
	if m.loadingTree {
		t.Fatal("loadingTree should be false after listing error")
	}
	if view := m.View(); !strings.Contains(view, "Error") {
		t.Errorf("view should show the listing error:\n%s", view)
	}
}

func testModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(&Reader{}, testTree())
	msg := tea.WindowSizeMsg{Width: 80, Height: 24}
	m2, _ := m.Update(msg)
	return m2.(Model)
}

func TestModelInitialRender(t *testing.T) {
	m := testModel(t)
	view := m.View()
	for _, want := range []string{"conversations", "JSONL (raw)", "Markdown (readable)"} {
		if !strings.Contains(view, want) {
			t.Errorf("initial view missing %q:\n%s", want, view)
		}
	}
}

func TestModelNavigateAndRead(t *testing.T) {
	m := testModel(t)

	// Descend: jsonl → mattermost → No team → chan-alpha → year → month.
	for _, label := range []string{"JSONL (raw)", "mattermost", "No team", "chan-alpha", "2017"} {
		m = navigateTo(t, m, label)
	}

	// The month leaf is selected; pressing Enter switches to reading.
	m2, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected load command after opening a month")
	}
	m = *m2.(*Model)
	if m.mode != modeReading {
		t.Fatalf("mode = %d, want modeReading", m.mode)
	}
	if !m.loading {
		t.Fatal("expected loading state")
	}

	// Simulate the loaded data.
	m2, _ = m.Update(loadedMsg{data: []byte(`{"timestamp":"2017-08-01T10:00:00+02:00","author":"carla","content":"bonjour"}`)})
	m = m2.(Model)
	view := m.View()
	if !strings.Contains(view, "carla") {
		t.Errorf("reading view missing content:\n%s", view)
	}
}

func TestModelBackAndQuit(t *testing.T) {
	m := testModel(t)
	m = navigateTo(t, m, "JSONL (raw)")

	// Back returns to root.
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = *m2.(*Model)
	if len(m.path) != 1 {
		t.Fatalf("path len = %d, want 1 (root)", len(m.path))
	}

	// q at root quits.
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected quit command at root")
	}
}

func TestModelToggleJSONView(t *testing.T) {
	m := testModel(t)
	m = navigateTo(t, m, "JSONL (raw)", "mattermost", "No team", "chan-alpha", "2017")
	m.curObject, _ = ParseObjectKey("jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl")
	m.mode = modeReading
	m.raw = []byte(`{"timestamp":"2017-08-01T10:00:00+02:00","author":"carla","content":"bonjour","raw":{"verbose":true}}`)

	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = *m2.(*Model)
	if !m.pretty {
		t.Fatal("r should toggle pretty view")
	}
	view := m.View()
	if !strings.Contains(view, `"content": "bonjour"`) {
		t.Errorf("pretty view missing indented JSON:\n%s", view)
	}
	if strings.Contains(view, "verbose") {
		t.Errorf("raw should be hidden in pretty view by default:\n%s", view)
	}

	m2, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = *m2.(*Model)
	if !m.showRaw {
		t.Fatal("R should reveal raw")
	}
	if view := m.View(); !strings.Contains(view, "verbose") {
		t.Errorf("raw should be visible after R:\n%s", view)
	}
}

// navigateTo selects the child with the given label at the current level,
// descending through each label in turn.
func navigateTo(t *testing.T, m Model, labels ...string) Model {
	t.Helper()
	for _, label := range labels {
		found := false
		for i, it := range m.list.Items() {
			it, ok := it.(item)
			if !ok {
				continue
			}
			if it.node.Label == label {
				m.list.Select(i)
				m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
				m = *m2.(*Model)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no item with label %q at current level", label)
		}
	}
	return m
}
