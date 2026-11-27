package tui

import (
	"cmp"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// sessionPrompt is the dialog that starts a session of a package: a name and
// an optional prefix, which together are the branch every active member is on,
// then what the new branches are built on and what each reference member is
// checked out at.
//
// Build on is only ever picked from the members' remote branches, with space:
// once for every member, and in the row of each active member that should
// differ, which may be another branch or its base. It can never be the
// session's own branch, which the dialog says as soon as the two match. A
// reference member's field holds what it is checked out at in place of the
// package's ref, typed, or picked with space, which no ref can hold.
//
// Enter runs the create on the progress screen, which asks whether to work on
// the branch when it is already on a member's remote, and ends at the harness
// picker, so creating a package and starting its first session still ends at a
// running harness.
type sessionPrompt struct {
	e      *env
	pkg    string
	name   textinput.Model
	prefix textinput.Model
	// active names the active members, and bases what the package builds each
	// one on. width is the column their names and the references' are set in.
	active []string
	bases  map[string]string
	width  int
	// from is the branch picked for every member to build on.
	from pickedBranch
	// own is what a member was given in place of from: a branch, or "" for its
	// base. A member that is absent follows from.
	own  map[string]string
	refs refFields
	// focus is the field with the keyboard, in the order of the constants below
	// and then one per active member and one per reference member.
	focus int
	msg   string
}

// pickedBranch is a remote branch chosen in the picker, and the members whose
// remote has it.
type pickedBranch struct {
	name    string
	members []string
}

// Fields of the prompt ahead of the members' fields.
const (
	nameField = iota
	prefixField
	fromField
	firstMember
)

// promptFields is the number of fields in the prompt.
func (m *sessionPrompt) promptFields() int { return m.firstRef() + m.refs.count() }

// firstRef is the first of the reference members' fields.
func (m *sessionPrompt) firstRef() int { return firstMember + len(m.active) }

// focusedMember is the active member whose field has the keyboard, or "".
func (m *sessionPrompt) focusedMember() string {
	if i := m.focus - firstMember; i >= 0 && i < len(m.active) {
		return m.active[i]
	}
	return ""
}

// focusedRef is the reference member whose field has the keyboard, or "".
func (m *sessionPrompt) focusedRef() string {
	if i := m.focus - m.firstRef(); i >= 0 && i < m.refs.count() {
		return m.refs.members[i]
	}
	return ""
}

// newSessionPrompt builds the prompt for a new session of pkg.
func newSessionPrompt(e *env, pkg string) *sessionPrompt {
	input := func(prompt, placeholder string, limit int) textinput.Model {
		in := newTextInput()
		in.Prompt = prompt
		in.CharLimit = limit
		in.Placeholder = placeholder
		// A text input with no width truncates its placeholder to one character.
		in.Width = 48
		return in
	}
	m := &sessionPrompt{
		e: e, pkg: pkg,
		name:   input("session:  ", "token-refresh", 64),
		prefix: input("prefix:   ", "none", 64),
		bases:  map[string]string{},
		own:    map[string]string{},
	}
	var refs []string
	at := map[string]string{}
	if p, err := e.core.Load(pkg); err == nil {
		for _, mem := range p.M.Members {
			m.width = max(m.width, len(mem.Name))
			if mem.Mode == manifest.ModeActive {
				m.active = append(m.active, mem.Name)
				m.bases[mem.Name] = cmp.Or(mem.Base, "origin/HEAD")
			} else {
				refs = append(refs, mem.Name)
				at[mem.Name] = mem.Ref
			}
		}
	}
	m.refs = newRefFields(m.width, refs)
	for name, ref := range at {
		m.refs.placeholder(name, ref)
	}
	return m
}

// branch is the session's branch as the fields stand, "" until it has a name.
func (m *sessionPrompt) branch() string {
	name := strings.TrimSpace(m.name.Value())
	if name == "" {
		return ""
	}
	return manifest.DefaultBranch(strings.TrimSpace(m.prefix.Value()), name)
}

// buildsOn is the picked branch the member's new branch starts on, or "" for
// its base.
func (m *sessionPrompt) buildsOn(member string) string {
	if own, ok := m.own[member]; ok {
		return own
	}
	if contains(m.from.members, member) {
		return m.from.name
	}
	return ""
}

// selfBuilt says that a pick to build on is the session's own branch, or "".
// It is checked on every render, since typing a name can make it true after the
// pick was made.
func (m *sessionPrompt) selfBuilt() string {
	branch := m.branch()
	if branch == "" {
		return ""
	}
	clash := m.from.name == branch
	for _, own := range m.own {
		clash = clash || own == branch
	}
	if !clash {
		return ""
	}
	return fmt.Sprintf("build on cannot be %s, the session's own branch", branch)
}

// modal implements modalScreen.
func (m *sessionPrompt) modal() {}

// capturing implements capturing: the text fields own the keyboard.
func (m *sessionPrompt) capturing() bool { return true }

// Init implements screen.
func (m *sessionPrompt) Init() tea.Cmd { return tea.Batch(m.name.Focus(), textinput.Blink) }

// Title implements screen.
func (m *sessionPrompt) Title() string { return "new session" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *sessionPrompt) Crumb() string { return "" }

// Hints implements screen.
func (m *sessionPrompt) Hints() []hint { return nil }

// setFocus gives the keyboard to field i, wrapping around at either end, and
// takes it from the others.
func (m *sessionPrompt) setFocus(i int) tea.Cmd {
	n := m.promptFields()
	m.focus = (i + n) % n
	m.name.Blur()
	m.prefix.Blur()
	cmd := m.refs.focus(m.focus - m.firstRef())
	switch m.focus {
	case nameField:
		return m.name.Focus()
	case prefixField:
		return m.prefix.Focus()
	}
	return cmd
}

// Update implements screen.
func (m *sessionPrompt) Update(msg tea.Msg) (screen, tea.Cmd) {
	if t, ok := msg.(remotePickedMsg); ok {
		if t.owner == m {
			m.picked(t)
		}
		return m, nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	key := k.String()
	picks := m.focus == fromField || m.focusedMember() != "" || m.focusedRef() != ""
	if key == " " && picks {
		return m.openPicker()
	}
	switch key {
	case "esc":
		return m, pop(m)
	case "tab", "down":
		return m, m.setFocus(m.focus + 1)
	case "shift+tab", "up":
		return m, m.setFocus(m.focus - 1)
	case "enter":
		return m.submit()
	}
	m.msg = ""
	msg = hyphenateSpaces(k)
	var cmd tea.Cmd
	switch m.focus {
	case nameField:
		m.name, cmd = m.name.Update(msg)
	case prefixField:
		m.prefix, cmd = m.prefix.Update(msg)
	default:
		cmd = m.refs.update(m.focus-m.firstRef(), msg)
	}
	return m, cmd
}

// openPicker opens the remote branches for the focused field.
func (m *sessionPrompt) openPicker() (screen, tea.Cmd) {
	p, err := m.e.core.Load(m.pkg)
	if err != nil {
		m.msg = err.Error()
		return m, nil
	}
	target := pickTarget{member: m.focusedMember()}
	if ref := m.focusedRef(); ref != "" {
		target = pickTarget{member: ref, reference: true}
	}
	picker := newRemotePicker(m, m.e.core, p, target)
	if !target.reference {
		picker.exclude = m.branch()
		// A member's base only needs choosing when following build on would
		// give it something else.
		if target.member != "" && m.from.name != "" {
			picker.base = m.bases[target.member]
		}
	}
	return m, push(m, picker)
}

// picked records a choice made in the picker.
func (m *sessionPrompt) picked(t remotePickedMsg) {
	m.msg = ""
	switch {
	case t.target.reference:
		m.refs.set(t.target.member, t.branch)
	case t.target.member == "":
		m.from = pickedBranch{name: t.branch, members: t.members}
	case t.branch != "" || t.base:
		m.own[t.target.member] = t.branch
	default:
		delete(m.own, t.target.member)
	}
}

// fromView renders the field of what every member builds on.
func (m *sessionPrompt) fromView() string {
	label, text := "build on: ", styleDim.Render("each member's base branch")
	if m.from.name != "" {
		text = m.from.name + styleDim.Render("  ("+strings.Join(m.from.members, ", ")+")")
	}
	if m.focus == fromField {
		return styleSelected.Render(label) + text + styleDim.Render("  space to pick")
	}
	return label + text
}

// memberView renders an active member's row under build on: what its new branch
// starts on, dimmed unless it was chosen for this member.
func (m *sessionPrompt) memberView(member string) string {
	own, chosen := m.own[member]
	var text string
	switch {
	case own != "":
		text = own
	case chosen:
		text = "base (" + m.bases[member] + ")"
	case m.buildsOn(member) != "":
		text = styleDim.Render(m.buildsOn(member))
	case m.from.name != "":
		text = styleDim.Render("base (" + m.bases[member] + "), lacks that branch")
	default:
		text = styleDim.Render("base (" + m.bases[member] + ")")
	}
	name := fit(member, m.width, false)
	hint := ""
	if m.focusedMember() == member {
		name = styleSelected.Render(name)
		hint = styleDim.Render("  space to pick")
	}
	return strings.Repeat(" ", len("build on: ")) + name + "  " + text + hint
}

// options turns the fields into what core takes.
func (m *sessionPrompt) options() core.SessionOptions {
	opts := core.SessionOptions{
		Prefix:       strings.TrimSpace(m.prefix.Value()),
		FromByMember: m.refs.values(),
	}
	for _, name := range m.active {
		if from := m.buildsOn(name); from != "" {
			opts.FromByMember[name] = from
		}
	}
	return opts
}

// trackNote words what working on a branch that is already on a remote means
// for each member, ahead of asking whether to.
func trackNote(e *core.RemoteBranchExistsError, opts core.SessionOptions) []string {
	lines := []string{fmt.Sprintf("Branch %q is already on the remote of %s.", e.Branch, strings.Join(e.On, ", "))}
	for _, name := range e.On {
		if from := opts.FromByMember[name]; from != "" {
			lines = append(lines, fmt.Sprintf("%s continues it there, so it is not built on %s.", name, from))
		}
	}
	for _, name := range e.Lacking {
		start := "its base"
		if from := opts.FromByMember[name]; from != "" {
			start = from
		}
		lines = append(lines, fmt.Sprintf("%s does not have it, and starts a new branch of that name on %s.", name, start))
	}
	return lines
}

// hyphenateSpaces rewrites a key press so whitespace reaches the dialog's
// fields as hyphens. Every field takes a session name, branch or ref, none of
// which may hold whitespace, so a space bar press types "-" and pasted text has
// each whitespace rune replaced, rather than leaving a name that is only
// rejected on submit. Keys the dialog acts on itself (a space that opens a
// picker) are handled before this is called; other keys pass through as is.
func hyphenateSpaces(k tea.KeyMsg) tea.KeyMsg {
	switch k.Type {
	case tea.KeySpace:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}, Alt: k.Alt}
	case tea.KeyRunes:
		runes := make([]rune, len(k.Runes))
		for i, r := range k.Runes {
			if unicode.IsSpace(r) {
				r = '-'
			}
			runes[i] = r
		}
		k.Runes = runes
	}
	return k
}

