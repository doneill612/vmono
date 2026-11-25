package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// branchFields is a column of text inputs, one per member, holding the branch
// each active member works on when that is not the session's, or the ref each
// reference member is checked out at when that is not the package's. Repos need
// not share a branch name. The new-session prompt embeds one of each.
type branchFields struct {
	members []string
	inputs  []textinput.Model
}

// newBranchFields builds an empty field for each member, labeled with label and
// the member's name in a column of the given width.
func newBranchFields(label string, width int, members []string) branchFields {
	f := branchFields{members: members}
	for _, name := range members {
		in := newTextInput()
		in.Prompt = fmt.Sprintf("%-6s %-*s  ", label, width, name)
		in.CharLimit = 200
		// A text input with no width truncates its placeholder to one character.
		in.Width = 36
		f.inputs = append(f.inputs, in)
	}
	return f
}

// placeholder sets what the named member's field shows while it is empty.
func (f *branchFields) placeholder(member, text string) {
	for i, name := range f.members {
		if name == member {
			f.inputs[i].Placeholder = text
		}
	}
}

// count is the number of fields.
func (f *branchFields) count() int { return len(f.inputs) }

// focus gives the keyboard to field i and takes it from the rest. An index
// outside the fields only blurs them.
func (f *branchFields) focus(i int) tea.Cmd {
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
func (f *branchFields) update(i int, msg tea.Msg) tea.Cmd {
	if i < 0 || i >= len(f.inputs) {
		return nil
	}
	var cmd tea.Cmd
	f.inputs[i], cmd = f.inputs[i].Update(msg)
	return cmd
}

// set fills in the named member's field.
func (f *branchFields) set(member, text string) {
	for i, name := range f.members {
		if name == member {
			f.inputs[i].SetValue(text)
			f.inputs[i].CursorEnd()
		}
	}
}

// values returns what was typed for each member, leaving out empty fields, which
// stand for the session's branch or the package's ref.
func (f *branchFields) values() map[string]string {
	out := map[string]string{}
	for i, name := range f.members {
		if v := strings.TrimSpace(f.inputs[i].Value()); v != "" {
			out[name] = v
		}
	}
	return out
}

// view renders the fields, one per line.
func (f *branchFields) view() string {
	var b strings.Builder
	for _, in := range f.inputs {
		b.WriteString(" " + in.View() + "\n")
	}
	return b.String()
}
