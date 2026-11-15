package tui

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/harness"
)

// harnesses table columns.
const (
	hsColName = iota
	hsColCommand
	hsColAdapter
	hsColInstalled
	hsColDefault
)

// harnessesModel lists the configured harnesses: whether each command is
// on PATH, which adapter generates its config, and which is the default.
// "s" makes the highlighted one the default and saves the config.
type harnessesModel struct {
	e      *env
	tb     *table
	filter string
}

// newHarnesses builds the harnesses view.
func newHarnesses(e *env) *harnessesModel {
	m := &harnessesModel{e: e, tb: newTable([]column{
		{title: "name"},
		{title: "command", flex: true},
		{title: "adapter"},
		{title: "installed"},
		{title: "default"},
	})}
	m.fill()
	return m
}

// Init implements screen.
func (m *harnessesModel) Init() tea.Cmd { return nil }

// Title implements screen.
func (m *harnessesModel) Title() string {
	return frameTitle("harnesses", "", m.filter, len(m.tb.rows))
}

// Crumb implements screen.
func (m *harnessesModel) Crumb() string { return "harnesses" }

// Hints implements screen.
func (m *harnessesModel) Hints() []hint {
	return []hint{{"s", "Set default"}, {"N", "Sort name"}, {"esc", "Back"}}
}

// setFilter implements filterable.
func (m *harnessesModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
}

// filterValue implements filterable.
func (m *harnessesModel) filterValue() string { return m.filter }

// fill rebuilds the rows from the config.
func (m *harnessesModel) fill() {
	cfg := m.e.core.Cfg
	var rows []row
	for _, name := range cfg.HarnessNames() {
		h := cfg.Harness[name]
		cmd := strings.TrimSpace(h.Command + " " + strings.Join(h.Args, " "))
		installed := styled("no", styleWarn)
		if _, err := exec.LookPath(h.Command); err == nil {
			installed = styled("yes", styleOK)
		}
		def := plain("")
		if name == cfg.DefaultHarness {
			def = styled("●", styleSelected)
		}
		adapter := styled("-", styleDim)
		if h.Adapter != "" {
			adapter = plain(h.Adapter)
		}
		rows = append(rows, row{
			id: name, search: name + " " + h.Command,
			cells: []cell{plain(name), styled(cmd, styleDim), adapter, installed, def},
		})
	}
	m.tb.empty = "No harnesses configured."
	m.tb.setRows(rows)
}

// Update implements screen.
func (m *harnessesModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case refreshMsg:
		m.fill()
		return m, nil
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// key handles the harnesses key map.
func (m *harnessesModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.tb.handleKey(k.String()) {
		return m, nil
	}
	switch k.String() {
	case "esc":
		if m.filter != "" {
			m.setFilter("")
			return m, nil
		}
		return m, pop()
	case "N":
		m.tb.sortBy(hsColName)
	case "s":
		r, ok := m.tb.selected()
		if !ok {
			return m, nil
		}
		cfg := m.e.core.Cfg
		if h := cfg.Harness[r.id]; !harness.Available(h) {
			return m, flashErr(h.Command + " is not on PATH; install it before making it the default")
		}
		return m, m.setDefault(r.id)
	}
	return m, nil
}

// setDefault saves a harness as the default.
func (m *harnessesModel) setDefault(name string) tea.Cmd {
	cfg := m.e.core.Cfg
	cfg.DefaultHarness = name
	if err := cfg.Save(); err != nil {
		return flashErr(err.Error())
	}
	m.e.core.Cfg = cfg
	m.fill()
	return flash("default harness is now " + name)
}

// View implements screen.
func (m *harnessesModel) View(w, h int) string { return m.tb.view(w, h) }
