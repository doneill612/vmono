package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/testutil"
)

// driver feeds messages to an App and executes the commands it returns,
// without a terminal. Timer-driven messages (spinner and cursor blink) are dropped so
// tests never wait on animation; a command that exceeds a generous timeout
// is abandoned.
type driver struct {
	t    *testing.T
	app  *App
	quit bool
}

// newDriver builds an App over an isolated environment with a loaded
// config, sized like a modest terminal.
func newDriver(t *testing.T, env *testutil.Env, message string) *driver {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Loaded = true
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return newDriverFor(t, c, message)
}

// newDriverFor builds a driver around an existing Core.
func newDriverFor(t *testing.T, c *core.Core, message string) *driver {
	t.Helper()
	d := &driver{t: t, app: NewApp(c, message)}
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	d.run(d.app.Init())
	return d
}

// send delivers a message and runs the resulting commands to completion.
func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	_, cmd := d.app.Update(msg)
	d.run(cmd)
}

// run executes a command and feeds its message back, recursively.
func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(3 * time.Second):
		return
	}
	if strings.HasPrefix(fmt.Sprintf("%T", msg), "cursor.") {
		return // cursor blink timers; irrelevant to behavior
	}
	switch m := msg.(type) {
	case nil:
	case spinner.TickMsg:
		// Timer-driven animation; feeding it back would recurse forever.
	case tea.QuitMsg:
		d.quit = true
	case tea.BatchMsg:
		for _, c := range m {
			d.run(c)
		}
	default:
		d.send(m)
	}
}

// key sends a key by name: single runes, or "enter", "esc", "tab", "up",
// "down", "space", "ctrl+s" and similar.
func (d *driver) key(names ...string) {
	d.t.Helper()
	for _, n := range names {
		d.send(keyMsg(n))
	}
}

// typeText sends each rune as a key press.
func (d *driver) typeText(s string) {
	d.t.Helper()
	for _, r := range s {
		d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// keyMsg builds a tea.KeyMsg from a name.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// view returns the current screen's text.
func (d *driver) view() string { return d.app.View() }

// has asserts the view contains every substring.
func (d *driver) has(subs ...string) {
	d.t.Helper()
	v := d.view()
	for _, s := range subs {
		if !strings.Contains(v, s) {
			d.t.Errorf("view missing %q:\n%s", s, v)
		}
	}
}

// lacks asserts the view contains none of the substrings.
func (d *driver) lacks(subs ...string) {
	d.t.Helper()
	v := d.view()
	for _, s := range subs {
		if strings.Contains(v, s) {
			d.t.Errorf("view should not contain %q:\n%s", s, v)
		}
	}
}

// eventually re-checks cond until it holds or a deadline passes, pumping
// no messages itself; it exists for assertions on async state.
func (d *driver) eventually(cond func() bool, what string) {
	d.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.t.Fatalf("timed out waiting for %s\n%s", what, d.view())
}
