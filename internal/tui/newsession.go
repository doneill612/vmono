package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// sessionPrompt is the dialog that starts a session of a package: a name,
// and optionally a branch. The branch field shows the default it will use,
// <branch_prefix><package>/<session>, as its placeholder. Enter runs the
// create on the progress screen, which ends at the harness picker, so
// creating a package and starting its first session still ends at a
// running harness.
type sessionPrompt struct {
	e      *env
	pkg    string
	name   textinput.Model
	branch textinput.Model
	// focus is 0 for the name field and 1 for the branch field.
	focus int
	msg   string
}

// newSessionPrompt builds the prompt for a new session of pkg.
func newSessionPrompt(e *env, pkg string) *sessionPrompt {
	n := newTextInput()
	n.Prompt = "session: "
	n.CharLimit = 64
	n.Placeholder = "token-refresh"
	// A text input with no width truncates its placeholder to one character.
	n.Width = 48
	b := newTextInput()
	b.Prompt = "branch:  "
	b.CharLimit = 200
	b.Width = 48
	m := &sessionPrompt{e: e, pkg: pkg, name: n, branch: b}
	m.syncPlaceholder()
	return m
}

// defaultBranch is the branch used when the branch field is left empty.
func (m *sessionPrompt) defaultBranch() string {
	name := strings.TrimSpace(m.name.Value())
	if name == "" {
		name = "<session>"
	}
	return manifest.DefaultBranch(m.e.core.Cfg.BranchPrefix, m.pkg, name)
}

// syncPlaceholder keeps the branch placeholder in step with the name typed.
func (m *sessionPrompt) syncPlaceholder() { m.branch.Placeholder = m.defaultBranch() }

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

// setFocus moves the focus between the two fields.
func (m *sessionPrompt) setFocus(i int) tea.Cmd {
	m.focus = i
	if i == 0 {
		m.branch.Blur()
		return m.name.Focus()
	}
	m.name.Blur()
	return m.branch.Focus()
}

// Update implements screen.
func (m *sessionPrompt) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "esc":
		return m, pop()
	case "tab", "shift+tab", "down", "up":
		return m, m.setFocus(1 - m.focus)
	case "enter":
		return m.submit()
	}
	m.msg = ""
	var cmd tea.Cmd
	if m.focus == 0 {
		m.name, cmd = m.name.Update(msg)
		m.syncPlaceholder()
	} else {
		m.branch, cmd = m.branch.Update(msg)
	}
	return m, cmd
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
	branch := strings.TrimSpace(m.branch.Value())
	return m, goTo(newProgress(m.e, operation{
		title:   "Start " + pkg + "/" + name,
		pkg:     pkg,
		session: name,
		run: func(force, reuse bool) error {
			_, err := c.CreateSession(p, name, core.SessionOptions{Branch: branch, ReuseBranch: reuse})
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
	b.WriteString(" " + m.branch.View() + "\n")
	b.WriteString("\n " + styleDim.Render("Every member of the package gets a worktree on the branch.") + "\n")
	if m.msg != "" {
		b.WriteString(" " + styleErr.Render(m.msg) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "start", "tab", "next field", "esc", "cancel"))
	width := w - 8
	if width > 76 {
		width = 76
	}
	return dialog("new session", b.String(), width)
}
