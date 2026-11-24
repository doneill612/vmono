package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/core"
)

// operation is a unit of work the progress screen runs and reports on.
type operation struct {
	title string
	// run performs the work. The progress screen attaches an event channel
	// to the core first, so run only calls core operations.
	run func(force, reuse bool) error
	// pkg and session name the session to offer launching afterward; an
	// empty pkg means return to Home instead.
	pkg, session string
	// home, when set, returns straight to the packages screen on success
	// instead of offering a launch or waiting on the done table. Creating a
	// package uses it so the user decides whether to start a session.
	home bool
	// force and reuse are passed to run; reuse is also set when the user
	// answers the branch-exists prompt.
	force, reuse bool
	// back builds the screen to return to when the operation fails.
	back func() screen
	// done builds the screen to show once the user acknowledges success. Nil
	// means Home.
	done func() screen
	// fixIn, when set, is the session where a failure can be fixed: the
	// failed screen offers l to launch the default harness there.
	fixIn *LaunchRequest
	// removed, when set, names a package the operation deleted. On success
	// the packages screen drops it at once instead of showing it until the
	// reload that follows returns.
	removed string
}

// progressState is where the progress screen is in its life.
type progressState int

const (
	progRunning progressState = iota
	progDone
	progFailed
	progAskReuse
)

// progressRow is one member step and its live status.
type progressRow struct {
	member, step string
	status       apply.Status
	msg          string
}

// eventMsg carries one apply event into the model.
type eventMsg apply.Event

// opDoneMsg reports that the operation finished.
type opDoneMsg struct{ err error }

// progressModel shows one row per member step with a live status. It reads
// the same event stream the CLI prints, so both always report the same
// steps. Shown on create, edit, sync and delete.
type progressModel struct {
	e     *env
	op    operation
	ch    chan apply.Event
	err   *error
	rows  []progressRow
	index map[string]int
	spin  spinner.Model
	state progressState
	fail  error
}

// newSpinner returns the spinner shared by every screen that waits on work,
// so the progress view and the repo scans animate identically.
func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return s
}

// scanningLabel is the animated placeholder every screen shows while it waits
// on work, whether that is scanning for repos or loading a list: the current
// spinner frame followed by the given text.
func scanningLabel(s spinner.Model, text string) string {
	return s.View() + " " + text
}

// newProgress builds the screen; it starts the operation on Init.
func newProgress(e *env, op operation) *progressModel {
	return &progressModel{e: e, op: op, index: map[string]int{}, spin: newSpinner()}
}

// Init implements screen.
func (m *progressModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.start())
}

// start runs the operation in a goroutine and returns the first wait.
func (m *progressModel) start() tea.Cmd {
	ch := make(chan apply.Event, 64)
	var opErr error
	m.ch, m.err = ch, &opErr
	m.state = progRunning
	m.fail = nil
	c := m.e.core
	run, force, reuse := m.op.run, m.op.force, m.op.reuse
	c.Events = ch
	go func() {
		opErr = run(force, reuse)
		c.Events = nil
		close(ch)
	}()
	return m.wait()
}

// wait blocks for the next event, or reports completion when the channel
// closes. The error is written before the close, so reading it here is safe.
func (m *progressModel) wait() tea.Cmd {
	ch, errp := m.ch, m.err
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return opDoneMsg{err: *errp}
		}
		return eventMsg(ev)
	}
}

// Title implements screen.
func (m *progressModel) Title() string { return m.op.title }

// Crumb implements screen.
func (m *progressModel) Crumb() string {
	return strings.ToLower(strings.Fields(m.op.title + " ")[0])
}

// Hints implements screen.
func (m *progressModel) Hints() []hint {
	switch m.state {
	case progAskReuse:
		return []hint{{"y", "Reuse branch"}, {"n", "Cancel"}}
	case progFailed:
		if m.op.fixIn != nil {
			return []hint{{"enter", "Back"}, {"l", "Launch harness in the session"}}
		}
		return []hint{{"enter", "Back"}}
	case progDone:
		return []hint{{"enter", "Back"}}
	}
	return nil
}