// submit validates the fields and starts the create on the progress screen.
func (m *sessionPrompt) submit() (screen, tea.Cmd) {
	name := strings.TrimSpace(m.name.Value())
	if err := manifest.ValidateSessionName(name); err != nil {
		m.msg = err.Error()
		return m, m.setFocus(0)
	}
	if m.selfBuilt() != "" {
		return m, nil
	}
	c, pkg := m.e.core, m.pkg
	p, err := c.Load(pkg)
	if err != nil {
		m.msg = err.Error()
		return m, nil
	}
	names, _ := p.Sessions()
	for _, other := range names {
		if strings.EqualFold(other, name) {
			m.msg = fmt.Sprintf("%s already has a session named %q", pkg, other)
			return m, m.setFocus(0)
		}
	}
	opts := m.options()
	return m, goTo(m, newProgress(m.e, operation{
		title:   "Start " + pkg + "/" + name,
		pkg:     pkg,
		session: name,
		run: func(force, reuse bool) error {
			opts.ReuseBranch = reuse
			_, err := c.CreateSession(p, name, opts)
			return err
		},
		trackNote: func(e *core.RemoteBranchExistsError) []string { return trackNote(e, opts) },
		track:     func() { opts.Track = true },
		back: func() screen {
			m.setFocus(nameField)
			return m
		},
	}))
}

