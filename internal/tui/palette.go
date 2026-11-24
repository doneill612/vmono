package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// Messages the palette emits for the App (or the screen beneath it) to act on.
type (
	// navigateMsg switches to a top-level view from anywhere; see App.navigate.
	// The screen is built only when the entry is chosen.
	navigateMsg struct{ build func() screen }
	// sortMsg sorts the top screen's table by a column.
	sortMsg struct{ col int }
	// reloadMsg reloads the top screen.
	reloadMsg struct{}
)

// tabular is implemented by list screens, so the palette can offer to sort
// them.
type tabular interface{ tbl() *table }

// rowContext describes the row a list screen stands on: what kind of thing it
// is, its id in that screen, the name to show, and what can be done to it.
type rowContext struct {
	kind, subject, label string
	items                []action
}

// rowActor is implemented by list screens whose highlighted row has actions.
// The palette lists them, so every action the row's menu offers is also
// reachable by typing its name.
type rowActor interface{ rowActions() (rowContext, bool) }

// paletteEntry is one line of the palette. msg is what choosing it sends once
// the palette has closed. group says what kind of entry it is and is searched
// along with the label; key is the shortcut that does the same thing.
type paletteEntry struct {
	label string
	group string
	key   string
	msg   tea.Msg
}

// palette is the command palette opened by ":" or ctrl+p: one fuzzy-filtered
// list of everything that can be done from here (go to a view, act on the
// highlighted row, sort, reload), so none of it has to be remembered as a key.
type palette struct {
	in     textinput.Model
	all    []paletteEntry
	shown  []paletteEntry
	cursor int
	offset int
	rows   int
	// closing is set once an entry is chosen, so keys queued behind the
	// choosing Enter cannot run a second entry before the pop is applied.
	closing bool
}

// newPalette builds the palette over a list of entries.
func newPalette(entries []paletteEntry) *palette {
	in := newTextInput()
	in.Prompt = "› "
	in.CharLimit = 128
	p := &palette{in: in, all: entries, shown: entries, rows: 10}
	return p
}

// modal implements modalScreen.
func (p *palette) modal() {}

// capturing implements capturing: the input owns every key.
func (p *palette) capturing() bool { return true }

// Init implements screen.
func (p *palette) Init() tea.Cmd { return p.in.Focus() }

// Title implements screen.
func (p *palette) Title() string { return "palette" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (p *palette) Crumb() string { return "" }

// Hints implements screen.
func (p *palette) Hints() []hint { return nil }

// refilter narrows the entries to those matching the query, best match first.
func (p *palette) refilter() {
	q := strings.TrimSpace(p.in.Value())
	p.cursor, p.offset = 0, 0
	if q == "" {
		p.shown = p.all
		return
	}
	hay := make([]string, len(p.all))
	for i, e := range p.all {
		hay[i] = e.label + " " + e.group
	}
	matches := fuzzy.Find(q, hay)
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	p.shown = p.shown[:0:0]
	for _, m := range matches {
		p.shown = append(p.shown, p.all[m.Index])
	}
}

// Update implements screen.
func (p *palette) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok || p.closing {
		return p, nil
	}
	switch k.String() {
	case "esc", "ctrl+p":
		return p, pop(p)
	case "up", "ctrl+k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "ctrl+j":
		if p.cursor < len(p.shown)-1 {
			p.cursor++
		}
	case "enter":
		if p.cursor < len(p.shown) {
			e := p.shown[p.cursor]
			p.closing = true
			return p, tea.Sequence(pop(p), func() tea.Msg { return e.msg })
		}
	default:
		var cmd tea.Cmd
		before := p.in.Value()
		p.in, cmd = p.in.Update(k)
		if p.in.Value() != before {
			p.refilter()
		}
		return p, cmd
	}
	return p, nil
}

// View implements screen.
func (p *palette) View(w, h int) string {
	width := min(64, w-4)
	inner := max(width-3, 10)
	rows := max(min(p.rows, h-8), 3)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}

	var b strings.Builder
	b.WriteString(" " + clip(p.in.View(), inner-1) + "\n\n")
	if len(p.shown) == 0 {
		b.WriteString(" " + styleDim.Render("no match") + "\n")
	}
	for i := p.offset; i < len(p.shown) && i < p.offset+rows; i++ {
		e := p.shown[i]
		right := e.group
		if e.key != "" {
			right = e.key
		}
		rw := lipgloss.Width(right)
		label := fit(e.label, max(inner-rw-6, 4), false)
		gap := strings.Repeat(" ", max(inner-4-lipgloss.Width(label)-rw, 1))
		cursor := "  "
		if i == p.cursor {
			cursor = styleSelected.Render("▸ ")
			label = styleSelected.Render(label)
		}
		b.WriteString(" " + cursor + label + gap + styleDim.Render(right) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "run", "↑/↓", "move", "esc", "close"))
	return dialog("go to…", b.String(), width)
}

// paletteEntries lists everything the palette offers from the screen on top:
// the actions of its highlighted row first, then the views, one entry per
// package's sessions, sorting, and the app-wide commands.
func (a *App) paletteEntries() []paletteEntry {
	var out []paletteEntry
	top := a.top()
	if ra, ok := top.(rowActor); ok {
		if rc, ok := ra.rowActions(); ok {
			group := rc.kind + " " + rc.label
			for _, it := range rc.items {
				out = append(out, paletteEntry{
					label: it.label, group: group, key: keyLabel(it.key),
					msg: actionMsg{owner: top, subject: rc.subject, id: it.id},
				})
			}
		}
	}
	e := a.env
	out = append(out,
		paletteEntry{label: "Packages", group: "go to", msg: homeMsg{}},
		paletteEntry{label: "Repos", group: "go to", msg: navigateMsg{func() screen { return newRepos(e) }}},
		paletteEntry{label: "Harnesses", group: "go to", msg: navigateMsg{func() screen { return newHarnesses(e) }}},
		paletteEntry{label: "Settings", group: "go to", msg: navigateMsg{func() screen { return newReconfigure(e) }}},
		paletteEntry{label: "New package", group: "go to", key: "n", msg: navigateMsg{func() screen { return newEditor(e, nil) }}},
	)
	names := append([]string(nil), e.pkgNames...)
	sort.Strings(names)
	for _, n := range names {
		n := n
		out = append(out, paletteEntry{label: "Sessions of " + n, group: "go to", msg: navigateMsg{func() screen { return newSessions(e, n) }}})
	}
	if t, ok := top.(tabular); ok {
		for i, c := range t.tbl().cols {
			out = append(out, paletteEntry{label: "Sort by " + c.title, group: "view", msg: sortMsg{col: i}})
		}
	}
	return append(out,
		paletteEntry{label: "Reload", group: "view", key: "ctrl+r", msg: reloadMsg{}},
		paletteEntry{label: "Keyboard shortcuts", group: "view", key: "?", msg: pushMsg{from: top, s: newHelp(top.Hints())}},
		paletteEntry{label: "Quit", group: "app", key: "q", msg: tea.QuitMsg{}},
	)
}
