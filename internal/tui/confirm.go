package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
)

// confirmModel is the delete confirmation: one row per member, labeled with
// its session, showing dirty files, untracked files and commits not pushed.
// When every member is clean y confirms; otherwise the name must be typed.
// The same screen guards edits whose removals would discard work.
type confirmModel struct {
	e     *env
	title string
	// name is the name to type when work would be lost, and noun says what
	// it names: "package" or "session".
	name  string
	noun  string
	rows  []core.MemberSafety
	typed textinput.Model
	// deleteBranches is toggled with b and offered to the confirm callback.
	deleteBranches bool
	showBranches   bool
	// onConfirm builds the next screen; force is true when work is lost.
	onConfirm func(force, deleteBranches bool) screen
	onCancel  func() screen
	msg       string
}

// newConfirm builds the screen.
func newConfirm(e *env, title, name string, rows []core.MemberSafety, showBranches bool,
	onConfirm func(force, deleteBranches bool) screen, onCancel func() screen) *confirmModel {
	t := newTextInput()
	t.Prompt = "> "
	t.CharLimit = 128
	m := &confirmModel{e: e, title: title, name: name, noun: "package", rows: rows, typed: t,
		showBranches: showBranches, onConfirm: onConfirm, onCancel: onCancel}
	if !m.allClean() {
		m.typed.Focus()
	}
	return m
}

// about sets what the typed name identifies, for confirmations of something
// other than a package.
func (m *confirmModel) about(noun string) *confirmModel {
	m.noun = noun
	return m
}

// allClean reports whether no member would lose work.
func (m *confirmModel) allClean() bool {
	for _, r := range m.rows {
		if r.Unsafe() {
			return false
		}
	}
	return true
}

// cancel leaves the dialog: it goes to the screen onCancel builds, or back
// to the one underneath when onCancel is nil.
func (m *confirmModel) cancel() tea.Cmd {
	if m.onCancel == nil {
		return pop(m)
	}
	return goTo(m, m.onCancel())
}

// modal implements modalScreen.
func (m *confirmModel) modal() {}

// capturing implements capturing: the typed-name box owns the keyboard
// when work would be lost.
func (m *confirmModel) capturing() bool { return !m.allClean() }

// Init implements screen.
func (m *confirmModel) Init() tea.Cmd { return textinput.Blink }

// Title implements screen.
func (m *confirmModel) Title() string { return m.title }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *confirmModel) Crumb() string { return "" }

// Hints implements screen.
func (m *confirmModel) Hints() []hint { return nil }

// Update implements screen.
func (m *confirmModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	clean := m.allClean()
	switch k.String() {
	case "esc":
		return m, m.cancel()
	case "ctrl+b":
		if m.showBranches {
			m.deleteBranches = !m.deleteBranches
		}
		return m, nil
	}
	if clean {
		switch k.String() {
		case "y":
			return m, goTo(m, m.onConfirm(false, m.deleteBranches))
		case "n":
			return m, m.cancel()
		case "b":
			if m.showBranches {
				m.deleteBranches = !m.deleteBranches
			}
		}
		return m, nil
	}
	if k.String() == "enter" {
		if m.typed.Value() == m.name {
			return m, goTo(m, m.onConfirm(true, m.deleteBranches))
		}
		m.msg = "type the " + m.noun + " name exactly to confirm"
		return m, nil
	}
	var cmd tea.Cmd
	m.typed, cmd = m.typed.Update(msg)
	return m, cmd
}

// View implements screen.
func (m *confirmModel) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render(m.noun+" ") + styleLabel.Render(m.name) + "\n\n")
	for _, r := range m.rows {
		switch {
		case r.Missing:
			b.WriteString(fmt.Sprintf("   %-24s %s\n", r.Label(), styleDim.Render("worktree already gone")))
		case r.Error != "":
			b.WriteString(fmt.Sprintf("   %-24s %s\n", r.Label(), styleErr.Render("cannot inspect: "+r.Error)))
		case r.Unsafe():
			b.WriteString(fmt.Sprintf("   %-24s %s\n", r.Label(), styleWarn.Render(
				fmt.Sprintf("%d dirty · %d untracked · %d unpushed", r.Dirty, r.Untracked, r.Unpushed))))
		default:
			b.WriteString(fmt.Sprintf("   %-24s %s\n", r.Label(), styleOK.Render("clean")))
		}
	}
	b.WriteString("\n")
	if m.showBranches {
		box := "[ ]"
		if m.deleteBranches {
			box = "[x]"
		}
		b.WriteString(" " + box + " also delete session branches already merged into their base\n\n")
	}
	if m.allClean() {
		b.WriteString(" Everything is clean.\n " + keyHelp("y", "confirm", "n/esc", "cancel", "b", "toggle branch deletion"))
	} else {
		b.WriteString(" " + styleWarn.Render("This will discard the work above.") + "\n Type the " + m.noun + " name to confirm:\n ")
		b.WriteString(m.typed.View() + "\n")
		if m.msg != "" {
			b.WriteString(" " + styleErr.Render(m.msg) + "\n")
		}
		b.WriteString(" " + keyHelp("enter", "confirm", "esc", "cancel", "ctrl+b", "toggle branch deletion"))
	}
	width := w - 8
	if width > 84 {
		width = 84
	}
	return dialog(m.title, b.String(), width)
}
