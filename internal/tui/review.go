package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// reviewModel is where a person accepts, edits or dismisses the setup and
// teardown commands an agent suggested while initializing a package. Every
// decision is written at once, so leaving keeps what was decided and leaves
// the rest pending.
//
// Commands are always shown whole, since accepting one means running it.
type reviewModel struct {
	e   *env
	pkg string
	// only is set when the review is all the TUI was opened for, so that it
	// closes the TUI when it ends.
	only    bool
	loaded  bool
	pending []manifest.Suggestion
	cursor  int
	offset  int
	// fits is whether the last view showed every suggestion in full.
	fits    bool
	input   textinput.Model
	editing bool
	// busy is set while a decision is written; keys that decide wait for it.
	busy bool
	err  string
}

// reviewMsg delivers the suggestions that remain to the review of pkg. done
// says what was just settled.
type reviewMsg struct {
	pkg     string
	pending []manifest.Suggestion
	done    string
	err     error
}

// newReview builds the review of a package's suggestions.
func newReview(e *env, pkg string) *reviewModel {
	in := newTextInput()
	in.Prompt = "$ "
	return &reviewModel{e: e, pkg: pkg, input: in}
}

// Review opens the TUI on the suggestions of one package and returns when the
// user is done with them. notice is shown on arrival.
func Review(c *core.Core, pkg, notice string) error {
	_, err := tea.NewProgram(newReviewApp(c, pkg, notice), tea.WithAltScreen()).Run()
	return err
}

// newReviewApp builds an app that holds nothing but a review.
func newReviewApp(c *core.Core, pkg, notice string) *App {
	e := &env{core: c}
	m := newReview(e, pkg)
	m.only = true
	return &App{env: e, bar: newCmdBar(), notice: notice, stack: []screen{m}}
}

// Init implements screen.
func (m *reviewModel) Init() tea.Cmd { return m.settle("", nil) }

// settle writes decisions, if any, and loads what is still pending.
func (m *reviewModel) settle(done string, decisions []core.SuggestionDecision) tea.Cmd {
	c, pkg := m.e.core, m.pkg
	m.busy = true
	return func() tea.Msg {
		p, err := c.Load(pkg)
		if err != nil {
			return reviewMsg{pkg: pkg, err: err}
		}
		err = c.ReviewSuggestions(p, decisions)
		return reviewMsg{pkg: pkg, pending: p.M.Pending(), done: done, err: err}
	}
}

// leave closes the review: the whole TUI when that is all it was opened for.
func (m *reviewModel) leave() tea.Cmd {
	if m.only {
		return tea.Quit
	}
	return pop(m)
}

// capturing implements capturing: the command being edited has the keyboard.
func (m *reviewModel) capturing() bool { return m.editing }

// Title implements screen.
func (m *reviewModel) Title() string {
	return frameTitle("suggested commands", m.pkg, "", len(m.pending))
}

// Crumb implements screen.
func (m *reviewModel) Crumb() string { return "review" }

// Hints implements screen.
func (m *reviewModel) Hints() []hint {
	if m.editing {
		return []hint{{"enter", "Accept this command"}, {"esc", "Cancel"}}
	}
	return []hint{{"a", "Accept"}, {"e", "Edit"}, {"x", "Dismiss"}, {"A", "Accept all"}, {"q", "Later"}}
}

// Update implements screen.
func (m *reviewModel) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch t := msg.(type) {
	case refreshMsg:
		return m, m.settle("", nil)
	case reviewMsg:
		if t.pkg != m.pkg {
			return m, nil
		}
		m.busy = false
		if t.err != nil {
			if !m.loaded {
				m.err = t.err.Error()
			} else if t.pending != nil {
				m.pending = t.pending
			}
			m.cursor = max(min(m.cursor, len(m.pending)-1), 0)
			return m, flashErr(t.err.Error())
		}
		first := !m.loaded
		m.loaded, m.pending, m.err = true, t.pending, ""
		m.cursor = max(min(m.cursor, len(m.pending)-1), 0)
		switch {
		case len(m.pending) > 0 && t.done != "":
			return m, flash(t.done)
		case len(m.pending) > 0 || first && m.only:
			return m, nil
		case m.only:
			return m, tea.Quit
		case t.done == "":
			return m, nil
		}
		return m, tea.Batch(flash(t.done+"; all reviewed"), pop(m))
	case tea.KeyMsg:
		if m.editing {
			return m.editKey(t)
		}
		return m.key(t)
	}
	return m, nil
}

