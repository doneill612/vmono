package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/terms"
)

// Editor field focus order.
const (
	fieldName = iota
	fieldPurpose
	fieldContext
	fieldHarness
	fieldMembers
	fieldCount
)

// editedTextMsg returns from $EDITOR with the file the text was edited in.
type editedTextMsg struct {
	field int
	file  string
	err   error
}

// editorModel is the package editor, used to create, clone and edit. Fields:
// name (read-only on edit), purpose and context (multi-line; ctrl+e opens
// $EDITOR), default harness, and the member list with each member's mode
// and role.
type editorModel struct {
	e       *env
	pkg     *core.Package // nil when creating
	src     *core.Package // the package being cloned, else nil
	name    textinput.Model
	purpose textarea.Model
	context textarea.Model

	harnesses []string // "" means the global default
	hIdx      int

	members  []manifest.Member
	cursor   int
	roleEdit bool
	role     textinput.Model

	focus int

	// left holds each member, by name, as it was before its last change of
	// mode, so that changing back restores what it had.
	left map[string]manifest.Member
	// note is shown when the editor next comes up.
	note string
}

// newEditor builds the editor for a new package (pkg nil) or an existing
// one.
func newEditor(e *env, pkg *core.Package) *editorModel {
	m := &editorModel{e: e, pkg: pkg, left: map[string]manifest.Member{}}
	m.name = newTextInput()
	m.name.Prompt = ""
	m.name.Placeholder = "lowercase-with-hyphens"
	m.name.CharLimit = 64
	m.purpose = newArea("What is this work, and why are these repos grouped?")
	m.context = newArea("Tickets, links, constraints, decisions already made")
	m.role = newTextInput()
	m.role.Prompt = "role: "
	m.role.CharLimit = 200

	m.harnesses = append([]string{""}, e.core.Cfg.HarnessNames()...)
	if pkg != nil {
		m.name.SetValue(pkg.Name)
		m.fill(pkg.M)
		m.focus = fieldPurpose
	}
	m.applyFocus()
	return m
}

// newCloneEditor builds the new-package editor filled in from src, to define a
// copy of it. Everything on the form can be changed before saving.
func newCloneEditor(e *env, src *core.Package) *editorModel {
	m := newEditor(e, nil)
	m.src = src
	m.name.SetValue(src.Name + "-copy")
	m.fill(src.M)
	return m
}

// fill puts everything the form shows of from into it, the name aside.
func (m *editorModel) fill(from *manifest.Manifest) {
	m.purpose.SetValue(strings.TrimSpace(from.Purpose))
	m.context.SetValue(strings.TrimSpace(from.Context))
	m.members = append([]manifest.Member(nil), from.Members...)
	for i, h := range m.harnesses {
		if h == from.Harness {
			m.hIdx = i
		}
	}
}

// formInto overwrites everything the form shows in to, the name aside.
func (m *editorModel) formInto(to *manifest.Manifest) {
	to.Harness = m.harnessName()
	to.Purpose = strings.TrimSpace(m.purpose.Value())
	to.Context = strings.TrimSpace(m.context.Value())
	to.Members = append([]manifest.Member(nil), m.members...)
}

// newArea builds a multi-line input.
func newArea(placeholder string) textarea.Model {
	t := newTextArea()
	t.Placeholder = placeholder
	t.ShowLineNumbers = false
	t.CharLimit = 0
	t.Prompt = ""
	t.SetHeight(3)
	return t
}

// Init implements screen.
func (m *editorModel) Init() tea.Cmd {
	note := m.note
	m.note = ""
	if note != "" {
		return tea.Batch(textinput.Blink, flash(note))
	}
	return textinput.Blink
}

// capturing implements capturing: true while a text field has focus.
func (m *editorModel) capturing() bool {
	if m.roleEdit {
		return true
	}
	switch m.focus {
	case fieldName:
		return m.pkg == nil
	case fieldPurpose, fieldContext:
		return true
	}
	return false
}

// Title implements screen.
func (m *editorModel) Title() string {
	switch {
	case m.pkg != nil:
		return "Edit · " + m.pkg.Name
	case m.src != nil:
		return "Clone · " + m.src.Name
	}
	return "New package"
}

