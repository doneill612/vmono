package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// action is one entry of an action menu. key is the shortcut that performs the
// same thing from the list view itself, and is pressed inside the menu too, so
// a menu is both the way to discover a view's shortcuts and a way to never
// need them. An empty key means the action has no shortcut.
type action struct {
	id     string
	label  string
	key    string
	danger bool
}

// keyLabel renders a key name for display.
func keyLabel(key string) string {
	switch key {
	case "right":
		return "→"
	case "left":
		return "←"
	}
	return key
}

// findAction returns the action bound to key, if any.
func findAction(items []action, key string) (action, bool) {
	if key == "" {
		return action{}, false
	}
	for _, a := range items {
		if a.key == key {
			return a, true
		}
	}
	return action{}, false
}

// actionMsg asks the list view that opened a menu to perform an action on the
// row the menu was opened for. owner identifies that view so a message
// broadcast to the whole screen stack is acted on exactly once; subject is the
// row's id in that view.
type actionMsg struct {
	owner   screen
	subject string
	id      string
}

// actionMenu is the dialog opened by Enter on a row of any list view: every
// action available for it, chosen with the arrow keys and Enter or by pressing
// the action's own key. It closes itself first and hands the choice back to
// the list view as an actionMsg, so the action's own screen (a picker, a
// confirmation, a progress view) is pushed over the list rather than over the
// menu.
type actionMenu struct {
	owner    screen
	kind     string
	subject  string
	label    string
	subtitle string
	items    []action
	cursor   int
}

// newActionMenu builds the menu for one row. kind names what the row is
// ("session", "package"), subject is its id and subtitle optional dim text
// beside its name.
func newActionMenu(owner screen, kind, subject, subtitle string, items []action) *actionMenu {
	return &actionMenu{owner: owner, kind: kind, subject: subject, label: subject, subtitle: subtitle, items: items}
}

// named sets the text shown as the menu's heading when it differs from the
// row's id, as it does for a repo, which is identified by its path.
func (m *actionMenu) named(label string) *actionMenu {
	m.label = label
	return m
}

// modal implements modalScreen.
func (m *actionMenu) modal() {}

// Init implements screen.
func (m *actionMenu) Init() tea.Cmd { return nil }

// Title implements screen.
func (m *actionMenu) Title() string { return m.kind }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *actionMenu) Crumb() string { return "" }

// Hints implements screen.
func (m *actionMenu) Hints() []hint { return nil }

// choose closes the menu and delivers the action to the list view. The
// sequence matters: the pop reveals the list view, and only then does the
// action message reach it.
func (m *actionMenu) choose(id string) tea.Cmd {
	msg := actionMsg{owner: m.owner, subject: m.subject, id: id}
	return tea.Sequence(pop(), func() tea.Msg { return msg })
}

// Update implements screen.
func (m *actionMenu) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch s := k.String(); s {
	case "esc", "q":
		return m, pop()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.items) > 0 {
			return m, m.choose(m.items[m.cursor].id)
		}
	default:
		if a, ok := findAction(m.items, s); ok {
			return m, m.choose(a.id)
		}
	}
	return m, nil
}

// View implements screen.
func (m *actionMenu) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleTitle.Render(m.label))
	if m.subtitle != "" {
		b.WriteString(styleDim.Render("  " + m.subtitle))
	}
	b.WriteString("\n\n")
	for i, a := range m.items {
		cursor, label := "  ", a.label
		if i == m.cursor {
			cursor = styleSelected.Render("▸ ")
		}
		if a.danger {
			label = styleErr.Render(label)
		}
		pad := strings.Repeat(" ", max(1, 18-len([]rune(a.label))))
		b.WriteString(fmt.Sprintf(" %s%s%s%s\n", cursor, label, pad, styleDim.Render(keyLabel(a.key))))
	}
	b.WriteString("\n " + keyHelp("enter", "select", "↑/↓", "move", "esc", "close"))
	return dialog(m.kind, b.String(), w-4)
}
