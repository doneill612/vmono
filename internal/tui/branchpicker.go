package tui

import (
	"fmt"
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

// Messages of the branch picker.
type (
	// branchListMsg delivers the members' remote branches to one picker:
	// those on disk, or those seen after a fetch.
	branchListMsg struct {
		owner   *branchPicker
		list    []core.MemberBranches
		fetched bool
	}
	// branchPickedMsg reports what was chosen to the prompt that opened the
	// picker: a branch, or base for the member's own base, or neither for the
	// row that chooses nothing.
	branchPickedMsg struct {
		owner  *sessionPrompt
		target pickTarget
		pick   pickedBranch
		base   bool
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

// branchChoice is one row of the picker: a branch merged across the members
// that have it, or with no branch, the choice of nothing or of the member's
// base. A local branch and the remote's of the same name are separate rows.
type branchChoice struct {
	pickedBranch
	base    bool
	missing []string
	when    time.Time
	// apart holds the members whose local branch differs from their remote's.
	apart []divergence
}

// divergence is how far a member's local branch is from counterpart, the branch
// of the same name on its remote.
type divergence struct {
	member, counterpart string
	ahead, behind       int
}

// String words the distance, as in "2 ahead, 3 behind origin/main". The row
// already names the branch, so a long counterpart is cut to its remote.
func (d divergence) String() string {
	if remote, _, ok := strings.Cut(d.counterpart, "/"); ok && len(d.counterpart) > 24 {
		d.counterpart = remote
	}
	switch {
	case d.behind == 0:
		return fmt.Sprintf("%d ahead of %s", d.ahead, d.counterpart)
	case d.ahead == 0:
		return fmt.Sprintf("%d behind %s", d.behind, d.counterpart)
	}
	return fmt.Sprintf("%d ahead, %d behind %s", d.ahead, d.behind, d.counterpart)
}

// marker warns that the local branch of a row is not what the remote has, so
// that choosing it is a choice. It is "" when every member's is level.
func (ch branchChoice) marker() string {
	if len(ch.apart) == 0 {
		return ""
	}
	first := ch.apart[0]
	alike := len(ch.apart) == len(ch.members)
	for _, d := range ch.apart {
		alike = alike && d.counterpart == first.counterpart && d.ahead == first.ahead && d.behind == first.behind
	}
	switch {
	case alike:
		return first.String()
	case len(ch.apart) == 1:
		return first.member + ": " + first.String()
	}
	var names []string
	for _, d := range ch.apart {
		names = append(names, d.member)
	}
	return "differs from the remote in " + strings.Join(names, ", ")
}

// branchPicker is the dialog that chooses a branch for a new session to build
// its branches on, or to check a reference member out at. It lists the members'
// local branches and those on their remotes, a remote one in its own colour and
// under its remote's name. A branch is one row, saying which members have it. A
// local row says how far it is from the remote's when they differ. Names most
// members have come first, then the most recent. It opens on what the last fetch
// saw and fetches meanwhile, so it is usable at once and stays usable offline: a
// member that cannot be fetched or listed is named in a notice and never blocks
// a choice.
type branchPicker struct {
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
	all     []branchChoice
	shown   []branchChoice
	notices []string
	cursor  int
	offset  int
	// loaded is set by the first result, fetched by the one after fetching.
	loaded, fetched bool
	// closing is set once a branch is chosen; see palette.
	closing bool
}

// newBranchPicker builds the picker for a new session of p.
func newBranchPicker(owner *sessionPrompt, c *core.Core, p *core.Package, target pickTarget) *branchPicker {
	in := newTextInput()
	in.Prompt = "› "
	in.CharLimit = 200
	return &branchPicker{owner: owner, target: target, c: c, p: p, in: in, spin: newSpinner()}
}

// modal implements modalScreen.
func (m *branchPicker) modal() {}

// capturing implements capturing: the input owns every key.
func (m *branchPicker) capturing() bool { return true }

// Init implements screen. It lists what is on disk and fetches side by side.
func (m *branchPicker) Init() tea.Cmd {
	return tea.Batch(m.in.Focus(), m.spin.Tick, m.load(false), m.load(true))
}

// load lists the branches, after a fetch when asked, off the UI goroutine.
func (m *branchPicker) load(fetch bool) tea.Cmd {
	return func() tea.Msg {
		return branchListMsg{owner: m, list: m.c.ListBranches(m.p, m.target.member, fetch), fetched: fetch}
	}
}

// Title implements screen.
func (m *branchPicker) Title() string {
	switch {
	case m.target.reference:
		return "branch for " + m.target.member
	case m.target.member != "":
		return "build " + m.target.member + " on"
	}
	return "build on a branch"
}

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *branchPicker) Crumb() string { return "" }

// Hints implements screen.
func (m *branchPicker) Hints() []hint { return nil }

// receive merges the members' branches into rows, keeping the highlighted
// branch under the cursor when rows arrive or move. Branches of one name stay
// together, the local one first; the names most members have come first, then
// the most recent.
func (m *branchPicker) receive(list []core.MemberBranches) {
	var selected branchChoice
	if m.cursor < len(m.shown) {
		selected = m.shown[m.cursor]
	}
	byRef := map[string]*branchChoice{}
	var unfetched, unlisted []string
	for _, mb := range list {
		if mb.ListErr != nil {
			unlisted = append(unlisted, mb.Member)
		} else if mb.FetchErr != nil {
			unfetched = append(unfetched, mb.Member)
		}
		for _, b := range mb.Branches {
			if b.Branch == m.exclude {
				continue
			}
			ch := byRef[b.Ref]
			if ch == nil {
				ch = &branchChoice{pickedBranch: pickedBranch{name: b.Name, branch: b.Branch, ref: b.Ref, remote: b.Remote}}
				byRef[b.Ref] = ch
			}
			ch.members = append(ch.members, mb.Member)
			if b.When.After(ch.when) {
				ch.when = b.When
			}
			if b.Ahead+b.Behind > 0 {
				ch.apart = append(ch.apart, divergence{member: mb.Member, counterpart: b.Counterpart, ahead: b.Ahead, behind: b.Behind})
			}
		}
	}
	// A name ranks by the widest and the latest of its rows.
	type rank struct {
		members int
		when    time.Time
	}
	ranks := map[string]rank{}
	m.all = nil
	for _, ch := range byRef {
		for _, mb := range list {
			if !contains(ch.members, mb.Member) {
				ch.missing = append(ch.missing, mb.Member)
			}
		}
		r := ranks[ch.branch]
		r.members = max(r.members, len(ch.members))
		if ch.when.After(r.when) {
			r.when = ch.when
		}
		ranks[ch.branch] = r
		m.all = append(m.all, *ch)
	}
	sort.Slice(m.all, func(i, j int) bool {
		a, b := m.all[i], m.all[j]
		if a.branch != b.branch {
			ra, rb := ranks[a.branch], ranks[b.branch]
			if ra.members != rb.members {
				return ra.members > rb.members
			}
			if !ra.when.Equal(rb.when) {
				return ra.when.After(rb.when)
			}
			return a.branch < b.branch
		}
		if a.remote != b.remote {
			return b.remote
		}
		return a.name < b.name
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
		if ch.ref == selected.ref && ch.base == selected.base {
			m.cursor = i
		}
	}
}

// fixed are the rows that are not branches, listed first.
func (m *branchPicker) fixed() []branchChoice {
	if m.base != "" {
		return []branchChoice{{}, {base: true}}
	}
	return []branchChoice{{}}
}

// refilter narrows the rows to those matching the query, best match first. The
// rows that are not branches are only offered without a query.
func (m *branchPicker) refilter() {
	q := strings.TrimSpace(m.in.Value())
	m.cursor, m.offset = 0, 0
	if q == "" || len(m.all) == 0 {
		m.shown = m.all
		return
	}
	rows := m.all[len(m.fixed()):]
	hay := make([]string, len(rows))
	for i, ch := range rows {
		hay[i] = ch.name
	}
	m.shown = nil
	for _, match := range fuzzy.Find(q, hay) {
		m.shown = append(m.shown, rows[match.Index])
	}
}

// Update implements screen.
func (m *branchPicker) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if m.fetched {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		return m, cmd
	case branchListMsg:
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
func (m *branchPicker) key(k tea.KeyMsg) (screen, tea.Cmd) {
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
				return branchPickedMsg{owner: m.owner, target: m.target, pick: ch.pickedBranch, base: ch.base}
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

// fitRow cuts the parts of a picker row to width columns. What is left of
// detail gives way first, then the marker; the branch is kept whole while it
// fits on its own.
func fitRow(width int, label, marker, detail string) (string, string, string) {
	cut := func(s string, n int) string {
		if lipgloss.Width(s) <= n {
			return s
		}
		if n < 4 {
			return ""
		}
		return fit(s, n, false)
	}
	mark := 0
	if marker != "" {
		mark = 2 + lipgloss.Width(marker)
	}
	// Half the row is the most the detail takes, and it keeps two spaces
	// before it.
	room := min(width/2, width-lipgloss.Width(label)-mark-2)
	if room < 8 {
		room = 0
	}
	detail = cut(detail, room)
	left := width - lipgloss.Width(detail) - 2
	label = cut(label, max(left, 4))
	marker = cut(marker, left-lipgloss.Width(label)-2)
	return label, marker, detail
}

// choiceDetail words what stands right of a branch: the members that have it,
// those that do not, and its age.
func choiceDetail(ch branchChoice) string {
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
func (m *branchPicker) View(w, h int) string {
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
		label := ch.name
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
		label, marker, detail := fitRow(inner-4, label, ch.marker(), choiceDetail(ch))
		used := lipgloss.Width(label) + lipgloss.Width(detail)
		if marker != "" {
			used += 2 + lipgloss.Width(marker)
		}
		gap := strings.Repeat(" ", max(inner-4-used, 2))
		cursor := "  "
		switch {
		case i == m.cursor:
			cursor, label = styleSelected.Render("▸ "), styleSelected.Render(label)
		case ch.remote:
			label = styleRemote.Render(label)
		}
		if marker != "" {
			label += "  " + styleWarn.Render(marker)
		}
		b.WriteString(" " + cursor + label + gap + styleDim.Render(detail) + "\n")
	}
	for _, n := range m.notices {
		b.WriteString(" " + styleWarn.Render(clip(n, inner-1)) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "pick", "↑/↓", "move", "esc", "cancel"))
	return dialog(m.Title(), b.String(), width)
}
