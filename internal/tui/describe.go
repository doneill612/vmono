package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// describeMsg delivers what a describe view shows. build renders it for a
// given width, so the text re-wraps when the terminal is resized.
type describeMsg struct {
	id    string
	pkg   *core.Package
	build func(width int) string
	err   error
}

// describeModel is the scrollable detail view of a package, of a package as
// seen from one of its sessions, or of one member in a session: purpose,
// context, notes and paths for a package, plus branch and git state when a
// session is given; location and git state for a member.
type describeModel struct {
	e       *env
	name    string
	session string
	member  string
	pkg     *core.Package
	build   func(width int) string
	err     string
	vp      viewport.Model
	renderW int
}

// newDescribe builds the detail view. session is empty to describe the
// package alone, with no live git state; member is empty to describe the
// package itself rather than one member, and needs a session.
func newDescribe(e *env, name, session, member string) *describeModel {
	return &describeModel{e: e, name: name, session: session, member: member, vp: viewport.New(0, 0)}
}

// id identifies the subject so stale results are ignored.
func (m *describeModel) id() string { return m.name + "/" + m.session + "/" + m.member }

// Init implements screen.
func (m *describeModel) Init() tea.Cmd { return m.load() }

// load gathers the data off the UI goroutine.
func (m *describeModel) load() tea.Cmd {
	c, name, session, member, id := m.e.core, m.name, m.session, m.member, m.id()
	return func() tea.Msg {
		p, err := c.Load(name)
		if err != nil {
			return describeMsg{id: id, err: err}
		}
		rows, err := c.ListSessions(p, false)
		if err != nil {
			return describeMsg{id: id, pkg: p, err: err}
		}
		if session == "" {
			return describeMsg{id: id, pkg: p, build: func(w int) string { return describePackage(p, rows, nil, nil, w) }}
		}
		s, err := c.LoadSession(p, session)
		if err != nil {
			return describeMsg{id: id, pkg: p, err: err}
		}
		ins, err := c.Inspect(s)
		if err != nil {
			return describeMsg{id: id, pkg: p, err: err}
		}
		st := c.Status(s)
		if member == "" {
			return describeMsg{id: id, pkg: p, build: func(w int) string { return describePackage(p, rows, &ins, &st, w) }}
		}
		for i, im := range ins.Members {
			if im.Name == member {
				ms := st.Members[i]
				return describeMsg{id: id, pkg: p, build: func(w int) string { return describeMember(p, s, im, ms, w) }}
			}
		}
		return describeMsg{id: id, pkg: p, err: fmt.Errorf("%s/%s has no member %q", name, session, member)}
	}
}

// Title implements screen.
func (m *describeModel) Title() string {
	t := "describe(" + m.name + ")"
	if m.session != "" {
		t = "describe(" + m.name + "/" + m.session + ")"
	}
	if m.member != "" {
		t = "describe(" + m.name + "/" + m.session + "/" + m.member + ")"
	}
	if m.build != nil && !(m.vp.AtTop() && m.vp.AtBottom()) {
		t += fmt.Sprintf("[%d%%]", int(m.vp.ScrollPercent()*100))
	}
	return t
}

// Crumb implements screen.
func (m *describeModel) Crumb() string { return "describe" }

// Hints implements screen.
func (m *describeModel) Hints() []hint {
	hs := []hint{{"j/k", "Scroll"}, {"pgup/pgdn", "Page"}, {"g/G", "Top / bottom"}}
	if m.member == "" {
		hs = append(hs, hint{"e", "Edit"})
	}
	return append(hs, hint{"esc", "Back"})
}

// selectedPackage implements packageSelector.
func (m *describeModel) selectedPackage() string { return m.name }

