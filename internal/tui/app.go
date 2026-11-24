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
	"github.com/doneill612/vmono/internal/launcher"
)

// LaunchRequest asks the caller to start a harness at a session once the
// TUI has released the terminal. Session is empty to use the package's only
// session. Harness is empty to use the package's default.
type LaunchRequest struct {
	Package string
	Session string
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

// footerHinter is implemented by screens that choose which few keys the
// footer shows. The footer is deliberately short: everything else is in the
// screen's action menu, the palette and the "?" help, which lists Hints in
// full. Screens without it show their Hints.
type footerHinter interface{ footerHints() []hint }

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
//
// Every navigation message names the screen that issued it (from). A command
// runs asynchronously, so its message is queued behind any keys that were
// already waiting, and those keys are still delivered to the screen that
// issued it. Without the origin a held or double-pressed key would push the
// same view several times ("packages > pay > pay > pay"), or close the view
// revealed beneath the one it was meant for. The App therefore applies a
// navigation message only while its origin is still where the message
// expects it: a push or pop only from the top screen, a goto only from a
// screen still in the stack. Anything else is stale and dropped.
type (
	// gotoMsg replaces the screen from with s.
	gotoMsg struct{ from, s screen }
	// pushMsg shows s above from, the top screen.
	pushMsg struct{ from, s screen }
	// popMsg removes from, the top screen, and refreshes the one revealed.
	popMsg struct{ from screen }
	// popPastMsg removes from, the top screen, and then every screen beneath
	// it that past reports true for, and refreshes the one revealed.
	popPastMsg struct {
		from screen
		past func(screen) bool
	}
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

// goTo returns a command that replaces the screen from with s.
func goTo(from, s screen) tea.Cmd { return func() tea.Msg { return gotoMsg{from, s} } }

// push returns a command that shows s above from.
func push(from, s screen) tea.Cmd { return func() tea.Msg { return pushMsg{from, s} } }

// pop returns a command that closes from.
func pop(from screen) tea.Cmd { return func() tea.Msg { return popMsg{from} } }

// popPast returns a command that closes from and then any screens beneath it
// that past reports true for. It is for an operation that removes what those
// screens show, so they are not revealed with nothing left to display.
func popPast(from screen, past func(screen) bool) tea.Cmd {
	return func() tea.Msg { return popPastMsg{from, past} }
}

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

// indexOf returns the position of s in the stack, or -1 when it is no longer
// there.
func (a *App) indexOf(s screen) int {
	for i, t := range a.stack {
		if t == s {
			return i
		}
	}
	return -1
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return a.top().Init() }

// Update implements tea.Model. After handling a message it lets the list on
// screen start loading the preview of whatever row the message left
// highlighted.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := a.update(msg)
	if a.env.w >= previewMinW {
		if p, ok := a.stack[a.baseIndex()].(previewer); ok {
			cmd = tea.Batch(cmd, p.previewSync())
		}
	}
	return m, cmd
}

// update handles one message.
func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		i := a.indexOf(m.from)
		if i < 0 {
			return a, nil
		}
		a.stack[i] = m.s
		if i < len(a.stack)-1 {
			return a, m.s.Init()
		}
		return a, tea.Batch(m.s.Init(), a.refreshBeneath(m.s))
	case pushMsg:
		if m.from != a.top() {
			return a, nil
		}
		a.stack = append(a.stack, m.s)
		return a, m.s.Init()
	case popMsg:
		if m.from != a.top() {
			return a, nil
		}
		return a, a.pop()
	case popPastMsg:
		if m.from != a.top() {
			return a, nil
		}
		return a, a.popPast(m.past)
	case actionMsg:
		if m.owner != a.top() {
			return a, nil
		}
		return a, a.updateTop(m)
	case homeMsg:
		a.notice, a.noticeEr = m.text, false
		return a, a.home()
	case repoAddedMsg:
		if _, ok := a.top().(*finderModel); ok {
			a.stack = a.stack[:len(a.stack)-1]
		}
		return a, a.broadcast(m)
	case navigateMsg:
		return a, a.navigate(m.build())
	case reloadMsg:
		return a, a.updateTop(refreshMsg{})
	case sortMsg:
		if t, ok := a.top().(tabular); ok {
			t.tbl().sortBy(m.col)
		}
		return a, nil
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

// refreshBeneath tells every screen under the top one to reload when the top
// screen is a dialog. A goto that ends an operation, such as the "Launch
// now?" picker replacing the create progress screen, bypasses pop, which is
// what normally refreshes a revealed screen. Without this the view drawn
// behind the dialog keeps the data it loaded before the operation ran, for
// example "No sessions yet" for a session that was just created.
func (a *App) refreshBeneath(top screen) tea.Cmd {
	if _, isModal := top.(modalScreen); !isModal {
		return nil
	}
	var cmds []tea.Cmd
	for i, s := range a.stack[:len(a.stack)-1] {
		ns, cmd := s.Update(refreshMsg{})
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

// popPast closes the top screen and then each screen beneath it that past
// reports true for, always keeping the root, and tells the screen revealed to
// reload.
func (a *App) popPast(past func(screen) bool) tea.Cmd {
	if len(a.stack) <= 1 {
		return nil
	}
	a.stack = a.stack[:len(a.stack)-1]
	for len(a.stack) > 1 && past(a.top()) {
		a.stack = a.stack[:len(a.stack)-1]
	}
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
	case ":", "ctrl+p":
		return push(a.top(), newPalette(a.paletteEntries())), true
	case "/":
		if f, ok := a.top().(filterable); ok {
			return a.bar.open(f.filterValue()), true
		}
	case "?":
		return push(a.top(), newHelp(a.top().Hints())), true
	case "ctrl+r":
		return a.updateTop(refreshMsg{}), true
	case "q":
		if len(a.stack) == 1 {
			return tea.Quit, true
		}
	}
	return nil, false
}

// baseIndex returns the index of the screen the app is drawn around: the top
// of the stack, or the screen beneath the dialogs above it.
func (a *App) baseIndex() int {
	base := len(a.stack) - 1
	for base > 0 {
		if _, isModal := a.stack[base].(modalScreen); !isModal {
			break
		}
		base--
	}
	return base
}

// chromeLines is the number of lines the top bar, its rule and the footer
// take; the screen's own heading takes one more.
const chromeLines = 3

// View implements tea.Model.
func (a *App) View() string {
	w, h := a.env.w, a.env.h
	if w < 20 {
		w = 80
	}
	if h < 8 {
		h = 24
	}
	base := a.baseIndex()
	scr := a.stack[base]

	barH := 0
	if a.bar.active {
		barH = 1
	}
	bodyH := max(h-chromeLines-barH, 4)

	var crumbs []string
	for _, s := range a.stack[:base+1] {
		if c := s.Crumb(); c != "" {
			crumbs = append(crumbs, c)
		}
	}

	parts := []string{renderTopBar(w, crumbs, a.summary()), renderRule(w)}
	if a.bar.active {
		parts = append(parts, a.bar.view(w))
	}
	heading := func(cw int) string { return clip(" "+styleLabel.Render(truncate(scr.Title(), cw-2)), cw) }
	var body string
	if p, ok := scr.(previewer); ok && w >= previewMinW {
		lw := w * 58 / 100
		rw := w - lw - 1
		body = splitColumns(heading(lw)+"\n"+scr.View(lw, bodyH-1), p.previewView(rw, bodyH), lw, rw, bodyH)
	} else {
		body = fitBlock(heading(w)+"\n"+scr.View(w, bodyH-1), w, bodyH)
	}
	for _, s := range a.stack[base+1:] {
		d := s.View(w, bodyH)
		dw, dh := lipgloss.Width(firstLine(d)), strings.Count(d, "\n")+1
		body = overlay(body, d, max((w-dw)/2, 0), max((bodyH-dh)/2, 0))
	}
	parts = append(parts, body, renderFooter(w, a.footer(), a.notice, a.noticeEr))
	return strings.Join(parts, "\n")
}

// fitBlock cuts or pads a block of lines to exactly w by h cells, so the
// footer stays on the last line however short the screen is and no screen
// can push the layout wider than the terminal.
func fitBlock(body string, w, h int) string {
	lines := strings.Split(body, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = clip(l, w)
	}
	return strings.Join(lines, "\n")
}

// firstLine returns the first line of s.
func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

// footer returns the keys the footer lists. Nothing is listed over a dialog,
// which carries its own key help. Elsewhere the screen's own keys come first,
// then the two global ones, unless a text input has the keyboard and would
// take them as text.
func (a *App) footer() []hint {
	top := a.top()
	if _, isModal := top.(modalScreen); isModal {
		return nil
	}
	var own []hint
	if f, ok := top.(footerHinter); ok {
		own = f.footerHints()
	} else {
		own = top.Hints()
	}
	if a.isCapturing() {
		return own
	}
	return append(append([]hint(nil), own...), hint{":", "palette"}, hint{"?", "help"})
}

// summary is the short line at the right of the top bar: how many packages
// there are and which harness opens them.
func (a *App) summary() string {
	cfg := a.env.core.Cfg
	var parts []string
	if a.env.pkgKnown {
		pk := fmt.Sprintf("%d packages", a.env.pkgCount)
		if a.env.pkgCount == 1 {
			pk = "1 package"
		}
		if a.env.pkgDirty > 0 {
			pk += fmt.Sprintf(" (%d dirty)", a.env.pkgDirty)
		}
		parts = append(parts, pk)
	}
	if cfg.DefaultHarness != "" {
		parts = append(parts, cfg.DefaultHarness)
	}
	return strings.Join(parts, " · ")
}

// runHarness starts a prepared harness. It is a variable so tests can
// observe the handoff without replacing the test process.
var runHarness = harness.Run

// Launch returns the launch request chosen during the session, if any.
func (a *App) Launch() *LaunchRequest { return a.env.launch }

// resolveLauncher picks the launcher for a handoff. It is a variable so
// tests can stand in a launcher without a real tmux.
var resolveLauncher = launcher.Resolve

// handoff runs after the TUI has released the terminal. It prepares the
// harness for the request and starts it with the configured launcher.
//
// With the direct launcher and no return_to_tui (macOS and Linux) vmono is
// replaced by the harness and the result is final. With return_to_tui
// (always on Windows) the harness runs as a child; again is true so the
// caller reopens the TUI when it exits, carrying message if the launch
// itself failed. A harness's own non-zero exit is not a vmono failure.
//
// With the tmux launcher, a launch that switched the tmux client to the
// session's own tmux session is final: the harness runs there, and quitting
// is what closes the TUI when it was opened in a tmux popup. Attaching from
// outside tmux follows return_to_tui like a direct launch, reopening the TUI
// once the client detaches. A tmux failure reopens the TUI with tmux's
// message instead of leaving the user at a bare shell.
func handoff(c *core.Core, req *LaunchRequest) (message string, again bool, err error) {
	target := req.Package
	if req.Session != "" {
		target += "/" + req.Session
	}
	sess, err := c.ResolveSession(target)
	if err != nil {
		return err.Error(), true, nil
	}
	l, err := harness.Prepare(c, sess, req.Harness, nil)
	if err != nil {
		return err.Error(), true, nil
	}
	ln, err := resolveLauncher(c.Cfg)
	if err != nil {
		return err.Error(), true, nil
	}
	if _, direct := ln.(launcher.Direct); !direct {
		res, err := ln.Launch(l, launcher.Opts{Replace: !c.Cfg.ReturnToTUI})
		switch {
		case err != nil:
			return err.Error(), true, nil
		case res == launcher.Elsewhere || !c.Cfg.ReturnToTUI:
			return "", false, nil
		default:
			return "", true, nil
		}
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
		c.CloseSession = launcher.CloseSession
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
