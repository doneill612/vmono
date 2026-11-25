package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/herdr"
	"github.com/doneill612/vmono/internal/paths"
)

// sessionsMsg delivers one package's sessions.
type sessionsMsg struct {
	name string
	pkg  *core.Package
	list []core.SessionSummary
	err  error
}

// sessionActions lists everything the sessions view can do to one session,
// in the order the menu shows it. Launching comes first because it is what a
// session is for; deleting comes last. Each key is also the view's own shortcut
// for the action.
var sessionActions = []action{
	{id: "launch", label: "Launch", key: "o"},
	{id: "launch-with", label: "Launch with…", key: "l"},
	{id: "members", label: "Members", key: "m"},
	{id: "describe", label: "Describe", key: "d"},
	{id: "sync", label: "Sync", key: "s"},
	{id: "fetch", label: "Fetch", key: "f"},
	{id: "update", label: "Update", key: "u"},
	{id: "copy", label: "Copy path", key: "y"},
	{id: "push", label: "Push", key: "P"},
	{id: "delete", label: "Delete", key: "ctrl+d", danger: true},
}

// sessionsModel is the drill-down from a package: one row per session, which
// is the unit of work a harness is launched into. Every session has a
// worktree of every member of the package, each on the session's own branch.
type sessionsModel struct {
	e      *env
	name   string
	pkg    *core.Package
	list   []core.SessionSummary
	loaded bool
	err    string
	tb     *table
	filter string
	pv     previewPane
	spin   spinner.Model
	// updating is the session an update was asked for while its agent was
	// working; asking again goes ahead.
	updating string
}

// newSessions builds the sessions view of a package.
func newSessions(e *env, name string) *sessionsModel {
	cols := []column{
		{title: "session", max: 28},
		{title: "branch", flex: true},
		{title: "state"},
		{title: "opened"},
	}
	if e.agents != nil {
		cols = append(cols, column{title: "agent"})
	}
	m := &sessionsModel{e: e, name: name, spin: newSpinner(), tb: newTable(cols)}
	m.fill()
	return m
}

// Init implements screen. It starts the load and the spinner that animates
// the placeholder until the sessions arrive.
func (m *sessionsModel) Init() tea.Cmd { return tea.Batch(m.load(), m.spin.Tick) }

// load reads the package and its sessions with dirty markers, off the UI
// goroutine.
func (m *sessionsModel) load() tea.Cmd {
	c, name := m.e.core, m.name
	return func() tea.Msg {
		p, err := c.Load(name)
		if err != nil {
			return sessionsMsg{name: name, err: err}
		}
		list, err := c.ListSessions(p, true)
		return sessionsMsg{name: name, pkg: p, list: list, err: err}
	}
}

// Title implements screen.
func (m *sessionsModel) Title() string {
	n := len(m.tb.rows)
	if !m.loaded {
		n = -1
	}
	return frameTitle("sessions", m.name, m.filter, n)
}

// Crumb implements screen.
func (m *sessionsModel) Crumb() string { return m.name }

// Hints implements screen.
func (m *sessionsModel) Hints() []hint {
	return []hint{
		{"enter", "Actions"}, {"o", "Launch"}, {"l", "Launch with…"}, {"n", "New session"}, {"m", "Members"},
		{"d", "Describe"}, {"y", "Copy path"}, {"e", "Edit package"}, {"s", "Sync"},
		{"f", "Fetch"}, {"u", "Update"}, {"P", "Push"}, {"ctrl+d", "Delete"},
		{"esc", "Back"},
	}
}

// setFilter implements filterable.
func (m *sessionsModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
}

// filterValue implements filterable.
func (m *sessionsModel) filterValue() string { return m.filter }

// previewSync implements previewer.
func (m *sessionsModel) previewSync() tea.Cmd { return m.pv.sync(m, m.tb.selectedID()) }

// previewView implements previewer.
//
// The agent's state goes in as the pane is drawn, under the heading: it changes
// far more often than the rest, which is loaded once.
func (m *sessionsModel) previewView(w, h int) string {
	id := m.tb.selectedID()
	state := m.e.sessionStates(m.name)[id]
	if state == "" {
		return m.pv.view(id, w, h)
	}
	head, rest, _ := strings.Cut(m.pv.view(id, w, h-1), "\n")
	return head + "\n" + strings.TrimRight(previewField("agent", agentGlyphs[state]+" "+state, w), "\n") + "\n" + rest
}

