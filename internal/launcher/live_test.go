package launcher

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/doneill612/vmono/internal/herdr"
)

func TestLiveMapsHarnessesToSessions(t *testing.T) {
	root := t.TempDir()
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	f := &fakeHerdr{snap: herdr.Snapshot{Agents: []herdr.Pane{
		{Cwd: at("one"), ForegroundCwd: at("one", "alpha"), Status: herdr.Idle},
		{Cwd: at("one"), Status: herdr.Blocked},
		{Cwd: at("two"), Status: herdr.Unknown},
		{Cwd: root, Status: herdr.Working},
		{Cwd: at("..", "other", "one"), Status: herdr.Working},
		{Status: herdr.Working},
	}}}
	f.install(t)
	l, err := TakeLive()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := l.Sessions(root), map[string]string{"one": herdr.Blocked, "two": Running}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions = %v", got)
	}

	f.fail = map[string]error{"api snapshot": &herdr.Error{Code: herdr.CodeNotRunning}}
	if l, err := TakeLive(); err != nil || len(l.agents) != 0 {
		t.Errorf("a server that is down = %+v, %v", l, err)
	}

	tm := &fakeTmux{sessions: "pay/three\t" + at("three") + "\n\t" + at("mine") + "\n"}
	tm.install(t)
	if l, err = TakeLive(); err != nil || !reflect.DeepEqual(l.Sessions(root), map[string]string{"three": Running}) {
		t.Errorf("tmux = %+v, %v", l, err)
	}
}