// key handles the review's keys.
func (m *reviewModel) key(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		return m, m.leave()
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		return m, nil
	case "down", "j":
		m.cursor = max(min(m.cursor+1, len(m.pending)-1), 0)
		return m, nil
	}
	if len(m.pending) == 0 || m.busy {
		return m, nil
	}
	s := m.pending[m.cursor]
	switch k.String() {
	case "a":
		return m, m.settle("added to "+s.Member+"'s "+s.Kind, []core.SuggestionDecision{{ID: s.ID(), Accept: true}})
	case "x":
		return m, m.settle("dismissed", []core.SuggestionDecision{{ID: s.ID()}})
	case "A":
		if !m.fits {
			return m, flashErr("they do not all fit on the screen, so accept them one at a time")
		}
		var all []core.SuggestionDecision
		for _, s := range m.pending {
			all = append(all, core.SuggestionDecision{ID: s.ID(), Accept: true})
		}
		return m, m.settle(fmt.Sprintf("accepted %d", len(all)), all)
	case "e":
		m.editing = true
		m.input.SetValue(s.Command)
		m.input.CursorEnd()
		return m, m.input.Focus()
	}
	return m, nil
}

// editKey handles the keys while a command is edited before accepting it.
func (m *reviewModel) editKey(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.editing = false
		m.input.Blur()
		return m, nil
	case "enter":
		command := strings.TrimSpace(m.input.Value())
		if command == "" {
			return m, flashErr("the command is empty; esc cancels, x dismisses it")
		}
		m.editing = false
		m.input.Blur()
		s := m.pending[m.cursor]
		return m, m.settle("added to "+s.Member+"'s "+s.Kind, []core.SuggestionDecision{{ID: s.ID(), Accept: true, Command: command}})
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

// View implements screen.
func (m *reviewModel) View(w, h int) string {
	switch {
	case m.err != "":
		return "\n " + styleErr.Render(m.err)
	case !m.loaded:
		return ""
	case len(m.pending) == 0:
		return "\n " + styleDim.Render("Nothing to review.")
	}
	lines := []string{"", " " + styleDim.Render(truncate("Nothing runs until you accept it. Accepted commands run as you, outside the harness's sandbox.", w-2)), ""}
	head := len(lines)
	var top, bottom int
	for i, s := range m.pending {
		start := len(lines)
		when := "setup, in each new worktree"
		if s.Kind == manifest.SuggestTeardown {
			when = "teardown, before a worktree is removed"
		}
		mark, name := "  ", styleLabel.Render(s.Member)
		if i == m.cursor {
			mark, name = styleSelected.Render("▸ "), styleSelected.Render(s.Member)
		}
		lines = append(lines, " "+mark+name+styleDim.Render("  "+when))
		if i == m.cursor && m.editing {
			m.input.Width = max(w-9, 10)
			lines = append(lines, "     "+m.input.View())
		} else {
			for n, l := range strings.Split(hardWrap(s.Command, max(w-9, 10)), "\n") {
				prompt := "$ "
				if n > 0 {
					prompt = "  "
				}
				lines = append(lines, "     "+styleDim.Render(prompt)+l)
			}
		}
		if s.Reason != "" {
			lines = append(lines, "       "+styleDim.Render(truncate(s.Reason, w-9)))
		}
		lines = append(lines, "")
		if i == m.cursor {
			top, bottom = start, len(lines)
		}
	}
	if m.fits = len(lines) <= h; m.fits {
		m.offset = 0
		return strings.Join(lines, "\n")
	}
	body, room := lines[head:], max(h-head, 1)
	top, bottom = top-head, bottom-head
	m.offset = max(m.offset, bottom-room)
	m.offset = max(min(m.offset, top, len(body)-room), 0)
	return strings.Join(append(lines[:head:head], body[m.offset:min(m.offset+room, len(body))]...), "\n")
}
