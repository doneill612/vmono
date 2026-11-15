package tui

import (
	"fmt"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// membersMsg delivers one package's members with live git status.
type membersMsg struct {
	name   string
	pkg    *core.Package
	status core.PackageStatus
	err    error
}

// members table columns.
const (
	memColName = iota
	memColMode
	memColWhere
	memColHead
	memColUpstream
	memColAhead
	memColBehind
	memColDirty
	memColUntracked
	memColRole
)

// packageSelector is implemented by screens that stand on one package, so
// commands like ":members" can default to it.
type packageSelector interface{ selectedPackage() string }

// membersModel is the drill-down from a package: one row per member with
// its branch or ref, upstream, and working tree state.
type membersModel struct {
	e      *env
	name   string
	pkg    *core.Package
	status core.PackageStatus
	loaded bool
	err    string
	tb     *table
	filter string
}

// newMembers builds the members view of a package.
func newMembers(e *env, name string) *membersModel {
	return &membersModel{e: e, name: name, tb: newTable([]column{
		{title: "name", max: 24},
		{title: "mode"},
		{title: "branch/ref", max: 32},
		{title: "head"},
		{title: "upstream", max: 28},
		{title: "↑", right: true},
		{title: "↓", right: true},
		{title: "dirty", right: true},
		{title: "untr", right: true},
		{title: "role", flex: true},
	})}
}

// Init implements screen.
func (m *membersModel) Init() tea.Cmd { return m.load() }

// load reads the package and its member status off the UI goroutine.
func (m *membersModel) load() tea.Cmd {
	c, name := m.e.core, m.name
	return func() tea.Msg {
		p, err := c.Load(name)
		if err != nil {
			return membersMsg{name: name, err: err}
		}
		return membersMsg{name: name, pkg: p, status: c.Status(p)}
	}
}

// Title implements screen.
func (m *membersModel) Title() string {
	return frameTitle("members", m.name, m.filter, len(m.tb.rows))
}

// Crumb implements screen.
func (m *membersModel) Crumb() string { return m.name }

// Hints implements screen.
func (m *membersModel) Hints() []hint {
	return []hint{
		{"o", "Open"}, {"l", "Launch…"}, {"d", "Describe"}, {"y", "Copy path"},
		{"e", "Edit package"}, {"s", "Sync"}, {"f", "Fetch"},
		{"N", "Sort name"}, {"M", "Sort mode"}, {"esc", "Back"},
	}
}

// setFilter implements filterable.
func (m *membersModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
}

// filterValue implements filterable.
func (m *membersModel) filterValue() string { return m.filter }

// selectedPackage implements packageSelector.
func (m *membersModel) selectedPackage() string { return m.name }

// selectedMember returns the highlighted member's status.
func (m *membersModel) selectedMember() (core.MemberStatus, bool) {
	r, ok := m.tb.selected()
	if !ok {
		return core.MemberStatus{}, false
	}
	for _, ms := range m.status.Members {
		if ms.Name == r.id {
			return ms, true
		}
	}
	return core.MemberStatus{}, false
}

// count formats a counter cell: dim zero, warning colour otherwise.
func count(n int, applicable bool) cell {
	if !applicable {
		return keyed("-", numKey(0), styleDim)
	}
	if n == 0 {
		return keyed("0", numKey(0), styleDim)
	}
	return keyed(fmt.Sprint(n), numKey(n), styleWarn)
}

// memberRow builds one table row from a member's status.
func memberRow(ms core.MemberStatus) row {
	mode := plain("active")
	if ms.Mode == manifest.ModeReference {
		mode = styled("ref", styleDim)
	}
	where := plain(ms.Branch)
	if ms.Branch == "" {
		where = plain(ms.Ref)
	}
	switch {
	case ms.Missing:
		where = styled("missing", styleErr)
	case ms.Error != "":
		where = styled("error: "+ms.Error, styleErr)
	}
	head := ms.Head
	if len(head) > 8 {
		head = head[:8]
	}
	tracked := ms.Upstream != ""
	return row{
		id:     ms.Name,
		search: ms.Name + " " + ms.Branch + " " + ms.Role,
		cells: []cell{
			plain(ms.Name), mode, where, styled(head, styleDim), styled(ms.Upstream, styleDim),
			count(ms.Ahead, tracked), count(ms.Behind, tracked),
			count(ms.Dirty, !ms.Missing), count(ms.Untracked, !ms.Missing),
			styled(ms.Role, styleDim),
		},
	}
}

// fill rebuilds the rows from the loaded status.
func (m *membersModel) fill() {
	var rows []row
	for _, ms := range m.status.Members {
		rows = append(rows, memberRow(ms))
	}
	switch {
	case m.err != "":
		m.tb.empty = m.err
	case !m.loaded:
		m.tb.empty = "loading…"
	case len(m.status.Members) == 0:
		m.tb.empty = "This package has no members."
	default:
		m.tb.empty = "No match."
	}
	m.tb.setRows(rows)
}

// Update implements screen.
func (m *membersModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case membersMsg:
		if t.name != m.name {
			return m, nil
		}
		m.loaded = true
		if t.err != nil {
			m.err = t.err.Error()
			m.status = core.PackageStatus{}
			m.fill()
			return m, flashErr(m.err)
		}
		m.err, m.pkg, m.status = "", t.pkg, t.status
		m.fill()
		return m, nil
	case refreshMsg:
		return m, m.load()
	case fetchedMsg:
		if t.name != m.name {
			return m, nil
		}
		if t.err != nil {
			return m, tea.Batch(flashErr("fetch "+t.name+": "+t.err.Error()), m.load())
		}
		return m, tea.Batch(flash(fmt.Sprintf("fetched %d member(s)", t.n)), m.load())
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// key handles the members key map.
func (m *membersModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
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
		m.tb.sortBy(memColName)
	case "M":
		m.tb.sortBy(memColMode)
	case "enter", "o":
		return m, func() tea.Msg { return launchMsg{LaunchRequest{Package: m.name}} }
	case "l":
		return m, push(newLaunchPicker(m.e, m.name, "Launch with"))
	case "s":
		return m, push(newProgress(m.e, syncOp(m.e.core, m.name)))
	case "f":
		return m, tea.Batch(flash("fetching "+m.name+"…"), fetchAll(m.e.core, m.name))
	case "e":
		if m.pkg == nil {
			return m, nil
		}
		return m, push(newEditor(m.e, m.pkg))
	case "d":
		if ms, ok := m.selectedMember(); ok {
			return m, push(newDescribe(m.e, m.name, ms.Name))
		}
	case "y":
		if ms, ok := m.selectedMember(); ok && m.pkg != nil {
			for _, mem := range m.pkg.M.Members {
				if mem.Name != ms.Name {
					continue
				}
				dir := m.pkg.MemberDir(mem)
				if err := clipboard.WriteAll(dir); err != nil {
					return m, flashErr("could not copy: " + err.Error() + " — " + dir)
				}
				return m, flash("copied " + paths.Contract(dir))
			}
		}
	}
	return m, nil
}

// View implements screen.
func (m *membersModel) View(w, h int) string { return m.tb.view(w, h) }