// View implements screen.
func (m *sessionPrompt) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("package ") + styleLabel.Render(m.pkg) + "\n\n")
	b.WriteString(" " + m.name.View() + "\n")
	b.WriteString(" " + m.prefix.View() + "\n")
	b.WriteString(" " + m.fromView() + "\n")
	for _, name := range m.active {
		b.WriteString(" " + m.memberView(name) + "\n")
	}
	b.WriteString(m.refs.view())
	help := m.fieldHelp()
	b.WriteString("\n " + styleDim.Render(help[0]) + "\n")
	b.WriteString(" " + styleDim.Render(help[1]) + "\n")
	if msg := cmp.Or(m.selfBuilt(), m.msg); msg != "" {
		b.WriteString(" " + styleErr.Render(msg) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "start", "tab", "next field", "space", "pick a branch", "esc", "cancel"))
	width := w - 8
	if width > 76 {
		width = 76
	}
	return dialog("new session", b.String(), width)
}

// fieldHelp is two lines saying what the focused field is for, and the flag of
// `vmono session new` that does the same. A line longer than 68 columns is cut
// off on a terminal 80 wide.
func (m *sessionPrompt) fieldHelp() [2]string {
	branch := cmp.Or(m.branch(), manifest.DefaultBranch(strings.TrimSpace(m.prefix.Value()), "<session>"))
	switch m.focus {
	case nameField:
		return [2]string{
			"Names this piece of work, and the branch every active member is on:",
			truncate(branch, 68),
		}
	case prefixField:
		return [2]string{
			"Optional (--prefix). Goes before the session name in the branch:",
			truncate(branch, 68),
		}
	case fromField:
		return [2]string{
			"Optional (--from). Press space to start the new branches on a",
			"remote branch instead of each member's base. Rows below can differ.",
		}
	}
	if m.focusedRef() != "" {
		return [2]string{
			"What this read-only member is checked out at (--from MEMBER=REF).",
			"Type a branch, tag or commit, or press space to pick a branch.",
		}
	}
	return [2]string{
		"Press space to choose what this member's new branch starts on",
		"(--from MEMBER=REF), if it should differ from build on above.",
	}
}

// contains reports whether list has s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
