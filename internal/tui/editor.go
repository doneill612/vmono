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

// editorModel is the package editor, used for both create and edit. Fields:
// name (read-only on edit), purpose and context (multi-line; ctrl+e opens
// $EDITOR), default harness, and the member list with each member's mode
// and role.
type editorModel struct {
	e       *env
	pkg     *core.Package // nil when creating
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
}

// newEditor builds the editor for a new package (pkg nil) or an existing
// one.
func newEditor(e *env, pkg *core.Package) *editorModel {
	m := &editorModel{e: e, pkg: pkg}
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
		m.purpose.SetValue(strings.TrimSpace(pkg.M.Purpose))
		m.context.SetValue(strings.TrimSpace(pkg.M.Context))
		m.members = append([]manifest.Member(nil), pkg.M.Members...)
		for i, h := range m.harnesses {
			if h == pkg.M.Harness {
				m.hIdx = i
			}
		}
		m.focus = fieldPurpose
	}
	m.applyFocus()
	return m
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
func (m *editorModel) Init() tea.Cmd { return textinput.Blink }

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
	if m.pkg != nil {
		return "edit(" + m.pkg.Name + ")"
	}
	return "new package"
}

// Crumb implements screen.
func (m *editorModel) Crumb() string {
	if m.pkg != nil {
		return "edit"
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
		hs = append(hs, hint{"a", "Add/change repos"}, hint{"x", "Remove"}, hint{"m", "Active/ref"}, hint{"r", "Role"})
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
		return m, pop()
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
			if mem.Mode == manifest.ModeActive {
				mem.Mode = manifest.ModeReference
			} else {
				mem.Mode = manifest.ModeActive
			}
			// Path, base and ref follow the mode; blank them so defaults
			// for the new mode apply.
			mem.Path, mem.Base, mem.Ref = "", "", ""
		}
	case "r":
		if m.cursor < len(m.members) {
			m.role.SetValue(m.members[m.cursor].Role)
			m.roleEdit = true
			return m, m.role.Focus()
		}
	}
	return m, nil
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
	return goTo(newPicker(m.e, initial, extra,
		func(sel []pickSel) screen { m.applySelection(sel); return m },
		func() screen { return m }))
}

// applySelection reconciles the member list with the picker's result:
// existing members that are still selected keep their role and settings,
// unselected ones are dropped, and new ones are appended.
func (m *editorModel) applySelection(sel []pickSel) {
	var next []manifest.Member
	used := map[string]bool{}
	for _, mem := range m.members {
		src, _ := paths.Abs(mem.Source)
		for _, s := range sel {
			if !samePath(src, s.Repo.Path) {
				continue
			}
			if mem.Mode != s.Mode {
				mem.Mode = s.Mode
				mem.Path, mem.Base, mem.Ref = "", "", ""
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

// saveCreate runs Create through the progress screen.
func (m *editorModel) saveCreate() (screen, tea.Cmd) {
	c := m.e.core
	name := strings.TrimSpace(m.name.Value())
	nm := &manifest.Manifest{
		Name: name, Harness: m.harnessName(),
		Purpose: strings.TrimSpace(m.purpose.Value()), Context: strings.TrimSpace(m.context.Value()),
		Members: append([]manifest.Member(nil), m.members...),
	}
	return m, goTo(newProgress(m.e, operation{
		title: "Create " + name,
		pkg:   name,
		run: func(force, reuse bool) error {
			_, err := c.Create(nm, core.CreateOptions{ReuseBranch: reuse})
			return err
		},
		back: func() screen { return m },
	}))
}

// saveEdit applies the edited form as a diff. Removals that would discard
// work go through the same confirmation as delete.
func (m *editorModel) saveEdit() (screen, tea.Cmd) {
	c := m.e.core
	edited, err := manifest.Parse(manifest.Encode(m.pkg.M))
	if err != nil {
		return m, flashErr(err.Error())
	}
	edited.Harness = m.harnessName()
	edited.Purpose = strings.TrimSpace(m.purpose.Value())
	edited.Context = strings.TrimSpace(m.context.Value())
	edited.Members = append([]manifest.Member(nil), m.members...)

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
			pkg:   name,
			force: force,
			run: func(force, reuse bool) error {
				_, err := c.ApplyEdit(pkg, edited, core.EditOptions{Force: force, ReuseBranch: reuse})
				return err
			},
			back: func() screen { return m },
		})
	}
	if len(ep.Unsafe) > 0 {
		return m, goTo(newConfirm(m.e, "Discard unsaved work?", name, ep.Unsafe, false,
			func(force, _ bool) screen { return apply(force) },
			func() screen { return m }))
	}
	return m, goTo(apply(false))
}

// areaHeight is the text area height for a body of h lines: what is left
// after the fixed rows is split between purpose and context.
func (m *editorModel) areaHeight(h int) int {
	rows := len(m.members)
	if rows < 3 {
		rows = 3
	}
	if rows > 6 {
		rows = 6
	}
	n := (h-9-rows)/2 - 2
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

// View implements screen.
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

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(m.label("Name", fieldName) + "  ")
	if m.pkg != nil {
		b.WriteString(styleDim.Render(m.pkg.Name + "  (read-only: session history is keyed by path)"))
	} else {
		b.WriteString(m.name.View())
	}
	b.WriteString("\n\n")
	b.WriteString(m.label("Purpose", fieldPurpose) + styleDim.Render("  ctrl+e opens $EDITOR") + "\n" + m.box(m.purpose.View(), fieldPurpose, boxW) + "\n")
	b.WriteString(m.label("Context", fieldContext) + styleDim.Render("  tickets, links, decisions") + "\n" + m.box(m.context.View(), fieldContext, boxW) + "\n")

	hn := m.harnessName()
	if hn == "" {
		hn = "(default: " + m.e.core.Cfg.DefaultHarness + ")"
	}
	b.WriteString(m.label("Harness", fieldHarness) + "  ◂ " + hn + " ▸\n\n")

	b.WriteString(m.label("Members", fieldMembers) + "\n")
	if len(m.members) == 0 {
		b.WriteString(styleDim.Render("   none yet — press a to choose repos") + "\n")
	}
	for i, mem := range m.members {
		cursor := "   "
		if m.focus == fieldMembers && i == m.cursor {
			cursor = " " + styleSelected.Render("▸ ")
		}
		mode := "active"
		if mem.Mode == manifest.ModeReference {
			mode = "ref   "
		}
		role := mem.Role
		if role == "" {
			role = styleDim.Render("(no role)")
		}
		b.WriteString(fmt.Sprintf("%s%-16s %s  %s\n", cursor, truncate(mem.Name, 16), styleDim.Render(mode), truncate(role, boxW-30)))
	}
	if m.roleEdit {
		b.WriteString("\n " + m.role.View() + "\n")
	}
	return b.String()
}

// box frames a text area, brighter when its field has focus.
func (m *editorModel) box(content string, field, w int) string {
	col := colFrame
	if m.focus == field {
		col = colCyan
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
