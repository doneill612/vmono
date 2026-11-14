package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

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

// pickerModel is the fuzzy-searchable list of every known repo. Space
// toggles membership, a and r set active or reference, Enter returns to the
// editor, and p adds a repo by path from anywhere on disk.
type pickerModel struct {
	e         *env
	items     []discover.Repo
	chosen    map[string]string // repo path -> mode
	loaded    bool
	filter    textinput.Model
	filtering bool
	cursor    int

	adding    bool
	pathInput textinput.Model
	cands     []string
	msg       string

	done   func([]pickSel) screen
	cancel func() screen
}

// newPicker builds the picker. initial maps already-chosen repo paths to
// their modes; extra are repos that must appear even if not discovered (a
// current member from outside any source root).
func newPicker(e *env, initial map[string]string, extra []discover.Repo,
	done func([]pickSel) screen, cancel func() screen) *pickerModel {
	f := newTextInput()
	f.Prompt = "/ "
	f.Placeholder = "fuzzy filter"
	pi := newTextInput()
	pi.Prompt = "path: "
	pi.CharLimit = 1024
	m := &pickerModel{e: e, chosen: map[string]string{}, filter: f, pathInput: pi, done: done, cancel: cancel}
	for k, v := range initial {
		m.chosen[k] = v
	}
	m.items = append(m.items, extra...)
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

// visible returns repos matching the filter.
func (m *pickerModel) visible() []discover.Repo {
	q := strings.TrimSpace(m.filter.Value())
	if q == "" {
		return m.items
	}
	names := make([]string, len(m.items))
	for i, r := range m.items {
		names[i] = r.Name + " " + r.Path
	}
	var out []discover.Repo
	for _, match := range fuzzy.Find(q, names) {
		out = append(out, m.items[match.Index])
	}
	return out
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
			m.msg = styleErr.Render(t.err.Error())
			return m, nil
		}
		for _, r := range t.repos {
			m.addItem(r)
		}
		return m, nil
	case tea.KeyMsg:
		switch {
		case m.adding:
			return m.updateAdding(t)
		case m.filtering:
			return m.updateFilter(t)
		}
		return m.updateKeys(t)
	}
	return m, nil
}

// updateFilter handles keys while filtering.
func (m *pickerModel) updateFilter(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.filtering = false
		m.filter.SetValue("")
		m.filter.Blur()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	m.cursor = 0
	return m, cmd
}

// updateAdding handles keys in the add-by-path prompt, including tab
// completion of directories.
func (m *pickerModel) updateAdding(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.adding, m.cands, m.msg = false, nil, ""
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
			m.msg = styleErr.Render(err.Error())
			return m, nil
		}
		m.addItem(repo)
		m.chosen[repo.Path] = manifest.ModeActive
		m.adding, m.cands, m.msg = false, nil, "added "+repo.Name
		m.pathInput.Blur()
		m.pathInput.SetValue("")
		m.cursor = m.indexOf(repo.Path)
		return m, nil
	}
	var cmd tea.Cmd
	m.pathInput, cmd = m.pathInput.Update(k)
	m.cands = nil
	return m, cmd
}

// indexOf returns the visible index of a repo path, or 0.
func (m *pickerModel) indexOf(path string) int {
	for i, r := range m.visible() {
		if samePath(r.Path, path) {
			return i
		}
	}
	return 0
}

// updateKeys handles the main key map.
func (m *pickerModel) updateKeys(k tea.KeyMsg) (screen, tea.Cmd) {
	v := m.visible()
	var cur *discover.Repo
	if m.cursor >= 0 && m.cursor < len(v) {
		cur = &v[m.cursor]
	}
	m.msg = ""
	switch k.String() {
	case "esc":
		return m, goTo(m.cancel())
	case "enter":
		return m, goTo(m.done(m.selection()))
	case "/":
		m.filtering = true
		return m, m.filter.Focus()
	case "p":
		m.adding = true
		return m, m.pathInput.Focus()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(v)-1 {
			m.cursor++
		}
	case " ", "space":
		if cur != nil {
			if _, on := m.chosen[cur.Path]; on {
				delete(m.chosen, cur.Path)
			} else {
				m.chosen[cur.Path] = manifest.ModeActive
			}
		}
	case "a":
		if cur != nil {
			m.chosen[cur.Path] = manifest.ModeActive
		}
	case "r":
		if cur != nil {
			m.chosen[cur.Path] = manifest.ModeReference
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
func (m *pickerModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Choose repos") + styleDim.Render(fmt.Sprintf("  %d selected", len(m.chosen))) + "\n\n")
	v := m.visible()
	switch {
	case !m.loaded:
		b.WriteString(styleDim.Render("scanning…") + "\n")
	case len(v) == 0:
		b.WriteString(styleDim.Render("No repos found. Press p to add one by path, or set a source root in the config.") + "\n")
	}
	height := m.e.h - 10
	if height < 5 {
		height = 15
	}
	start := 0
	if m.cursor >= height {
		start = m.cursor - height + 1
	}
	for i := start; i < len(v) && i-start < height; i++ {
		r := v[i]
		mark, mode := "[ ]", ""
		if md, ok := m.chosen[r.Path]; ok {
			mark = styleOK.Render("[x]")
			mode = md
			if md == manifest.ModeReference {
				mode = "ref"
			}
		}
		cursor := "  "
		if i == m.cursor {
			cursor = styleSelected.Render("▸ ")
		}
		branch := ""
		if r.Branch != "" {
			branch = " " + r.Branch
		}
		b.WriteString(fmt.Sprintf("%s%s %-22s %-7s %s\n", cursor, mark, truncate(r.Name, 22), mode,
			styleDim.Render(truncate(paths.Contract(r.Path), 40)+branch)))
	}
	b.WriteString("\n")
	if m.filtering || m.filter.Value() != "" {
		b.WriteString(m.filter.View() + "\n")
	}
	if m.adding {
		b.WriteString(m.pathInput.View() + "\n")
		for i, c := range m.cands {
			if i >= 6 {
				b.WriteString(styleDim.Render(fmt.Sprintf("  … %d more\n", len(m.cands)-6)))
				break
			}
			b.WriteString(styleDim.Render("  "+c) + "\n")
		}
		b.WriteString(keyHelp("tab", "complete", "enter", "add", "esc", "cancel"))
		if m.msg != "" {
			b.WriteString("\n" + m.msg)
		}
		return b.String()
	}
	if m.msg != "" {
		b.WriteString(m.msg + "\n")
	}
	b.WriteString(keyHelp("space", "toggle", "a", "active", "r", "reference", "p", "add by path", "/", "filter", "enter", "done", "esc", "cancel"))
	return b.String()
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
