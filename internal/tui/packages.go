package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/paths"
)

// packagesMsg delivers the package list.
type packagesMsg struct {
	list []core.Summary
	err  error
}

// packageRemovedMsg reports that a package was deleted, so the packages view
// can drop it before its next reload.
type packageRemovedMsg struct{ name string }

// fetchedMsg reports a finished fetch-all.
type fetchedMsg struct {
	name string
	n    int
	err  error
}

// packageActions lists everything the packages view can do to one package, in
// the order its menu shows it. Drilling into the sessions comes first because
// it is what Enter on a package most often means. Each key is also the view's
// own shortcut for the action.
var packageActions = []action{
	{id: "sessions", label: "Sessions", key: "right"},
	{id: "launch", label: "Launch latest", key: "o"},
	{id: "launch-with", label: "Launch with…", key: "l"},
	{id: "new-session", label: "New session"},
	{id: "describe", label: "Describe", key: "d"},
	{id: "edit", label: "Edit", key: "e"},
	{id: "sync", label: "Sync all", key: "s"},
	{id: "fetch", label: "Fetch", key: "f"},
	{id: "copy", label: "Copy path", key: "y"},
	{id: "delete", label: "Delete", key: "ctrl+d", danger: true},
}

// legacyActions is all that can be done to a package from before sessions.
var legacyActions = []action{{id: "copy", label: "Copy path", key: "y"}}

// packagesModel is the root view: a table of every package. It can be
// scoped to the packages that use one source repo, which the repos view
// does when you open a repo.
type packagesModel struct {
	e      *env
	tb     *table
	all    []core.Summary
	loaded bool
	filter string
	// scopeRepo is the source path packages must use to be listed; empty
	// lists everything. scopeName labels it in the frame title.
	scopeRepo, scopeName string
}

// newPackages builds the unscoped packages view.
func newPackages(e *env) *packagesModel {
	tb := newTable([]column{
		{title: "name", flex: true},
		{title: "members"},
		{title: "sessions"},
		{title: "harness"},
		{title: "state"},
		{title: "opened"},
	})
	m := &packagesModel{e: e, tb: tb}
	m.fill()
	return m
}

// newScopedPackages builds the view limited to packages that use a repo.
func newScopedPackages(e *env, repoPath, repoName string) *packagesModel {
	m := newPackages(e)
	m.scopeRepo, m.scopeName = repoPath, repoName
	m.fill()
	return m
}

// Init implements screen.
func (m *packagesModel) Init() tea.Cmd { return m.loadPackages() }

// loadPackages lists packages with dirty markers, off the UI goroutine.
func (m *packagesModel) loadPackages() tea.Cmd {
	c := m.e.core
	return func() tea.Msg {
		list, err := c.List(true)
		return packagesMsg{list: list, err: err}
	}
}

// Title implements screen.
func (m *packagesModel) Title() string {
	return frameTitle("packages", m.scopeName, m.filter, len(m.tb.rows))
}

// Crumb implements screen.
func (m *packagesModel) Crumb() string {
	if m.scopeName != "" {
		return "packages:" + m.scopeName
	}
	return "packages"
}

// Hints implements screen.
func (m *packagesModel) Hints() []hint {
	return []hint{
		{"enter", "Actions"}, {"→", "Sessions"}, {"o", "Open latest"}, {"l", "Launch…"}, {"d", "Describe"},
		{"n", "New package"}, {"e", "Edit"}, {"ctrl+d", "Delete"}, {"s", "Sync all"},
		{"f", "Fetch"}, {"y", "Copy path"}, {"c", "Edit config"},
	}
}

// contextHints implements contextHinter: "c" edits the package root, source
// roots and default harness shown in the header.
func (m *packagesModel) contextHints() []hint { return []hint{{"c", "Edit config"}} }

