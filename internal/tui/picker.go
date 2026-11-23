package tui

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// pickSel is one repo chosen in the picker, with the mode it was given.
type pickSel struct {
	Repo discover.Repo
	Mode string
}

// reposMsg delivers the known repos.
type reposMsg struct {
	repos []discover.Repo
	err   error
}

// pickerModel is the fuzzy-filterable table of every known repo. Space
// toggles membership, a and r set active or reference, Enter returns to the
// editor, and p adds a repo by path from anywhere on disk.
type pickerModel struct {
	e      *env
	items  []discover.Repo
	chosen map[string]string // repo path -> mode
	loaded bool
	spin   spinner.Model
	tb     *table
	filter string

	done   func([]pickSel) screen
	cancel func() screen
}

// newPicker builds the picker. initial maps already-chosen repo paths to
// their modes; extra are repos that must appear even if not discovered (a
// current member from outside any source root).
func newPicker(e *env, initial map[string]string, extra []discover.Repo,
	done func([]pickSel) screen, cancel func() screen) *pickerModel {
	m := &pickerModel{e: e, chosen: map[string]string{}, spin: newSpinner(), done: done, cancel: cancel,
		tb: newTable([]column{{title: " ", max: 3}, {title: "name", max: 28}, {title: "mode", max: 5}, {title: "branch", max: 24}, {title: "path", flex: true}})}
	for k, v := range initial {
		m.chosen[k] = v
	}
	m.items = append(m.items, extra...)
	m.fill()
	return m
}

// Init implements screen. It starts the repo scan and the spinner that
// animates the placeholder until the scan reports back.
func (m *pickerModel) Init() tea.Cmd {
	c := m.e.core
	load := func() tea.Msg {
		repos, err := c.Repos()
		return reposMsg{repos: repos, err: err}
	}
	return tea.Batch(load, m.spin.Tick)
}

// Title implements screen.
func (m *pickerModel) Title() string {
	t := "Choose repos"
	if m.filter != "" {
		t += " /" + m.filter
	}
	return fmt.Sprintf("%s · %d selected", t, len(m.chosen))
}

// Crumb implements screen.
func (m *pickerModel) Crumb() string { return "repos" }

// Hints implements screen.
func (m *pickerModel) Hints() []hint {
	return []hint{
		{"space", "Toggle"}, {"a", "Active"}, {"r", "Reference"}, {"p", "Find by path"},
		{"/", "Filter"}, {"enter", "Done"}, {"esc", "Cancel"},
	}
}

// setFilter implements filterable.
func (m *pickerModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
	m.fill()
}

// filterValue implements filterable.
func (m *pickerModel) filterValue() string { return m.filter }

// fill rebuilds the table rows from the items and the current selection.
func (m *pickerModel) fill() {
	var rows []row
	for _, r := range m.items {
		mark, mode := styled("[ ]", styleDim), plain("")
		if md, ok := m.chosen[r.Path]; ok {
			mark = styled("[x]", styleOK)
			mode = styled("active", styleOK)
			if md == manifest.ModeReference {
				mode = styled("ref", styleOK)
			}
		}
		rows = append(rows, row{
			id:     r.Path,
			search: r.Name + " " + r.Path,
			cells:  []cell{mark, plain(r.Name), mode, styled(r.Branch, styleDim), styled(paths.Contract(r.Path), styleDim)},
		})
	}
	switch {
	case !m.loaded:
		m.tb.empty = scanningLabel(m.spin, "scanning…")
	case len(m.items) == 0 && len(m.e.core.Cfg.SourceRoots) == 0:
		m.tb.empty = "No repos found, and no source root is configured. Press p to find a repo by path."
	case len(m.items) == 0:
		m.tb.empty = "No repos found. Press p to find one by path."
	default:
		m.tb.empty = "No match."
	}
	m.tb.setRows(rows)
}

// addItem appends a repo unless it is already listed.
func (m *pickerModel) addItem(r discover.Repo) {
	for _, it := range m.items {
		if samePath(it.Path, r.Path) {
			return
		}
	}
	m.items = append(m.items, r)
	sort.SliceStable(m.items, func(i, j int) bool { return m.items[i].Name < m.items[j].Name })
}

// Update implements screen.
func (m *pickerModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if m.loaded {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		m.tb.empty = scanningLabel(m.spin, "scanning…")
		return m, cmd
	case reposMsg:
		m.loaded = true
		if t.err != nil {
			return m, flashErr(t.err.Error())
		}
		for _, r := range t.repos {
			m.addItem(r)
		}
		m.fill()
		return m, nil
	case repoAddedMsg:
		if t.owner != screen(m) {
			return m, nil
		}
		m.addItem(t.repo)
		m.chosen[t.repo.Path] = manifest.ModeActive
		m.fill()
		m.tb.restore(t.repo.Path)
		return m, flash("added " + t.repo.Name)
	case tea.KeyMsg:
		return m.updateKeys(t)
	}
	return m, nil
}

// updateKeys handles the main key map.
func (m *pickerModel) updateKeys(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.tb.handleKey(k.String()) {
		return m, nil
	}
	cur, has := m.tb.selected()
	switch k.String() {
	case "esc":
		if m.filter != "" {
			m.setFilter("")
			return m, nil
		}
		return m, goTo(m.cancel())
	case "enter":
		return m, goTo(m.done(m.selection()))
	case "p":
		return m, push(newFinder(m.e, m))
	case " ", "space":
		if has {
			if _, on := m.chosen[cur.id]; on {
				delete(m.chosen, cur.id)
			} else {
				m.chosen[cur.id] = manifest.ModeActive
			}
			m.fill()
		}
	case "a":
		if has {
			m.chosen[cur.id] = manifest.ModeActive
			m.fill()
		}
	case "r":
		if has {
			m.chosen[cur.id] = manifest.ModeReference
			m.fill()
		}
	}
	return m, nil
}

// selection returns the chosen repos in list order.
func (m *pickerModel) selection() []pickSel {
	var out []pickSel
	for _, r := range m.items {
		if mode, ok := m.chosen[r.Path]; ok {
			out = append(out, pickSel{Repo: r, Mode: mode})
		}
	}
	return out
}

// View implements screen.
func (m *pickerModel) View(w, h int) string { return m.tb.view(w, h) }

// samePath compares two paths after resolving symlinks.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
