package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// sessionPrompt is the dialog that starts a session of a package: a name, and
// optionally which branches it works on. Empty fields show the branch they will
// use as their placeholder: the session's name, after the prefix and a slash
// when there is one, unless the branch field names another, and for a member
// the session's branch. The track and from fields open a picker of the members'
// remote branches. Tracking works on the branch itself, in each member whose
// remote has it; the others get a new branch of that name. From builds new
// branches on the one picked, in each member that has it, in place of its base.
// A reference member's field holds what it is checked out at in place of the
// package's ref, typed, or picked from its remote branches with space, which no
// ref can hold.
// Enter runs the create on the progress screen, which ends at the harness
// picker, so creating a package and starting its first session still ends at a
// running harness.
type sessionPrompt struct {
	e       *env
	pkg     string
	name    textinput.Model
	prefix  textinput.Model
	branch  textinput.Model
	members branchFields
	refs    branchFields
	// track and from are the remote branches picked, and the members whose
	// remote has each.
	track, from pickedBranch
	// focus is the field with the keyboard, in the order of the constants below
	// and then one per active member and one per reference member.
	focus int
	msg   string
}

// pickedBranch is a remote branch chosen in the picker.
type pickedBranch struct {
	name    string
	members []string
}

// Fields of the prompt ahead of the members' branch fields.
const (
	nameField = iota
	prefixField
	branchField
	trackField
	fromField
	firstMember
)

// promptFields is the number of fields in the prompt.
func (m *sessionPrompt) promptFields() int { return m.firstRef() + m.refs.count() }

// firstRef is the first of the reference members' fields.
func (m *sessionPrompt) firstRef() int { return firstMember + m.members.count() }

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
	var members, refs []string
	at := map[string]string{}
	width := 0
	if p, err := e.core.Load(pkg); err == nil {
		for _, mem := range p.M.Members {
			width = max(width, len(mem.Name))
			if mem.Mode == manifest.ModeActive {
				members = append(members, mem.Name)
			} else {
				refs = append(refs, mem.Name)
				at[mem.Name] = mem.Ref
			}
		}
	}
	m := &sessionPrompt{
		e: e, pkg: pkg,
		name:    input("session:  ", "token-refresh", 64),
		prefix:  input("prefix:   ", "none", 64),
		branch:  input("branch:   ", "", 200),
		members: newBranchFields("branch", width, members),
		refs:    newBranchFields("read-only", width, refs),
	}
	for name, ref := range at {
		m.refs.placeholder(name, ref)
	}
	m.sync()
	return m
}

// sessionBranch is the branch members with no branch of their own work on.
func (m *sessionPrompt) sessionBranch() string {
	if b := strings.TrimSpace(m.branch.Value()); b != "" {
		return b
	}
	return m.defaultBranch()
}

// defaultBranch is the branch used when the branch field is left empty.
func (m *sessionPrompt) defaultBranch() string {
	if m.track.name != "" {
		return m.track.name
	}
	name := strings.TrimSpace(m.name.Value())
	if name == "" {
		name = "<session>"
	}
	return manifest.DefaultBranch(strings.TrimSpace(m.prefix.Value()), name)
}

// sync keeps the placeholders in step with what has been typed and picked.
func (m *sessionPrompt) sync() {
	m.branch.Placeholder = m.defaultBranch()
	for _, name := range m.members.members {
		text := m.sessionBranch()
		if contains(m.track.members, name) {
			text = m.track.name
		}
		m.members.placeholder(name, text)
	}
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
	for _, in := range []*textinput.Model{&m.name, &m.prefix, &m.branch} {
		in.Blur()
	}
	cmd := tea.Batch(m.members.focus(m.focus-firstMember), m.refs.focus(m.focus-m.firstRef()))
	switch m.focus {
	case nameField:
		return m.name.Focus()
	case prefixField:
		return m.prefix.Focus()
	case branchField:
		return m.branch.Focus()
	}
	return cmd
}

