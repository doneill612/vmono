package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/paths"
)

// setupModel is the setup screen. On first run it suggests ~/source as a
// source root when that directory exists, asks for the package root, and
// reports which known harnesses are installed. When reopened from Home
// (reconfig) it starts from the saved config instead, keeps the configured
// default harness, adds a field for each global hook, and esc returns to Home rather than quitting.
type setupModel struct {
	e         *env
	reconfig  bool
	src, pkg  textinput.Model
	focus     int
	installed map[string]bool
	order     []string
	def       string
	err       string
	// hooks are the global hook fields, in config.HookNames order; none on
	// first run. They take focus after the two paths.
	hooks []hookField
	// cands are the directories tab completion found for the focused field.
	cands []string
}

// hookField edits one global hook: its command, and whether a failure aborts
// the operation. A timeout is kept but only config.toml can change it.
type hookField struct {
	name  string
	in    textinput.Model
	abort bool
	// before is the text the field opened with, and orig the hook it stands for.
	before string
	orig   config.Hook
}

// newHookFields builds a field for each global hook.
func newHookFields(set map[string]config.Hook) []hookField {
	var out []hookField
	for _, name := range config.HookNames() {
		h := set[name]
		f := hookField{name: name, in: newTextInput(), abort: h.OnFailure == config.OnFailureAbort, orig: h}
		f.in.Prompt = ""
		f.in.CharLimit = 1000
		f.in.Placeholder = "no command"
		f.in.SetValue(h.Command)
		f.before = f.in.Value()
		out = append(out, f)
	}
	return out
}

// hook returns the hook as edited, and false when it has no command.
func (f hookField) hook() (config.Hook, bool) {
	h := f.orig
	if v := f.in.Value(); v != f.before {
		h.Command = strings.TrimSpace(v)
	}
	if f.abort != (h.OnFailure == config.OnFailureAbort) {
		h.OnFailure = config.OnFailureWarn
		if f.abort {
			h.OnFailure = config.OnFailureAbort
		}
	}
	return h, h.Command != ""
}

// fields is the number of inputs.
func (m *setupModel) fields() int { return 2 + len(m.hooks) }

// input returns the input at index i.
func (m *setupModel) input(i int) *textinput.Model {
	switch i {
	case 0:
		return &m.src
	case 1:
		return &m.pkg
	}
	return &m.hooks[i-2].in
}

// harnessOrder is the preference order when picking a default harness.
var harnessOrder = []string{"claude", "opencode", "codex", "pi"}

// newSetup builds the first-run setup screen with detected defaults.
func newSetup(e *env) *setupModel { return buildSetup(e, false) }

// newReconfigure builds the setup screen for a config that already exists.
// The source root field starts from the saved source roots (joined with the
// OS path-list separator when there are several) and the default harness
// stays as configured.
func newReconfigure(e *env) *setupModel { return buildSetup(e, true) }

// buildSetup is the shared constructor behind newSetup and newReconfigure.
func buildSetup(e *env, reconfig bool) *setupModel {
	m := &setupModel{e: e, reconfig: reconfig, installed: map[string]bool{}, order: harnessOrder}
	m.src = newTextInput()
	m.src.Placeholder = "~/source (optional)"
	m.src.Prompt = ""
	m.src.CharLimit = 512
	if reconfig {
		m.hooks = newHookFields(e.core.Cfg.Hooks)
		m.src.SetValue(strings.Join(e.core.Cfg.SourceRoots, string(filepath.ListSeparator)))
	} else if home, err := os.UserHomeDir(); err == nil {
		if st, err := os.Stat(filepath.Join(home, "source")); err == nil && st.IsDir() {
			m.src.SetValue("~/source")
		}
	}
	m.pkg = newTextInput()
	m.pkg.Prompt = ""
	m.pkg.CharLimit = 512
	m.pkg.SetValue(e.core.Cfg.PackageRoot)
	m.src.Focus()

	m.def = e.core.Cfg.DefaultHarness
	for _, name := range m.order {
		h, ok := e.core.Cfg.Harness[name]
		if !ok {
			continue
		}
		m.installed[name] = harness.Available(h)
	}
	if !reconfig {
		for _, name := range m.order {
			if m.installed[name] {
				m.def = name
				break
			}
		}
	}
	return m
}

// capturing implements capturing: both inputs take every key.
func (m *setupModel) capturing() bool { return true }

// Init implements screen.
func (m *setupModel) Init() tea.Cmd { return textinput.Blink }

// Title implements screen.
func (m *setupModel) Title() string {
	if m.reconfig {
		return "Config"
	}
	return "Setup"
}

// Crumb implements screen.
func (m *setupModel) Crumb() string { return strings.ToLower(m.Title()) }

// Hints implements screen.
func (m *setupModel) Hints() []hint {
	esc := "Quit"
	if m.reconfig {
		esc = "Cancel"
	}
	hs := []hint{{"tab", "Complete path"}, {"↑/↓", "Switch field"}, {"enter", "Continue / save"}, {"esc", esc}}
	if m.reconfig {
		hs = append(hs, hint{"ctrl+o", "Hook: warn/abort on failure"})
	}
	return hs
}

// Update implements screen.
func (m *setupModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			if m.reconfig {
				return m, pop(m)
			}
			return m, tea.Quit
		case "shift+tab", "up":
			m.setFocus(m.focus - 1)
			m.cands = nil
			return m, nil
		case "down":
			m.setFocus(m.focus + 1)
			m.cands = nil
			return m, nil
		case "ctrl+o":
			if m.focus >= 2 {
				m.hooks[m.focus-2].abort = !m.hooks[m.focus-2].abort
			}
			return m, nil
		case "tab":
			if m.focus >= 2 {
				m.setFocus(m.focus + 1)
				return m, nil
			}
			m.complete()
			return m, nil
		case "enter":
			if m.focus == 0 {
				m.setFocus(1)
				return m, nil
			}
			return m.save()
		}
	}
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.cands = nil
	}
	var cmd tea.Cmd
	in := m.input(m.focus)
	*in, cmd = in.Update(msg)
	return m, cmd
}

