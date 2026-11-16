package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// cmdBar is the one-line prompt opened by ":" (commands) and "/" (filter).
// It sits between the header and the frame while active.
type cmdBar struct {
	active bool
	mode   rune
	in     textinput.Model
}

// newCmdBar builds a closed bar.
func newCmdBar() cmdBar {
	in := newTextInput()
	in.CharLimit = 256
	return cmdBar{in: in}
}

// open shows the bar in a mode (':' or '/') with initial text and focuses it.
func (b *cmdBar) open(mode rune, initial string) tea.Cmd {
	b.active, b.mode = true, mode
	b.in.Prompt = string(mode) + " "
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

// view draws the bar. With ghost text, the dimmed rest of the command that
// tab would complete, the cursor sits on the first ghost character, as in a
// shell; the text input's own rendering is used otherwise.
func (b *cmdBar) view(w int, ghost string) string {
	if ghost == "" || b.in.Position() != len([]rune(b.in.Value())) {
		return clip(" "+styleAmber.Render(b.in.View()), w)
	}
	r := []rune(ghost)
	line := " " + styleAmber.Render(b.in.Prompt+b.in.Value()) +
		lipgloss.NewStyle().Reverse(true).Render(string(r[0])) + styleDim.Render(string(r[1:]))
	return clip(line, w)
}

// commandNames lists the commands in the order ghost text prefers them.
var commandNames = []string{"packages", "pkg", "sessions", "members", "repos", "harnesses", "config", "new", "help", "quit"}

// commandAliases maps short forms to command names.
var commandAliases = map[string]string{
	"p": "packages", "pkg": "packages", "mem": "members", "hs": "harnesses", "sess": "sessions",
	"h": "help", "?": "help", "q": "quit", "q!": "quit",
}

// candidates returns the full command lines that start with text.
func (a *App) candidates(text string) []string {
	text = strings.ToLower(strings.TrimLeft(text, " "))
	var out []string
	if cmd, arg, ok := strings.Cut(text, " "); ok {
		if full := commandAliases[cmd]; full == "members" || full == "sessions" || cmd == "members" || cmd == "sessions" {
			names := append([]string(nil), a.env.pkgNames...)
			sort.Strings(names)
			for _, n := range names {
				if strings.HasPrefix(strings.ToLower(n), strings.TrimLeft(arg, " ")) {
					out = append(out, cmd+" "+n)
				}
			}
		}
		return out
	}
	for _, c := range commandNames {
		if strings.HasPrefix(c, text) {
			out = append(out, c)
		}
	}
	return out
}

// ghost returns the text tab would add to the command being typed.
func (a *App) ghost() string {
	if a.bar.mode != ':' {
		return ""
	}
	text := a.bar.in.Value()
	if strings.TrimSpace(text) == "" {
		return ""
	}
	c := a.candidates(text)
	if len(c) == 0 {
		return ""
	}
	lowerPrefix := strings.ToLower(strings.TrimLeft(text, " "))
	full := c[0]
	if len(full) <= len(lowerPrefix) {
		return ""
	}
	return full[len(lowerPrefix):]
}

// updateBar handles a key while the bar is open.
func (a *App) updateBar(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		if a.bar.mode == '/' {
			if f, ok := a.top().(filterable); ok {
				f.setFilter("")
			}
		}
		a.bar.close()
		return nil
	case "enter":
		text := strings.TrimSpace(a.bar.in.Value())
		mode := a.bar.mode
		if mode == ':' {
			if g := a.ghost(); g != "" && text != "" {
				text += g
			}
		}
		a.bar.close()
		if mode == ':' {
			return a.runCommand(text)
		}
		return nil
	case "tab":
		if g := a.ghost(); g != "" {
			a.bar.in.SetValue(a.bar.in.Value() + g)
			a.bar.in.CursorEnd()
		}
		return nil
	}
	var cmd tea.Cmd
	a.bar.in, cmd = a.bar.in.Update(k)
	if a.bar.mode == '/' {
		if f, ok := a.top().(filterable); ok {
			f.setFilter(a.bar.in.Value())
		}
	}
	return cmd
}

// runCommand executes a command typed in the bar.
func (a *App) runCommand(text string) tea.Cmd {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	name := strings.ToLower(fields[0])
	if full, ok := commandAliases[name]; ok {
		name = full
	}
	args := fields[1:]
	switch name {
	case "sessions":
		pkg := ""
		if len(args) > 0 {
			pkg = args[0]
		} else if sel, ok := a.top().(packageSelector); ok {
			pkg = sel.selectedPackage()
		}
		if pkg == "" {
			return flashErr("usage: sessions <package>")
		}
		if a.env.pkgKnown && !contains(a.env.pkgNames, pkg) {
			return flashErr("no such package: " + pkg)
		}
		return a.navigate(newSessions(a.env, pkg))
	case "members":
		target := ""
		if len(args) > 0 {
			target = args[0]
		} else if sel, ok := a.top().(sessionSelector); ok {
			if pkg, session := sel.selectedSession(); pkg != "" && session != "" {
				target = pkg + "/" + session
			}
		} else if sel, ok := a.top().(packageSelector); ok {
			target = sel.selectedPackage()
		}
		if target == "" {
			return flashErr("usage: members <package>/<session>")
		}
		s, err := a.env.core.ResolveSession(target)
		if err != nil {
			return flashErr(err.Error())
		}
		return a.navigate(newMembers(a.env, s.Pkg.Name, s.Name))
	case "repos":
		return a.navigate(newRepos(a.env))
	case "harnesses":
		return a.navigate(newHarnesses(a.env))
	case "packages":
		return a.home()
	case "quit":
		return tea.Quit
	case "new":
		return a.navigate(newEditor(a.env, nil))
	case "config":
		return a.navigate(newReconfigure(a.env))
	case "help":
		return push(newHelp(a.top().Hints()))
	}
	return flashErr(fmt.Sprintf("unknown command: %s", fields[0]))
}

// contains reports whether list has s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
