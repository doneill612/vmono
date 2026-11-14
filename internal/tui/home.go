package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// packagesMsg delivers the package list.
type packagesMsg struct {
	list []core.Summary
	err  error
}

// previewMsg delivers the live preview of one package.
type previewMsg struct {
	name    string
	purpose string
	status  core.PackageStatus
	err     error
}

// fetchedMsg reports a finished fetch-all.
type fetchedMsg struct {
	name string
	n    int
	err  error
}

// homeModel lists packages on the left and previews the selected one on the
// right. Enter launches the default harness, which is what everything else
// here exists to get you to.
type homeModel struct {
	e         *env
	all       []core.Summary
	loaded    bool
	filter    textinput.Model
	filtering bool
	cursor    int
	previews  map[string]previewMsg
	msg       string
	showHelp  bool
}

// newHome builds Home, optionally with a message on arrival.
func newHome(e *env, msg string) *homeModel {
	f := textinput.New()
	f.Prompt = "/ "
	f.Placeholder = "filter"
	return &homeModel{e: e, filter: f, previews: map[string]previewMsg{}, msg: msg}
}

// Init implements screen.
func (m *homeModel) Init() tea.Cmd { return m.loadPackages() }

// loadPackages lists packages with dirty markers, off the UI goroutine.
func (m *homeModel) loadPackages() tea.Cmd {
	c := m.e.core
	return func() tea.Msg {
		list, err := c.List(true)
		return packagesMsg{list: list, err: err}
	}
}

// loadPreview gathers the selected package's purpose and member status.
func (m *homeModel) loadPreview(name string) tea.Cmd {
	c := m.e.core
	return func() tea.Msg {
		p, err := c.Load(name)
		if err != nil {
			return previewMsg{name: name, err: err}
		}
		return previewMsg{name: name, purpose: strings.TrimSpace(p.M.Purpose), status: c.Status(p)}
	}
}

// visible returns the packages matching the filter, best matches first.
func (m *homeModel) visible() []core.Summary {
	q := strings.TrimSpace(m.filter.Value())
	if q == "" {
		return m.all
	}
	names := make([]string, len(m.all))
	for i, s := range m.all {
		names[i] = s.Name
	}
	var out []core.Summary
	for _, match := range fuzzy.Find(q, names) {
		out = append(out, m.all[match.Index])
	}
	return out
}

// selected returns the highlighted package, if any.
func (m *homeModel) selected() (core.Summary, bool) {
	v := m.visible()
	if m.cursor < 0 || m.cursor >= len(v) {
		return core.Summary{}, false
	}
	return v[m.cursor], true
}

// previewForSelection requests a preview if the selection has none cached.
func (m *homeModel) previewForSelection() tea.Cmd {
	s, ok := m.selected()
	if !ok {
		return nil
	}
	if _, cached := m.previews[s.Name]; cached {
		return nil
	}
	return m.loadPreview(s.Name)
}

// Update implements screen.
func (m *homeModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case packagesMsg:
		m.loaded = true
		if t.err != nil {
			m.msg = styleErr.Render(t.err.Error())
			return m, nil
		}
		m.all = t.list
		if m.cursor >= len(m.visible()) {
			m.cursor = 0
		}
		return m, m.previewForSelection()
	case previewMsg:
		m.previews[t.name] = t
		return m, nil
	case fetchedMsg:
		if t.err != nil {
			m.msg = styleErr.Render("fetch " + t.name + ": " + t.err.Error())
		} else {
			m.msg = fmt.Sprintf("fetched %d member(s) of %s", t.n, t.name)
		}
		delete(m.previews, t.name)
		return m, tea.Batch(m.loadPackages(), m.loadPreview(t.name))
	case tea.KeyMsg:
		if m.filtering {
			return m.updateFilter(t)
		}
		return m.updateKeys(t)
	}
	return m, nil
}

// updateFilter handles keys while the filter box has focus.
func (m *homeModel) updateFilter(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.filtering = false
		m.filter.SetValue("")
		m.filter.Blur()
		m.cursor = 0
		return m, m.previewForSelection()
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, m.previewForSelection()
	case "up", "down":
		return m.updateKeys(k)
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	m.cursor = 0
	return m, tea.Batch(cmd, m.previewForSelection())
}

// updateKeys handles Home's key map.
func (m *homeModel) updateKeys(k tea.KeyMsg) (screen, tea.Cmd) {
	m.msg = ""
	sel, has := m.selected()
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "/":
		m.filtering = true
		return m, m.filter.Focus()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.previewForSelection()
	case "down", "j":
		if m.cursor < len(m.visible())-1 {
			m.cursor++
		}
		return m, m.previewForSelection()
	case "enter":
		if has {
			return m, func() tea.Msg { return launchMsg{LaunchRequest{Package: sel.Name}} }
		}
	case "y":
		if has {
			if err := clipboard.WriteAll(sel.Path); err != nil {
				m.msg = styleWarn.Render("could not copy: " + err.Error() + " — " + sel.Path)
			} else {
				m.msg = "copied " + paths.Contract(sel.Path)
			}
		}
	case "f":
		if has {
			m.msg = "fetching " + sel.Name + "…"
			return m, m.fetchAll(sel.Name)
		}
	default:
		return m.updateActions(k.String(), sel, has)
	}
	return m, nil
}

// updateActions handles keys that open other screens. It is separate so the
// screens it opens can be wired in as they exist.
func (m *homeModel) updateActions(key string, sel core.Summary, has bool) (screen, tea.Cmd) {
	return m, nil
}

