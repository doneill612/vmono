package herdr

import (
	"os"
	"path/filepath"
	"testing"
)

// answer makes Run print a file from testdata.
func answer(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	orig := Run
	Run = func(...string) ([]byte, error) { return data, nil }
	t.Cleanup(func() { Run = orig })
}

func TestParseRealOutput(t *testing.T) {
	answer(t, "snapshot.json")
	s, err := TakeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Workspaces) != 2 || s.Workspaces[0].Tokens[Token] != "pay/work" || s.Workspaces[0].Status != Blocked || s.Workspaces[1].Tokens[Token] != "" {
		t.Errorf("workspaces = %+v", s.Workspaces)
	}
	if len(s.Agents) != 1 || s.Agents[0].WorkspaceID != "w1" || s.Agents[0].Agent != "claude" || s.Agents[0].ForegroundCwd != "/pk/pay/work/alpha" {
		t.Errorf("agents = %+v", s.Agents)
	}

	answer(t, "create.json")
	if id, pane, err := CreateWorkspace("/x", "y"); err != nil || id != "w1" || pane != "w1:p1" {
		t.Errorf("create = %q, %q, %v", id, pane, err)
	}
	answer(t, "process_info.json")
	if at, err := AtPrompt("w1:p1"); err != nil || !at {
		t.Errorf("at prompt = %v, %v", at, err)
	}
	if e := parseError([]byte("unknown option: x")); e != nil {
		t.Errorf("plain text parsed as %+v", e)
	}
	data, _ := os.ReadFile(filepath.Join("testdata", "error.json"))
	if e := parseError(data); e == nil || !IsCode(e, CodeNotRunning) {
		t.Errorf("error = %+v", e)
	}
}

func TestUnexpectedOutputIsAnError(t *testing.T) {
	orig := Run
	defer func() { Run = orig }()
	Run = func(...string) ([]byte, error) { return []byte("hello"), nil }
	if _, err := TakeSnapshot(); err == nil {
		t.Error("accepted")
	}
}
