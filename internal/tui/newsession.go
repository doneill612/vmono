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
// optionally a branch, and a landing branch for each active member. The
// branch field shows the default it will use,
// <branch_prefix><package>/<session>, as its placeholder. Each member's into
// field starts as the branch its source repo has checked out, which is where
// its work lands when the session is finished; a landing branch other than
// the member's base branch is also where the session starts from, when it
// exists. Enter runs the create on the progress screen, which ends at the
// harness picker, so creating a package and starting its first session
// still ends at a running harness.
type sessionPrompt struct {
	e      *env
	pkg    string
	name   textinput.Model
	branch textinput.Model
	into   intoFields
	// focus is the field with the keyboard: 0 name, 1 branch, then one per
	// member in into.
	focus int
	msg   string
}

// promptFields is the number of fields in the prompt.
func (m *sessionPrompt) promptFields() int { return 2 + m.into.count() }

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
	var members []string
	var defaults map[string]string
	if p, err := e.core.Load(pkg); err == nil {
		for _, mem := range p.M.Members {
			if mem.Mode == manifest.ModeActive {
				members = append(members, mem.Name)
			}
		}
		defaults = e.core.DefaultLanding(p)
	}
	m := &sessionPrompt{e: e, pkg: pkg, name: n, branch: b, into: newIntoFields(members, defaults)}
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

// setFocus gives the keyboard to field i, wrapping around at either end, and
// takes it from the others.
func (m *sessionPrompt) setFocus(i int) tea.Cmd {
	n := m.promptFields()
	m.focus = (i + n) % n
	m.name.Blur()
	m.branch.Blur()
	switch m.focus {
	case 0:
		m.into.focus(-1)
		return m.name.Focus()
	case 1:
		m.into.focus(-1)
		return m.branch.Focus()
	}
	return m.into.focus(m.focus - 2)
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
	case 0:
		m.name, cmd = m.name.Update(msg)
		m.syncPlaceholder()
	case 1:
		m.branch, cmd = m.branch.Update(msg)
	default:
		cmd = m.into.update(m.focus-2, msg)
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
	branch, into := strings.TrimSpace(m.branch.Value()), m.into.values()
	return m, goTo(newProgress(m.e, operation{
		title:   "Start " + pkg + "/" + name,
		pkg:     pkg,
		session: name,
		run: func(force, reuse bool) error {
			_, err := c.CreateSession(p, name, core.SessionOptions{Branch: branch, IntoByMember: into, ReuseBranch: reuse})
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
	b.WriteString(m.into.view())
	b.WriteString("\n " + styleDim.Render("Every member gets a worktree on the branch. Finishing the session lands") + "\n")
	b.WriteString(" " + styleDim.Render("each member's work on its into branch, which starts as its checked-out branch.") + "\n")
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

// contains reports whether list has s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