// complete tab-completes the directory being typed in the focused field.
// With several source roots (joined by the path-list separator) only the
// last one is completed. Candidates are listed under the fields.
func (m *setupModel) complete() {
	in := &m.src
	if m.focus == 1 {
		in = &m.pkg
	}
	val := in.Value()
	head, seg := "", val
	if i := strings.LastIndex(val, string(filepath.ListSeparator)); i >= 0 {
		head, seg = val[:i+1], val[i+1:]
	}
	completed, cands := completePath(strings.TrimSpace(seg))
	if completed != "" {
		in.SetValue(head + completed)
		in.CursorEnd()
	}
	m.cands = cands
}

// setFocus moves focus to input i, wrapping around at either end.
func (m *setupModel) setFocus(i int) {
	m.input(m.focus).Blur()
	m.focus = (i + m.fields()) % m.fields()
	m.input(m.focus).Focus()
}

// save writes the config and continues to Home.
func (m *setupModel) save() (screen, tea.Cmd) {
	pkgRoot := strings.TrimSpace(m.pkg.Value())
	if pkgRoot == "" {
		m.err = "the package root cannot be empty"
		return m, nil
	}
	if _, err := paths.Abs(pkgRoot); err != nil {
		m.err = err.Error()
		return m, nil
	}
	cfg := m.e.core.Cfg
	cfg.PackageRoot = pkgRoot
	cfg.SourceRoots = nil
	for _, r := range filepath.SplitList(strings.TrimSpace(m.src.Value())) {
		if r = strings.TrimSpace(r); r != "" {
			cfg.SourceRoots = append(cfg.SourceRoots, r)
		}
	}
	if _, ok := cfg.Harness[m.def]; ok {
		cfg.DefaultHarness = m.def
	}
	if m.reconfig {
		cfg.Hooks = map[string]config.Hook{}
		for _, f := range m.hooks {
			if h, ok := f.hook(); ok {
				cfg.Hooks[f.name] = h
			}
		}
	}
	if err := cfg.Save(); err != nil {
		m.err = err.Error()
		return m, nil
	}
	cfg.Loaded = true
	m.e.core.Cfg = cfg
	return m, goHome("config saved")
}

// writeCands writes the tab-completion candidates (at most five, then a dim
// "… N more" line) to b. Each candidate is drawn in the cyan accent with a
// "›" marker so the list reads as something actionable rather than output.
// View calls it directly beneath the focused input so the list appears next
// to the field being completed rather than under whichever field comes last.
func (m *setupModel) writeCands(b *strings.Builder) {
	for i, c := range m.cands {
		if i >= 5 {
			b.WriteString(styleDim.Render(fmt.Sprintf("   … %d more", len(m.cands)-5)) + "\n")
			return
		}
		b.WriteString("  " + styleCyan.Render("› "+c) + "\n")
	}
}

// View implements screen.
func (m *setupModel) View(w, h int) string {
	m.src.Width, m.pkg.Width = w-8, w-8
	var b strings.Builder
	if !m.reconfig {
		b.WriteString("\n " + styleTitle.Render("Welcome to vmono") + "\n")
		b.WriteString(" " + strings.ReplaceAll(wrap("vmono groups several git repos into a package so a coding harness opened there sees all of them. A little setup first.", 70), "\n", "\n ") + "\n\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(" " + styleLabel.Render("Source root") + styleDim.Render("  scanned to fill the repo picker; optional") + "\n")
	b.WriteString(" " + m.src.View() + "\n")
	if m.focus == 0 {
		m.writeCands(&b)
	}
	b.WriteString("\n")
	b.WriteString(" " + styleLabel.Render("Package root") + styleDim.Render("  where packages are created") + "\n")
	b.WriteString(" " + m.pkg.View() + "\n")
	if m.focus == 1 {
		m.writeCands(&b)
	}
	b.WriteString("\n")
	b.WriteString(" " + styleLabel.Render("Harnesses") + "\n")
	for _, name := range m.order {
		if _, ok := m.e.core.Cfg.Harness[name]; !ok {
			continue
		}
		mark, st := "✗ not found on PATH", styleDim
		if m.installed[name] {
			mark, st = "✓ installed", styleOK
		}
		def := ""
		if name == m.def {
			def = styleSelected.Render("  (default)")
		}
		b.WriteString(fmt.Sprintf("   %-10s %s%s\n", name, st.Render(mark), def))
	}
	if len(m.hooks) > 0 {
		b.WriteString("\n " + styleLabel.Render("Hooks") + styleDim.Render("  commands run for every session; ctrl+o switches what a failure does") + "\n")
	}
	for i := range m.hooks {
		f := &m.hooks[i]
		f.in.Width = max(w-32, 10)
		mode := styleDim.Render("warn ")
		if f.abort {
			mode = styleWarn.Render("abort")
		}
		name := fmt.Sprintf("%-12s", f.name)
		if m.focus == i+2 {
			name = styleSelected.Render(name)
		}
		b.WriteString("   " + name + " " + mode + "  " + f.in.View() + "\n")
	}
	if m.err != "" {
		b.WriteString("\n " + styleErr.Render(m.err) + "\n")
	}
	return b.String()
}
