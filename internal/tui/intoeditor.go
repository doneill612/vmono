package tui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// intoEditor is the dialog that changes the branch each member's work lands
// on. It is opened with i from the sessions view, where it sits above the
// list and closing it reveals the list reloaded, or with i from the finish
// confirm, where it takes the confirm's place and both saving and cancelling
// return to a confirm built from a fresh plan, so the rows always show the
// branches now in effect.
type intoEditor struct {
	e            *env
	pkg, session string
	fields       intoFields
	focus        int
	msg          string
	// back builds the screen that replaces the editor when it closes. Nil
	// means the editor was pushed, so closing it pops it.
	back func() (screen, error)
}

// newIntoEditor builds the editor from each member's name and current landing
// branch, listed in the order the fields should appear. back is described on
// the intoEditor type.
func newIntoEditor(e *env, pkg, session string, names []string, current map[string]string, back func() (screen, error)) *intoEditor {
	return &intoEditor{e: e, pkg: pkg, session: session, fields: newIntoFields(names, current), back: back}
}

// newIntoEditorFromPlan builds the editor opened from the finish confirm. Its
// plan's rows carry each member's current landing branch, and closing it
// returns to a finish confirm built from the session as it is then.
func newIntoEditorFromPlan(e *env, pkg, session string, plan core.FinishResult) *intoEditor {
	var names []string
	current := map[string]string{}
	for _, r := range plan.Members {
		names = append(names, r.Name)
		current[r.Name] = r.Into
	}
	back := func() (screen, error) {
		s, err := e.core.ResolveSession(pkg + "/" + session)
		if err != nil {
			return nil, err
		}
		return newFinishConfirm(e, pkg, session, e.core.PlanFinish(s, core.FinishOptions{})), nil
	}
	return newIntoEditor(e, pkg, session, names, current, back)
}

// newIntoEditorFor builds the editor opened from the sessions view, from the
// landing branches the session records now. It returns an error when the
// session cannot be read or has no active member to set a branch for.
func newIntoEditorFor(e *env, pkg, session string) (*intoEditor, error) {
	s, err := e.core.ResolveSession(pkg + "/" + session)
	if err != nil {
		return nil, err
	}
	var names []string
	current := map[string]string{}
	for _, m := range e.core.Status(s).Members {
		if m.Mode == manifest.ModeActive {
			names = append(names, m.Name)
			current[m.Name] = m.Into
		}
	}
	if len(names) == 0 {
		return nil, errors.New("session " + pkg + "/" + session + " has no active members")
	}
	return newIntoEditor(e, pkg, session, names, current, nil), nil
}

// leave closes the editor: it pops it when it was pushed, and otherwise
// replaces it with the screen back builds.
func (m *intoEditor) leave(extra ...tea.Cmd) (screen, tea.Cmd) {
	if m.back == nil {
		return m, tea.Batch(append([]tea.Cmd{pop(m)}, extra...)...)
	}
	prev, err := m.back()
	if err != nil {
		return m, flashErr(err.Error())
	}
	return m, tea.Batch(append([]tea.Cmd{goTo(m, prev)}, extra...)...)
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
		return m.leave()
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

// save records the branches typed and closes the editor. An empty field
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
	return m.leave(flash("landing branches updated"))
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
