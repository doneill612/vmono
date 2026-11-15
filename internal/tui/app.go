// Package tui is vmono's interactive terminal UI: Bubble Tea models, one per
// screen, driving the same core operations as the CLI. Every path through it
// ends at a running harness except delete.
package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
// is normally itself; moving between screens is done by returning one of the
// navigation messages below. View draws only the body, sized to the interior
// of the frame the App draws around it; Title is the text set into the
// frame's top border, Crumb the label in the bottom breadcrumb trail, and
// Hints the keys listed in the header.
type screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (screen, tea.Cmd)
	View(w, h int) string
	Title() string
	Crumb() string
	Hints() []hint
}

// capturing is implemented by screens whose text input is focused. While it
// reports true the App leaves every key, including ":", "/" and "?", to the
// screen.
type capturing interface{ capturing() bool }

// modalScreen marks a screen drawn as a dialog over the screen beneath it.
type modalScreen interface{ modal() }

// filterable is implemented by screens that can be narrowed with "/".
type filterable interface {
	setFilter(q string)
	filterValue() string
}

// env is state shared by every screen.
type env struct {
	core *core.Core
	w, h int
	// launch is set when the user chose to launch a harness.
	launch *LaunchRequest
	// pkgNames, pkgCount and pkgDirty describe the last package list that
	// loaded, for command completion and the header.
	pkgNames           []string
	pkgCount, pkgDirty int
	pkgKnown           bool
}

// setPackages records the package list for completion and the header.
func (e *env) setPackages(list []core.Summary) {
	e.pkgNames = e.pkgNames[:0]
	e.pkgCount, e.pkgDirty, e.pkgKnown = len(list), 0, true
	for _, s := range list {
		e.pkgNames = append(e.pkgNames, s.Name)
		if s.Dirty {
			e.pkgDirty++
		}
	}
}

// Navigation and notification messages. Screens return commands that
// produce these; the App applies them to its screen stack.
type (
	// gotoMsg replaces the top screen.
	gotoMsg struct{ s screen }
	// pushMsg shows a screen above the current one.
	pushMsg struct{ s screen }
	// popMsg removes the top screen and refreshes the one revealed.
	popMsg struct{}
	// homeMsg returns to the packages view, reloading it, and flashes text.
	homeMsg struct{ text string }
	// flashMsg shows a one-line notice in the status bar until the next key.
	flashMsg struct {
		text string
		err  bool
	}
	// refreshMsg asks a screen to reload its data.
	refreshMsg struct{}
	// launchMsg records a launch request and quits so the terminal is released.
	launchMsg struct{ req LaunchRequest }
)

// goTo returns a command that replaces the top screen.
func goTo(s screen) tea.Cmd { return func() tea.Msg { return gotoMsg{s} } }

// push returns a command that shows a screen above the current one.
func push(s screen) tea.Cmd { return func() tea.Msg { return pushMsg{s} } }

// pop returns a command that closes the top screen.
func pop() tea.Cmd { return func() tea.Msg { return popMsg{} } }

// goHome returns a command that goes back to the packages view.
func goHome(text string) tea.Cmd { return func() tea.Msg { return homeMsg{text} } }

// flash returns a command that shows a notice.
func flash(text string) tea.Cmd { return func() tea.Msg { return flashMsg{text: text} } }

// flashErr returns a command that shows an error notice.
func flashErr(text string) tea.Cmd { return func() tea.Msg { return flashMsg{text: text, err: true} } }

// App is the root Bubble Tea model. It owns the screen stack, the header,
// frame and status bar drawn around every screen, the command bar, and the
// global keys.
type App struct {
	env      *env
	stack    []screen
	bar      cmdBar
	notice   string
	noticeEr bool
}

// NewApp builds the app. It opens first-run setup when no config exists,
// otherwise the packages view, optionally showing a notice (such as why a
// launch failed) on arrival.
func NewApp(c *core.Core, message string) *App {
	e := &env{core: c}
	a := &App{env: e, bar: newCmdBar(), notice: message}
	if !c.Cfg.Loaded {
		a.stack = []screen{newSetup(e)}
	} else {
		a.stack = []screen{newPackages(e)}
	}
	return a
}

