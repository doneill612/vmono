package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/manifest"
)

// commandsDialog edits the setup and teardown commands of one member in the
// package editor, one command per line. Like a role, what it sets is part of
// the form and is written when the package is saved.
type commandsDialog struct {
	member string
	// areas holds the setup commands, then the teardown ones.
	areas [2]textarea.Model
	// before holds the text each area opened with.
	before [2]string
	focus  int
	set    func(setup, teardown manifest.Commands)
	orig   [2]manifest.Commands
}

// newCommandsDialog builds the dialog for mem; set receives the result.
func newCommandsDialog(mem manifest.Member, set func(setup, teardown manifest.Commands)) *commandsDialog {
	m := &commandsDialog{member: mem.Name, set: set, orig: [2]manifest.Commands{mem.Setup, mem.Teardown}}
	for i, hint := range []string{"pnpm install", "docker compose down"} {
		m.areas[i] = newArea(hint)
		m.areas[i].SetValue(strings.Join(m.orig[i], "\n"))
		m.before[i] = m.areas[i].Value()
	}
	return m
}

// modal implements modalScreen.
func (m *commandsDialog) modal() {}

// capturing implements capturing: the text areas own the keyboard.
func (m *commandsDialog) capturing() bool { return true }

// Init implements screen.
func (m *commandsDialog) Init() tea.Cmd { return tea.Batch(m.areas[0].Focus(), textinput.Blink) }

// Title implements screen.
func (m *commandsDialog) Title() string { return "commands" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *commandsDialog) Crumb() string { return "" }

// Hints implements screen.
func (m *commandsDialog) Hints() []hint { return nil }

// commands returns the commands in area i. An area left alone keeps what it
// had, so a command that itself spans lines is not split by opening it.
func (m *commandsDialog) commands(i int) manifest.Commands {
	if m.areas[i].Value() == m.before[i] {
		return m.orig[i]
	}
	var out manifest.Commands
	for _, line := range strings.Split(m.areas[i].Value(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Update implements screen.
func (m *commandsDialog) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return m, pop(m)
		case "ctrl+s":
			m.set(m.commands(0), m.commands(1))
			return m, pop(m)
		case "tab", "shift+tab":
			m.areas[m.focus].Blur()
			m.focus = 1 - m.focus
			return m, m.areas[m.focus].Focus()
		}
	}
	var cmd tea.Cmd
	m.areas[m.focus], cmd = m.areas[m.focus].Update(msg)
	return m, cmd
}

// View implements screen.
func (m *commandsDialog) View(w, h int) string {
	width := min(76, w-8)
	rows := max(min(5, (h-12)/2), 1)
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("member ") + styleLabel.Render(m.member) + styleDim.Render("   one command per line") + "\n")
	for i, label := range []string{"Setup", "Teardown"} {
		m.areas[i].SetWidth(width - 6)
		m.areas[i].SetHeight(rows)
		about := "  in each new worktree"
		if i == 1 {
			about = "  before a worktree is removed"
		}
		name := styleHeaderCol.Render("  " + label)
		if m.focus == i {
			name = styleSelected.Render("▸ " + label)
		}
		b.WriteString("\n " + name + styleDim.Render(about) + "\n")
		for _, line := range strings.Split(m.areas[i].View(), "\n") {
			b.WriteString("   " + line + "\n")
		}
	}
	b.WriteString("\n " + keyHelp("ctrl+s", "set", "tab", "setup/teardown", "esc", "cancel"))
	return dialog("setup and teardown", b.String(), width)
}
