package integrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// herdrEnv returns an Env and herdr's config path in it, and skips on Windows.
func herdrEnv(t *testing.T) (Integration, Env, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("herdr is not offered on Windows")
	}
	e := testEnv(t)
	i, err := Get("herdr")
	if err != nil {
		t.Fatal(err)
	}
	return i, e, filepath.Join(e.Home, ".config", "herdr", "config.toml")
}

func TestHerdrInstallAndUninstall(t *testing.T) {
	i, e, conf := herdrEnv(t)
	before := "onboarding = false\n\n[keys]\nprefix = \"ctrl+b\"\n\n[[keys.command]]\nkey = \"prefix+alt+g\"\ntype = \"popup\"\ncommand = \"lazygit\"\n"
	write(t, conf, before)
	if st := i.Status(e); st.Installed {
		t.Errorf("status before = %+v", st)
	}
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	got := read(t, conf)
	var parsed struct {
		Keys struct {
			Prefix  string
			Command []struct{ Key, Type, Command string }
		}
	}
	if _, err := toml.Decode(got, &parsed); err != nil || parsed.Keys.Prefix != "ctrl+b" || len(parsed.Keys.Command) != 2 || parsed.Keys.Command[1].Key != herdrKey {
		t.Errorf("config = %s\n%+v, %v", got, parsed, err)
	}
	if changes, err := i.Install(e); err != nil || len(changes) != 0 {
		t.Errorf("second install = %v, %v", changes, err)
	}
	if st := i.Status(e); !st.Installed || st.Outdated || st.Wired != conf {
		t.Errorf("status = %+v", st)
	}

	// Another key replaces the block, and stays across plain installs.
	e.Key = "prefix+f9"
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	e.Key = ""
	write(t, conf, strings.Replace(read(t, conf), "90%", "50%", 1))
	if st := i.Status(e); !st.Outdated {
		t.Errorf("an edited block should be outdated: %+v", st)
	}
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	if got := read(t, conf); strings.Count(got, herdrBegin) != 1 || !strings.Contains(got, "prefix+f9") || !strings.Contains(got, "90%") {
		t.Errorf("config = %s", got)
	}

	if _, err := i.Uninstall(e); err != nil {
		t.Fatal(err)
	}
	if got := read(t, conf); got != before {
		t.Errorf("uninstall left %q", got)
	}
}

func TestHerdrInstallRefuses(t *testing.T) {
	i, e, conf := herdrEnv(t)
	for name, text := range map[string]string{
		"builtin":     "[keys]\nsplit = \"prefix+shift+v\"\n",
		"list":        "[keys]\nsplit = [\"f1\", \"prefix+Shift+V\"]\n",
		"command":     "[[keys.command]]\nkey = \"prefix+shift+v\"\ncommand = \"x\"\n",
		"broken":      "[keys\n",
		"inline":      "[keys]\ncommand = [{ key = \"f2\" }]\n",
		"open marker": "# vmono:begin\nmine = 1\n",
	} {
		write(t, conf, text)
		if _, err := i.Install(e); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if got := read(t, conf); got != text {
			t.Errorf("%s: config changed to %q", name, got)
		}
	}
}

func TestHerdrInstallCreatesAndKeepsSymlinks(t *testing.T) {
	i, e, conf := herdrEnv(t)
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	if st := i.Status(e); !st.Installed || st.Outdated {
		t.Errorf("status = %+v", st)
	}
	e.HerdrConfig = filepath.Join(e.Home, "link.toml")
	real := filepath.Join(e.Home, "dotfiles.toml")
	write(t, real, "x = 1\n")
	if err := os.Symlink(real, e.HerdrConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Install(e); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(e.HerdrConfig); fi.Mode()&os.ModeSymlink == 0 || !strings.Contains(read(t, real), herdrBegin) {
		t.Error("the symlink must survive")
	}
	if _, err := os.Stat(conf); err != nil {
		t.Error(err)
	}
}
