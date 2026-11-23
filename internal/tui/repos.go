package tui

import (
	"fmt"
	"path/filepath"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// repoListMsg delivers every known repo and how many packages use each.
type repoListMsg struct {
	repos []discover.Repo
	used  map[string]int
	err   error
}

// repos table columns.
const (
	repoColName = iota
	repoColBranch
	repoColRemote
	repoColPath
	repoColUsed
)

// canonPath normalizes a path for comparing repos: symlinks resolved when
// possible, otherwise cleaned.
func canonPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// repoActions lists everything the repos view can do to one repo. Each key is
// also the view's own shortcut for the action.
var repoActions = []action{
	{id: "packages", label: "Packages using it", key: "right"},
	{id: "new-package", label: "New package", key: "n"},
	{id: "copy", label: "Copy path", key: "y"},
}

// reposModel lists every repo vmono knows (source roots, configured repos,
// and ones added by path) with how many packages use each. Opening a repo
// shows those packages; "a" finds repos that are not listed.
type reposModel struct {
	e      *env
	repos  []discover.Repo
	used   map[string]int
	loaded bool
	spin   spinner.Model
	tb     *table
	filter string
}

// newRepos builds the repos view.
func newRepos(e *env) *reposModel {
	return &reposModel{e: e, spin: newSpinner(), tb: newTable([]column{
		{title: "name", max: 28},
		{title: "branch", max: 24},
		{title: "remote", flex: true},
		{title: "path", flex: true},
		{title: "used by", right: true},
	})}
}

// Init implements screen. It starts the repo scan and the spinner that
// animates the placeholder until the scan reports back.
func (m *reposModel) Init() tea.Cmd { return tea.Batch(m.load(), m.spin.Tick) }

// load lists repos and package usage off the UI goroutine.
func (m *reposModel) load() tea.Cmd {
	c := m.e.core
	return func() tea.Msg {
		repos, err := c.Repos()
		if err != nil {
			return repoListMsg{err: err}
		}
		pkgs, err := c.List(false)
		if err != nil {
			return repoListMsg{err: err}
		}
		used := map[string]int{}
		for _, p := range pkgs {
			seen := map[string]bool{}
			for _, src := range p.Sources {
				abs, err := paths.Abs(src)
				if err != nil {
					continue
				}
				k := canonPath(abs)
				if !seen[k] {
					seen[k] = true
					used[k]++
				}
			}
		}
		return repoListMsg{repos: repos, used: used}
	}
}

// Title implements screen.
func (m *reposModel) Title() string { return frameTitle("repos", "", m.filter, len(m.tb.rows)) }

// Crumb implements screen.
func (m *reposModel) Crumb() string { return "repos" }

// Hints implements screen.
func (m *reposModel) Hints() []hint {
	return []hint{
		{"enter", "Actions"}, {"→", "Packages"}, {"n", "New package"}, {"a", "Find repo"}, {"y", "Copy path"},
		{"N", "Sort name"}, {"U", "Sort used by"}, {"esc", "Back"},
	}
}

// setFilter implements filterable.
func (m *reposModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
}

// filterValue implements filterable.
func (m *reposModel) filterValue() string { return m.filter }

// fill rebuilds the rows.
func (m *reposModel) fill() {
	var rows []row
	for _, r := range m.repos {
		n := m.used[canonPath(r.Path)]
		usedCell := keyed(fmt.Sprint(n), numKey(n), styleDim)
		if n > 0 {
			usedCell = keyed(fmt.Sprint(n), numKey(n), styleOK)
		}
		rows = append(rows, row{
			id:     r.Path,
			search: r.Name + " " + r.Path + " " + r.Remote,
			cells: []cell{
				plain(r.Name), styled(r.Branch, styleDim), styled(r.Remote, styleDim),
				styled(paths.Contract(r.Path), styleDim), usedCell,
			},
		})
	}
	switch {
	case !m.loaded:
		m.tb.empty = scanningLabel(m.spin, "scanning…")
	case len(m.repos) == 0 && len(m.e.core.Cfg.SourceRoots) == 0:
		m.tb.empty = "No repos known, and no source root is configured. Press a to find a repo by path, or c to set a source root."
	case len(m.repos) == 0:
		m.tb.empty = "No repos found. Press a to find one by path."
	default:
		m.tb.empty = "No match."
	}
	m.tb.setRows(rows)
}

// selected returns the highlighted repo.
func (m *reposModel) selected() (discover.Repo, bool) {
	r, ok := m.tb.selected()
	if !ok {
		return discover.Repo{}, false
	}
	for _, x := range m.repos {
		if x.Path == r.id {
			return x, true
		}
	}
	return discover.Repo{}, false
}

// Update implements screen.
func (m *reposModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if m.loaded {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		m.tb.empty = scanningLabel(m.spin, "scanning…")
		return m, cmd
	case repoListMsg:
		m.loaded = true
		if t.err != nil {
			return m, flashErr(t.err.Error())
		}
		m.repos, m.used = t.repos, t.used
		m.fill()
		return m, nil
	case refreshMsg:
		return m, m.load()
	case repoAddedMsg:
		if t.owner != screen(m) {
			return m, nil
		}
		m.addRepo(t.repo)
		return m, flash("added " + t.repo.Name)
	case actionMsg:
		if t.owner != screen(m) {
			return m, nil
		}
		for _, r := range m.repos {
			if r.Path == t.subject {
				return m.act(t.id, r)
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// addRepo shows a repo the finder just remembered without a full reload.
func (m *reposModel) addRepo(r discover.Repo) {
	for _, x := range m.repos {
		if samePath(x.Path, r.Path) {
			m.tb.restore(x.Path)
			return
		}
	}
	m.repos = append(m.repos, r)
	m.fill()
	m.tb.restore(r.Path)
}

// key handles the repos key map.
func (m *reposModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.tb.handleKey(k.String()) {
		return m, nil
	}
	sel, has := m.selected()
	switch k.String() {
	case "esc":
		if m.filter != "" {
			m.setFilter("")
			return m, nil
		}
		return m, pop()
	case "N":
		m.tb.sortBy(repoColName)
	case "U":
		m.tb.sortBy(repoColUsed)
	case "a":
		return m, push(newFinder(m.e, m))
	}
	if !has {
		return m, nil
	}
	if k.String() == "enter" {
		return m, push(newActionMenu(m, "repo", sel.Name, paths.Contract(sel.Path), repoActions))
	}
	if a, ok := findAction(repoActions, k.String()); ok {
		return m.act(a.id, sel)
	}
	return m, nil
}

// act performs the repo action with the given id on sel. It serves both the
// repos view's own shortcut keys and the choices made in the repo menu.
func (m *reposModel) act(id string, sel discover.Repo) (screen, tea.Cmd) {
	switch id {
	case "packages":
		return m, push(newScopedPackages(m.e, sel.Path, sel.Name))
	case "new-package":
		return m, push(newEditorWithRepo(m.e, sel))
	case "copy":
		if err := clipboard.WriteAll(sel.Path); err != nil {
			return m, flashErr("could not copy: " + err.Error() + " — " + sel.Path)
		}
		return m, flash("copied " + paths.Contract(sel.Path))
	}
	return m, nil
}

// View implements screen.
func (m *reposModel) View(w, h int) string { return m.tb.view(w, h) }

// newEditorWithRepo builds the new-package editor with a repo already
// chosen as an active member.
func newEditorWithRepo(e *env, r discover.Repo) *editorModel {
	ed := newEditor(e, nil)
	ed.applySelection([]pickSel{{Repo: r, Mode: manifest.ModeActive}})
	ed.focus = fieldName
	ed.applyFocus()
	return ed
}
