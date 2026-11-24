package integrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testEnv returns an Env rooted in a temporary directory.
func testEnv(t *testing.T) Env {
	t.Helper()
	root := t.TempDir()
	return Env{ConfigDir: filepath.Join(root, "cfg"), Home: filepath.Join(root, "home")}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTmuxInstallCreatesConfWhenMissing pins that with no tmux.conf at all,
// install writes vmono.tmux and creates ~/.tmux.conf holding only the
// source line.
func TestTmuxInstallCreatesConfWhenMissing(t *testing.T) {
	e := testEnv(t)
	i, err := Get("tmux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.ConfigDir, "integrations", "tmux", "vmono.tmux")
	want, _ := i.Content("vmono.tmux")
	if read(t, file) != string(want) {
		t.Error("vmono.tmux does not match the embedded file")
	}
	conf := filepath.Join(e.Home, ".tmux.conf")
	if got := read(t, conf); got != tmuxSourceLine(file)+"\n" {
		t.Errorf("tmux.conf = %q", got)
	}
	if st := i.Status(e); !st.Installed || st.Outdated || st.Wired != conf {
		t.Errorf("status = %+v", st)
	}
}

// TestTmuxInstallIsIdempotentAndUninstallIsExact pins that installing twice
// adds one line, keeps the user's own lines around it, refreshes an edited
// vmono.tmux, and that uninstall removes exactly the marked line and
// vmono's directory.
func TestTmuxInstallIsIdempotentAndUninstallIsExact(t *testing.T) {
	e := testEnv(t)
	e.XDGConfigHome = filepath.Join(e.Home, "xdg")
	conf := filepath.Join(e.XDGConfigHome, "tmux", "tmux.conf")
	mine := "set -g mouse on\nbind r source-file ~/.tmux.conf\n"
	write(t, conf, mine)
	i, _ := Get("tmux")

	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.Dir("tmux"), "vmono.tmux")
	write(t, file, "stale\n")
	if st := i.Status(e); !st.Outdated {
		t.Errorf("an edited vmono.tmux should be outdated: %+v", st)
	}
	changes, err := i.Install(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != file || changes[0].Action != "updated" {
		t.Errorf("reinstall changes = %+v, want only vmono.tmux updated", changes)
	}
	got := read(t, conf)
	if got != mine+tmuxSourceLine(file)+"\n" {
		t.Errorf("tmux.conf after two installs = %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".tmux.conf")); !os.IsNotExist(err) {
		t.Error("must wire the existing XDG tmux.conf, not create ~/.tmux.conf")
	}

	if _, err := i.Uninstall(e); err != nil {
		t.Fatal(err)
	}
	if got := read(t, conf); got != mine {
		t.Errorf("tmux.conf after uninstall = %q, want %q", got, mine)
	}
	if _, err := os.Stat(e.Dir("tmux")); !os.IsNotExist(err) {
		t.Error("uninstall should remove vmono's tmux directory")
	}
	if st := i.Status(e); st.Installed {
		t.Errorf("status after uninstall = %+v", st)
	}
}

// TestTmuxInstallKeepsASymlinkedConf pins that a tmux.conf symlinked into a
// dotfiles repo is edited through the link rather than replaced by a copy.
func TestTmuxInstallKeepsASymlinkedConf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	e := testEnv(t)
	real := filepath.Join(e.Home, "dotfiles", "tmux.conf")
	write(t, real, "set -g mouse on\n")
	link := filepath.Join(e.Home, ".tmux.conf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	i, _ := Get("tmux")
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.tmux.conf is no longer a symlink: %v", err)
	}
	if !strings.Contains(read(t, real), tmuxMarker) {
		t.Error("the link's target should hold the source line")
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("screen"); err == nil || !strings.Contains(err.Error(), "tmux") {
		t.Errorf("err = %v, want one listing the known integrations", err)
	}
}
