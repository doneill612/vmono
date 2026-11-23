package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// cmdBar is the one-line prompt opened by "/" to filter the current list. It
// sits between the header and the frame while active.
type cmdBar struct {
	active bool
	in     textinput.Model
}

// newCmdBar builds a closed bar.
func newCmdBar() cmdBar {
	in := newTextInput()
	in.CharLimit = 256
	in.Prompt = "/ "
	return cmdBar{in: in}
}

// open shows the bar with initial text and focuses it.
func (b *cmdBar) open(initial string) tea.Cmd {
	b.active = true
	b.in.SetValue(initial)
	b.in.CursorEnd()
	return b.in.Focus()
}

// close hides the bar.
func (b *cmdBar) close() {
	b.active = false
	b.in.Blur()
	b.in.SetValue("")
}

// view draws the bar.
func (b *cmdBar) view(w int) string {
	return clip(" "+styleAmber.Render(b.in.View()), w)
}

// updateBar handles a key while the filter bar is open. The list narrows as
// the text changes; enter keeps the filter and esc clears it.
func (a *App) updateBar(k tea.KeyMsg) tea.Cmd {
	f, _ := a.top().(filterable)
	switch k.String() {
	case "esc":
		if f != nil {
			f.setFilter("")
		}
		a.bar.close()
		return nil
	case "enter":
		a.bar.close()
		return nil
	}
	var cmd tea.Cmd
	a.bar.in, cmd = a.bar.in.Update(k)
	if f != nil {
		f.setFilter(a.bar.in.Value())
	}
	return cmd
}
