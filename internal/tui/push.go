package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// pushConfirm is the dialog that precedes pushing a finished session: one row
// per member saying which branch would go to which remote, or why it cannot.
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
	// back builds the screen the dialog was opened from, which is also where
	// the push returns to when it ends.
	back func() screen
}

// newPushConfirm builds the dialog for a plan from core.PlanPush.
func newPushConfirm(e *env, pkg, session string, members []string, plan core.PushResult, back func() screen) *pushConfirm {
	return &pushConfirm{e: e, pkg: pkg, session: session, members: members, plan: plan, back: back}
}

// startPush plans a push of the session and opens the confirmation for it. A
// session that is not finished gets a dialog saying so, which offers to finish
// it unless uncommitted work stops it from finishing; a member named that is
// not an active one is flashed. members limits the push, and is empty for all.
// back builds the screen the push returns to; from is the screen asking.
func startPush(from screen, e *env, pkg, session string, members []string, back func() screen) tea.Cmd {
	c := e.core
	s, err := c.ResolveSession(pkg + "/" + session)
	if err != nil {
		return flashErr(err.Error())
	}
	plan, err := c.PlanPush(s, core.PushOptions{Members: members})
	var nf *core.PushNotFinishedError
	switch {
	case errors.As(err, &nf):
		return push(from, newPushNotFinished(e, pkg, session, unfinishable(c.Status(s))))
	case err != nil:
		return flashErr(err.Error())
	}
	return push(from, newPushConfirm(e, pkg, session, members, plan, back))
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
// session afresh, so it pushes what is landed now rather than what the dialog
// was opened on. Whether it succeeds or fails, the user returns to the screen
// the dialog came from.
func (m *pushConfirm) run() screen {
	c, pkg, session, members, back := m.e.core, m.pkg, m.session, m.members, m.back
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
		back: back,
		done: back,
	})
}

// pushText words one member's row and picks its style.
func pushText(r core.MemberPush) (string, lipgloss.Style) {
	switch r.Status {
	case core.PushNothing:
		return "nothing to push", styleDim
	case core.PushBlocked:
		return "blocked: " + r.Reason, styleWarn
	case core.PushFailed:
		return "failed: " + r.Reason, styleErr
	}
	text := fmt.Sprintf("%d commit", r.Commits)
	if r.Commits != 1 {
		text += "s"
	}
	text += fmt.Sprintf(" of %s → %s/%s", r.Branch, r.Remote, r.Dest)
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

// View implements screen.
func (m *pushConfirm) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("session ") + styleLabel.Render(m.pkg+"/"+m.session) + "\n\n")
	if len(m.plan.Members) == 0 {
		b.WriteString("   " + styleDim.Render("no active members") + "\n")
	}
	for _, r := range m.plan.Members {
		text, st := pushText(r)
		b.WriteString(fmt.Sprintf("   %-18s %s\n", r.Name, st.Render(text)))
	}
	b.WriteString("\n")
	switch {
	case m.ready() == 0 && m.blocked() > 0:
		b.WriteString(" " + styleWarn.Render("Nothing can be pushed until the members above are fixed.") + "\n\n ")
		b.WriteString(keyHelp("esc", "back"))
	case m.ready() == 0:
		b.WriteString(" Everything landed is already on its remote.\n\n ")
		b.WriteString(keyHelp("esc", "back"))
	default:
		b.WriteString(" Each branch above is pushed from its source repo, never forced.\n The session's own branch is not pushed.")
		if m.blocked() > 0 {
			b.WriteString(" Blocked members\n are left alone and the rest still go.")
		}
		b.WriteString("\n " + styleDim.Render("Compared with the last fetch; f refreshes it.") + "\n\n ")
		b.WriteString(keyHelp("y", "push", "esc", "cancel"))
	}
	width := w - 8
	if width > 84 {
		width = 84
	}
	return dialog(m.Title(), b.String(), width)
}

