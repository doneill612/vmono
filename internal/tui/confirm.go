package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
)

// confirmModel is the delete confirmation: one row per member showing dirty
// files, untracked files and commits not pushed. When every member is clean
// y confirms; otherwise the package name must be typed. The same screen
// guards edits whose removals would discard work.
type confirmModel struct {
	e     *env
	title string
	// name is the package name to type when work would be lost.
	name  string
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
	t.Placeholder = name
	t.CharLimit = 128
	m := &confirmModel{e: e, title: title, name: name, rows: rows, typed: t,
		showBranches: showBranches, onConfirm: onConfirm, onCancel: onCancel}
	if !m.allClean() {
		m.typed.Focus()
	}
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

// Init implements screen.
func (m *confirmModel) Init() tea.Cmd { return textinput.Blink }

// Update implements screen.
func (m *confirmModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	clean := m.allClean()
	switch k.String() {
	case "esc":
		return m, goTo(m.onCancel())
	case "ctrl+b":
		if m.showBranches {
			m.deleteBranches = !m.deleteBranches
		}
		return m, nil
	}
	if clean {
		switch k.String() {
		case "y":
			return m, goTo(m.onConfirm(false, m.deleteBranches))
		case "n":
			return m, goTo(m.onCancel())
		case "b":
			if m.showBranches {
				m.deleteBranches = !m.deleteBranches
			}
		}
		return m, nil
	}
	if k.String() == "enter" {
		if m.typed.Value() == m.name {
			return m, goTo(m.onConfirm(true, m.deleteBranches))
		}
		m.msg = "type the package name exactly to confirm"
		return m, nil
	}
	var cmd tea.Cmd
	m.typed, cmd = m.typed.Update(msg)
	return m, cmd
}

// View implements screen.
func (m *confirmModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(m.title) + styleDim.Render("  "+m.name) + "\n\n")
	for _, r := range m.rows {
		switch {
		case r.Missing:
			b.WriteString(fmt.Sprintf("  %-16s %s\n", r.Name, styleDim.Render("worktree already gone")))
		case r.Error != "":
			b.WriteString(fmt.Sprintf("  %-16s %s\n", r.Name, styleErr.Render("cannot inspect: "+r.Error)))
		case r.Unsafe():
			b.WriteString(fmt.Sprintf("  %-16s %s\n", r.Name, styleWarn.Render(
				fmt.Sprintf("%d dirty · %d untracked · %d unpushed", r.Dirty, r.Untracked, r.Unpushed))))
		default:
			b.WriteString(fmt.Sprintf("  %-16s %s\n", r.Name, styleOK.Render("clean")))
		}
	}
	b.WriteString("\n")
	if m.showBranches {
		box := "[ ]"
		if m.deleteBranches {
			box = "[x]"
		}
		b.WriteString(box + " also delete package branches already merged into their base\n\n")
	}
	if m.allClean() {
		b.WriteString("Everything is clean.\n" + keyHelp("y", "confirm", "n/esc", "cancel", "b", "toggle branch deletion"))
	} else {
		b.WriteString(styleWarn.Render("This will discard the work above.") + "\nType the package name to confirm:\n")
		b.WriteString(m.typed.View() + "\n")
		if m.msg != "" {
			b.WriteString(styleErr.Render(m.msg) + "\n")
		}
		b.WriteString(keyHelp("enter", "confirm", "esc", "cancel", "ctrl+b", "toggle branch deletion"))
	}
	return b.String()
}