// Update implements screen.
func (m *describeModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case describeMsg:
		if t.id != m.id() {
			return m, nil
		}
		m.pkg, m.build, m.renderW = t.pkg, t.build, -1
		m.err = ""
		if t.err != nil {
			m.err = t.err.Error()
			return m, flashErr(m.err)
		}
		return m, nil
	case refreshMsg:
		return m, m.load()
	case tea.KeyMsg:
		switch t.String() {
		case "esc", "q":
			return m, pop()
		case "e":
			if m.member == "" && m.pkg != nil {
				return m, push(newEditor(m.e, m.pkg))
			}
			return m, nil
		case "g", "home":
			m.vp.GotoTop()
			return m, nil
		case "G", "end":
			m.vp.GotoBottom()
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(t)
		return m, cmd
	}
	return m, nil
}

// View implements screen.
func (m *describeModel) View(w, h int) string {
	if m.err != "" {
		return "\n " + styleErr.Render(m.err)
	}
	if m.build == nil {
		return "\n " + styleDim.Render("loading…")
	}
	m.vp.Width, m.vp.Height = w, h
	if m.renderW != w {
		m.vp.SetContent(m.build(w))
		m.renderW = w
	}
	return m.vp.View()
}

// field formats one "Label: value" line; the value wraps under itself.
func field(label, value string, w int) string {
	const labelW = 14
	if value == "" {
		value = styleDim.Render("-")
		return " " + styleAmber.Render(fit(label+":", labelW, false)) + value
	}
	lines := strings.Split(wrap(value, w-labelW-3), "\n")
	var b strings.Builder
	for i, l := range lines {
		if i == 0 {
			b.WriteString(" " + styleAmber.Render(fit(label+":", labelW, false)) + l)
		} else {
			b.WriteString("\n " + strings.Repeat(" ", labelW) + l)
		}
	}
	return b.String()
}

// textBlock formats free text under a heading, indented and wrapped.
func textBlock(title, text string, w int) string {
	var b strings.Builder
	b.WriteString("\n " + styleHeaderCol.Render(strings.ToUpper(title)) + "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		b.WriteString("   " + styleDim.Render("none recorded") + "\n")
		return b.String()
	}
	b.WriteString("   " + strings.ReplaceAll(wrap(text, w-5), "\n", "\n   ") + "\n")
	return b.String()
}

// describePackage renders the package detail view. With ins and st (a
// session was given) it also shows that session's branch and each member's
// live state; without them it shows the package as defined.
func describePackage(p *core.Package, sessions []core.SessionSummary, ins *core.InspectResult, st *core.SessionStatus, w int) string {
	var b strings.Builder
	h := "default"
	if p.M.Harness != "" {
		h = p.M.Harness
	}
	if ins != nil && ins.Harness != nil {
		h = fmt.Sprintf("%s (%s)", ins.Harness.Name, ins.Harness.Command)
		if !ins.Harness.Available {
			h += " — not found on PATH"
		}
	}
	b.WriteString("\n")
	fields := [][2]string{
		{"Name", p.Name},
		{"Path", paths.Contract(p.Dir)},
	}
	if ins != nil {
		fields = append(fields,
			[2]string{"Session", ins.Session},
			[2]string{"Session path", paths.Contract(ins.Path)},
			[2]string{"Branch", ins.Branch},
		)
	}
	fields = append(fields,
		[2]string{"Harness", h},
		[2]string{"Created", p.M.Created.Local().Format("2006-01-02 15:04")},
		[2]string{"Manifest", paths.Contract(manifest.Path(p.Dir))},
	)
	for _, f := range fields {
		b.WriteString(field(f[0], f[1], w) + "\n")
	}
	b.WriteString(textBlock("Purpose", p.M.Purpose, w))
	b.WriteString(textBlock("Context", p.M.Context, w))

	b.WriteString("\n " + styleHeaderCol.Render("NOTES") + "\n")
	notes := p.M.RecentNotes(10)
	if len(notes) == 0 {
		b.WriteString("   " + styleDim.Render("none") + "\n")
	}
	for _, n := range notes {
		b.WriteString("   " + styleDim.Render(n.At.Local().Format("2006-01-02 15:04")+"  "+n.By) + "\n")
		b.WriteString("   " + strings.ReplaceAll(wrap(n.Text, w-5), "\n", "\n   ") + "\n")
	}

	b.WriteString("\n " + styleHeaderCol.Render("SESSIONS") + "\n")
	if len(sessions) == 0 {
		b.WriteString("   " + styleDim.Render("none yet") + "\n")
	}
	for _, s := range sessions {
		b.WriteString("   " + styleLabel.Render(s.Name) + styleDim.Render("  "+s.Branch) + "\n")
	}

	b.WriteString("\n " + styleHeaderCol.Render("MEMBERS") + "\n")
	for i, mem := range p.M.Members {
		mode := "active"
		if mem.Mode == manifest.ModeReference {
			mode = "reference"
		}
		line := "   " + styleLabel.Render(mem.Name) + styleDim.Render("  "+mode)
		if st != nil {
			ms := st.Members[i]
			where := ms.Branch
			if where == "" {
				where = ms.Ref
			}
			line = "   " + styleLabel.Render(mem.Name) + styleDim.Render("  "+mode+"  "+where) + "  " + memberState(ms)
		}
		b.WriteString(line + "\n")
		if mem.Role != "" {
			b.WriteString("     " + strings.ReplaceAll(wrap(mem.Role, w-7), "\n", "\n     ") + "\n")
		}
		if ins != nil {
			b.WriteString("     " + styleDim.Render(truncate(paths.Contract(ins.Members[i].Path), w-7)) + "\n")
		} else {
			b.WriteString("     " + styleDim.Render(truncate(mem.Source, w-7)) + "\n")
		}
	}
	return b.String()
}

