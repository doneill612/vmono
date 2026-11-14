// Package tui is vmono's interactive terminal UI: Bubble Tea models, one per
// screen, driving the same core operations as the CLI. Every path through it
// ends at a running harness except delete.
package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
)

// LaunchRequest asks the caller to start a harness at a package once the
// TUI has released the terminal. Harness is empty to use the package's
// default.
type LaunchRequest struct {
	Package string
	Harness string
}

// screen is one TUI view. Update returns the screen to keep showing, which
// is normally itself; switching screens is done by returning a gotoMsg.
type screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (screen, tea.Cmd)
	View() string
}

// env is state shared by every screen.
type env struct {
	core *core.Core
	w, h int
	// launch is set when the user chose to launch a harness.
	launch *LaunchRequest
}

// gotoMsg replaces the current screen.
type gotoMsg struct{ s screen }

// launchMsg records a launch request and quits so the terminal is released.
type launchMsg struct{ req LaunchRequest }

// goTo returns a command that switches screens.
func goTo(s screen) tea.Cmd { return func() tea.Msg { return gotoMsg{s} } }

// App is the root Bubble Tea model. It owns screen routing and global keys.
type App struct {
	env *env
	cur screen
}

// NewApp builds the app. It opens first-run setup when no config exists,
// otherwise Home, optionally showing a message (such as why a launch
// failed) on arrival.
func NewApp(c *core.Core, message string) *App {
	e := &env{core: c}
	a := &App{env: e}
	if !c.Cfg.Loaded {
		a.cur = newSetup(e)
	} else {
		a.cur = newHome(e, message)
	}
	return a
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return a.cur.Init() }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.env.w, a.env.h = m.Width, m.Height
	case tea.KeyMsg:
		if m.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case gotoMsg:
		a.cur = m.s
		return a, a.cur.Init()
	case launchMsg:
		req := m.req
		a.env.launch = &req
		return a, tea.Quit
	}
	var cmd tea.Cmd
	a.cur, cmd = a.cur.Update(msg)
	return a, cmd
}

// View implements tea.Model.
func (a *App) View() string { return a.cur.View() }

// Launch returns the launch request chosen during the session, if any.
func (a *App) Launch() *LaunchRequest { return a.env.launch }

// Run opens the TUI. When the user picks a harness, the terminal is
// restored, vmono execs the harness (replacing itself on macOS and Linux),
// or, with return_to_tui and always on Windows, runs it as a child and
// reopens Home when it exits.
func Run() error {
	message := ""
	for {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		c, err := core.New(cfg)
		if err != nil {
			return err
		}
		app := NewApp(c, message)
		message = ""
		final, err := tea.NewProgram(app, tea.WithAltScreen()).Run()
		if err != nil {
			return err
		}
		req := final.(*App).Launch()
		if req == nil {
			return nil
		}
		pkg, err := c.Load(req.Package)
		if err != nil {
			message = err.Error()
			continue
		}
		l, err := harness.Prepare(c, pkg, req.Harness, nil)
		if err != nil {
			message = err.Error()
			continue
		}
		if !cfg.ReturnToTUI {
			return harness.Run(l, true)
		}
		var exit *harness.ExitError
		if err := harness.Run(l, false); err != nil && !errors.As(err, &exit) {
			message = err.Error()
		}
	}
}
