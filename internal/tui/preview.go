package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/terms"
)

// previewMinW is the terminal width from which a list view shows a preview of
// its highlighted row beside it. Narrower terminals get the list alone.
const previewMinW = 100

// previewDelay is how long the highlight must rest on a row before its
// preview is loaded, so scrolling down a list does not load every row on the
// way. Tests set it to zero.
var previewDelay = 120 * time.Millisecond

// previewer is implemented by list screens that can show the highlighted row
// beside the list. previewSync is called after every message the screen
// handles and starts loading whatever the preview needs; previewView draws
// the pane, whose first line is its heading.
type previewer interface {
	previewSync() tea.Cmd
	previewView(w, h int) string
}

// Messages of the preview pipeline. owner identifies the screen they belong
// to, since they are broadcast to the whole stack.
type (
	// previewDueMsg says the highlight has rested on id for previewDelay.
	previewDueMsg struct {
		owner screen
		id    string
	}
	// previewMsg delivers what a row's preview shows. build renders it for a
	// width, so it re-wraps when the pane is resized.
	previewMsg struct {
		owner screen
		id    string
		build func(w int) string
		err   error
	}
)

// previewPane is the state a list screen keeps for its preview: which row
// the highlight is on, and what has been loaded for the rows seen so far.
// Loaded previews are kept until the screen reloads, so moving back to a row
// is instant. While the highlighted row's preview loads, the pane shows the
// shared spinner; spin is created on first use so the zero value works, and
// ticking is true while a tick chain is running.
type previewPane struct {
	want    string
	built   map[string]func(w int) string
	errs    map[string]string
	spin    spinner.Model
	ticking bool
}

// pending reports whether the highlighted row's preview is still to be
// loaded, which is when the pane shows its spinner.
func (p *previewPane) pending() bool {
	if p.want == "" {
		return false
	}
	if _, ok := p.built[p.want]; ok {
		return false
	}
	_, failed := p.errs[p.want]
	return !failed
}

// startSpin makes sure the spinner is created and ticking, and returns the
// command that starts its tick chain, or nil when one is already running.
func (p *previewPane) startSpin() tea.Cmd {
	if p.spin.ID() == 0 {
		p.spin = newSpinner()
	}
	if p.ticking {
		return nil
	}
	p.ticking = true
	return p.spin.Tick
}

// sync notes the row the highlight is on and, when nothing is known about it
// yet, schedules its load after previewDelay. id is empty when there is no
// row.
func (p *previewPane) sync(owner screen, id string) tea.Cmd {
	if id == p.want {
		return nil
	}
	p.want = id
	if id == "" {
		return nil
	}
	if _, ok := p.built[id]; ok {
		return nil
	}
	if previewDelay <= 0 {
		return tea.Batch(func() tea.Msg { return previewDueMsg{owner, id} }, p.startSpin())
	}
	return tea.Batch(tea.Tick(previewDelay, func(time.Time) tea.Msg { return previewDueMsg{owner, id} }), p.startSpin())
}

// handle advances the pipeline for a message and reports whether it was one
// of the preview's. load starts loading a row; it is only called for the row
// the highlight is still on.
func (p *previewPane) handle(msg tea.Msg, owner screen, load func(id string) tea.Cmd) (tea.Cmd, bool) {
	switch t := msg.(type) {
	case spinner.TickMsg:
		if p.spin.ID() == 0 || t.ID != p.spin.ID() {
			return nil, false
		}
		if !p.pending() {
			p.ticking = false
			return nil, true
		}
		var cmd tea.Cmd
		p.spin, cmd = p.spin.Update(t)
		return cmd, true
	case previewDueMsg:
		if t.owner != owner {
			return nil, false
		}
		if _, ok := p.built[t.id]; ok || t.id != p.want {
			return nil, true
		}
		return load(t.id), true
	case previewMsg:
		if t.owner != owner {
			return nil, false
		}
		if t.err != nil {
			p.setErr(t.id, t.err.Error())
			return nil, true
		}
		if p.built == nil {
			p.built = map[string]func(int) string{}
		}
		p.built[t.id] = t.build
		delete(p.errs, t.id)
		return nil, true
	case refreshMsg:
		p.built, p.errs, p.want = nil, nil, ""
	}
	return nil, false
}

