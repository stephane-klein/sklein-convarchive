package browse

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// KeyMap defines the browse-specific key bindings.
type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Open     key.Binding
	Back     key.Binding
	Quit     key.Binding
	Toggle   key.Binding
	ShowRaw  key.Binding
	Refresh  key.Binding
	Filter   key.Binding
	PageUp   key.Binding
	PageDown key.Binding
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Open:     key.NewBinding(key.WithKeys("enter", "right", "l"), key.WithHelp("enter/→", "open")),
		Back:     key.NewBinding(key.WithKeys("esc", "left", "h"), key.WithHelp("esc/←", "back")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Toggle:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "toggle JSON view")),
		ShowRaw:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "toggle raw")),
		Refresh:  key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "refresh listing")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		PageUp:   key.NewBinding(key.WithKeys("pgup", "b"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown", "space", "f"), key.WithHelp("pgdn", "page down")),
	}
}

// Model is the Bubble Tea model of the archive browser.
type Model struct {
	reader   *Reader
	tree     *Tree
	keys     KeyMap
	list     list.Model
	viewport viewport.Model
	path     []*Node // stack of ancestor nodes; last is the current directory

	mode        viewMode
	loading     bool // reading a month object
	loadingTree bool // initial tree listing in progress
	spinner     spinner.Model
	err         string

	// reading state
	curObject *Object
	raw       []byte
	pretty    bool
	showRaw   bool
	width     int
	height    int
}

type viewMode int

const (
	modeList viewMode = iota
	modeReading
)

type item struct {
	node *Node
}

func (i item) Title() string       { return i.node.Label }
func (i item) Description() string { return description(i.node) }
func (i item) FilterValue() string { return i.node.Label }

func description(n *Node) string {
	if n.Kind == KindMonth {
		return "open"
	}
	return fmt.Sprintf("%d items", len(n.Children))
}

// NewModel creates the browser model. tree is optional: when nil, the tree is
// loaded asynchronously at startup (Init fires a refresh command) and the TUI
// shows a spinner until the listing completes.
func NewModel(reader *Reader, tree *Tree) Model {
	keys := DefaultKeyMap()

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)

	s := spinner.New()
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	m := Model{
		reader:  reader,
		tree:    tree,
		keys:    keys,
		list:    list.New([]list.Item{}, delegate, 0, 0),
		mode:    modeList,
		spinner: s,
	}

	m.list.KeyMap = listKeyMap(keys)
	m.list.DisableQuitKeybindings()
	m.list.SetShowHelp(false)
	m.list.SetShowStatusBar(true)
	m.list.SetStatusBarItemName("item", "items")
	m.list.SetShowTitle(true)

	if tree != nil {
		m.loadingTree = false
		m.path = []*Node{tree.Root}
		m.setList(tree.Root)
	} else {
		m.loadingTree = true
		m.path = []*Node{}
	}
	return m
}

// listKeyMap maps the browse keys onto the bubbles/list key bindings so the
// native component does not steal "q"/"esc"/"g" and pagination keys.
func listKeyMap(keys KeyMap) list.KeyMap {
	km := list.DefaultKeyMap()
	km.CursorUp = keys.Up
	km.CursorDown = keys.Down
	km.PrevPage = keys.PageUp
	km.NextPage = keys.PageDown
	km.GoToStart = key.NewBinding(key.WithKeys("home"))
	km.GoToEnd = key.NewBinding(key.WithKeys("end"))
	km.Quit = key.NewBinding()
	km.ForceQuit = key.NewBinding()
	km.Filter = keys.Filter
	km.ClearFilter = key.NewBinding(key.WithKeys("esc"))
	return km
}

// setList replaces the list items with the children of n.
func (m *Model) setList(n *Node) {
	items := make([]list.Item, 0, len(n.Children))
	for _, c := range n.Children {
		items = append(items, item{node: c})
	}
	m.list.SetItems(items)
	m.list.Title = pathTitle(m.path)
}