// top returns the screen on top of the stack.
func (a *App) top() screen { return a.stack[len(a.stack)-1] }

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return a.top().Init() }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if m.String() == "ctrl+c" {
			return a, tea.Quit
		}
		a.notice = ""
		if a.bar.active {
			return a, a.updateBar(m)
		}
		if cmd, handled := a.globalKey(m); handled {
			return a, cmd
		}
		return a, a.updateTop(m)
	case tea.WindowSizeMsg:
		a.env.w, a.env.h = m.Width, m.Height
	case flashMsg:
		a.notice, a.noticeEr = m.text, m.err
		return a, nil
	case gotoMsg:
		a.stack[len(a.stack)-1] = m.s
		return a, m.s.Init()
	case pushMsg:
		a.stack = append(a.stack, m.s)
		return a, m.s.Init()
	case popMsg:
		return a, a.pop()
	case homeMsg:
		a.notice, a.noticeEr = m.text, false
		return a, a.home()
	case repoAddedMsg:
		if _, ok := a.top().(*finderModel); ok {
			a.stack = a.stack[:len(a.stack)-1]
		}
		return a, a.broadcast(m)
	case launchMsg:
		req := m.req
		a.env.launch = &req
		return a, tea.Quit
	}
	return a, a.broadcast(msg)
}

// updateTop delivers a message to the top screen only.
func (a *App) updateTop(msg tea.Msg) tea.Cmd {
	s, cmd := a.top().Update(msg)
	a.stack[len(a.stack)-1] = s
	return cmd
}