// loadPreview reads one session's live status, off the UI goroutine, for the
// preview pane.
func (m *sessionsModel) loadPreview(id string) tea.Cmd {
	c, pkg := m.e.core, m.pkg
	var sum core.SessionSummary
	for _, s := range m.list {
		if s.Name == id {
			sum = s
		}
	}
	return loadPreview(m, id, func() (func(int) string, error) {
		if pkg == nil {
			return nil, fmt.Errorf("package not loaded")
		}
		s, err := c.LoadSession(pkg, id)
		if err != nil {
			return nil, err
		}
		return buildSessionPreview(sum, c.Status(s)), nil
	})
}

// footerHints implements footerHinter.
func (m *sessionsModel) footerHints() []hint {
	return []hint{{"enter", "actions"}, {"n", "new session"}, {"/", "filter"}, {"esc", "back"}}
}

// tbl implements tabular.
func (m *sessionsModel) tbl() *table { return m.tb }

// rowActions implements rowActor.
func (m *sessionsModel) rowActions() (rowContext, bool) {
	s, ok := m.selected()
	return rowContext{kind: "session", subject: s.Name, label: s.Name, items: sessionActions}, ok
}

// selected returns the highlighted session.
func (m *sessionsModel) selected() (core.SessionSummary, bool) {
	r, ok := m.tb.selected()
	if !ok {
		return core.SessionSummary{}, false
	}
	for _, s := range m.list {
		if s.Name == r.id {
			return s, true
		}
	}
	return core.SessionSummary{}, false
}

// sessionRow builds one table row. A merged session is drawn dim, since it is
// done with.
func sessionRow(s core.SessionSummary) row {
	state, stateKey := sessionState(s)
	name := plain(s.Name)
	if s.State == core.SessionMerged {
		name = styled(s.Name, styleDim)
	}
	state.key = stateKey
	opened := styled("never", styleDim)
	if t, err := time.Parse(time.RFC3339, s.LastOpened); err == nil {
		opened = keyed(ago(t), s.LastOpened, styleDim)
	}
	return row{
		id:     s.Name,
		search: s.Name + " " + s.Branch,
		cells:  []cell{name, styled(s.Branch, styleDim), state, opened},
	}
}

// sessionState returns the state cell of a session and its sort key, the states
// that want attention first.
func sessionState(s core.SessionSummary) (c cell, key string) {
	switch {
	case s.Dirty:
		return styled("dirty", styleWarn), "0"
	case len(s.RemoteDeleted) > 0:
		return styled("remote deleted", styleWarn), "1"
	case s.State == core.SessionPushed:
		return styled(s.Label(), styleOK), "3"
	case s.State == core.SessionMerged:
		return styled(s.Label(), styleDim), "4"
	}
	return styled("clean", styleOK), "2"
}

// mergedLast returns the sessions with merged ones moved below the rest, each
// group keeping its order.
func mergedLast(list []core.SessionSummary) []core.SessionSummary {
	out := make([]core.SessionSummary, 0, len(list))
	for _, merged := range []bool{false, true} {
		for _, s := range list {
			if (s.State == core.SessionMerged) == merged {
				out = append(out, s)
			}
		}
	}
	return out
}

// fill rebuilds the rows from the loaded list.
func (m *sessionsModel) fill() {
	var rows []row
	states := m.e.sessionStates(m.name)
	for _, s := range mergedLast(m.list) {
		r := sessionRow(s)
		if m.e.agents != nil {
			r.cells = append(r.cells, agentCell(states[s.Name]))
		}
		rows = append(rows, r)
	}
	switch {
	case m.err != "":
		m.tb.empty = m.err
	case !m.loaded:
		m.tb.empty = scanningLabel(m.spin, "loading…")
	case len(m.list) == 0:
		m.tb.empty = "No sessions yet. Press n to start one."
	default:
		m.tb.empty = "No match."
	}
	m.tb.setRows(rows)
}

