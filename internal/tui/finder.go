package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/paths"
)

// repoAddedMsg tells owner that the finder resolved a repo. The repo is
// already remembered in the index. The App closes the finder before
// delivering it.
type repoAddedMsg struct {
	owner screen
	repo  discover.Repo
}

// finderScanMsg delivers the repos found beneath a directory.
type finderScanMsg struct {
	dir   string
	repos []string
}

// suggestion is one line of the finder's list: a directory, or a git repo.
// nested marks a repo found by scanning below the typed directory rather
// than being one of its direct children.
type suggestion struct {
	name   string
	path   string
	repo   bool
	nested bool
}

// maxSuggestions caps the list; the dialog shows a window of it.
const maxSuggestions = 200

// finderModel is the dialog for finding a repo that is not already listed:
// a path prompt with live suggestions. It lists the directories under what
// is typed, marks the git repos among them, and scans below a typed
// directory for repos nested deeper, so it works with no source root
// configured. Tab completes, enter picks a repo or steps into a directory.
type finderModel struct {
	e      *env
	owner  screen
	in     textinput.Model
	sugg   []suggestion
	cursor int
	// scans caches repos found below a directory; pending marks scans
	// still running.
	scans   map[string][]string
	pending map[string]bool
	dir     string
	// spin animates the scanning line; ticking is true while a tick chain
	// is in flight so a second scan does not start another.
	spin    spinner.Model
	ticking bool
}

// newFinder builds the finder for owner, starting in the first source root
// or the home directory.
func newFinder(e *env, owner screen) *finderModel {
	in := newTextInput()
	in.Prompt = "path: "
	in.CharLimit = 1024
	start := "~/"
	if roots, err := e.core.Cfg.SourceRootPaths(); err == nil && len(roots) > 0 {
		start = paths.Contract(roots[0]) + string(os.PathSeparator)
	}
	in.SetValue(start)
	in.CursorEnd()
	return &finderModel{e: e, owner: owner, in: in, scans: map[string][]string{}, pending: map[string]bool{}, spin: newSpinner()}
}

// modal implements modalScreen.
func (m *finderModel) modal() {}

// capturing implements capturing: the path prompt owns the keyboard.
func (m *finderModel) capturing() bool { return true }

// Init implements screen.
func (m *finderModel) Init() tea.Cmd {
	return tea.Batch(m.in.Focus(), m.recompute())
}

// Title implements screen.
func (m *finderModel) Title() string { return "find repo" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *finderModel) Crumb() string { return "" }

// Hints implements screen.
func (m *finderModel) Hints() []hint { return nil }

// isRepoDir reports whether dir is the root of a git repo (a .git
// directory; worktree checkouts, whose .git is a file, do not count).
func isRepoDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.IsDir()
}

