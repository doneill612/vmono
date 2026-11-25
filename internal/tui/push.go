package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doneill612/vmono/internal/core"
)

// pushConfirm is the dialog that precedes pushing a session: one row per member
// saying which branch would go to which remote, or why it cannot.
// y runs the push on the progress screen; the members that are blocked are
// left alone and the rest still go, since each member is pushed on its own.
// Nothing is forced.
type pushConfirm struct {
	e            *env
	pkg, session string
	// members is the scope the plan was made for, empty for every member, so
	// the push that follows is exactly the one shown.
	members []string
	plan    core.PushResult
}

// newPushConfirm builds the dialog for a plan from core.PlanPush.
func newPushConfirm(e *env, pkg, session string, members []string, plan core.PushResult) *pushConfirm {
	return &pushConfirm{e: e, pkg: pkg, session: session, members: members, plan: plan}
}

// startPush plans a push of the session and opens the confirmation for it. A
// member named that is not an active one is flashed. members limits the push,
// and is empty for all.
// from is the screen asking, which the push returns to, reloaded, when it ends.
func startPush(from screen, e *env, pkg, session string, members []string) tea.Cmd {
	c := e.core
	s, err := c.ResolveSession(pkg + "/" + session)
	if err != nil {
		return flashErr(err.Error())
	}
	plan, err := c.PlanPush(s, core.PushOptions{Members: members})
	if err != nil {
		return flashErr(err.Error())
	}
	return push(from, newPushConfirm(e, pkg, session, members, plan))
}

// ready counts the members that have commits to push.
func (m *pushConfirm) ready() int {
	n := 0
	for _, r := range m.plan.Members {
		if r.Status == core.PushReady {
			n++
		}
	}
	return n
}

// blocked counts the members that cannot be pushed until something is fixed.
func (m *pushConfirm) blocked() int {
	n := 0
	for _, r := range m.plan.Members {
		if r.Status == core.PushBlocked {
			n++
		}
	}
	return n
}

// modal implements modalScreen.
func (m *pushConfirm) modal() {}

// Init implements screen.
func (m *pushConfirm) Init() tea.Cmd { return nil }

// Title implements screen.
func (m *pushConfirm) Title() string { return "push session" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *pushConfirm) Crumb() string { return "" }

// Hints implements screen.
func (m *pushConfirm) Hints() []hint { return nil }

// Update implements screen.
func (m *pushConfirm) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "esc", "n":
		return m, pop(m)
	case "y":
		if m.ready() > 0 {
			return m, goTo(m, m.run())
		}
	}
	return m, nil
}

// run builds the progress screen that pushes the session. It resolves the
// session afresh, so it pushes what is committed now rather than what the
// dialog was opened on. Whether it succeeds or fails, the user returns to the screen
// the dialog came from, reloaded, rather than to a new copy of it.
func (m *pushConfirm) run() screen {
	c, pkg, session, members := m.e.core, m.pkg, m.session, m.members
	return newProgress(m.e, operation{
		title: "Push " + pkg + "/" + session,
		run: func(_, _ bool) error {
			s, err := c.ResolveSession(pkg + "/" + session)
			if err != nil {
				return err
			}
			_, err = c.Push(s, core.PushOptions{Members: members})
			return err
		},
		reveal: true,
	})
}

// pushText words one member's row and picks its style.
func pushText(r core.MemberPush) (string, lipgloss.Style) {
	switch r.Status {
	case core.PushNothing:
		return "nothing to push", styleDim
	case core.PushMerged:
		return r.Reason, styleDim
	case core.PushBlocked:
		return "blocked: " + r.Reason, styleWarn
	case core.PushFailed:
		return "failed: " + r.Reason, styleErr
	}
	text := fmt.Sprintf("%d commit", r.Commits)
	if r.Commits != 1 {
		text += "s"
	}
	if r.Branch != r.Dest {
		text += " of " + r.Branch
	}
	text += " → " + r.Remote + "/" + r.Dest
	switch {
	case r.New && r.SetsUpstream:
		text += ", new branch, will track it"
	case r.New:
		text += ", new branch"
	case r.SetsUpstream:
		text += ", will track it"
	}
	return text, styleOK
}

// pushRow renders a member's row, its text wrapped in a column of its own so a
// long branch name or reason is never cut off.
func pushRow(name, text string, st lipgloss.Style, width int) string {
	var b strings.Builder
	for i, line := range strings.Split(wrap(text, max(width-26, 20)), "\n") {
		if i > 0 {
			name = ""
		}
		b.WriteString(fmt.Sprintf("   %-18s %s\n", name, st.Render(line)))
	}
	return b.String()
}

// View implements screen.
func (m *pushConfirm) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("session ") + styleLabel.Render(m.pkg+"/"+m.session) + "\n\n")
	if len(m.plan.Members) == 0 {
		b.WriteString("   " + styleDim.Render("no active members") + "\n")
	}
	width := min(w-8, 84)
	for _, r := range m.plan.Members {
		text, st := pushText(r)
		b.WriteString(pushRow(r.Name, text, st, width))
		for _, n := range r.Notes {
			b.WriteString(pushRow("", n, styleWarn, width))
		}
	}
	b.WriteString("\n")
	switch {
	case m.ready() == 0 && m.blocked() > 0:
		b.WriteString(" " + styleWarn.Render("Nothing can be pushed until the members above are fixed.") + "\n\n ")
		b.WriteString(keyHelp("esc", "back"))
	case m.ready() == 0:
		b.WriteString(" There is nothing to push.\n\n ")
		b.WriteString(keyHelp("esc", "back"))
	default:
		b.WriteString(" Each branch above is pushed to its remote, never forced.")
		if m.blocked() > 0 {
			b.WriteString(" Blocked members\n are left alone and the rest still go.")
		}
		b.WriteString("\n " + styleDim.Render("Compared with the last fetch; f refreshes it.") + "\n\n ")
		b.WriteString(keyHelp("y", "push", "esc", "cancel"))
	}
	return dialog(m.Title(), b.String(), width)
}