// setErr records why a row's preview could not be loaded.
func (p *previewPane) setErr(id, err string) {
	if p.errs == nil {
		p.errs = map[string]string{}
	}
	p.errs[id] = err
}

// view draws the pane for the row the highlight is on: its heading, then the
// loaded body, or a placeholder while it loads.
func (p *previewPane) view(id string, w, h int) string {
	if id == "" {
		return ""
	}
	head := " " + styleLabel.Render(truncate(id, w-2))
	if e, ok := p.errs[id]; ok {
		return head + "\n\n " + styleErr.Render(truncate(e, w-2))
	}
	build, ok := p.built[id]
	if !ok {
		if p.spin.ID() == 0 {
			p.spin = newSpinner()
		}
		return head + "\n\n " + styleDim.Render(scanningLabel(p.spin, "loading…"))
	}
	return head + "\n" + build(w)
}

// loadPreview returns a command that builds a row's preview off the UI
// goroutine and reports it as a previewMsg.
func loadPreview(owner screen, id string, build func() (func(w int) string, error)) tea.Cmd {
	return func() tea.Msg {
		b, err := build()
		return previewMsg{owner: owner, id: id, build: b, err: err}
	}
}

// previewField is one "label  value" line of a preview, with the value
// wrapped under itself. Empty values are left out.
func previewField(label, value string, w int) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	const labelW = 12
	lines := strings.Split(wrap(value, max(w-labelW-2, 10)), "\n")
	var b strings.Builder
	for i, l := range lines {
		if i == 0 {
			b.WriteString(" " + styleDim.Render(fit(label, labelW, false)) + truncate(l, w-labelW-2) + "\n")
		} else {
			b.WriteString(" " + strings.Repeat(" ", labelW) + truncate(l, w-labelW-2) + "\n")
		}
	}
	return b.String()
}

// previewSection is a dim section heading with a blank line above it.
func previewSection(title string) string { return "\n " + styleHeaderCol.Render(title) + "\n" }

// sessionStateLabel is the short coloured state of a session in a list.
func sessionStateLabel(s core.SessionSummary) string {
	c, _ := sessionState(s)
	return c.style.Render(c.text)
}

// initState says whether a package has an overview and whether it still covers
// the members.
func initState(m *manifest.Manifest) string {
	if m.Init == nil {
		return "none; i initializes"
	}
	if drift := m.DriftText(); drift != "" {
		return "out of date: " + drift
	}
	return "recorded " + ago(m.Init.At)
}

// buildPackagePreview renders a package for the preview pane: its purpose,
// harness, sessions and members.
func buildPackagePreview(p *core.Package, sessions []core.SessionSummary) func(w int) string {
	return func(w int) string {
		var b strings.Builder
		b.WriteString("\n")
		if purpose := strings.TrimSpace(p.M.Purpose); purpose != "" {
			lines := strings.Split(wrap(purpose, w-2), "\n")
			if len(lines) > 4 {
				lines = append(lines[:3], truncate(lines[3], w-5)+"…")
			}
			for _, l := range lines {
				b.WriteString(" " + l + "\n")
			}
			b.WriteString("\n")
		}
		h := p.M.Harness
		if h == "" {
			h = "default"
		}
		b.WriteString(previewField("harness", h, w))
		b.WriteString(previewField("path", paths.Contract(p.Dir), w))
		b.WriteString(previewField("overview", initState(p.M), w))
		if n := len(p.M.Pending()); n > 0 {
			b.WriteString(previewField("suggested", fmt.Sprintf("%d to review; v", n), w))
		}
		b.WriteString(previewSection("SESSIONS"))
		if len(sessions) == 0 {
			b.WriteString(" " + styleDim.Render("none yet") + "\n")
		}
		for _, s := range sessions {
			b.WriteString(" " + fit(s.Name, 18, false) + " " + sessionStateLabel(s) + "\n")
		}
		b.WriteString(previewSection("MEMBERS"))
		for _, mem := range p.M.Members {
			mode := terms.ModeLabel(mem.Mode, false)
			b.WriteString(" " + fit(mem.Name, 18, false) + " " + styleDim.Render(mode) + "\n")
			if mem.Role != "" {
				b.WriteString("   " + styleDim.Render(truncate(mem.Role, w-5)) + "\n")
			}
		}
		return b.String()
	}
}

