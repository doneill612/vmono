package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/harness"
)

// harnessPicker lists configured harnesses, marking any whose command is
// not on PATH. Enter launches the highlighted one. It serves both the `l`
// key on Home and the "Launch now?" step that ends create, edit and sync,
// where the package's default harness is preselected.
type harnessPicker struct {
	e      *env
	pkg    string
	title  string
	names  []string
	cursor int
	def    string
	msg    string
}

// newLaunchPicker builds a picker for launching into pkg, with the
// package's default harness (or the global default) preselected.
func newLaunchPicker(e *env, pkg, title string) *harnessPicker {
	p := &harnessPicker{e: e, pkg: pkg, title: title, names: e.core.Cfg.HarnessNames()}
	pkgHarness := ""
	if loaded, err := e.core.Load(pkg); err == nil {
		pkgHarness = loaded.M.Harness
	}
	if name, _, err := e.core.Cfg.ResolveHarness("", pkgHarness); err == nil {
		p.def = name
	}
	for i, n := range p.names {
		if n == p.def {
			p.cursor = i
		}
	}
	return p
}

// modal implements modalScreen.
func (p *harnessPicker) modal() {}

// Init implements screen.
func (p *harnessPicker) Init() tea.Cmd { return nil }

// Title implements screen.
func (p *harnessPicker) Title() string { return "launch" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (p *harnessPicker) Crumb() string { return "" }

// Hints implements screen.
func (p *harnessPicker) Hints() []hint { return nil }

// Update implements screen.
func (p *harnessPicker) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "esc", "q", "n":
		return p, pop()
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.names)-1 {
			p.cursor++
		}
	case "enter":
		if len(p.names) == 0 {
			return p, nil
		}
		name := p.names[p.cursor]
		h := p.e.core.Cfg.Harness[name]
		if !harness.Available(h) {
			p.msg = fmt.Sprintf("%q is not on PATH; install it or pick another", h.Command)
			return p, nil
		}
		return p, func() tea.Msg { return launchMsg{LaunchRequest{Package: p.pkg, Harness: name}} }
	}
	return p, nil
}

// View implements screen.
func (p *harnessPicker) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleTitle.Render(p.title) + styleDim.Render("  "+p.pkg) + "\n\n")
	for i, name := range p.names {
		h := p.e.core.Cfg.Harness[name]
		cursor := "  "
		if i == p.cursor {
			cursor = styleSelected.Render("▸ ")
		}
		note := ""
		if !harness.Available(h) {
			note = styleWarn.Render("  not found on PATH")
		}
		def := ""
		if name == p.def {
			def = styleDim.Render("  default")
		}
		b.WriteString(fmt.Sprintf(" %s%-12s %s%s%s\n", cursor, name, styleDim.Render(h.Command), def, note))
	}
	if p.msg != "" {
		b.WriteString("\n " + styleWarn.Render(p.msg) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "launch", "↑/↓", "choose", "esc", "not now"))
	return dialog("launch", b.String(), w-4)
}
