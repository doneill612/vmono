package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
)

func TestPrepareInit(t *testing.T) {
	t.Setenv("VMONO_SESSION", "outer")
	t.Setenv("VMONO_BRANCH", "outer")
	w := &core.InitWorkspace{Pkg: &core.Package{Name: "pkg"}, Dir: t.TempDir()}
	h := config.Harness{Command: "git", Args: []string{"--own"}, InitArgs: []string{"--x", "{prompt}"}}
	l, prompted, err := PrepareInit(w, "fake", h, "go on")
	if err != nil || !prompted || !slices.Equal(l.Args, []string{"--own", "--x", "go on"}) || l.Dir != w.Dir {
		t.Fatalf("launch = %+v prompted=%v err=%v", l, prompted, err)
	}
	env := "\n" + strings.Join(l.Env, "\n") + "\n"
	for _, want := range []string{"VMONO_PACKAGE=pkg", "VMONO_ROOT=" + w.Dir, "VMONO_SESSION=", "VMONO_BRANCH=", "VMONO_AUTHOR=agent:fake"} {
		if !strings.Contains(env, "\n"+want+"\n") {
			t.Errorf("env lacks %q", want)
		}
	}
	h.InitArgs = nil
	if _, prompted, _ = PrepareInit(w, "fake", h, "x"); prompted {
		t.Error("with no init args there is nothing to carry the prompt")
	}
	if _, _, err := PrepareInit(w, "fake", config.Harness{Command: "definitely-not-installed-xyz"}, "x"); err == nil {
		t.Error("a missing command should fail")
	}
}

func shLaunch(t *testing.T, script string) *Launch {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	return &Launch{Path: "/bin/sh", Args: []string{"-c", script}, Dir: t.TempDir(), Env: os.Environ()}
}

func TestRunAndReturnReportsTheExitStatus(t *testing.T) {
	var ee *ExitError
	if err := RunAndReturn(shLaunch(t, "exit 4"), true); !errors.As(err, &ee) || ee.Code != 4 {
		t.Errorf("err = %v", err)
	}
	if err := RunAndReturn(shLaunch(t, "exit 0"), true); err != nil {
		t.Errorf("err = %v", err)
	}
}

// TestRunAndReturnSurvivesAHangup pins that losing the terminal stops the
// harness and still returns, and that an interrupt stops neither.
func TestRunAndReturnSurvivesAHangup(t *testing.T) {
	l := shLaunch(t, "echo > started; exec sleep 30")
	done := make(chan error, 1)
	go func() { done <- RunAndReturn(l, false) }()
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(l.Dir, "started")); err == nil {
			break
		}
		if i > 200 {
			t.Fatal("never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	self, _ := os.FindProcess(os.Getpid())
	_ = self.Signal(os.Interrupt)
	select {
	case err := <-done:
		t.Fatalf("an interrupt ended the wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	_ = self.Signal(syscall.SIGHUP)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("did not return")
	}
}
