package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// refFields is a column of text inputs, one per reference member, holding what
// the member is checked out at when that is not the package's ref. The
// new-session prompt embeds one.
type refFields struct {
	members []string
	inputs  []textinput.Model
	// picked is the branch last chosen in the picker for each member.
	picked map[string]pickedBranch
}

// newRefFields builds an empty field for each member, labeled read-only with the
// member's name in a column of the given width.
func newRefFields(width int, members []string) refFields {
	f := refFields{members: members, picked: map[string]pickedBranch{}}
	for _, name := range members {
		in := newTextInput()
		in.Prompt = fmt.Sprintf("read-only %-*s  ", width, name)
		in.CharLimit = 200
		// A text input with no width truncates its placeholder to one character.
		in.Width = 36
		f.inputs = append(f.inputs, in)
	}
	return f
}

// placeholder sets what the named member's field shows while it is empty.
// The input counts its prompt against its width when showing a placeholder, so
// the field is widened for one that a long member name would otherwise cut.
func (f *refFields) placeholder(member, text string) {
	for i, name := range f.members {
		if name == member {
			f.inputs[i].Placeholder = text
			f.inputs[i].Width = max(f.inputs[i].Width, lipgloss.Width(f.inputs[i].Prompt)+lipgloss.Width(text)+1)
		}
	}
}

// count is the number of fields.
func (f *refFields) count() int { return len(f.inputs) }

// focus gives the keyboard to field i and takes it from the rest. An index
// outside the fields only blurs them.
func (f *refFields) focus(i int) tea.Cmd {
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
func (f *refFields) update(i int, msg tea.Msg) tea.Cmd {
	if i < 0 || i >= len(f.inputs) {
		return nil
	}
	var cmd tea.Cmd
	f.inputs[i], cmd = f.inputs[i].Update(msg)
	return cmd
}

// pick fills in the named member's field with a branch chosen in the picker,
// or empties it for none.
func (f *refFields) pick(member string, branch pickedBranch) {
	for i, name := range f.members {
		if name == member {
			f.inputs[i].SetValue(branch.name)
			f.inputs[i].CursorEnd()
			f.picked[member] = branch
		}
	}
}

// standing is the picked branch field i still holds. Typing over it makes the
// field's text what counts.
func (f *refFields) standing(i int) (pickedBranch, bool) {
	p, ok := f.picked[f.members[i]]
	return p, ok && p.ref != "" && f.inputs[i].Value() == p.name
}

// values returns what each member is to be checked out at, leaving out empty
// fields, which stand for the package's ref: the exact ref of a picked branch,
// and otherwise what was typed.
func (f *refFields) values() map[string]string {
	out := map[string]string{}
	for i, name := range f.members {
		if p, ok := f.standing(i); ok {
			out[name] = p.ref
		} else if v := strings.TrimSpace(f.inputs[i].Value()); v != "" {
			out[name] = v
		}
	}
	return out
}

// view renders the fields, one per line. A field holding a pick shows it the
// way the build on rows do, until it is typed over.
func (f *refFields) view() string {
	var b strings.Builder
	for i, in := range f.inputs {
		line := in.View()
		if p, ok := f.standing(i); ok {
			line = in.PromptStyle.Render(in.Prompt) + p.render(false)
			if in.Focused() {
				line = styleSelected.Render(in.Prompt) + p.render(false) + styleDim.Render("  space to pick")
			}
		}
		b.WriteString(" " + line + "\n")
	}
	return b.String()
}
