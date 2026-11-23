package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doneill612/vmono/internal/core"
)

// finishConfirm is the dialog that precedes finishing a session: one row per
// active member saying what would land on which branch, or why it cannot. y
// runs the finish on the progress screen. When a member is blocked or
// conflicted, nothing can be confirmed; l launches the harness in the session
// so the work can be fixed there.
type finishConfirm struct {
	e            *env
	pkg, session string
	plan         core.FinishResult
	closeAfter   bool
}

// newFinishConfirm builds the dialog for a plan from core.PlanFinish.
func newFinishConfirm(e *env, pkg, session string, plan core.FinishResult) *finishConfirm {
	return &finishConfirm{e: e, pkg: pkg, session: session, plan: plan}
}

// blocked reports whether any member stops the finish.
func (m *finishConfirm) blocked() bool {
	for _, r := range m.plan.Members {
		if r.Status == core.LandBlocked || r.Status == core.LandConflict {
			return true
		}
	}
	return false
}

// landable counts the members that have commits to land.
func (m *finishConfirm) landable() int {
	n := 0
	for _, r := range m.plan.Members {
		if r.Status == core.LandReady {
			n++
		}
	}
	return n
}

// modal implements modalScreen.
func (m *finishConfirm) modal() {}

// Init implements screen.
func (m *finishConfirm) Init() tea.Cmd { return nil }

// Title implements screen.
func (m *finishConfirm) Title() string { return "finish session" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *finishConfirm) Crumb() string { return "" }

// Hints implements screen.
func (m *finishConfirm) Hints() []hint { return nil }

// Update implements screen.
func (m *finishConfirm) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "esc", "n":
		return m, pop()
	case "l":
		if m.blocked() {
			req := LaunchRequest{Package: m.pkg, Session: m.session}
			return m, func() tea.Msg { return launchMsg{req} }
		}
	case "i":
		if len(m.plan.Members) > 0 {
			return m, goTo(newIntoEditorFromPlan(m.e, m.pkg, m.session, m.plan))
		}
	case "c":
		if !m.blocked() {
			m.closeAfter = !m.closeAfter
		}
	case "y":
		if !m.blocked() {
			return m, goTo(m.run())
		}
	}
	return m, nil
}

// run builds the progress screen that finishes the session. It resolves the
// session afresh so the file it records is the one on disk. A failure offers
// to launch the harness in the session, where conflicts are resolved, and on
// success the user returns to the package's sessions.
func (m *finishConfirm) run() screen {
	c, e, pkg, session, closeAfter := m.e.core, m.e, m.pkg, m.session, m.closeAfter
	back := func() screen { return newSessions(e, pkg) }
	return newProgress(e, operation{
		title: "Finish " + pkg + "/" + session,
		run: func(_, _ bool) error {
			s, err := c.ResolveSession(pkg + "/" + session)
			if err != nil {
				return err
			}
			_, err = c.Finish(s, core.FinishOptions{Close: closeAfter})
			return err
		},
		back:  back,
		done:  back,
		fixIn: &LaunchRequest{Package: pkg, Session: session},
	})
}

// landingText words one member's row and picks its style.
func landingText(r core.MemberLanding) (string, lipgloss.Style) {
	switch r.Status {
	case core.LandNothing:
		return "nothing to land", styleDim
	case core.LandBlocked:
		return "blocked: " + r.Reason, styleWarn
	case core.LandConflict:
		return fmt.Sprintf("rebase stopped on conflicts in %s; resolve them, then finish again", strings.Join(r.Conflicts, ", ")), styleWarn
	}
	text := fmt.Sprintf("%d commit", r.Commits)
	if r.Commits != 1 {
		text += "s"
	}
	text += " → " + r.Into + " (" + r.Via + ")"
	if r.Create {
		text += ", new branch"
	}
	return text, styleOK
}

// View implements screen.
func (m *finishConfirm) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("session ") + styleLabel.Render(m.pkg+"/"+m.session) + "\n\n")
	if len(m.plan.Members) == 0 {
		b.WriteString("   " + styleDim.Render("no active members") + "\n")
	}
	for _, r := range m.plan.Members {
		text, st := landingText(r)
		b.WriteString(fmt.Sprintf("   %-18s %s\n", r.Name, st.Render(text)))
		for _, n := range r.Notes {
			b.WriteString(fmt.Sprintf("   %-18s %s\n", "", styleDim.Render("note: "+n)))
		}
	}
	b.WriteString("\n")
	switch {
	case m.blocked():
		b.WriteString(" " + styleWarn.Render("Nothing lands until every member above can.") + "\n ")
		b.WriteString(keyHelp("l", "launch harness in the session", "i", "change landing branches", "esc", "back"))
	default:
		if m.landable() == 0 {
			b.WriteString(" No member has commits of its own to land. Finishing only marks\n the session finished.\n\n")
		} else {
			b.WriteString(" Commits are rebased in the session, then each branch above moves\n in its source repo. Nothing is pushed; P on the finished session does that.\n\n")
		}
		box := "[ ]"
		if m.closeAfter {
			box = "[x]"
		}
		b.WriteString(" " + box + " also remove the session once its work has landed\n\n ")
		b.WriteString(keyHelp("y", "finish", "i", "change landing branches", "c", "toggle remove", "esc", "cancel"))
	}
	width := w - 8
	if width > 84 {
		width = 84
	}
	return dialog(m.Title(), b.String(), width)
}
