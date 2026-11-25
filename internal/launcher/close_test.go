package launcher

import (
	"strings"
	"testing"
)

func TestCloseSessionKillsItsTmuxSession(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, sessions, tmux, here string
		author                     string
		kill                       string
		note                       string
		leaves                     bool
	}{
		{name: "tagged", sessions: "$1\t\t/x\n$3\tpay/work\t" + dir + "\n", kill: "$3", note: `closed tmux session "pay/work"`},
		{name: "another package root", sessions: "$3\tpay/work\t/elsewhere\n"},
		{name: "untagged", sessions: "$3\t\t" + dir + "\n"},
		{name: "from another session", sessions: "$3\tpay/work\t" + dir, tmux: "sock", here: "$1", kill: "$3", note: "closed"},
		{name: "from inside", sessions: "$3\tpay/work\t" + dir, tmux: "sock", here: "$3", kill: "$3", note: "closed", leaves: true},
		{name: "by the agent inside", sessions: "$3\tpay/work\t" + dir, tmux: "sock", here: "$3", author: "agent:claude", note: "vmono is running inside it"},
		{name: "by an agent elsewhere", sessions: "$3\tpay/work\t" + dir, tmux: "sock", here: "$1", author: "agent:claude", kill: "$3", note: "closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMUX", tc.tmux)
			t.Setenv("VMONO_AUTHOR", tc.author)
			f := &fakeTmux{sessions: tc.sessions, here: tc.here}
			f.install(t)
			notes := strings.Join(closeNow("pay", "work", dir), "\n")
			if tc.note == "" && notes != "" || !strings.Contains(notes, tc.note) {
				t.Errorf("notes = %q", notes)
			}
			kill := f.call("kill-session")
			if tc.kill == "" && kill != nil || tc.kill != "" && (kill == nil || kill[2] != tc.kill) {
				t.Errorf("kill = %v", kill)
			}
			if sw := f.call("switch-client"); (sw != nil) != tc.leaves {
				t.Errorf("switch = %v", sw)
			}
			if in := InsideOf("pay", "work", dir); (in != "") != tc.leaves {
				t.Errorf("inside of %q", in)
			}
		})
	}
}

// closeNow finds a session and closes it straight away.
func closeNow(pkg, session, dir string) []string {
	if f, _ := FindSession(pkg, session, dir); f != nil {
		return f()
	}
	return nil
}

func TestFindSessionClosesNothingYet(t *testing.T) {
	dir := t.TempDir()
	f := &fakeTmux{sessions: "$3\tpay/work\t" + dir}
	f.install(t)
	t.Setenv("TMUX", "")
	if find, inside := FindSession("pay", "work", dir); find == nil || inside || f.call("kill-session") != nil {
		t.Errorf("calls = %v", f.calls)
	}
}