// Update implements screen.
func (m *sessionsModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	if cmd, ok := m.pv.handle(msg, m, m.loadPreview); ok {
		return m, cmd
	}
	switch t := msg.(type) {
	case spinner.TickMsg:
		if m.loaded {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		m.tb.empty = scanningLabel(m.spin, "loading…")
		return m, cmd
	case sessionsMsg:
		if t.name != m.name {
			return m, nil
		}
		m.loaded = true
		if t.err != nil {
			m.err, m.list = t.err.Error(), nil
			m.fill()
			return m, flashErr(m.err)
		}
		m.err, m.pkg, m.list = "", t.pkg, t.list
		m.fill()
		return m, nil
	case agentStateMsg:
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
	case actionMsg:
		if t.owner != screen(m) {
			return m, nil
		}
		for _, s := range m.list {
			if s.Name == t.subject {
				return m.act(t.id, s)
			}
		}
		return m, flashErr("session " + t.subject + " no longer exists")
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// key handles the sessions key map.
func (m *sessionsModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.tb.handleKey(k.String()) {
		return m, nil
	}
	switch k.String() {
	case "esc":
		if m.filter != "" {
			m.setFilter("")
			return m, nil
		}
		return m, pop(m)
	case "n":
		return m, push(m, newSessionPrompt(m.e, m.name))
	case "e":
		if m.pkg == nil {
			return m, nil
		}
		return m, push(m, newEditor(m.e, m.pkg))
	case "f":
		return m, m.fetch()
	}
	sel, ok := m.selected()
	if !ok {
		return m, nil
	}
	if k.String() == "enter" {
		return m, push(m, newActionMenu(m, "session", sel.Name, m.name, sessionActions))
	}
	if a, ok := findAction(sessionActions, k.String()); ok {
		return m.act(a.id, sel)
	}
	return m, nil
}

// fetch starts fetching every member's source for the package.
func (m *sessionsModel) fetch() tea.Cmd {
	return tea.Batch(flash("fetching "+m.name+"…"), fetchAll(m.e.core, m.name))
}

// act performs the per-session action with the given id on sel. It serves
// both the sessions view's own shortcut keys and the choices made in the
// session menu.
func (m *sessionsModel) act(id string, sel core.SessionSummary) (screen, tea.Cmd) {
	switch id {
	case "launch":
		return m, func() tea.Msg { return launchMsg{LaunchRequest{Package: m.name, Session: sel.Name}} }
	case "fetch":
		return m, m.fetch()
	case "launch-with":
		return m, push(m, newLaunchPicker(m.e, m.name, sel.Name, "Launch with"))
	case "members":
		return m, push(m, newMembers(m.e, m.name, sel.Name))
	case "describe":
		return m, push(m, newDescribe(m.e, m.name, sel.Name, ""))
	case "sync":
		return m, push(m, newProgress(m.e, syncSessionOp(m.e.core, m.name, sel.Name)))
	case "update":
		if m.e.sessionStates(m.name)[sel.Name] == herdr.Working && m.updating != sel.Name {
			m.updating = sel.Name
			return m, flashErr("an agent is working in " + sel.Name + " and updating rewrites its branch; u again to update anyway")
		}
		m.updating = ""
		return m, push(m, newProgress(m.e, updateOp(m.e.core, m.name, sel.Name)))
	case "copy":
		if err := clipboard.WriteAll(sel.Path); err != nil {
			return m, flashErr("could not copy: " + err.Error() + " — " + sel.Path)
		}
		return m, flash("copied " + paths.Contract(sel.Path))
	case "delete":
		return m, m.startDelete(sel.Name)
	case "push":
		return m, startPush(m, m.e, m.name, sel.Name, nil)
	}
	return m, nil
}

// syncSessionOp builds the sync operation for one session. Like create it
// ends by offering to launch a harness there.
func syncSessionOp(c *core.Core, pkg, session string) operation {
	return operation{
		title:   "Sync " + pkg + "/" + session,
		pkg:     pkg,
		session: session,
		run: func(force, reuse bool) error {
			s, err := c.ResolveSession(pkg + "/" + session)
			if err != nil {
				return err
			}
			_, err = c.Sync(s, core.SyncOptions{})
			return err
		},
	}
}

// updateOp builds the operation that brings one session's branches up to date
// with what they are built on. Nothing is asked first: a member that
// cannot be updated cleanly is left as it was. A failure offers to launch the
// harness in the session, which is where a conflict gets resolved.
func updateOp(c *core.Core, pkg, session string) operation {
	return operation{
		title: "Update " + pkg + "/" + session,
		run: func(_, _ bool) error {
			s, err := c.ResolveSession(pkg + "/" + session)
			if err != nil {
				return err
			}
			_, err = c.Update(s, core.UpdateOptions{})
			return err
		},
		reveal: true,
		fixIn:  &LaunchRequest{Package: pkg, Session: session},
	}
}

// startDelete opens the delete confirmation for a session. The package and
// its other sessions are left alone.
func (m *sessionsModel) startDelete(session string) tea.Cmd {
	c := m.e.core
	s, err := c.ResolveSession(m.name + "/" + session)
	if err != nil {
		return flashErr(err.Error())
	}
	rows := c.SafetyCheck(s)
	return push(m, newConfirm(m.e, "Delete session", session, rows, true,
		func(force, deleteBranches bool) screen {
			return newProgress(m.e, operation{
				title: "Delete " + m.name + "/" + session,
				run: func(_, _ bool) error {
					_, err := c.DeleteSession(s, core.DeleteOptions{Force: force, DeleteBranches: deleteBranches})
					return err
				},
				reveal: true,
			})
		},
		nil).about("session"))
}

// View implements screen.
func (m *sessionsModel) View(w, h int) string { return m.tb.view(w, h) }