// Update implements screen.
func (m *progressModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if m.state != progRunning {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		return m, cmd
	case eventMsg:
		m.apply(apply.Event(t))
		return m, m.wait()
	case opDoneMsg:
		return m.finish(t.err)
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// apply folds an event into the row list.
func (m *progressModel) apply(ev apply.Event) {
	key := ev.Member + "|" + ev.Step
	i, ok := m.index[key]
	if !ok {
		m.rows = append(m.rows, progressRow{member: ev.Member, step: ev.Step})
		i = len(m.rows) - 1
		m.index[key] = i
	}
	r := &m.rows[i]
	if ev.Status == apply.StatusWarn {
		r.msg = ev.Message
		if r.status == "" {
			r.status = apply.StatusRunning
		}
		return
	}
	r.status = ev.Status
	if ev.Err != nil {
		r.msg = ev.Err.Error()
	}
}

// finish moves to the right end state.
func (m *progressModel) finish(err error) (screen, tea.Cmd) {
	var be *core.BranchExistsError
	switch {
	case err == nil:
		m.state = progDone
		if m.op.removed != "" {
			name := m.op.removed
			return m, func() tea.Msg { return packageRemovedMsg{name} }
		}
		if m.op.home {
			return m, goHome(m.op.title + " done")
		}
		if m.op.pkg != "" {
			return m, goTo(m, newLaunchPicker(m.e, m.op.pkg, m.op.session, "Launch now?"))
		}
	case errors.As(err, &be) && !m.op.reuse:
		m.state, m.fail = progAskReuse, err
	default:
		m.state, m.fail = progFailed, err
	}
	return m, nil
}

// key handles keys in each state.
func (m *progressModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	switch m.state {
	case progAskReuse:
		switch k.String() {
		case "y":
			m.op.reuse = true
			m.rows, m.index = nil, map[string]int{}
			return m, tea.Batch(m.spin.Tick, m.start())
		case "n", "esc":
			return m.leave()
		}
	case progFailed:
		switch k.String() {
		case "enter", "esc", "q":
			return m.leave()
		case "l":
			if m.op.fixIn != nil {
				req := *m.op.fixIn
				return m, func() tea.Msg { return launchMsg{req} }
			}
		}
	case progDone:
		switch k.String() {
		case "enter", "esc", "q":
			if m.op.done != nil {
				return m, goTo(m, m.op.done())
			}
			return m, goHome(m.op.title + " done")
		}
	}
	return m, nil
}

// leave returns to the screen the operation came from.
func (m *progressModel) leave() (screen, tea.Cmd) {
	if m.op.back != nil {
		return m, goTo(m, m.op.back())
	}
	return m, goHome("")
}

// View implements screen: a table of steps that follows the latest one,
// then a status block for the state the operation is in.
func (m *progressModel) View(w, h int) string {
	tb := newTable([]column{{title: "member", max: 24}, {title: "step", max: 24}, {title: "status"}, {title: "message", flex: true}})
	tb.static = true
	var rows []row
	for i, r := range m.rows {
		who := r.member
		if who == "" {
			who = "package"
		}
		rows = append(rows, row{id: fmt.Sprint(i), cells: []cell{
			plain(who), plain(r.step), m.statusCell(r.status), styled(r.msg, styleDim),
		}})
	}
	tb.setRows(rows)
	tb.cursor = len(rows) - 1
	tb.clamp()

	var foot []string
	switch m.state {
	case progRunning:
		foot = []string{" " + m.spin.View() + styleDim.Render(" working…")}
	case progDone:
		back := "back to packages"
		if m.op.done != nil {
			back = "back"
		}
		foot = []string{" " + styleOK.Render("done") + "  " + keyHelp("enter", back)}
	case progFailed:
		help := keyHelp("enter", "go back")
		if m.op.fixIn != nil {
			help = keyHelp("enter", "go back", "l", "launch a harness in the session to fix it")
		}
		foot = []string{" " + styleErr.Render(m.fail.Error()), " " + help}
	case progAskReuse:
		var be *core.BranchExistsError
		errors.As(m.fail, &be)
		foot = []string{
			" " + styleWarn.Render(fmt.Sprintf("Branch %q already exists in %s.", be.Branch, be.Member)),
			" Check it out instead of creating it?  " + keyHelp("y", "reuse", "n", "cancel"),
		}
	}
	return tb.view(w, h-len(foot)-1) + "\n\n" + strings.Join(foot, "\n")
}

// statusCell renders a step's status as a glyph and a word.
func (m *progressModel) statusCell(s apply.Status) cell {
	switch s {
	case apply.StatusDone:
		return styled("✓ done", styleOK)
	case apply.StatusFailed:
		return styled("✗ failed", styleErr)
	case apply.StatusUndone:
		return styled("↩ undone", styleWarn)
	case apply.StatusRunning:
		return styled("… running", styleDim)
	}
	return plain("")
}
