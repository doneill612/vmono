package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/paths"
)

// setupModel is the first-run screen: it suggests ~/source as a source root
// when that directory exists, asks for the package root, and reports which
// known harnesses are installed.
type setupModel struct {
	e         *env
	src, pkg  textinput.Model
	focus     int
	installed map[string]bool
	order     []string
	def       string
	err       string
}

// harnessOrder is the preference order when picking a default harness.
var harnessOrder = []string{"claude", "opencode", "codex", "pi"}

// newSetup builds the setup screen with detected defaults.
func newSetup(e *env) *setupModel {
	m := &setupModel{e: e, installed: map[string]bool{}, order: harnessOrder}
	m.src = newTextInput()
	m.src.Placeholder = "~/source (optional)"
	m.src.Prompt = ""
	m.src.CharLimit = 512
	if home, err := os.UserHomeDir(); err == nil {
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
	for _, name := range m.order {
		if m.installed[name] {
			m.def = name
			break
		}
	}
	return m
}

// Init implements screen.
func (m *setupModel) Init() tea.Cmd { return textinput.Blink }

// Update implements screen.
func (m *setupModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return m, tea.Quit
		case "tab", "shift+tab", "up", "down":
			m.setFocus(1 - m.focus)
			return m, nil
		case "enter":
			if m.focus == 0 {
				m.setFocus(1)
				return m, nil
			}
			return m.save()
		}
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.src, cmd = m.src.Update(msg)
	} else {
		m.pkg, cmd = m.pkg.Update(msg)
	}
	return m, cmd
}

// setFocus moves focus between the two inputs.
func (m *setupModel) setFocus(i int) {
	m.focus = i
	if i == 0 {
		m.src.Focus()
		m.pkg.Blur()
	} else {
		m.pkg.Focus()
		m.src.Blur()
	}
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
	if s := strings.TrimSpace(m.src.Value()); s != "" {
		cfg.SourceRoots = []string{s}
	}
	if _, ok := cfg.Harness[m.def]; ok {
		cfg.DefaultHarness = m.def
	}
	if err := cfg.Save(); err != nil {
		m.err = err.Error()
		return m, nil
	}
	cfg.Loaded = true
	m.e.core.Cfg = cfg
	return m, goTo(newHome(m.e, "config saved"))
}

// View implements screen.
func (m *setupModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Welcome to vmono") + "\n\n")
	b.WriteString(wrap("vmono groups several git repos into a package so a coding harness opened there sees all of them. A little setup first.", 70) + "\n\n")
	b.WriteString(styleLabel.Render("Source root") + styleDim.Render("  scanned to fill the repo picker; optional") + "\n")
	b.WriteString(m.src.View() + "\n\n")
	b.WriteString(styleLabel.Render("Package root") + styleDim.Render("  where packages are created") + "\n")
	b.WriteString(m.pkg.View() + "\n\n")
	b.WriteString(styleLabel.Render("Harnesses") + "\n")
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
		b.WriteString(fmt.Sprintf("  %-10s %s%s\n", name, st.Render(mark), def))
	}
	if m.err != "" {
		b.WriteString("\n" + styleErr.Render(m.err) + "\n")
	}
	b.WriteString("\n" + keyHelp("tab", "next field", "enter", "continue / save", "esc", "quit"))
	return b.String()
}
