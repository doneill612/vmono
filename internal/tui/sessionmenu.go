package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// sessionAction is one entry of the session menu. key is the sessions-view
// key that performs the same thing, so the menu is a browsable list of the
// view's per-session keys and pressing one inside the menu runs it directly.
type sessionAction struct {
	key    string
	label  string
	danger bool
}

// sessionActions lists everything the sessions view can do to one session,
// in the order the menu shows it. Launching comes first because it is what
// a session is for; deleting comes last.
var sessionActions = []sessionAction{
	{key: "o", label: "Launch"},
	{key: "l", label: "Launch with…"},
	{key: "m", label: "Members"},
	{key: "d", label: "Describe"},
	{key: "s", label: "Sync"},
	{key: "f", label: "Fetch"},
	{key: "y", label: "Copy path"},
	{key: "F", label: "Finish"},
	{key: "ctrl+d", label: "Delete", danger: true},
}

// sessionActionMsg asks the sessions view that opened a menu to perform an
// action on the session the menu was opened for. owner identifies that view
// so a message broadcast to the whole stack is acted on exactly once.
type sessionActionMsg struct {
	owner   *sessionsModel
	session string
	key     string
}

// sessionMenu is the dialog opened by Enter on a session: every action
// available for it, chosen with the arrow keys and Enter or by pressing the
// action's own key. It closes itself first and hands the choice back to the
// sessions view as a sessionActionMsg, so the action's own screen (a picker,
// a confirmation, a progress view) is pushed over the sessions view rather
// than over the menu.
type sessionMenu struct {
	owner   *sessionsModel
	pkg     string
	session string
	cursor  int
}

// newSessionMenu builds the menu for one session of the view's package.
func newSessionMenu(owner *sessionsModel, session string) *sessionMenu {
	return &sessionMenu{owner: owner, pkg: owner.name, session: session}
}

// modal implements modalScreen.
func (m *sessionMenu) modal() {}

// Init implements screen.
func (m *sessionMenu) Init() tea.Cmd { return nil }

// Title implements screen.
func (m *sessionMenu) Title() string { return "session" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *sessionMenu) Crumb() string { return "" }

// Hints implements screen.
func (m *sessionMenu) Hints() []hint { return nil }

// choose closes the menu and delivers the action to the sessions view. The
// sequence matters: the pop reveals the sessions view, and only then does
// the action message reach it.
func (m *sessionMenu) choose(key string) tea.Cmd {
	msg := sessionActionMsg{owner: m.owner, session: m.session, key: key}
	return tea.Sequence(pop(), func() tea.Msg { return msg })
}

// Update implements screen.
func (m *sessionMenu) Update(msg tea.Msg) (screen, tea.Cmd) {
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
		if m.cursor < len(sessionActions)-1 {
			m.cursor++
		}
	case "enter":
		return m, m.choose(sessionActions[m.cursor].key)
	default:
		for _, a := range sessionActions {
			if a.key == s {
				return m, m.choose(a.key)
			}
		}
	}
	return m, nil
}

// View implements screen.
func (m *sessionMenu) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleTitle.Render(m.session) + styleDim.Render("  "+m.pkg) + "\n\n")
	for i, a := range sessionActions {
		cursor, label := "  ", a.label
		if i == m.cursor {
			cursor = styleSelected.Render("▸ ")
		}
		if a.danger {
			label = styleErr.Render(label)
		}
		pad := strings.Repeat(" ", max(1, 16-len([]rune(a.label))))
		b.WriteString(fmt.Sprintf(" %s%s%s%s\n", cursor, label, pad, styleDim.Render(a.key)))
	}
	b.WriteString("\n " + keyHelp("enter", "select", "↑/↓", "move", "esc", "close"))
	return dialog("session", b.String(), w-4)
}
