package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/doneill612/vmono/internal/core"
)

// Messages of the remote-branch picker.
type (
	// remoteBranchesMsg delivers the members' remote branches to one picker:
	// those on disk, or those seen after a fetch.
	remoteBranchesMsg struct {
		owner   *remotePicker
		list    []core.MemberBranches
		fetched bool
	}
	// remotePickedMsg reports what was chosen to the prompt that opened the
	// picker: a branch and the members whose remote has it, or base for the
	// member's own base, or neither for the row that chooses nothing.
	remotePickedMsg struct {
		owner   *sessionPrompt
		target  pickTarget
		branch  string
		base    bool
		members []string
	}
)

// pickTarget says what a branch is being picked for.
type pickTarget struct {
	// member is the one the choice is for, which is then the only one listed,
	// or "" for every active member.
	member string
	// reference is set when member is a reference member, and the choice is
	// what it is checked out at rather than what it is built on.
	reference bool
}

// remoteChoice is one row of the picker: a branch merged across the members,
// or with no branch, the choice of nothing or of the member's base.
type remoteChoice struct {
	branch           string
	base             bool
	members, missing []string
	when             time.Time
}

// remotePicker is the dialog that chooses a branch on the members' remotes for
// a new session to build its branches on, or to check a reference member out
// at. Branches of the same name are one row, saying which members
// have it; those most members have come first, then the most
// recent. It opens on what the last fetch saw and fetches meanwhile, so it is
// usable at once and stays usable offline: a member that cannot be fetched or
// listed is named in a notice and never blocks a choice.
type remotePicker struct {
	owner  *sessionPrompt
	target pickTarget
	// exclude is a branch that cannot be chosen: the session's own, when the
	// choice is what to build on.
	exclude string
	// base, when set, offers the member's base, which it names, as a choice.
	base    string
	c       *core.Core
	p       *core.Package
	in      textinput.Model
	spin    spinner.Model
	all     []remoteChoice
	shown   []remoteChoice
	notices []string
	cursor  int
	offset  int
	// loaded is set by the first result, fetched by the one after fetching.
	loaded, fetched bool
	// closing is set once a branch is chosen; see palette.
	closing bool
}

// newRemotePicker builds the picker for a new session of p.
func newRemotePicker(owner *sessionPrompt, c *core.Core, p *core.Package, target pickTarget) *remotePicker {
	in := newTextInput()
	in.Prompt = "› "
	in.CharLimit = 200
	return &remotePicker{owner: owner, target: target, c: c, p: p, in: in, spin: newSpinner()}
}

// modal implements modalScreen.
func (m *remotePicker) modal() {}

// capturing implements capturing: the input owns every key.
func (m *remotePicker) capturing() bool { return true }

// Init implements screen. It lists what is on disk and fetches side by side.
func (m *remotePicker) Init() tea.Cmd {
	return tea.Batch(m.in.Focus(), m.spin.Tick, m.load(false), m.load(true))
}

// load lists the branches, after a fetch when asked, off the UI goroutine.
func (m *remotePicker) load(fetch bool) tea.Cmd {
	return func() tea.Msg {
		return remoteBranchesMsg{owner: m, list: m.c.RemoteBranches(m.p, m.target.member, fetch), fetched: fetch}
	}
}

// Title implements screen.
func (m *remotePicker) Title() string {
	switch {
	case m.target.reference:
		return "remote branch for " + m.target.member
	case m.target.member != "":
		return "build " + m.target.member + " on"
	}
	return "build on a remote branch"
}

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *remotePicker) Crumb() string { return "" }

// Hints implements screen.
func (m *remotePicker) Hints() []hint { return nil }

// receive merges the members' branches into rows, keeping the highlighted
// branch under the cursor when rows arrive or move.
func (m *remotePicker) receive(list []core.MemberBranches) {
	var selected remoteChoice
	if m.cursor < len(m.shown) {
		selected = m.shown[m.cursor]
	}
	byName := map[string]*remoteChoice{}
	var unfetched, unlisted []string
	for _, mb := range list {
		if mb.ListErr != nil {
			unlisted = append(unlisted, mb.Member)
		} else if mb.FetchErr != nil {
			unfetched = append(unfetched, mb.Member)
		}
		for _, b := range mb.Branches {
			if b.Name == m.exclude {
				continue
			}
			ch := byName[b.Name]
			if ch == nil {
				ch = &remoteChoice{branch: b.Name}
				byName[b.Name] = ch
			}
			ch.members = append(ch.members, mb.Member)
			if b.When.After(ch.when) {
				ch.when = b.When
			}
		}
	}
	m.all = nil
	for _, ch := range byName {
		for _, mb := range list {
			if !contains(ch.members, mb.Member) {
				ch.missing = append(ch.missing, mb.Member)
			}
		}
		m.all = append(m.all, *ch)
	}
	sort.Slice(m.all, func(i, j int) bool {
		a, b := m.all[i], m.all[j]
		if len(a.members) != len(b.members) {
			return len(a.members) > len(b.members)
		}
		if !a.when.Equal(b.when) {
			return a.when.After(b.when)
		}
		return a.branch < b.branch
	})
	m.all = append(m.fixed(), m.all...)
	m.notices = nil
	if len(unfetched) > 0 {
		m.notices = append(m.notices, "couldn't fetch "+strings.Join(unfetched, ", ")+"; showing the last fetch")
	}
	if len(unlisted) > 0 {
		m.notices = append(m.notices, "couldn't list the branches of "+strings.Join(unlisted, ", "))
	}
	m.refilter()
	for i, ch := range m.shown {
		if ch.branch == selected.branch && ch.base == selected.base {
			m.cursor = i
		}
	}
}