// fetchAll fetches every member's source of a package and refreshes status.
func (m *homeModel) fetchAll(name string) tea.Cmd {
	c := m.e.core
	return func() tea.Msg {
		p, err := c.Load(name)
		if err != nil {
			return fetchedMsg{name: name, err: err}
		}
		n := 0
		for _, mem := range p.M.Members {
			src, err := paths.Abs(mem.Source)
			if err != nil {
				return fetchedMsg{name: name, n: n, err: err}
			}
			if err := gitx.Fetch(src); err != nil {
				return fetchedMsg{name: name, n: n, err: fmt.Errorf("%s: %w", mem.Name, err)}
			}
			n++
		}
		return fetchedMsg{name: name, n: n}
	}
}

// View implements screen.
func (m *homeModel) View() string {
	w, h := m.e.w, m.e.h
	if w < 40 {
		w = 80
	}
	if h < 10 {
		h = 24
	}
	leftW := w * 2 / 5
	if leftW < 34 {
		leftW = 34
	}
	rightW := w - leftW - 3
	if rightW < 20 {
		rightW = 20
	}

	left := m.renderList(leftW, h-6)
	right := m.renderPreview(rightW)
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftW).Render(left),
		styleDim.Render(" │ "),
		lipgloss.NewStyle().Width(rightW).Render(right),
	)

	var b strings.Builder
	b.WriteString(styleTitle.Render("vmono") + styleDim.Render("  packages") + "\n\n")
	b.WriteString(body + "\n\n")
	if m.filtering || m.filter.Value() != "" {
		b.WriteString(m.filter.View() + "\n")
	}
	if m.msg != "" {
		b.WriteString(m.msg + "\n")
	}
	if m.showHelp {
		b.WriteString(m.helpText())
	} else {
		b.WriteString(keyHelp("enter", "launch", "n", "new", "e", "edit", "d", "delete", "l", "harness", "s", "sync", "f", "fetch", "?", "help", "q", "quit"))
	}
	return b.String()
}

// renderList draws the package list.
func (m *homeModel) renderList(width, height int) string {
	if !m.loaded {
		return styleDim.Render("loading…")
	}
	v := m.visible()
	if len(v) == 0 {
		if len(m.all) == 0 {
			return styleDim.Render("No packages yet.\nPress n to create one.")
		}
		return styleDim.Render("No match.")
	}
	var lines []string
	start := 0
	if m.cursor >= height && height > 0 {
		start = m.cursor - height + 1
	}
	for i := start; i < len(v) && (height <= 0 || i-start < height); i++ {
		s := v[i]
		dirty := " "
		if s.Dirty {
			dirty = styleWarn.Render("*")
		}
		opened := "never"
		if t, err := time.Parse(time.RFC3339, s.LastOpened); err == nil {
			opened = ago(t)
		}
		line := fmt.Sprintf("%s %-18s %2d  %s", dirty, truncate(s.Name, 18), s.Members, styleDim.Render(truncate(s.Branch, width-34)+" "+opened))
		if i == m.cursor {
			line = styleSelected.Render("▸") + line
		} else {
			line = " " + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// renderPreview draws the selected package's purpose and members.
func (m *homeModel) renderPreview(width int) string {
	s, ok := m.selected()
	if !ok {
		return ""
	}
	p, ok := m.previews[s.Name]
	if !ok {
		return styleDim.Render("loading…")
	}
	if p.err != nil {
		return styleErr.Render(p.err.Error())
	}
	var b strings.Builder
	b.WriteString(styleLabel.Render(s.Name) + "\n")
	if p.purpose != "" {
		b.WriteString(wrap(p.purpose, width) + "\n")
	} else {
		b.WriteString(styleDim.Render("no purpose recorded") + "\n")
	}
	b.WriteString("\n")
	for _, mem := range p.status.Members {
		b.WriteString(memberLine(mem, width) + "\n")
	}
	return b.String()
}

// memberLine summarizes one member: mode, branch or ref, dirty state and
// ahead/behind its upstream.
func memberLine(mem core.MemberStatus, width int) string {
	mode := "active"
	if mem.Mode == manifest.ModeReference {
		mode = "ref   "
	}
	where := mem.Branch
	if where == "" {
		where = mem.Ref
	}
	state := styleOK.Render("clean")
	switch {
	case mem.Missing:
		state = styleErr.Render("missing")
	case mem.Error != "":
		state = styleErr.Render("error")
	case mem.Dirty > 0 || mem.Untracked > 0:
		state = styleWarn.Render(fmt.Sprintf("%d~ %d?", mem.Dirty, mem.Untracked))
	}
	ab := ""
	if mem.Upstream != "" {
		ab = fmt.Sprintf(" ↑%d↓%d", mem.Ahead, mem.Behind)
	}
	return fmt.Sprintf("%s %-14s %s %s%s", styleDim.Render(mode), truncate(mem.Name, 14), truncate(where, width-34), state, ab)
}

// helpText is the expanded key reference.
func (m *homeModel) helpText() string {
	rows := [][2]string{
		{"enter", "launch the package's default harness"},
		{"l", "launch with a chosen harness"},
		{"n", "new package"},
		{"e", "edit the selected package"},
		{"d", "delete the selected package"},
		{"s", "sync: rebuild what's missing, regenerate files"},
		{"f", "fetch all members and refresh status"},
		{"y", "copy the package path to the clipboard"},
		{"/", "filter"},
		{"?", "toggle this help"},
		{"q", "quit"},
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("  %-6s %s\n", styleLabel.Render(r[0]), r[1]))
	}
	return b.String()
}
