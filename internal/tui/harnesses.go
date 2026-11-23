package tui

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/harness"
)

// harnessActions lists what the harnesses view can do to one harness. The key
// is also the view's own shortcut for the action.
var harnessActions = []action{{id: "default", label: "Set as default", key: "s"}}

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
	return []hint{{"enter", "Actions"}, {"s", "Set default"}, {"esc", "Back"}}
}

// footerHints implements footerHinter.
func (m *harnessesModel) footerHints() []hint {
	return []hint{{"enter", "actions"}, {"/", "filter"}, {"esc", "back"}}
}

// tbl implements tabular.
func (m *harnessesModel) tbl() *table { return m.tb }

// rowActions implements rowActor.
func (m *harnessesModel) rowActions() (rowContext, bool) {
	r, ok := m.tb.selected()
	return rowContext{kind: "harness", subject: r.id, label: r.id, items: harnessActions}, ok
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
	case actionMsg:
		if t.owner != screen(m) {
			return m, nil
		}
		return m.act(t.id, t.subject)
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
	}
	r, ok := m.tb.selected()
	if !ok {
		return m, nil
	}
	if k.String() == "enter" {
		return m, push(newActionMenu(m, "harness", r.id, "", harnessActions))
	}
	if a, ok := findAction(harnessActions, k.String()); ok {
		return m.act(a.id, r.id)
	}
	return m, nil
}

// act performs the harness action with the given id on the named harness.
func (m *harnessesModel) act(id, name string) (screen, tea.Cmd) {
	if id != "default" {
		return m, nil
	}
	cfg := m.e.core.Cfg
	if h := cfg.Harness[name]; !harness.Available(h) {
		return m, flashErr(h.Command + " is not on PATH; install it before making it the default")
	}
	return m, m.setDefault(name)
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
