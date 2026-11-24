package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/herdr"
	"github.com/doneill612/vmono/internal/launcher"
)

// How often running agents are looked at, normally and after a failure. Tests
// shorten them.
var (
	agentInterval = 3 * time.Second
	agentBackoff  = 15 * time.Second
)

// Messages of the agent poll.
type (
	// agentTickMsg says it is time to look again.
	agentTickMsg struct{}
	// agentStateMsg delivers what is running. It is broadcast, so views
	// rebuild their rows from env.live.
	agentStateMsg struct {
		live launcher.Live
		err  error
	}
)

// agentAware marks the views that show agent state. Only while one of them is
// on screen, under any dialogs, are the launchers looked at.
type agentAware interface{ showsAgents() }

// showsAgents implements agentAware.
func (m *sessionsModel) showsAgents() {}

// showsAgents implements agentAware.
func (m *packagesModel) showsAgents() {}

// watchAgents turns on the agent column and the poll that fills it, from the
// given source. Without it the views look as they do under the direct
// launcher.
func (a *App) watchAgents(source func() (launcher.Live, error)) {
	a.env.agents = source
	if len(a.stack) == 1 {
		if _, ok := a.stack[0].(*packagesModel); ok {
			a.stack[0] = newPackages(a.env)
		}
	}
}

// pollAgents looks at what is running when a view that shows it is on screen,
// and otherwise waits for the next tick.
func (a *App) pollAgents() tea.Cmd {
	source := a.env.agents
	if source == nil {
		return nil
	}
	i := len(a.stack) - 1
	for ; i > 0; i-- {
		if _, modal := a.stack[i].(modalScreen); !modal {
			break
		}
	}
	if _, ok := a.stack[i].(agentAware); !ok {
		return agentTick(agentInterval)
	}
	return func() tea.Msg {
		live, err := source()
		return agentStateMsg{live, err}
	}
}

// agentTick schedules the next look.
func agentTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return agentTickMsg{} })
}

// receiveAgents records what is running and schedules the next look. Only the
// first failure in a row is shown, and what was last seen stays up.
func (a *App) receiveAgents(m agentStateMsg) tea.Cmd {
	if m.err != nil {
		first := !a.env.agentsFailed
		a.env.agentsFailed = true
		if first {
			return tea.Batch(flashErr("could not read agent state: "+m.err.Error()), agentTick(agentBackoff))
		}
		return agentTick(agentBackoff)
	}
	a.env.agentsFailed = false
	a.env.live = m.live
	return tea.Batch(a.broadcast(m), agentTick(agentInterval))
}

// sessionStates returns the state of each running session of a package.
func (e *env) sessionStates(pkg string) map[string]string {
	dir, err := e.core.PackageDir(pkg)
	if e.agents == nil || err != nil {
		return nil
	}
	return e.live.Sessions(dir)
}

// agentGlyphs marks each state.
var agentGlyphs = map[string]string{herdr.Blocked: "▲", herdr.Working: "●", herdr.Idle: "○", herdr.Done: "✓", launcher.Running: "●"}

// agentCell renders a session's state, keyed to sort the most urgent first.
func agentCell(state string) cell {
	c := styled("", styleDim)
	if state != "" {
		st := styleDim
		switch state {
		case herdr.Blocked:
			st = styleWarn
		case herdr.Working:
			st = styleOK
		}
		c = styled(agentGlyphs[state]+" "+state, st)
	}
	c.key = fmt.Sprint(9 - launcher.Urgency(state))
	return c
}

// agentsCell sums up the states of a package's sessions, as "1▲ 2●".
func agentsCell(states map[string]string) cell {
	counts := map[string]int{}
	top := 0
	for _, s := range states {
		counts[s]++
		top = max(top, launcher.Urgency(s))
	}
	st := styleDim
	if counts[herdr.Blocked] > 0 {
		st = styleWarn
	}
	var parts []string
	for _, s := range []string{herdr.Blocked, herdr.Working, herdr.Done, herdr.Idle, launcher.Running} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", n, agentGlyphs[s]))
		}
	}
	c := styled(strings.Join(parts, " "), st)
	c.key = fmt.Sprint(9 - top)
	return c
}
