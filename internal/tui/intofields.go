package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// intoFields is a column of text inputs, one per active member, holding the
// branch each member's work lands on when its session is finished. Landing
// branches are per member because repos need not share a branch name. The
// new-session prompt and the landing-branch editor both embed one.
type intoFields struct {
	members []string
	inputs  []textinput.Model
}

// newIntoFields builds a field for each member, filled from values (by member
// name). A member with no value shows its base branch as the placeholder.
func newIntoFields(members []string, values map[string]string) intoFields {
	f := intoFields{members: members}
	width := 0
	for _, name := range members {
		if len(name) > width {
			width = len(name)
		}
	}
	for _, name := range members {
		in := newTextInput()
		in.Prompt = fmt.Sprintf("into %-*s  ", width, name)
		in.CharLimit = 200
		// A text input with no width truncates its placeholder to one character.
		in.Width = 36
		in.Placeholder = "its base branch"
		in.SetValue(values[name])
		in.CursorEnd()
		f.inputs = append(f.inputs, in)
	}
	return f
}

// count is the number of fields.
func (f *intoFields) count() int { return len(f.inputs) }

// focus gives the keyboard to field i and takes it from the rest. An index
// outside the fields only blurs them.
func (f *intoFields) focus(i int) tea.Cmd {
	var cmd tea.Cmd
	for j := range f.inputs {
		if j == i {
			cmd = f.inputs[j].Focus()
		} else {
			f.inputs[j].Blur()
		}
	}
	return cmd
}

// update passes a message to field i.
func (f *intoFields) update(i int, msg tea.Msg) tea.Cmd {
	if i < 0 || i >= len(f.inputs) {
		return nil
	}
	var cmd tea.Cmd
	f.inputs[i], cmd = f.inputs[i].Update(msg)
	return cmd
}

// values returns the branch typed for each member, leaving out empty fields,
// which stand for the member's default.
func (f *intoFields) values() map[string]string {
	out := map[string]string{}
	for i, name := range f.members {
		if v := strings.TrimSpace(f.inputs[i].Value()); v != "" {
			out[name] = v
		}
	}
	return out
}

// view renders the fields, one per line.
func (f *intoFields) view() string {
	var b strings.Builder
	for _, in := range f.inputs {
		b.WriteString(" " + in.View() + "\n")
	}
	return b.String()
}
