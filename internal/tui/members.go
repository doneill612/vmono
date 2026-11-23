package tui

import (
	"fmt"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// membersMsg delivers one session's members with live git status.
type membersMsg struct {
	id      string
	session *core.Session
	status  core.SessionStatus
	err     error
}

// memberActions lists everything the members view can do, in the order its
// menu shows it. Most act on the whole session; describe and copy path act on
// the highlighted member. Each key is also the view's own shortcut.
var memberActions = []action{
	{id: "launch", label: "Launch", key: "o"},
	{id: "launch-with", label: "Launch with…", key: "l"},
	{id: "describe", label: "Describe", key: "d"},
	{id: "copy", label: "Copy path", key: "y"},
	{id: "edit", label: "Edit package", key: "e"},
	{id: "sync", label: "Sync", key: "s"},
	{id: "fetch", label: "Fetch", key: "f"},
}

// membersModel is the drill-down from a session: one row per member with its
// branch or ref, upstream, and working tree state in that session.
type membersModel struct {
	e       *env
	name    string
	session string
	sess    *core.Session
	status  core.SessionStatus
	loaded  bool
	err     string
	tb      *table
	filter  string
}

// newMembers builds the members view of one session of a package.
func newMembers(e *env, name, session string) *membersModel {
	return &membersModel{e: e, name: name, session: session, tb: newTable([]column{
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

// id identifies the session so stale results are ignored.
func (m *membersModel) id() string { return m.name + "/" + m.session }

// load reads the session and its member status off the UI goroutine.
func (m *membersModel) load() tea.Cmd {
	c, id := m.e.core, m.id()
	return func() tea.Msg {
		s, err := c.ResolveSession(id)
		if err != nil {
			return membersMsg{id: id, err: err}
		}
		return membersMsg{id: id, session: s, status: c.Status(s)}
	}
}

// Title implements screen.
func (m *membersModel) Title() string {
	return frameTitle("members", m.id(), m.filter, len(m.tb.rows))
}

// Crumb implements screen.
func (m *membersModel) Crumb() string { return m.session }

// Hints implements screen.
func (m *membersModel) Hints() []hint {
	return []hint{
		{"enter", "Actions"}, {"o", "Open"}, {"l", "Launch…"}, {"d", "Describe"}, {"y", "Copy path"},
		{"e", "Edit package"}, {"s", "Sync"}, {"f", "Fetch"}, {"esc", "Back"},
	}
}

// setFilter implements filterable.
func (m *membersModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
}

// filterValue implements filterable.
func (m *membersModel) filterValue() string { return m.filter }

// previewSync implements previewer. The status the preview shows is already
// loaded, so there is nothing to start.
func (m *membersModel) previewSync() tea.Cmd { return nil }

// previewView implements previewer.
func (m *membersModel) previewView(w, h int) string {
	ms, ok := m.selectedMember()
	if !ok {
		return ""
	}
	return " " + styleLabel.Render(truncate(ms.Name, w-2)) + "\n" + buildMemberPreview(ms, w)
}

// footerHints implements footerHinter.
func (m *membersModel) footerHints() []hint {
	return []hint{{"enter", "actions"}, {"/", "filter"}, {"esc", "back"}}
}

// tbl implements tabular.
func (m *membersModel) tbl() *table { return m.tb }

// rowActions implements rowActor.
func (m *membersModel) rowActions() (rowContext, bool) {
	ms, ok := m.selectedMember()
	return rowContext{kind: "member", subject: ms.Name, label: ms.Name, items: memberActions}, ok
}

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
		if t.id != m.id() {
			return m, nil
		}
		m.loaded = true
		if t.err != nil {
			m.err = t.err.Error()
			m.status = core.SessionStatus{}
			m.fill()
			return m, flashErr(m.err)
		}
		m.err, m.sess, m.status = "", t.session, t.status
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
		return m.act(t.id, t.subject)
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
	}
	if k.String() == "enter" {
		if ms, ok := m.selectedMember(); ok {
			return m, push(newActionMenu(m, "member", ms.Name, m.id(), memberActions))
		}
		return m, nil
	}
	if a, ok := findAction(memberActions, k.String()); ok {
		return m.act(a.id, m.selectedMemberName())
	}
	return m, nil
}

// selectedMemberName returns the highlighted member's name, or "".
func (m *membersModel) selectedMemberName() string {
	ms, _ := m.selectedMember()
	return ms.Name
}

// act performs the members-view action with the given id. member is the row
// the action was chosen on; it is empty when nothing is highlighted, which
// only the member-specific actions mind.
func (m *membersModel) act(id, member string) (screen, tea.Cmd) {
	switch id {
	case "launch":
		return m, func() tea.Msg { return launchMsg{LaunchRequest{Package: m.name, Session: m.session}} }
	case "launch-with":
		return m, push(newLaunchPicker(m.e, m.name, m.session, "Launch with"))
	case "sync":
		return m, push(newProgress(m.e, syncSessionOp(m.e.core, m.name, m.session)))
	case "fetch":
		return m, tea.Batch(flash("fetching "+m.name+"…"), fetchAll(m.e.core, m.name))
	case "edit":
		if m.sess == nil {
			return m, nil
		}
		return m, push(newEditor(m.e, m.sess.Pkg))
	case "describe":
		if member != "" {
			return m, push(newDescribe(m.e, m.name, m.session, member))
		}
	case "copy":
		if member == "" || m.sess == nil {
			return m, nil
		}
		for _, mem := range m.sess.Pkg.M.Members {
			if mem.Name != member {
				continue
			}
			dir := m.sess.MemberDir(mem)
			if err := clipboard.WriteAll(dir); err != nil {
				return m, flashErr("could not copy: " + err.Error() + " — " + dir)
			}
			return m, flash("copied " + paths.Contract(dir))
		}
	}
	return m, nil
}

// View implements screen.
func (m *membersModel) View(w, h int) string { return m.tb.view(w, h) }