// splitInput expands a leading "~" and splits what was typed into the
// directory to list and the name prefix typed after it.
func splitInput(input string) (dir, prefix string) {
	real := input
	if e, err := paths.Expand(input); err == nil && (input == "~" || strings.HasPrefix(input, "~/") || strings.HasPrefix(input, `~\`)) {
		real = e
		if strings.HasSuffix(input, "/") || strings.HasSuffix(input, `\`) || input == "~" {
			real += string(os.PathSeparator)
		}
	}
	dir, prefix = filepath.Split(real)
	if dir == "" {
		dir = "." + string(os.PathSeparator)
	}
	return dir, prefix
}

// recompute rebuilds the suggestion list for the current input and returns
// a command to scan below the typed directory when that is not cached.
func (m *finderModel) recompute() tea.Cmd {
	dir, prefix := splitInput(m.in.Value())
	m.dir = filepath.Clean(dir)
	m.sugg, m.cursor = nil, 0

	type ranked struct {
		s    suggestion
		rank int
	}
	var list []ranked
	if entries, err := os.ReadDir(dir); err == nil {
		names := make([]string, 0, len(entries))
		byName := map[string]os.DirEntry{}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if st, err := os.Stat(full); err != nil || !st.IsDir() {
				continue
			}
			if strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(prefix, ".") {
				continue
			}
			names = append(names, e.Name())
			byName[e.Name()] = e
		}
		lp := strings.ToLower(prefix)
		matched := map[string]int{}
		for _, n := range names {
			if strings.HasPrefix(strings.ToLower(n), lp) {
				matched[n] = 0
			}
		}
		if prefix != "" {
			for _, f := range fuzzy.Find(prefix, names) {
				if _, ok := matched[f.Str]; !ok {
					matched[f.Str] = 1
				}
			}
		}
		for n, rank := range matched {
			full := filepath.Join(dir, n)
			list = append(list, ranked{suggestion{name: n, path: full, repo: isRepoDir(full)}, rank})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.s.repo != b.s.repo {
			return a.s.repo
		}
		return a.s.name < b.s.name
	})
	for _, r := range list {
		m.sugg = append(m.sugg, r.s)
	}

	var cmd tea.Cmd
	if prefix == "" {
		for _, p := range m.scans[m.dir] {
			if filepath.Dir(p) != m.dir {
				m.sugg = append(m.sugg, suggestion{name: relTo(m.dir, p), path: p, repo: true, nested: true})
			}
		}
		if _, done := m.scans[m.dir]; !done && !m.pending[m.dir] && filepath.Dir(m.dir) != m.dir {
			m.pending[m.dir] = true
			cmd = m.scan(m.dir)
			if !m.ticking {
				m.ticking = true
				cmd = tea.Batch(cmd, m.spin.Tick)
			}
		}
	}
	if len(m.sugg) > maxSuggestions {
		m.sugg = m.sugg[:maxSuggestions]
	}
	return cmd
}

// relTo is p relative to dir, for display.
func relTo(dir, p string) string {
	if r, err := filepath.Rel(dir, p); err == nil {
		return r
	}
	return p
}

// scan looks for repos below dir without blocking the UI.
func (m *finderModel) scan(dir string) tea.Cmd {
	depth := m.e.core.Cfg.ScanDepth
	if depth < 2 {
		depth = 2
	}
	var skip []string
	if root, err := m.e.core.PackageRoot(); err == nil {
		skip = append(skip, root)
	}
	return func() tea.Msg {
		return finderScanMsg{dir: dir, repos: discover.Scan([]string{dir}, depth, skip)}
	}
}

// Update implements screen.
func (m *finderModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if len(m.pending) == 0 {
			m.ticking = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		return m, cmd
	case finderScanMsg:
		delete(m.pending, t.dir)
		m.scans[t.dir] = t.repos
		if t.dir == m.dir {
			cur := m.cursor
			cmd := m.recompute()
			if cur < len(m.sugg) {
				m.cursor = cur
			}
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// key handles keys in the finder.
func (m *finderModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		return m, pop()
	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.cursor < len(m.sugg)-1 {
			m.cursor++
		}
		return m, nil
	case "tab":
		if m.cursor < len(m.sugg) {
			m.accept(m.sugg[m.cursor])
			return m, m.recompute()
		}
		return m, nil
	case "enter":
		return m.enter()
	}
	var cmd tea.Cmd
	m.in, cmd = m.in.Update(k)
	return m, tea.Batch(cmd, m.recompute())
}

// accept puts a suggestion into the input: a directory gets a trailing
// separator so the list steps into it.
func (m *finderModel) accept(s suggestion) {
	v := paths.Contract(s.path)
	if !s.repo {
		v += string(os.PathSeparator)
	}
	m.in.SetValue(v)
	m.in.CursorEnd()
}

// enter picks a repo or steps into a directory. A typed path that is itself
// a repo wins over the highlighted suggestion; with nothing to choose, the
// text is resolved as a repo name or path.
func (m *finderModel) enter() (screen, tea.Cmd) {
	typed := strings.TrimSpace(m.in.Value())
	if abs, err := paths.Abs(typed); err == nil && typed != "" && !strings.HasSuffix(typed, "/") && isRepoDir(abs) {
		return m.pick(abs)
	}
	if m.cursor < len(m.sugg) {
		s := m.sugg[m.cursor]
		if s.repo {
			return m.pick(s.path)
		}
		m.accept(s)
		return m, m.recompute()
	}
	if typed == "" {
		return m, nil
	}
	return m.pick(typed)
}

// pick resolves a repo, remembers it, and hands it to the owner.
func (m *finderModel) pick(arg string) (screen, tea.Cmd) {
	if abs, err := paths.Abs(arg); err == nil {
		arg = abs
	}
	repo, err := m.e.core.ResolveRepo(arg)
	if err != nil {
		return m, flashErr(err.Error())
	}
	owner := m.owner
	return m, func() tea.Msg { return repoAddedMsg{owner: owner, repo: repo} }
}

// View implements screen.
func (m *finderModel) View(w, h int) string {
	width := w - 6
	if width > 92 {
		width = 92
	}
	if width < 36 {
		width = 36
	}
	rows := h - 10
	if rows > 12 {
		rows = 12
	}
	if rows < 3 {
		rows = 3
	}
	inner := width - 2
	m.in.Width = inner - 10

	var b strings.Builder
	b.WriteString(" " + styleDim.Render("Type a path. Repos are marked ●; tab completes, enter picks.") + "\n")
	b.WriteString(" " + m.in.View() + "\n\n")

	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	if len(m.sugg) == 0 {
		b.WriteString("   " + styleDim.Render("no matching directories") + "\n")
	}
	for i := start; i < len(m.sugg) && i-start < rows; i++ {
		s := m.sugg[i]
		mark, name, style := "▸", s.name+string(os.PathSeparator), styleDim
		if s.repo {
			mark, name, style = "●", s.name, styleOK
		}
		line := "  " + style.Render(mark) + " " + truncate(name, inner-6)
		if s.nested {
			line += styleDim.Render("  (below)")
		}
		if i == m.cursor {
			line = styleSelRow.Width(inner).Render("  " + mark + " " + truncate(name, inner-6))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	switch {
	case m.pending[m.dir]:
		b.WriteString(" " + styleDim.Render(scanningLabel(m.spin, "scanning "+paths.Contract(m.dir)+" for repos…")) + "\n")
	case len(m.sugg) > rows:
		b.WriteString(" " + styleDim.Render(fmt.Sprintf("%d matches; keep typing to narrow", len(m.sugg))) + "\n")
	default:
		b.WriteString("\n")
	}
	b.WriteString(" " + keyHelp("enter", "pick / open", "tab", "complete", "↑/↓", "choose", "esc", "cancel"))
	return dialog("find repo", b.String(), width)
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