// broadcast delivers an asynchronous result to every screen in the stack, so
// a screen under a dialog or a drilled-down view still receives its data.
// Screens ignore message types they do not own.
func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i, s := range a.stack {
		ns, cmd := s.Update(msg)
		a.stack[i] = ns
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// pop closes the top screen and tells the revealed one to reload.
func (a *App) pop() tea.Cmd {
	if len(a.stack) <= 1 {
		return nil
	}
	a.stack = a.stack[:len(a.stack)-1]
	return a.updateTop(refreshMsg{})
}

// home returns to the packages view. An existing packages root is kept, so
// its filter and cursor survive; anything else is replaced.
func (a *App) home() tea.Cmd {
	if _, ok := a.stack[0].(*packagesModel); ok {
		a.stack = a.stack[:1]
		return a.updateTop(refreshMsg{})
	}
	s := newPackages(a.env)
	a.stack = []screen{s}
	return s.Init()
}

// navigate switches to a top-level view from anywhere: the stack is cut
// back to the packages root and the view is pushed above it, so esc always
// leads back to packages.
func (a *App) navigate(s screen) tea.Cmd {
	cmd := a.home()
	a.stack = append(a.stack, s)
	return tea.Batch(cmd, s.Init())
}

// isCapturing reports whether the top screen has a focused text input.
func (a *App) isCapturing() bool {
	c, ok := a.top().(capturing)
	return ok && c.capturing()
}

// globalKey handles the keys that work on every screen. It reports whether
// the key was consumed.
func (a *App) globalKey(k tea.KeyMsg) (tea.Cmd, bool) {
	if a.isCapturing() {
		return nil, false
	}
	if _, isModal := a.top().(modalScreen); isModal {
		return nil, false
	}
	switch k.String() {
	case ":":
		return a.bar.open(':', ""), true
	case "/":
		if f, ok := a.top().(filterable); ok {
			return a.bar.open('/', f.filterValue()), true
		}
	case "?":
		return push(newHelp(a.top().Hints())), true
	case "ctrl+r":
		return a.updateTop(refreshMsg{}), true
	case "q":
		if len(a.stack) == 1 {
			return tea.Quit, true
		}
	}
	return nil, false
}

// View implements tea.Model.
func (a *App) View() string {
	w, h := a.env.w, a.env.h
	if w < 20 {
		w = 80
	}
	if h < 8 {
		h = 24
	}
	base := len(a.stack) - 1
	for base > 0 {
		if _, isModal := a.stack[base].(modalScreen); !isModal {
			break
		}
		base--
	}
	scr := a.stack[base]

	compact := h < 20 || w < 60
	hints := a.hints()
	var header string
	hh := headerHeight
	if compact {
		hh = 1
		header = renderHeaderCompact(w, a.context())
	} else {
		header = renderHeader(w, a.context(), hints)
	}
	barH := 0
	if a.bar.active {
		barH = 1
	}
	bodyH := h - hh - 1 - barH
	if bodyH < 3 {
		bodyH = 3
	}

	var crumbs []string
	for _, s := range a.stack[:base+1] {
		if c := s.Crumb(); c != "" {
			crumbs = append(crumbs, c)
		}
	}

	parts := []string{header}
	if a.bar.active {
		parts = append(parts, a.bar.view(w, a.ghost()))
	}
	frame := renderFrame(scr.Title(), scr.View(w-2, bodyH-2), w, bodyH)
	for _, s := range a.stack[base+1:] {
		d := s.View(w-2, bodyH-2)
		dw, dh := lipgloss.Width(firstLine(d)), strings.Count(d, "\n")+1
		x, y := (w-dw)/2, (bodyH-dh)/2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		frame = overlay(frame, d, x, y)
	}
	parts = append(parts, frame, renderStatus(w, crumbs, a.notice, a.noticeEr))
	return strings.Join(parts, "\n")
}

// firstLine returns the first line of s.
func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

// hints returns the header hints: the top screen's own, or the screen
// beneath a dialog. The global ":" and "?" live in the status bar.
func (a *App) hints() []hint {
	var hs []hint
	for i := len(a.stack) - 1; i >= 0; i-- {
		if hs = a.stack[i].Hints(); len(hs) > 0 {
			break
		}
	}
	return hs
}

// context returns the labelled lines at the left of the header.
func (a *App) context() []kv {
	cfg := a.env.core.Cfg
	src := "none"
	if len(cfg.SourceRoots) > 0 {
		src = strings.Join(cfg.SourceRoots, ", ")
	}
	pk := "…"
	if a.env.pkgKnown {
		pk = fmt.Sprint(a.env.pkgCount)
		if a.env.pkgDirty > 0 {
			pk += fmt.Sprintf(" (%d dirty)", a.env.pkgDirty)
		}
	}
	return []kv{
		{"Package root", cfg.PackageRoot},
		{"Source roots", src},
		{"Harness", cfg.DefaultHarness},
		{"Packages", pk},
	}
}

// renderHeaderCompact is the one-line header for small terminals.
func renderHeaderCompact(w int, ctx []kv) string {
	var parts []string
	for _, c := range ctx {
		if c.k == "Package root" || c.k == "Harness" {
			parts = append(parts, c.v)
		}
	}
	line := " " + styleTitle.Render("vmono") + styleDim.Render("  "+strings.Join(parts, " · "))
	return clip(line, w)
}

// runHarness starts a prepared harness. It is a variable so tests can
// observe the handoff without replacing the test process.
var runHarness = harness.Run

// Launch returns the launch request chosen during the session, if any.
func (a *App) Launch() *LaunchRequest { return a.env.launch }

// handoff runs after the TUI has released the terminal. It prepares the
// harness for the request and starts it. Without return_to_tui (macOS and
// Linux) vmono is replaced by the harness and the result is final. With it
// (always on Windows) the harness runs as a child; again is true so the
// caller reopens the TUI when it exits, carrying message if the launch
// itself failed. A harness's own non-zero exit is not a vmono failure.
func handoff(c *core.Core, req *LaunchRequest) (message string, again bool, err error) {
	pkg, err := c.Load(req.Package)
	if err != nil {
		return err.Error(), true, nil
	}
	l, err := harness.Prepare(c, pkg, req.Harness, nil)
	if err != nil {
		return err.Error(), true, nil
	}
	if !c.Cfg.ReturnToTUI {
		return "", false, runHarness(l, true)
	}
	var exit *harness.ExitError
	if err := runHarness(l, false); err != nil && !errors.As(err, &exit) {
		return err.Error(), true, nil
	}
	return "", true, nil
}

// Run opens the TUI and, when the user picks a harness, hands the terminal
// over to it (see handoff). It loops while return_to_tui is on, reopening
// the packages view after each harness exits.
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
		final, err := tea.NewProgram(NewApp(c, message), tea.WithAltScreen()).Run()
		if err != nil {
			return err
		}
		req := final.(*App).Launch()
		if req == nil {
			return nil
		}
		msg, again, err := handoff(c, req)
		if !again {
			return err
		}
		message = msg
	}
}