// Crumb implements screen.
func (m *editorModel) Crumb() string {
	switch {
	case m.pkg != nil:
		return "edit"
	case m.src != nil:
		return "clone"
	}
	return "new"
}

// Hints implements screen: the keys that apply to the focused field.
func (m *editorModel) Hints() []hint {
	hs := []hint{{"ctrl+s", "Save"}, {"tab", "Next field"}, {"shift+tab", "Previous"}, {"esc", "Cancel"}}
	switch {
	case m.roleEdit:
		return []hint{{"enter", "Set role"}, {"esc", "Cancel"}}
	case m.focus == fieldPurpose || m.focus == fieldContext:
		hs = append(hs, hint{"ctrl+e", "Open $EDITOR"})
	case m.focus == fieldHarness:
		hs = append(hs, hint{"←/→", "Choose harness"})
	case m.focus == fieldMembers:
		hs = append(hs, hint{"a", "Add/change repos"}, hint{"x", "Remove"}, hint{"m", "Active/read-only"}, hint{"r", "Role"}, hint{"c", "Setup/teardown"})
	}
	return hs
}

// applyFocus focuses the active field's input and blurs the rest.
func (m *editorModel) applyFocus() tea.Cmd {
	m.name.Blur()
	m.purpose.Blur()
	m.context.Blur()
	switch m.focus {
	case fieldName:
		if m.pkg == nil {
			return m.name.Focus()
		}
	case fieldPurpose:
		return m.purpose.Focus()
	case fieldContext:
		return m.context.Focus()
	}
	return nil
}

// resize fits the text areas to the terminal.
func (m *editorModel) resize() {
	w := m.e.w - 6
	if w > 100 {
		w = 100
	}
	if w < 30 {
		w = 30
	}
	m.purpose.SetWidth(w)
	m.context.SetWidth(w)
}

// Update implements screen.
func (m *editorModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize()
		return m, nil
	case editedTextMsg:
		return m.finishExternal(t)
	case tea.KeyMsg:
		return m.key(t)
	}
	return m.forward(msg)
}

// forward passes a non-key message to the focused input.
func (m *editorModel) forward(msg tea.Msg) (screen, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focus {
	case fieldName:
		if m.pkg == nil {
			m.name, cmd = m.name.Update(msg)
		}
	case fieldPurpose:
		m.purpose, cmd = m.purpose.Update(msg)
	case fieldContext:
		m.context, cmd = m.context.Update(msg)
	}
	return m, cmd
}

// key handles key presses.
func (m *editorModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	if m.roleEdit {
		return m.keyRole(k)
	}
	switch k.String() {
	case "ctrl+s":
		return m.save()
	case "esc":
		return m, pop(m)
	case "tab":
		m.focus = (m.focus + 1) % fieldCount
		return m, m.applyFocus()
	case "shift+tab":
		m.focus = (m.focus + fieldCount - 1) % fieldCount
		return m, m.applyFocus()
	case "ctrl+e":
		if m.focus == fieldPurpose || m.focus == fieldContext {
			return m, m.openExternal(m.focus)
		}
	}
	switch m.focus {
	case fieldHarness:
		switch k.String() {
		case "left", "h":
			m.hIdx = (m.hIdx + len(m.harnesses) - 1) % len(m.harnesses)
		case "right", "l":
			m.hIdx = (m.hIdx + 1) % len(m.harnesses)
		}
		return m, nil
	case fieldMembers:
		return m.keyMembers(k)
	}
	return m.forward(k)
}

// keyMembers handles the member list's keys.
func (m *editorModel) keyMembers(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.members)-1 {
			m.cursor++
		}
	case "a":
		return m, m.openPicker()
	case "x", "delete":
		if m.cursor < len(m.members) {
			m.members = append(m.members[:m.cursor], m.members[m.cursor+1:]...)
			if m.cursor > 0 && m.cursor >= len(m.members) {
				m.cursor--
			}
		}
	case "m":
		if m.cursor < len(m.members) {
			mem := &m.members[m.cursor]
			mode := manifest.ModeActive
			if mem.Mode == mode {
				mode = manifest.ModeReference
			}
			if note := m.setMode(mem, mode); note != "" {
				return m, flash(note)
			}
		}
	case "r":
		if m.cursor < len(m.members) {
			m.role.SetValue(m.members[m.cursor].Role)
			m.roleEdit = true
			return m, m.role.Focus()
		}
	case "c":
		if i := m.cursor; i < len(m.members) {
			return m, push(m, newCommandsDialog(m.members[i], func(setup, teardown manifest.Commands) {
				m.members[i].Setup, m.members[i].Teardown = setup, teardown
			}))
		}
	}
	return m, nil
}