// Update implements screen.
func (m *sessionPrompt) Update(msg tea.Msg) (screen, tea.Cmd) {
	if t, ok := msg.(remotePickedMsg); ok {
		if t.owner == m {
			picked := pickedBranch{name: t.branch, members: t.members}
			switch {
			case t.ref != "":
				m.refs.set(t.ref, t.branch)
			case t.from:
				m.from = picked
			default:
				m.track = picked
			}
			m.sync()
		}
		return m, nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	key := k.String()
	onPicker := (m.focus == trackField || m.focus == fromField) && (key == "enter" || key == " ")
	onRef := m.focusedRef() != "" && key == " "
	if key == "ctrl+t" || onPicker || onRef {
		p, err := m.e.core.Load(m.pkg)
		if err != nil {
			m.msg = err.Error()
			return m, nil
		}
		picker := newRemotePicker(m, m.e.core, p, onPicker && m.focus == fromField)
		if onRef {
			picker.ref = m.focusedRef()
		}
		return m, push(m, picker)
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
	var cmd tea.Cmd
	switch m.focus {
	case nameField:
		m.name, cmd = m.name.Update(msg)
	case prefixField:
		m.prefix, cmd = m.prefix.Update(msg)
	case branchField:
		m.branch, cmd = m.branch.Update(msg)
	case trackField, fromField:
	default:
		cmd = tea.Batch(m.members.update(m.focus-firstMember, msg), m.refs.update(m.focus-m.firstRef(), msg))
	}
	m.sync()
	return m, cmd
}

// pickView renders the field of a picked branch.
func (m *sessionPrompt) pickView(field int, label, none string, picked pickedBranch) string {
	text := styleDim.Render(none)
	if picked.name != "" {
		text = picked.name + styleDim.Render("  ("+strings.Join(picked.members, ", ")+")")
	}
	if m.focus == field {
		return styleSelected.Render(label) + text + styleDim.Render("  space to pick")
	}
	return label + text
}

// options turns the fields into what core takes.
func (m *sessionPrompt) options() core.SessionOptions {
	opts := core.SessionOptions{
		Branch:         strings.TrimSpace(m.branch.Value()),
		BranchByMember: m.members.values(),
		TrackByMember:  map[string]string{},
		FromByMember:   m.refs.values(),
	}
	if opts.Branch == "" {
		opts.Branch = m.track.name
	}
	if opts.Branch == "" {
		opts.Prefix = strings.TrimSpace(m.prefix.Value())
	}
	for _, name := range m.track.members {
		// A field filled in for the member wins over the branch picked.
		if opts.BranchByMember[name] == "" {
			opts.TrackByMember[name] = m.track.name
		}
	}
	for _, name := range m.from.members {
		if opts.TrackByMember[name] == "" {
			opts.FromByMember[name] = m.from.name
		}
	}
	return opts
}

// submit validates the name and starts the create on the progress screen.
func (m *sessionPrompt) submit() (screen, tea.Cmd) {
	name := strings.TrimSpace(m.name.Value())
	if err := manifest.ValidateName(name); err != nil {
		m.msg = err.Error()
		return m, m.setFocus(0)
	}
	c, pkg := m.e.core, m.pkg
	p, err := c.Load(pkg)
	if err != nil {
		m.msg = err.Error()
		return m, nil
	}
	if names, _ := p.Sessions(); contains(names, name) {
		m.msg = fmt.Sprintf("%s already has a session named %q", pkg, name)
		return m, m.setFocus(0)
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
		back: func() screen { return m },
	}))
}

// View implements screen.
func (m *sessionPrompt) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("package ") + styleLabel.Render(m.pkg) + "\n\n")
	b.WriteString(" " + m.name.View() + "\n")
	b.WriteString(" " + m.prefix.View() + "\n")
	b.WriteString(" " + m.branch.View() + "\n")
	b.WriteString(" " + m.pickView(trackField, "work on:  ", "nothing: start a new branch", m.track) + "\n")
	b.WriteString(" " + m.pickView(fromField, "build on: ", "each member's base branch", m.from) + "\n")
	b.WriteString(m.members.view())
	b.WriteString(m.refs.view())
	help := m.fieldHelp()
	b.WriteString("\n " + styleDim.Render(help[0]) + "\n")
	b.WriteString(" " + styleDim.Render(help[1]) + "\n")
	if m.msg != "" {
		b.WriteString(" " + styleErr.Render(m.msg) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "start", "tab", "next field", "ctrl+t", "remote branch", "esc", "cancel"))
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
	switch m.focus {
	case nameField:
		return [2]string{
			"Names this piece of work. Active members get a branch of this name",
			"unless a field below says otherwise.",
		}
	case prefixField:
		return [2]string{
			"Optional (--prefix). Goes before the session name in the branch:",
			"feat gives feat/<session>. Unused if you name or work on a branch.",
		}
	case branchField:
		return [2]string{
			"Optional (--branch). The branch the active members work on, which",
			"is what you push. Leave empty to use the one shown.",
		}
	case trackField:
		return [2]string{
			"Optional (--track). Continue a branch already on the remote, such",
			"as a teammate's. Members whose remote lacks it start a new one.",
		}
	case fromField:
		return [2]string{
			"Optional (--from). Start the new branch here instead of on each",
			"member's base, for work stacked on a branch that has not merged.",
		}
	}
	if m.focusedRef() != "" {
		return [2]string{
			"What this read-only member is checked out at (--from MEMBER=REF).",
			"Type a branch, tag or commit, or press space to pick a branch.",
		}
	}
	return [2]string{
		"Optional (--branch MEMBER=BRANCH). A different branch for this",
		"member only. Leave empty to use the one shown.",
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
