package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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
	tb     *table
	filter string

	adding    bool
	pathInput textinput.Model
	cands     []string

	done   func([]pickSel) screen
	cancel func() screen
}

// newPicker builds the picker. initial maps already-chosen repo paths to
// their modes; extra are repos that must appear even if not discovered (a
// current member from outside any source root).
func newPicker(e *env, initial map[string]string, extra []discover.Repo,
	done func([]pickSel) screen, cancel func() screen) *pickerModel {
	pi := newTextInput()
	pi.Prompt = "path: "
	pi.CharLimit = 1024
	m := &pickerModel{e: e, chosen: map[string]string{}, pathInput: pi, done: done, cancel: cancel,
		tb: newTable([]column{{title: " ", max: 3}, {title: "name", max: 28}, {title: "mode", max: 5}, {title: "branch", max: 24}, {title: "path", flex: true}})}
	for k, v := range initial {
		m.chosen[k] = v
	}
	m.items = append(m.items, extra...)
	m.fill()
	return m
}

// Init implements screen.
func (m *pickerModel) Init() tea.Cmd {
	c := m.e.core
	return func() tea.Msg {
		repos, err := c.Repos()
		return reposMsg{repos: repos, err: err}
	}
}

// Title implements screen.
func (m *pickerModel) Title() string {
	t := "repos(select)"
	if m.filter != "" {
		t += "(/" + m.filter + ")"
	}
	return fmt.Sprintf("%s[%d selected]", t, len(m.chosen))
}

// Crumb implements screen.
func (m *pickerModel) Crumb() string { return "repos" }

// Hints implements screen.
func (m *pickerModel) Hints() []hint {
	return []hint{
		{"space", "Toggle"}, {"a", "Active"}, {"r", "Reference"}, {"p", "Add by path"},
		{"/", "Filter"}, {"enter", "Done"}, {"esc", "Cancel"},
	}
}

// capturing implements capturing: the path prompt owns the keyboard.
func (m *pickerModel) capturing() bool { return m.adding }

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
			mode = plain("active")
			if md == manifest.ModeReference {
				mode = plain("ref")
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
		m.tb.empty = "scanning…"
	case len(m.items) == 0 && len(m.e.core.Cfg.SourceRoots) == 0:
		m.tb.empty = "No repos found, and no source root is configured. Press p to add a repo by path."
	case len(m.items) == 0:
		m.tb.empty = "No repos found. Press p to add one by path."
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
	case tea.KeyMsg:
		if m.adding {
			return m.updateAdding(t)
		}
		return m.updateKeys(t)
	}
	return m, nil
}

// updateAdding handles keys in the add-by-path prompt, including tab
// completion of directories.
func (m *pickerModel) updateAdding(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.adding, m.cands = false, nil
		m.pathInput.Blur()
		return m, nil
	case "tab":
		completed, cands := completePath(m.pathInput.Value())
		if completed != "" {
			m.pathInput.SetValue(completed)
			m.pathInput.CursorEnd()
		}
		m.cands = cands
		return m, nil
	case "enter":
		repo, err := m.e.core.ResolveRepo(strings.TrimSpace(m.pathInput.Value()))
		if err != nil {
			return m, flashErr(err.Error())
		}
		m.addItem(repo)
		m.chosen[repo.Path] = manifest.ModeActive
		m.adding, m.cands = false, nil
		m.pathInput.Blur()
		m.pathInput.SetValue("")
		m.fill()
		m.tb.restore(repo.Path)
		return m, flash("added " + repo.Name)
	}
	var cmd tea.Cmd
	m.pathInput, cmd = m.pathInput.Update(k)
	m.cands = nil
	return m, cmd
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
		m.adding = true
		return m, m.pathInput.Focus()
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
func (m *pickerModel) View(w, h int) string {
	if !m.adding {
		return m.tb.view(w, h)
	}
	var extra []string
	extra = append(extra, " "+m.pathInput.View())
	for i, c := range m.cands {
		if i >= 6 {
			extra = append(extra, styleDim.Render(fmt.Sprintf("   … %d more", len(m.cands)-6)))
			break
		}
		extra = append(extra, styleDim.Render("   "+c))
	}
	extra = append(extra, " "+keyHelp("tab", "complete", "enter", "add", "esc", "cancel"))
	return m.tb.view(w, h-len(extra)-1) + "\n\n" + strings.Join(extra, "\n")
}

// samePath compares two paths after resolving symlinks.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// completePath completes input against directories on disk. It returns the
// longest common extension of the matches (or input unchanged) and the
// candidate directories, each with a trailing separator. A leading "~" is
// expanded for lookup and kept in the result.
func completePath(input string) (string, []string) {
	if input == "" {
		return "", nil
	}
	home, _ := os.UserHomeDir()
	sep := string(os.PathSeparator)
	real := input
	tilde := false
	switch {
	case input == "~":
		real, tilde = home+sep, true
	case strings.HasPrefix(input, "~/") || strings.HasPrefix(input, `~\`):
		real, tilde = home+sep+input[2:], true
	}
	dir, prefix := filepath.Split(real)
	if dir == "" {
		dir = "." + sep
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return input, nil
	}
	var cands []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !info.IsDir() {
			continue
		}
		full := filepath.Join(dir, name) + sep
		if dir == "."+sep && !strings.HasPrefix(input, "."+sep) {
			full = name + sep
		}
		if tilde && home != "" && strings.HasPrefix(full, home) {
			full = "~" + strings.TrimPrefix(full, home)
		}
		cands = append(cands, full)
	}
	sort.Strings(cands)
	switch len(cands) {
	case 0:
		return input, nil
	case 1:
		return cands[0], cands
	}
	common := cands[0]
	for _, c := range cands[1:] {
		for !strings.HasPrefix(c, common) {
			common = common[:len(common)-1]
		}
	}
	if len(common) < len(input) {
		common = input
	}
	return common, cands
}