// frameTitle formats a table's frame title as name(scope)(/filter)[count].
func frameTitle(name, scope, filter string, n int) string {
	t := name
	if scope != "" {
		t += "(" + scope + ")"
	}
	if filter != "" {
		t += "(/" + filter + ")"
	}
	return fmt.Sprintf("%s[%d]", t, n)
}

// setFilter implements filterable.
func (m *packagesModel) setFilter(q string) {
	m.filter = q
	m.tb.setFilter(q)
	m.fill()
}

// filterValue implements filterable.
func (m *packagesModel) filterValue() string { return m.filter }

// uses reports whether a package has the scoped repo as a member source.
func (m *packagesModel) uses(s core.Summary) bool {
	for _, src := range s.Sources {
		p, err := paths.Abs(src)
		if err == nil && samePath(p, m.scopeRepo) {
			return true
		}
	}
	return false
}

// fill rebuilds the table rows from the loaded list.
func (m *packagesModel) fill() {
	var rows []row
	for _, s := range m.all {
		if m.scopeRepo != "" && !m.uses(s) {
			continue
		}
		rows = append(rows, packageRow(s))
	}
	switch {
	case !m.loaded:
		m.tb.empty = "loading…"
	case len(m.all) == 0:
		m.tb.empty = "No packages yet. Press n to create one."
	case m.scopeRepo != "" && len(rows) == 0:
		m.tb.empty = "No package uses this repo."
	default:
		m.tb.empty = "No match."
	}
	m.tb.setRows(rows)
}

// packageRow builds one table row.
func packageRow(s core.Summary) row {
	if s.Legacy {
		return row{
			id:     s.Name,
			search: s.Name,
			cells: []cell{
				plain(s.Name), styled("-", styleDim), styled("-", styleDim), styled("-", styleDim),
				styled("migrate", styleWarn), styled("run vmono doctor --fix", styleDim),
			},
		}
	}
	members := fmt.Sprintf("%dA", s.Active)
	if s.Reference > 0 {
		members += fmt.Sprintf(" %dR", s.Reference)
	}
	state, stateKey := styled("clean", styleOK), "0"
	if s.Dirty {
		state, stateKey = styled("dirty", styleWarn), "1"
	}
	state.key = stateKey
	harness := styled("default", styleDim)
	if s.Harness != "" {
		harness = plain(s.Harness)
	}
	opened := styled("never", styleDim)
	if t, err := time.Parse(time.RFC3339, s.LastOpened); err == nil {
		opened = keyed(ago(t), s.LastOpened, styleDim)
	}
	return row{
		id:     s.Name,
		search: s.Name,
		cells: []cell{
			plain(s.Name),
			keyed(members, numKey(s.Members), styleDim),
			keyed(fmt.Sprint(s.Sessions), numKey(s.Sessions), styleDim),
			harness,
			state,
			opened,
		},
	}
}

// selected returns the highlighted package.
func (m *packagesModel) selected() (core.Summary, bool) {
	r, ok := m.tb.selected()
	if !ok {
		return core.Summary{}, false
	}
	for _, s := range m.all {
		if s.Name == r.id {
			return s, true
		}
	}
	return core.Summary{}, false
}

// Update implements screen.
func (m *packagesModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case packagesMsg:
		m.loaded = true
		if t.err != nil {
			return m, flashErr(t.err.Error())
		}
		m.all = t.list
		m.e.setPackages(t.list)
		m.fill()
		return m, nil
	case actionMsg:
		if t.owner != screen(m) {
			return m, nil
		}
		for _, s := range m.all {
			if s.Name == t.subject {
				return m.act(t.id, s)
			}
		}
		return m, flashErr("package " + t.subject + " no longer exists")
	case packageRemovedMsg:
		m.drop(t.name)
		return m, nil
	case refreshMsg:
		return m, m.loadPackages()
	case fetchedMsg:
		if t.err != nil {
			return m, tea.Batch(flashErr("fetch "+t.name+": "+t.err.Error()), m.loadPackages())
		}
		return m, tea.Batch(flash(fmt.Sprintf("fetched %d member(s) of %s", t.n, t.name)), m.loadPackages())
	case tea.KeyMsg:
		return m.key(t)
	}
	return m, nil
}