// unfinishable lists what stops the active members of a session from being
// finished, one line each: uncommitted or untracked files, a rebase in
// progress, a worktree that is missing or cannot be read. These are the same
// conditions that make finishing refuse, so a session with none of them can
// always be offered a finish. Reference members are left out, since they have
// no work of their own.
func unfinishable(st core.SessionStatus) []string {
	var out []string
	for _, ms := range st.Members {
		if ms.Mode != manifest.ModeActive {
			continue
		}
		switch {
		case ms.Missing:
			out = append(out, ms.Name+": its worktree is missing; sync recreates it")
		case ms.Error != "":
			out = append(out, ms.Name+": cannot read its state")
		case ms.Rebasing:
			out = append(out, ms.Name+": a rebase is in progress")
		case ms.Dirty > 0 || ms.Untracked > 0:
			out = append(out, fmt.Sprintf("%s: %d modified, %d untracked", ms.Name, ms.Dirty, ms.Untracked))
		}
	}
	return out
}

// pushNotFinished is the dialog shown when a push is asked of a session that
// is not finished. A flash in the status bar is too easy to miss for a key
// that did nothing, so this says why. When the session could be finished it
// also offers the next step: F opens the finish dialog. When uncommitted work
// stops it, blockers lists that work and F does nothing, since the finish
// would only be refused.
type pushNotFinished struct {
	e            *env
	pkg, session string
	blockers     []string
}

// newPushNotFinished builds the dialog. blockers is what unfinishable found.
func newPushNotFinished(e *env, pkg, session string, blockers []string) *pushNotFinished {
	return &pushNotFinished{e: e, pkg: pkg, session: session, blockers: blockers}
}

// modal implements modalScreen.
func (m *pushNotFinished) modal() {}

// Init implements screen.
func (m *pushNotFinished) Init() tea.Cmd { return nil }

// Title implements screen.
func (m *pushNotFinished) Title() string { return "push session" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *pushNotFinished) Crumb() string { return "" }

// Hints implements screen.
func (m *pushNotFinished) Hints() []hint { return nil }

// Update implements screen. F replaces the dialog with the session's finish
// dialog, planned afresh, unless something blocks finishing; any other close
// key just closes it.
func (m *pushNotFinished) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "esc", "enter", "n", "q":
		return m, pop(m)
	case "F":
		if len(m.blockers) > 0 {
			return m, nil
		}
		c := m.e.core
		s, err := c.ResolveSession(m.pkg + "/" + m.session)
		if err != nil {
			return m, flashErr(err.Error())
		}
		return m, goTo(m, newFinishConfirm(m.e, m.pkg, m.session, c.PlanFinish(s, core.FinishOptions{})))
	}
	return m, nil
}

// View implements screen.
func (m *pushNotFinished) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("session ") + styleLabel.Render(m.pkg+"/"+m.session) + "\n\n")
	if len(m.blockers) > 0 {
		b.WriteString(" " + styleWarn.Render("This session is not finished, and cannot be yet:") + "\n\n")
		for _, l := range m.blockers {
			b.WriteString("   " + styleWarn.Render(l) + "\n")
		}
		b.WriteString("\n Commit or resolve that, finish the session, then push.\n\n ")
		b.WriteString(keyHelp("esc", "close"))
		return dialog(m.Title(), b.String(), min(w-8, 84))
	}
	b.WriteString(" " + styleWarn.Render("This session is not finished, so there is nothing landed to push.") + "\n\n")
	b.WriteString(" Finishing lands each member's commits on its landing branch, and\n")
	b.WriteString(" push then publishes them. A finished session that has new commits\n")
	b.WriteString(" or uncommitted edits since counts as not finished again.\n\n ")
	b.WriteString(keyHelp("F", "finish the session", "esc", "close"))
	width := w - 8
	if width > 84 {
		width = 84
	}
	return dialog(m.Title(), b.String(), width)
}