// setMode changes a member's mode. Changing it back gives the member the path,
// base and ref it had before, and otherwise core.SetMode decides what carries
// over; its note is returned.
func (m *editorModel) setMode(mem *manifest.Member, mode string) (note string) {
	if mem.Mode == mode {
		return ""
	}
	was, ok := m.left[mem.Name]
	m.left[mem.Name] = *mem
	if ok && was.Mode == mode && was.Source == mem.Source {
		mem.Mode, mem.Path, mem.Base, mem.Ref = mode, was.Path, was.Base, was.Ref
		return ""
	}
	return core.SetMode(mem, mode)
}

// keyRole handles keys while editing a member's role.
func (m *editorModel) keyRole(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "enter":
		if m.cursor < len(m.members) {
			m.members[m.cursor].Role = strings.Join(strings.Fields(m.role.Value()), " ")
		}
		fallthrough
	case "esc":
		m.roleEdit = false
		m.role.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.role, cmd = m.role.Update(k)
	return m, cmd
}

// openPicker opens the repo picker preloaded with the current members.
func (m *editorModel) openPicker() tea.Cmd {
	initial := map[string]string{}
	var extra []discover.Repo
	for _, mem := range m.members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			continue
		}
		initial[src] = mem.Mode
		extra = append(extra, discover.Repo{Name: filepath.Base(src), Path: src, Remote: mem.Remote})
	}
	return goTo(m, newPicker(m.e, initial, extra,
		func(sel []pickSel) screen { m.applySelection(sel); return m },
		func() screen { return m }))
}