// describeMember renders one member's detail view in a session.
func describeMember(p *core.Package, sess *core.Session, im core.InspectMember, ms core.MemberStatus, w int) string {
	var b strings.Builder
	mode := "active"
	if im.Mode == manifest.ModeReference {
		mode = "reference (read-only, detached)"
	}
	b.WriteString("\n")
	for _, f := range [][2]string{
		{"Package", p.Name},
		{"Session", sess.Name},
		{"Member", im.Name},
		{"Mode", mode},
		{"Role", im.Role},
	} {
		b.WriteString(field(f[0], f[1], w) + "\n")
	}
	b.WriteString("\n " + styleHeaderCol.Render("LOCATION") + "\n")
	for _, f := range [][2]string{
		{"Worktree", paths.Contract(im.Path)},
		{"Source repo", paths.Contract(im.Source)},
		{"Remote", im.Remote},
		{"Git dir", paths.Contract(im.GitCommonDir)},
	} {
		b.WriteString(field(f[0], f[1], w) + "\n")
	}
	b.WriteString("\n " + styleHeaderCol.Render("STATE") + "\n")
	where, base := im.Branch, im.Base
	if where == "" {
		where = im.Ref
	}
	rows := [][2]string{{"Branch/ref", where}, {"Base", base}}
	switch {
	case ms.Missing:
		rows = append(rows, [2]string{"Worktree", "missing (run sync to recreate it)"})
	case ms.Error != "":
		rows = append(rows, [2]string{"Error", ms.Error})
	default:
		rows = append(rows,
			[2]string{"Head", ms.Head},
			[2]string{"Upstream", ms.Upstream},
			[2]string{"Ahead/behind", fmt.Sprintf("↑%d ↓%d", ms.Ahead, ms.Behind)},
			[2]string{"Changes", fmt.Sprintf("%d modified, %d untracked", ms.Dirty, ms.Untracked)},
		)
	}
	for _, f := range rows {
		b.WriteString(field(f[0], f[1], w) + "\n")
	}
	return b.String()
}

// memberState is a short coloured summary of a member's working tree.
func memberState(ms core.MemberStatus) string {
	switch {
	case ms.Missing:
		return styleErr.Render("missing")
	case ms.Error != "":
		return styleErr.Render("error")
	case ms.Dirty > 0 || ms.Untracked > 0:
		return styleWarn.Render(fmt.Sprintf("%d~ %d?", ms.Dirty, ms.Untracked))
	}
	return styleOK.Render("clean")
}
