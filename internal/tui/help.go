package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// helpModel is the key reference dialog opened with "?". It lists the keys
// of the screen underneath, then the keys every screen shares.
type helpModel struct {
	view []hint
}

// newHelp builds the dialog for a screen's hints.
func newHelp(view []hint) *helpModel { return &helpModel{view: view} }

// globalHints are the keys that work on every screen.
var globalHints = []hint{
	{":", "Command palette: go to a view, act on the selection, sort"},
	{"/", "Filter the current table"},
	{"?", "This help"},
	{"ctrl+r", "Reload the current view"},
	{"esc", "Back, or clear the filter"},
	{"ctrl+c", "Quit"},
}

// navHints are the table navigation keys.
var navHints = []hint{
	{"j/k ↑/↓", "Move the selection"},
	{"g/G", "Jump to the top / bottom"},
	{"pgup/pgdn", "Move a page"},
}

// modal implements modalScreen.
func (h *helpModel) modal() {}

// Init implements screen.
func (h *helpModel) Init() tea.Cmd { return nil }

// Update implements screen.
func (h *helpModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q", "?", "enter":
			return h, pop()
		}
	}
	return h, nil
}

// Title implements screen.
func (h *helpModel) Title() string { return "help" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (h *helpModel) Crumb() string { return "" }

// Hints implements screen. The header keeps showing the screen beneath.
func (h *helpModel) Hints() []hint { return nil }

// View implements screen.
func (h *helpModel) View(w, ht int) string {
	width := w - 8
	if width > 76 {
		width = 76
	}
	if width < 30 {
		width = 30
	}
	var b strings.Builder
	section := func(name string, hs []hint) {
		if len(hs) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(" " + styleHeaderCol.Render(strings.ToUpper(name)) + "\n")
		for _, x := range hs {
			b.WriteString("  " + styleKey.Render(fit(x.key, 11, false)) + " " + truncate(x.label, width-17) + "\n")
		}
	}
	section("View", h.view)
	section("Global", globalHints)
	section("Navigation", navHints)
	body := strings.TrimRight(b.String(), "\n")
	return dialog("help", body, width)
}
