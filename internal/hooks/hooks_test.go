package hooks

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// collect runs spec and returns the lines it printed.
func collect(spec Spec) ([]string, error) {
	var lines []string
	err := Run(spec, func(l string) { lines = append(lines, l) })
	return lines, err
}

func TestRunPassesLinesEnvAndFailure(t *testing.T) {
	ref := "$VMONO_X"
	if runtime.GOOS == "windows" {
		ref = "%VMONO_X%"
	}
	lines, err := collect(Spec{Command: "echo one&& echo " + ref + "&& exit 3", Dir: t.TempDir(), Env: map[string]string{"VMONO_X": "two"}})
	if err == nil || !strings.Contains(err.Error(), "exit") {
		t.Errorf("err = %v", err)
	}
	if !reflect.DeepEqual(lines, []string{"one", "two"}) {
		t.Errorf("lines = %q", lines)
	}
	if _, err := collect(Spec{Command: "echo hi"}); err != nil {
		t.Error(err)
	}
}

func TestRunTimesOut(t *testing.T) {
	// The shell forks here, so killing only the shell would leave a child
	// holding the output open.
	command := "sleep 30; echo late"
	if runtime.GOOS == "windows" {
		command = "ping -n 30 127.0.0.1 >nul & echo late"
	}
	start := time.Now()
	lines, err := collect(Spec{Command: command, Timeout: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timed out") || len(lines) != 0 {
		t.Errorf("lines = %q, err = %v", lines, err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("took %s", d)
	}
}

func TestRunDoesNotWaitForBackgroundProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh job control")
	}
	start := time.Now()
	if _, err := collect(Spec{Command: "sleep 20 & echo started"}); err != nil {
		t.Error(err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s", d)
	}
}
