package launcher

import (
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/herdr"
)

// fakeHerdr records every herdr command and answers from a snapshot. busy
// lists panes whose shell is not at its prompt.
type fakeHerdr struct {
	snap  herdr.Snapshot
	busy  []string
	fail  map[string]error
	calls []string
}

// install swaps the herdr seams for the duration of a test.
func (f *fakeHerdr) install(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("herdr is not offered on Windows")
	}
	for _, k := range []string{"HERDR_ENV", "HERDR_WORKSPACE_ID", "VMONO_AUTHOR", "VMONO_PACKAGE_ROOT", "VMONO_CONFIG_DIR", "VMONO_STATE_DIR"} {
		t.Setenv(k, "")
	}
	run, look, self := herdr.Run, lookPath, selfPath
	t.Cleanup(func() { herdr.Run, lookPath, selfPath = run, look, self })
	selfPath = func() (string, error) { return "/bin/vmono", nil }
	lookPath = func(name string) (string, error) {
		if name != "herdr" {
			return "", exec.ErrNotFound
		}
		return "/bin/herdr", nil
	}
	herdr.Run = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		sub := args[0] + " " + args[1]
		if err := f.fail[sub]; err != nil {
			return nil, err
		}
		var result any = map[string]any{"type": "ok"}
		switch sub {
		case "api snapshot":
			result = map[string]any{"snapshot": f.snap}
		case "workspace create":
			result = map[string]any{"workspace": herdr.Workspace{ID: "w9"}, "root_pane": herdr.Pane{ID: "w9:p1"}}
		case "pane process-info":
			fg := 7
			if slices.Contains(f.busy, args[3]) {
				fg = 8
			}
			result = map[string]any{"process_info": map[string]any{"foreground_process_group_id": fg, "shell_pid": 7}}
		}
		return json.Marshal(map[string]any{"result": result})
	}
}

// ran reports whether a recorded command starts with prefix.
func (f *fakeHerdr) ran(prefix string) bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

// herdrLaunch is a launch of pay/work at dir.
func herdrLaunch(dir string) *harness.Launch {
	return &harness.Launch{Name: "claude", Dir: dir, Path: "/bin/claude", Package: "pay", Session: "work",
		Harness: config.Harness{Args: []string{"--own"}}, Args: []string{"--own", "it's"}}
}

// tagged is a snapshot whose workspace w1 is the session's.
func tagged(dir string) herdr.Snapshot {
	return herdr.Snapshot{
		Workspaces: []herdr.Workspace{{ID: "w1", Label: "renamed", Tokens: map[string]string{herdr.Token: "pay/work"}}},
		Panes:      []herdr.Pane{{ID: "w1:p1", WorkspaceID: "w1", Cwd: dir}},
	}
}

func TestHerdrCreatesAWorkspace(t *testing.T) {
	dir := t.TempDir()
	f := &fakeHerdr{}
	f.install(t)
	t.Setenv("VMONO_CONFIG_DIR", "/my cfg")
	res, err := Herdr{}.Launch(herdrLaunch(dir), Opts{})
	if err != nil || res != Elsewhere {
		t.Fatalf("launch = %v, %v", res, err)
	}
	want := []string{
		"api snapshot",
		"workspace create --cwd " + dir + " --label pay/work --no-focus",
		"workspace report-metadata w9 --source vmono --token vmono_session=pay/work",
		`pane run w9:p1 env VMONO_LAUNCHER=direct 'VMONO_CONFIG_DIR=/my cfg' /bin/vmono open pay/work --harness claude -- 'it'\''s'`,
		"workspace focus w9",
	}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls =\n%s", strings.Join(f.calls, "\n"))
	}
}

func TestHerdrReusesTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	restarted := tagged(dir)
	restarted.Workspaces[0].Tokens, restarted.Workspaces[0].Label = nil, "pay/work"
	agent := tagged(dir)
	agent.Agents = []herdr.Pane{{ID: "w1:p1", WorkspaceID: "w1"}}
	lookalike := tagged("/elsewhere")
	lookalike.Workspaces[0].Tokens, lookalike.Workspaces[0].Label = nil, "pay/work"
	for _, tc := range []struct {
		name         string
		snap         herdr.Snapshot
		busy         []string
		create, runs bool
	}{
		{name: "agent running", snap: agent},
		{name: "shell left behind", snap: tagged(dir), runs: true},
		{name: "busy with something else", snap: tagged(dir), busy: []string{"w1:p1"}},
		{name: "after a herdr restart", snap: restarted, runs: true},
		{name: "same label elsewhere", snap: lookalike, create: true, runs: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHerdr{snap: tc.snap, busy: tc.busy}
			f.install(t)
			if _, err := (Herdr{}).Launch(herdrLaunch(dir), Opts{}); err != nil {
				t.Fatal(err)
			}
			if f.ran("workspace create") != tc.create || f.ran("pane run") != tc.runs || !f.ran("workspace focus") {
				t.Errorf("calls =\n%s", strings.Join(f.calls, "\n"))
			}
		})
	}
}

func TestHerdrLeavesFocusAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		h      Herdr
		author string
	}{"setting": {h: Herdr{NoFocus: true}}, "agent": {author: "agent:claude"}} {
		t.Run(name, func(t *testing.T) {
			f := &fakeHerdr{}
			f.install(t)
			t.Setenv("VMONO_AUTHOR", tc.author)
			if res, err := tc.h.Launch(herdrLaunch(t.TempDir()), Opts{}); err != nil || res != Elsewhere || f.ran("workspace focus") {
				t.Errorf("launch = %v, %v, calls %v", res, err, f.calls)
			}
		})
	}
}

func TestHerdrErrors(t *testing.T) {
	down := &herdr.Error{Code: herdr.CodeNotRunning, Message: "down"}
	f := &fakeHerdr{fail: map[string]error{"api snapshot": down}}
	f.install(t)
	if _, err := (Herdr{}).Launch(herdrLaunch(t.TempDir()), Opts{}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("launch = %v", err)
	}
	if notes := closeNow("pay", "work", "/x"); notes != nil {
		t.Errorf("a server that is down has nothing to close: %v", notes)
	}
}

func TestCloseSessionClosesItsHerdrWorkspace(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name  string
		snap  herdr.Snapshot
		here  string
		fail  error
		tries bool
		note  string
	}{
		{name: "tagged", snap: tagged(dir), tries: true, note: "closed herdr workspace"},
		{name: "another package root", snap: tagged("/elsewhere")},
		{name: "from inside", snap: tagged(dir), here: "w1", note: "running inside it"},
		{name: "grouped", snap: tagged(dir), fail: errors.New("group"), tries: true, note: "left its herdr workspace \"pay/work\" open: group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHerdr{snap: tc.snap, fail: map[string]error{"workspace close": tc.fail}}
			f.install(t)
			if tc.here != "" {
				t.Setenv("HERDR_ENV", "1")
				t.Setenv("HERDR_WORKSPACE_ID", tc.here)
			}
			notes := strings.Join(closeNow("pay", "work", dir), "\n")
			if tc.note == "" && notes != "" || !strings.Contains(notes, tc.note) || f.ran("workspace close") != tc.tries {
				t.Errorf("notes = %q, calls %v", notes, f.calls)
			}
		})
	}
}

func TestResolveHerdr(t *testing.T) {
	f := &fakeHerdr{}
	f.install(t)
	t.Setenv("TMUX", "sock")
	name := func(launcher string) string {
		l, err := Resolve(config.Config{Launcher: launcher, HerdrFocus: true})
		if err != nil {
			return err.Error()
		}
		return l.Name()
	}
	if got := name(config.LauncherAuto); got != config.LauncherDirect {
		t.Errorf("auto outside herdr = %s", got)
	}
	t.Setenv("HERDR_ENV", "1")
	if got := name(config.LauncherAuto); got != config.LauncherHerdr {
		t.Errorf("auto inside herdr = %s", got)
	}
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if got := name(config.LauncherHerdr); !strings.Contains(got, "not found") {
		t.Errorf("missing herdr = %s", got)
	}
}

func TestHerdrTypesASelfLaunch(t *testing.T) {
	dir := t.TempDir()
	f := &fakeHerdr{}
	f.install(t)
	t.Setenv("VMONO_CONFIG_DIR", "/cfg")
	l, err := SelfLaunch("claude", dir, "pay", ".init", []string{"package", "init", "pay"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Herdr{}).Launch(l, Opts{}); err != nil {
		t.Fatal(err)
	}
	if want := "pane run w9:p1 env VMONO_LAUNCHER=direct VMONO_CONFIG_DIR=/cfg /bin/vmono package init pay"; !slices.Contains(f.calls, want) {
		t.Errorf("calls =\n%s", strings.Join(f.calls, "\n"))
	}
}
