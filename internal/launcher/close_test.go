package launcher

import (
	"strings"
	"testing"
)

func TestCloseSessionKillsItsTmuxSession(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, sessions, tmux, here string
		kill                       string
		note                       string
	}{
		{name: "tagged", sessions: "$1\t\t/x\n$3\tpay/work\t" + dir + "\n", kill: "$3", note: `closed tmux session "pay/work"`},
		{name: "another package root", sessions: "$3\tpay/work\t/elsewhere\n"},
		{name: "untagged", sessions: "$3\t\t" + dir + "\n"},
		{name: "from another session", sessions: "$3\tpay/work\t" + dir, tmux: "sock", here: "$1", kill: "$3", note: "closed"},
		{name: "from inside", sessions: "$3\tpay/work\t" + dir, tmux: "sock", here: "$3", note: "vmono is running inside it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMUX", tc.tmux)
			f := &fakeTmux{sessions: tc.sessions, here: tc.here}
			f.install(t)
			notes := strings.Join(CloseSession("pay", "work", dir), "\n")
			if tc.note == "" && notes != "" || !strings.Contains(notes, tc.note) {
				t.Errorf("notes = %q", notes)
			}
			kill := f.call("kill-session")
			if tc.kill == "" && kill != nil || tc.kill != "" && (kill == nil || kill[2] != tc.kill) {
				t.Errorf("kill = %v", kill)
			}
		})
	}
}