// fixed are the rows that are not branches, listed first.
func (m *remotePicker) fixed() []remoteChoice {
	if m.base != "" {
		return []remoteChoice{{}, {base: true}}
	}
	return []remoteChoice{{}}
}

// refilter narrows the rows to those matching the query, best match first. The
// rows that are not branches are only offered without a query.
func (m *remotePicker) refilter() {
	q := strings.TrimSpace(m.in.Value())
	m.cursor, m.offset = 0, 0
	if q == "" || len(m.all) == 0 {
		m.shown = m.all
		return
	}
	rows := m.all[len(m.fixed()):]
	hay := make([]string, len(rows))
	for i, ch := range rows {
		hay[i] = ch.branch
	}
	m.shown = nil
	for _, match := range fuzzy.Find(q, hay) {
		m.shown = append(m.shown, rows[match.Index])
	}
}

// Update implements screen.
func (m *remotePicker) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if m.fetched {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		return m, cmd
	case remoteBranchesMsg:
		// A listing from disk that arrives after the fetched one is older.
		if t.owner != m || (m.fetched && !t.fetched) {
			return m, nil
		}
		m.loaded = true
		m.fetched = m.fetched || t.fetched
		m.receive(t.list)
		return m, nil
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// key handles the picker's keys; anything it does not use is typed.
func (m *remotePicker) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.closing {
		return m, nil
	}
	switch k.String() {
	case "esc":
		m.closing = true
		return m, pop(m)
	case "up", "ctrl+k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "ctrl+j":
		m.cursor = max(min(m.cursor+1, len(m.shown)-1), 0)
	case "enter":
		if m.cursor < len(m.shown) {
			ch := m.shown[m.cursor]
			m.closing = true
			return m, tea.Sequence(pop(m), func() tea.Msg {
				return remotePickedMsg{owner: m.owner, target: m.target, branch: ch.branch, base: ch.base, members: ch.members}
			})
		}
	default:
		before := m.in.Value()
		var cmd tea.Cmd
		m.in, cmd = m.in.Update(k)
		if m.in.Value() != before {
			m.refilter()
		}
		return m, cmd
	}
	return m, nil
}

// choiceDetail words what stands right of a branch: the members that have it,
// those that do not, and its age.
func choiceDetail(ch remoteChoice) string {
	if ch.branch == "" {
		return ""
	}
	text := strings.Join(ch.members, " ")
	if len(ch.missing) > 0 {
		text += " (not in: " + strings.Join(ch.missing, ", ") + ")"
	}
	return text + " · " + ago(ch.when)
}

// View implements screen.
func (m *remotePicker) View(w, h int) string {
	width := min(76, w-4)
	inner := max(width-3, 10)
	rows := max(min(10, h-9-len(m.notices)), 3)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	var b strings.Builder
	b.WriteString(" " + clip(m.in.View(), inner-1) + "\n")
	status := ""
	if !m.fetched {
		status = scanningLabel(m.spin, "fetching…")
	}
	b.WriteString(" " + styleDim.Render(status) + "\n")
	if m.loaded && len(m.shown) == 0 {
		b.WriteString(" " + styleDim.Render("no match") + "\n")
	}
	for i := m.offset; i < len(m.shown) && i < m.offset+rows; i++ {
		ch := m.shown[i]
		label := ch.branch
		switch {
		case label != "":
		case ch.base:
			label = "its base: " + m.base
		case m.target.reference:
			label = "none: the package's ref"
		case m.target.member != "":
			label = "same as build on above"
		default:
			label = "none: start from each member's base"
		}
		detail := choiceDetail(ch)
		if detail != "" {
			detail = fit(detail, max(min(lipgloss.Width(detail), inner/2), 4), false)
		}
		dw := lipgloss.Width(detail)
		label = fit(label, max(min(lipgloss.Width(label), inner-dw-6), 4), false)
		gap := strings.Repeat(" ", max(inner-4-lipgloss.Width(label)-dw, 1))
		cursor := "  "
		if i == m.cursor {
			cursor, label = styleSelected.Render("▸ "), styleSelected.Render(label)
		}
		b.WriteString(" " + cursor + label + gap + styleDim.Render(detail) + "\n")
	}
	for _, n := range m.notices {
		b.WriteString(" " + styleWarn.Render(clip(n, inner-1)) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "pick", "↑/↓", "move", "esc", "cancel"))
	return dialog(m.Title(), b.String(), width)
}