// buildSessionPreview renders one session for the preview pane: its branch,
// state, and each member's mode, branch or ref and working tree state.
func buildSessionPreview(sum core.SessionSummary, st core.SessionStatus) func(w int) string {
	return func(w int) string {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(previewField("branch", st.Branch, w))
		b.WriteString(" " + styleDim.Render(fit("state", 12, false)) + sessionStateLabel(sum) + "\n")
		if t, err := time.Parse(time.RFC3339, sum.LastOpened); err == nil {
			b.WriteString(previewField("opened", ago(t), w))
		}
		b.WriteString(previewField("to push", unpushedSummary(st.Members), w))
		if sum.State == core.SessionMerged {
			b.WriteString(previewField("done", "everything is merged; ctrl+d removes the session", w))
		}
		b.WriteString(previewField("path", paths.Contract(sum.Path), w))
		b.WriteString(previewSection("MEMBERS"))
		for _, ms := range st.Members {
			b.WriteString(memberPreviewLine(ms, w))
		}
		return b.String()
	}
}

// unpushedSummary says how much of a session's committed work is not on its
// remotes: the commits to push and how many members they are in, or that
// nothing is.
func unpushedSummary(members []core.MemberStatus) string {
	commits, in, active := 0, 0, 0
	for _, ms := range members {
		if ms.Mode == manifest.ModeActive {
			active++
		}
		if ms.Unpushed > 0 {
			commits += ms.Unpushed
			in++
		}
	}
	if commits == 0 {
		return "nothing"
	}
	noun := "commits"
	if commits == 1 {
		noun = "commit"
	}
	return fmt.Sprintf("%d %s in %d of %d members (P)", commits, noun, in, active)
}

// memberPreviewLine is one member of a session: name, mode, where it is
// checked out and its working tree state.
func memberPreviewLine(ms core.MemberStatus, w int) string {
	mode := terms.ModeLabel(ms.Mode, false)
	where := ms.Branch
	if where == "" {
		where = ms.Ref
	}
	line := " " + fit(ms.Name, 14, false) + " " + styleDim.Render(fit(mode, 9, false)) + " " + memberState(ms)
	if where != "" {
		line += "\n   " + styleDim.Render(truncate(where, w-5))
	}
	if text := ms.PublishText(); text != "" {
		line += "\n   " + styleDim.Render(truncate(text, w-5))
	}
	return line + "\n"
}

// buildMemberPreview renders a member from the status the members view has
// already loaded.
func buildMemberPreview(ms core.MemberStatus, w int) string {
	var b strings.Builder
	b.WriteString("\n")
	mode := terms.ModeLabel(ms.Mode, true)
	where := ms.Branch
	if where == "" {
		where = ms.Ref
	}
	b.WriteString(previewField("mode", mode, w))
	b.WriteString(previewField("checked out", where, w))
	b.WriteString(previewField("head", ms.Head, w))
	if ms.Missing {
		b.WriteString(" " + styleErr.Render("worktree missing; sync recreates it") + "\n")
	} else if ms.Error != "" {
		b.WriteString(" " + styleErr.Render(truncate(ms.Error, w-2)) + "\n")
	} else {
		b.WriteString(previewField("changes", fmt.Sprintf("%d modified, %d untracked", ms.Dirty, ms.Untracked), w))
		if ms.Mode == manifest.ModeActive {
			b.WriteString(previewField("built on", ms.BuiltOn, w))
			b.WriteString(previewField("commits", fmt.Sprint(ms.OwnCommits), w))
			b.WriteString(previewField("published", ms.PublishText(), w))
		}
	}
	b.WriteString(previewField("role", ms.Role, w))
	return b.String()
}

// splitColumns lays two blocks of lines side by side, the left exactly lw
// cells wide, a dim rule between them, and the right clipped to rw cells.
func splitColumns(left, right string, lw, rw, h int) string {
	l, r := strings.Split(left, "\n"), strings.Split(right, "\n")
	rule := styleFrame.Render("│")
	out := make([]string, h)
	for i := range out {
		a, b := "", ""
		if i < len(l) {
			a = l[i]
		}
		if i < len(r) {
			b = r[i]
		}
		out[i] = clip(a, lw) + rule + clip(b, rw)
	}
	return strings.Join(out, "\n")
}