// drop removes a package from the loaded list and the table. A delete leaves
// the packages view underneath the progress screen showing the old list until
// the reload that follows returns; dropping the row first means the deleted
// package never reappears when the user goes back.
func (m *packagesModel) drop(name string) {
	kept := m.all[:0:0]
	for _, s := range m.all {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	m.all = kept
	m.e.setPackages(kept)
	m.fill()
}

// key handles the packages key map.
func (m *packagesModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.tb.handleKey(k.String()) {
		return m, nil
	}
	sel, has := m.selected()
	switch k.String() {
	case "esc":
		switch {
		case m.filter != "":
			m.setFilter("")
		case m.scopeRepo != "":
			return m, pop()
		}
		return m, nil
	case "n":
		return m, push(newEditor(m.e, nil))
	case "c":
		return m, push(newReconfigure(m.e))
	}
	if !has {
		return m, nil
	}
	if sel.Legacy {
		if a, ok := findAction(legacyActions, k.String()); ok {
			return m.act(a.id, sel)
		}
		if k.String() == "enter" || findKey(packageActions, k.String()) {
			return m, flashErr(sel.Name + " predates sessions; run `vmono doctor --fix` to migrate it")
		}
		return m, nil
	}
	if k.String() == "enter" {
		return m, push(newActionMenu(m, "package", sel.Name, "", packageActions))
	}
	if a, ok := findAction(packageActions, k.String()); ok {
		return m.act(a.id, sel)
	}
	return m, nil
}

// findKey reports whether any action is bound to key.
func findKey(items []action, key string) bool {
	_, ok := findAction(items, key)
	return ok
}

// act performs the per-package action with the given id on sel. It serves
// both the packages view's own shortcut keys and the choices made in the
// package menu.
func (m *packagesModel) act(id string, sel core.Summary) (screen, tea.Cmd) {
	switch id {
	case "sessions":
		return m, push(newSessions(m.e, sel.Name))
	case "launch":
		return m, m.openLatest(sel.Name, false)
	case "launch-with":
		return m, m.openLatest(sel.Name, true)
	case "new-session":
		return m, push(newSessionPrompt(m.e, sel.Name))
	case "describe":
		return m, push(newDescribe(m.e, sel.Name, "", ""))
	case "copy":
		if err := clipboard.WriteAll(sel.Path); err != nil {
			return m, flashErr("could not copy: " + err.Error() + " — " + sel.Path)
		}
		return m, flash("copied " + paths.Contract(sel.Path))
	case "fetch":
		return m, tea.Batch(flash("fetching "+sel.Name+"…"), fetchAll(m.e.core, sel.Name))
	case "edit":
		p, err := m.e.core.Load(sel.Name)
		if err != nil {
			return m, flashErr(err.Error())
		}
		return m, push(newEditor(m.e, p))
	case "sync":
		return m, push(newProgress(m.e, syncOp(m.e.core, sel.Name)))
	case "delete":
		return m, m.startDelete(sel.Name)
	}
	return m, nil
}

// openLatest launches into the package's most recently opened session, or
// its first by name when none has been opened. With pick it opens the
// harness picker for that session instead of launching the default. A package
// with no sessions opens the new-session prompt.
func (m *packagesModel) openLatest(name string, pick bool) tea.Cmd {
	c := m.e.core
	p, err := c.Load(name)
	if err != nil {
		return flashErr(err.Error())
	}
	rows, err := c.ListSessions(p, false)
	if err != nil {
		return flashErr(err.Error())
	}
	if len(rows) == 0 {
		return tea.Batch(flash(name+" has no sessions yet; start one"), push(newSessionPrompt(m.e, name)))
	}
	session := rows[0].Name
	if pick {
		return push(newLaunchPicker(m.e, name, session, "Launch with"))
	}
	return func() tea.Msg { return launchMsg{LaunchRequest{Package: name, Session: session}} }
}

// syncOp builds the sync operation for a whole package: every one of its
// sessions is synced. It ends back at Home, since there is no one session to
// offer launching into.
func syncOp(c *core.Core, name string) operation {
	return operation{
		title: "Sync " + name,
		run: func(force, reuse bool) error {
			p, err := c.Load(name)
			if err != nil {
				return err
			}
			_, err = c.SyncPackage(p, core.SyncOptions{})
			return err
		},
	}
}

// startDelete opens the delete confirmation for a package: every member of
// every session is checked, and the rows name their session.
func (m *packagesModel) startDelete(name string) tea.Cmd {
	c := m.e.core
	p, err := c.Load(name)
	if err != nil {
		return flashErr(err.Error())
	}
	rows, err := c.PackageSafety(p)
	if err != nil {
		return flashErr(err.Error())
	}
	return push(newConfirm(m.e, "Delete package", name, rows, true,
		func(force, deleteBranches bool) screen {
			return newProgress(m.e, operation{
				title:   "Delete " + name,
				removed: name,
				run: func(_, _ bool) error {
					_, err := c.Delete(p, core.DeleteOptions{Force: force, DeleteBranches: deleteBranches})
					return err
				},
			})
		},
		nil))
}

// fetchAll fetches every member's source of a package concurrently, once
// per distinct repo, and reports with a fetchedMsg. A repo that fails does
// not stop the others; the message says how many members were fetched and
// which were not.
func fetchAll(c *core.Core, name string) tea.Cmd {
	return func() tea.Msg {
		p, err := c.Load(name)
		if err != nil {
			return fetchedMsg{name: name, err: err}
		}
		var srcs []string
		owners := map[string][]string{}
		for _, mem := range p.M.Members {
			src, err := paths.Abs(mem.Source)
			if err != nil {
				return fetchedMsg{name: name, err: err}
			}
			srcs = append(srcs, src)
			owners[src] = append(owners[src], mem.Name)
		}
		n, err := summarizeFetch(owners, gitx.FetchMany(srcs, gitx.DefaultFetchConcurrency))
		return fetchedMsg{name: name, n: n, err: err}
	}
}

// summarizeFetch turns fetch results into the number of members whose
// source was fetched and, when any repo failed, one error naming the members
// left behind. owners maps each requested source path to the members that
// use it. Failures from missing credentials are reported with the fix
// instead of git's raw output.
func summarizeFetch(owners map[string][]string, results []gitx.FetchResult) (int, error) {
	n := 0
	var failed []string
	var cause error
	authOnly := true
	for _, r := range results {
		var names []string
		for _, p := range r.Paths {
			names = append(names, owners[p]...)
		}
		if r.Err == nil {
			n += len(names)
			continue
		}
		failed = append(failed, names...)
		if cause == nil {
			cause = r.Err
		}
		if !gitx.IsAuthError(r.Err) {
			authOnly = false
		}
	}
	if len(failed) == 0 {
		return n, nil
	}
	reason := strings.Join(strings.Fields(cause.Error()), " ")
	if authOnly {
		reason = gitx.AuthHint
	}
	return n, fmt.Errorf("fetched %d member(s); couldn't fetch %s: %s", n, strings.Join(failed, ", "), reason)
}

// tbl implements tabular.
func (m *packagesModel) tbl() *table { return m.tb }

// rowActions implements rowActor.
func (m *packagesModel) rowActions() (rowContext, bool) {
	s, ok := m.selected()
	items := packageActions
	if s.Legacy {
		items = legacyActions
	}
	return rowContext{kind: "package", subject: s.Name, label: s.Name, items: items}, ok
}

// View implements screen.
func (m *packagesModel) View(w, h int) string { return m.tb.view(w, h) }
