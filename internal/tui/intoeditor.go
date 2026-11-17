package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
)

// intoEditor is the dialog, opened with i from the finish confirm, that
// changes the branch each member's work lands on. It takes the confirm's
// place, and both saving and cancelling return to a confirm built from a
// fresh plan, so the rows always show the branches now in effect.
type intoEditor struct {
	e            *env
	pkg, session string
	fields       intoFields
	focus        int
	msg          string
}

// newIntoEditor builds the editor from a finish plan, whose rows carry each
// member's current landing branch.
func newIntoEditor(e *env, pkg, session string, plan core.FinishResult) *intoEditor {
	var names []string
	current := map[string]string{}
	for _, r := range plan.Members {
		names = append(names, r.Name)
		current[r.Name] = r.Into
	}
	return &intoEditor{e: e, pkg: pkg, session: session, fields: newIntoFields(names, current)}
}

// modal implements modalScreen.
func (m *intoEditor) modal() {}

// capturing implements capturing: the text fields own the keyboard.
func (m *intoEditor) capturing() bool { return true }

// Init implements screen.
func (m *intoEditor) Init() tea.Cmd { return m.fields.focus(0) }

// Title implements screen.
func (m *intoEditor) Title() string { return "landing branches" }

// Crumb implements screen. The dialog adds no breadcrumb.
func (m *intoEditor) Crumb() string { return "" }

// Hints implements screen.
func (m *intoEditor) Hints() []hint { return nil }

// back returns to a finish confirm built from the session as it is now.
func (m *intoEditor) back() (screen, error) {
	s, err := m.e.core.ResolveSession(m.pkg + "/" + m.session)
	if err != nil {
		return nil, err
	}
	return newFinishConfirm(m.e, m.pkg, m.session, m.e.core.PlanFinish(s, core.FinishOptions{})), nil
}

// Update implements screen.
func (m *intoEditor) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	n := m.fields.count()
	move := func(to int) (screen, tea.Cmd) {
		if n == 0 {
			return m, nil
		}
		m.focus = (to + n) % n
		return m, m.fields.focus(m.focus)
	}
	switch k.String() {
	case "esc":
		prev, err := m.back()
		if err != nil {
			return m, flashErr(err.Error())
		}
		return m, goTo(prev)
	case "tab", "down":
		return move(m.focus + 1)
	case "shift+tab", "up":
		return move(m.focus - 1)
	case "enter":
		return m.save()
	}
	m.msg = ""
	return m, m.fields.update(m.focus, msg)
}

// save records the branches typed and returns to the confirm. An empty field
// leaves that member's branch as it is.
func (m *intoEditor) save() (screen, tea.Cmd) {
	values := m.fields.values()
	if len(values) > 0 {
		s, err := m.e.core.ResolveSession(m.pkg + "/" + m.session)
		if err == nil {
			err = m.e.core.SetInto(s, "", values)
		}
		if err != nil {
			m.msg = err.Error()
			return m, nil
		}
	}
	prev, err := m.back()
	if err != nil {
		return m, flashErr(err.Error())
	}
	return m, tea.Batch(goTo(prev), flash("landing branches updated"))
}

// View implements screen.
func (m *intoEditor) View(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + styleDim.Render("session ") + styleLabel.Render(m.pkg+"/"+m.session) + "\n\n")
	b.WriteString(m.fields.view())
	b.WriteString("\n " + styleDim.Render("Each member's work lands on its branch when the session is finished.") + "\n")
	b.WriteString(" " + styleDim.Render("A branch that does not exist yet is created. Work already committed stays.") + "\n")
	if m.msg != "" {
		b.WriteString(" " + styleErr.Render(m.msg) + "\n")
	}
	b.WriteString("\n " + keyHelp("enter", "save", "tab", "next field", "esc", "cancel"))
	width := w - 8
	if width > 84 {
		width = 84
	}
	return dialog(m.Title(), b.String(), width)
}