// applySelection reconciles the member list with the picker's result:
// existing members that are still selected keep their role and settings,
// unselected ones are dropped, and new ones are appended.
func (m *editorModel) applySelection(sel []pickSel) {
	var next []manifest.Member
	var notes []string
	used := map[string]bool{}
	for _, mem := range m.members {
		src, _ := paths.Abs(mem.Source)
		for _, s := range sel {
			if !samePath(src, s.Repo.Path) {
				continue
			}
			if note := m.setMode(&mem, s.Mode); note != "" {
				notes = append(notes, note)
			}
			next = append(next, mem)
			used[s.Repo.Path] = true
			break
		}
	}
	names := map[string]bool{}
	for _, mem := range next {
		names[mem.Name] = true
	}
	for _, s := range sel {
		if used[s.Repo.Path] {
			continue
		}
		name := manifest.Slugify(filepath.Base(s.Repo.Path))
		if name == "" {
			name = "repo"
		}
		for base, i := name, 2; names[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		names[name] = true
		next = append(next, manifest.Member{Name: name, Source: s.Repo.Path, Remote: s.Repo.Remote, Mode: s.Mode})
	}
	m.members = next
	m.note = strings.Join(notes, "; ")
	if m.cursor >= len(m.members) {
		m.cursor = 0
	}
	m.focus = fieldMembers
	m.applyFocus()
}

// editorCommand returns the command to edit a file: $VISUAL, then $EDITOR,
// then vi (notepad on Windows).
func editorCommand(file string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	parts := strings.Fields(editor)
	return exec.Command(parts[0], append(parts[1:], file)...)
}

// openExternal writes the field's text to a temp file and hands the
// terminal to $EDITOR; the result returns as an editedTextMsg.
func (m *editorModel) openExternal(field int) tea.Cmd {
	text := m.purpose.Value()
	if field == fieldContext {
		text = m.context.Value()
	}
	f, err := os.CreateTemp("", "vmono-*.md")
	if err != nil {
		return flashErr(err.Error())
	}
	name := f.Name()
	_, werr := f.WriteString(text)
	f.Close()
	if werr != nil {
		return flashErr(werr.Error())
	}
	return tea.ExecProcess(editorCommand(name), func(err error) tea.Msg {
		return editedTextMsg{field: field, file: name, err: err}
	})
}

// finishExternal loads the edited text back into its field.
func (m *editorModel) finishExternal(t editedTextMsg) (screen, tea.Cmd) {
	defer os.Remove(t.file)
	if t.err != nil {
		return m, flashErr("editor: " + t.err.Error())
	}
	data, err := os.ReadFile(t.file)
	if err != nil {
		return m, flashErr(err.Error())
	}
	text := strings.TrimRight(string(data), "\n")
	if t.field == fieldContext {
		m.context.SetValue(text)
	} else {
		m.purpose.SetValue(text)
	}
	return m, nil
}

// harnessName returns the chosen harness key, or "" for the default.
func (m *editorModel) harnessName() string { return m.harnesses[m.hIdx] }

// save validates the form and moves to the apply step. With no members it
// opens the repo picker instead, which is the create flow's second step.
func (m *editorModel) save() (screen, tea.Cmd) {
	if m.pkg == nil {
		if err := manifest.ValidateName(strings.TrimSpace(m.name.Value())); err != nil {
			m.focus = fieldName
			return m, tea.Batch(m.applyFocus(), flashErr(err.Error()))
		}
	}
	if len(m.members) == 0 {
		return m, tea.Batch(flash("a package needs at least one member; choose repos"), m.openPicker())
	}
	if m.pkg == nil {
		return m.saveCreate()
	}
	return m.saveEdit()
}

// saveCreate defines the package through the progress screen, then returns
// to the packages screen. A clone also gets what the form does not show of its
// source, the notes and what init recorded. Starting a session is left to the
// user.
func (m *editorModel) saveCreate() (screen, tea.Cmd) {
	c, src := m.e.core, m.src
	name := strings.TrimSpace(m.name.Value())
	nm := &manifest.Manifest{Name: name}
	if src != nil {
		var err error
		if nm, _, err = core.CloneManifest(src, name, nil); err != nil {
			return m, flashErr(err.Error())
		}
	}
	m.formInto(nm)
	return m, goTo(m, newProgress(m.e, operation{
		title: "Create " + name,
		run: func(force, reuse bool) error {
			var err error
			if src != nil {
				_, err = c.ClonePackage(src, nm)
			} else {
				_, err = c.CreatePackage(nm)
			}
			return err
		},
		home: true,
		hint: func() string { return initHint(nm) },
		back: func() screen { return m },
	}))
}

// initHint says what initializing would do for a package just created from m.
func initHint(m *manifest.Manifest) string {
	if m.Init == nil {
		return "i initializes it"
	}
	if drift := m.DriftText(); drift != "" {
		return "overview out of date (" + drift + "), i updates it"
	}
	return "i re-initializes it"
}

// saveEdit applies the edited form as a diff. Removals that would discard
// work go through the same confirmation as delete.
func (m *editorModel) saveEdit() (screen, tea.Cmd) {
	c := m.e.core
	edited, err := manifest.Parse(manifest.Encode(m.pkg.M))
	if err != nil {
		return m, flashErr(err.Error())
	}
	m.formInto(edited)

	ep, err := c.PlanEdit(m.pkg, edited)
	if err != nil {
		return m, flashErr(err.Error())
	}
	if ep.Plan.Empty() && bytes.Equal(manifest.Encode(m.pkg.M), manifest.Encode(ep.Next)) {
		return m, goHome("no changes")
	}
	name, pkg := m.pkg.Name, m.pkg
	apply := func(force bool) screen {
		return newProgress(m.e, operation{
			title: "Edit " + name,
			force: force,
			run: func(force, reuse bool) error {
				_, err := c.ApplyEdit(pkg, edited, core.EditOptions{Force: force, ReuseBranch: reuse})
				return err
			},
			hint: func() string {
				if drift := ep.Next.DriftText(); drift != "" {
					return "overview out of date (" + drift + "), i updates it"
				}
				return ""
			},
			back: func() screen { return m },
		})
	}
	if len(ep.Unsafe) > 0 {
		return m, goTo(m, newConfirm(m.e, "Discard unsaved work?", name, ep.Unsafe, false,
			func(force, _ bool) screen { return apply(force) },
			func() screen { return m }))
	}
	return m, goTo(m, apply(false))
}

// memberRows is how many member lines the editor shows: all of them up to
// six, never fewer than two.
func (m *editorModel) memberRows() int {
	n := len(m.members)
	if n < 2 {
		n = 2
	}
	if n > 6 {
		n = 6
	}
	return n
}

// areaHeight is the text area height for a body of h lines. The fixed rows
// (name, three labels, two boxes' borders, harness, members) are taken out
// and the rest is split between purpose and context.
func (m *editorModel) areaHeight(h int) int {
	n := (h - 9 - m.memberRows()) / 2
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

// View implements screen. Spare height, when there is any, becomes blank
// lines between sections; on a short terminal everything tightens up so the
// member list stays on screen.
func (m *editorModel) View(w, h int) string {
	boxW := w - 4
	if boxW > 110 {
		boxW = 110
	}
	if boxW < 20 {
		boxW = 20
	}
	ah := m.areaHeight(h)
	for _, a := range []*textarea.Model{&m.purpose, &m.context} {
		a.SetWidth(boxW - 4)
		a.SetHeight(ah)
	}
	m.name.Width = boxW - 20
	m.role.Width = boxW - 10
	rows := m.memberRows()
	if m.roleEdit {
		rows--
	}
	airy := h-(9+2*ah+rows) >= 2

	var b strings.Builder
	b.WriteString(m.label("Name", fieldName) + "  ")
	if m.pkg != nil {
		b.WriteString(styleDim.Render(m.pkg.Name + "  (read-only: session history is keyed by path)"))
	} else {
		b.WriteString(m.name.View())
	}
	b.WriteString("\n")
	if airy {
		b.WriteString("\n")
	}
	b.WriteString(m.label("Purpose", fieldPurpose) + styleDim.Render("  ctrl+e opens $EDITOR") + "\n" + m.box(m.purpose.View(), fieldPurpose, boxW) + "\n")
	b.WriteString(m.label("Context", fieldContext) + styleDim.Render("  tickets, links, decisions") + "\n" + m.box(m.context.View(), fieldContext, boxW) + "\n")

	hn := m.harnessName()
	if hn == "" {
		hn = "(default: " + m.e.core.Cfg.DefaultHarness + ")"
	}
	b.WriteString(m.label("Harness", fieldHarness) + "  ◂ " + hn + " ▸\n")
	if airy {
		b.WriteString("\n")
	}

	b.WriteString(m.label("Members", fieldMembers) + "\n")
	if len(m.members) == 0 {
		b.WriteString(styleDim.Render("   none yet — press a to choose repos") + "\n")
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	for i := start; i < len(m.members) && i-start < rows; i++ {
		mem := m.members[i]
		cursor := "   "
		if m.focus == fieldMembers && i == m.cursor {
			cursor = " " + styleSelected.Render("▸ ")
		}
		mode := fit(terms.ModeLabel(mem.Mode, false), 9, false)
		role := mem.Role
		if role == "" {
			role = styleDim.Render("(no role)")
		}
		b.WriteString(fmt.Sprintf("%s%-16s %s  %s\n", cursor, truncate(mem.Name, 16), styleDim.Render(mode), truncate(role, max(boxW-33-lipgloss.Width(commandsTag(mem)), 8))+commandsTag(mem)))
	}
	if m.roleEdit {
		b.WriteString(" " + m.role.View() + "\n")
	}
	return b.String()
}

// commandsTag marks a member that has setup or teardown commands.
func commandsTag(mem manifest.Member) string {
	var has []string
	if len(mem.Setup) > 0 {
		has = append(has, "setup")
	}
	if len(mem.Teardown) > 0 {
		has = append(has, "teardown")
	}
	if len(has) == 0 {
		return ""
	}
	return styleDim.Render("  · " + strings.Join(has, ", "))
}

// box frames a text area, brighter when its field has focus.
func (m *editorModel) box(content string, field, w int) string {
	col := colFrame
	if m.focus == field {
		col = colAccent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(col).
		Padding(0, 1).MarginLeft(1).Width(w - 2).Render(content)
}

// label renders a field label, highlighted when focused.
func (m *editorModel) label(text string, field int) string {
	if m.focus == field {
		return " " + styleSelected.Render("▸ "+text)
	}
	return " " + styleHeaderCol.Render("  "+text)
}