func pathTitle(path []*Node) string {
	labels := make([]string, 0, len(path))
	for _, n := range path {
		labels = append(labels, n.Label)
	}
	return strings.Join(labels, " / ")
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.loadingTree {
		return tea.Batch(m.spinner.Tick, m.refresh())
	}
	return nil
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-1)
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 1
		if m.mode == modeReading && m.raw != nil {
			m.renderReading()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if !m.loadingTree {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			m.raw = nil
		} else {
			m.err = ""
			m.raw = msg.data
			m.renderReading()
		}
		return m, nil

	case refreshedMsg:
		m.loadingTree = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		tree, err := BuildTree(msg.keys)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.tree = tree
		m.path = []*Node{tree.Root}
		m.setList(tree.Root)
		m.mode = modeList
		return m, nil
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		if m.mode == modeReading {
			m.backToList()
		} else {
			return m, tea.Quit
		}

	case key.Matches(msg, m.keys.Back):
		if m.mode == modeReading {
			m.backToList()
		} else if len(m.path) > 1 {
			m.path = m.path[:len(m.path)-1]
			m.setList(m.path[len(m.path)-1])
		}

	case key.Matches(msg, m.keys.Open):
		if m.mode == modeReading {
			m.viewport, _ = m.viewport.Update(msg)
			return m, nil
		}
		if item, ok := m.list.SelectedItem().(item); ok {
			if item.node.Kind == KindMonth {
				m.openNode(item.node)
				return m, m.loadCurrent()
			}
			m.openNode(item.node)
		}

	case key.Matches(msg, m.keys.Toggle):
		if m.mode == modeReading && m.curObject.Layer == LayerJSONL && m.raw != nil {
			m.pretty = !m.pretty
			m.renderReading()
		}

	case key.Matches(msg, m.keys.ShowRaw):
		if m.mode == modeReading && m.pretty && m.raw != nil {
			m.showRaw = !m.showRaw
			m.renderReading()
		}

	case key.Matches(msg, m.keys.Refresh):
		if m.mode == modeList {
			m.loading = true
			return m, m.refresh()
		}

	case key.Matches(msg, m.keys.Filter):
		if m.mode == modeList {
			return m, nil // forwarded to list below
		}
	}

	// Forward navigation keys to the focused component.
	if m.mode == modeReading {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) openNode(n *Node) {
	if n.Kind == KindMonth {
		m.curObject = n.Object
		m.mode = modeReading
		m.loading = true
		m.err = ""
		m.raw = nil
		m.pretty = false
		m.showRaw = false
		m.viewport.GotoTop()
		m.renderReading()
		return
	}
	m.path = append(m.path, n)
	m.setList(n)
}

func (m *Model) backToList() {
	m.mode = modeList
	m.curObject = nil
	m.raw = nil
	m.loading = false
	m.err = ""
}

func (m *Model) renderReading() {
	if m.raw == nil {
		m.viewport.SetContent("")
		return
	}
	content := RenderContent(m.curObject.Layer, m.raw, m.pretty, m.showRaw)
	m.viewport.SetContent(content)
}

func (m *Model) refresh() tea.Cmd {
	return func() tea.Msg {
		keys, err := m.reader.ListKeys(context.Background())
		return refreshedMsg{keys: keys, err: err}
	}
}

type loadedMsg struct {
	data []byte
	err  error
}

type refreshedMsg struct {
	keys []string
	err  error
}

// load returns a command that reads the current object in the background.
func (m Model) loadCurrent() tea.Cmd {
	obj := m.curObject
	return func() tea.Msg {
		data, err := m.reader.Read(context.Background(), obj)
		return loadedMsg{data: data, err: err}
	}
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// View implements tea.Model.
func (m Model) View() string {
	switch m.mode {
	case modeReading:
		if m.loading {
			return "Loading…\n" + helpFooter(m.keys)
		}
		if m.err != "" {
			return "Error: " + m.err + "\n\n" + helpFooter(m.keys)
		}
		title := m.curObject.Layer + " / " + m.curObject.Month
		if m.curObject.Layer == LayerJSONL {
			view := "compact"
			if m.pretty {
				view = "pretty"
			}
			title += " (" + view + ")"
		}
		return titleStyle.Render(title) + "\n" + m.viewport.View() + "\n" + helpFooter(m.keys)
	default:
		if m.err != "" {
			return "Error: " + m.err + "\n\n" + helpFooter(m.keys)
		}
		if m.loadingTree {
			return m.spinner.View() + " Loading the tree…\n\n" + helpFooter(m.keys)
		}
		return m.list.View() + "\n" + helpFooter(m.keys)
	}
}

func helpFooter(keys KeyMap) string {
	return helpStyle.Render(
		"enter/→ open · esc/← back · ↑/↓ navigate · / filter · q quit" +
			" · r JSON view · R raw · g refresh",
	)
}
