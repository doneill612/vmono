package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/harness"
)

// Exit codes from the spec.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitRefused  = 2
	ExitNotFound = 3
)

// ExitCode maps an error to the process exit code: 2 when work would be
// lost (--force overrides), 3 when something was not found, 1 otherwise.
func ExitCode(err error) int {
	var nf *discover.NotFoundError
	var he *harness.ExitError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &he):
		return he.Code
	case errors.Is(err, core.ErrRefused):
		return ExitRefused
	case errors.Is(err, core.ErrNotFound), errors.As(err, &nf):
		return ExitNotFound
	default:
		return ExitError
	}
}

// loadCore reads config and builds a Core. It is a variable so tests could
// substitute it, but commands normally just call it.
func loadCore() (*core.Core, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return core.New(cfg)
}

// withEvents runs fn with a progress channel attached to c and prints each
// event as a line on w. The same events feed the TUI's progress screen.
func withEvents(w io.Writer, c *core.Core, fn func() error) error {
	ch := make(chan apply.Event, 64)
	c.Events = ch
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range ch {
			printEvent(w, ev)
		}
	}()
	err := fn()
	c.Events = nil
	close(ch)
	wg.Wait()
	return err
}

// printEvent renders one progress event as a single line. Running events
// are skipped; the CLI reports outcomes.
func printEvent(w io.Writer, ev apply.Event) {
	who := ev.Member
	if who == "" {
		who = "package"
	}
	switch ev.Status {
	case apply.StatusDone:
		fprintf(w, "  ok    %-14s %s\n", who, ev.Step)
	case apply.StatusOutput:
		fprintf(w, "  |     %-14s %s\n", who, ev.Message)
	case apply.StatusWarn:
		fprintf(w, "  warn  %-14s %s: %s\n", who, ev.Step, ev.Message)
	case apply.StatusFailed:
		fprintf(w, "  FAIL  %-14s %s: %v\n", who, ev.Step, ev.Err)
	case apply.StatusUndone:
		fprintf(w, "  undo  %-14s %s\n", who, ev.Step)
	}
}

// intoHelp describes the repeatable --into flag.
const intoHelp = "landing branch: BRANCH for every member, or MEMBER=BRANCH for one (repeatable)"

// trackHelp describes the repeatable --track flag.
const trackHelp = "start from a branch on the members' remotes and land back on it: BRANCH for every member whose remote has it, or MEMBER=BRANCH for one (repeatable); fetches first"

// parseBranches splits the values of flag, --into or --track, into the branch for every member and the
// branches for individual members. A value is MEMBER=BRANCH when it has an
// "=" with a member name before it, and otherwise a branch for every member.
func parseBranches(flag string, vals []string) (all string, byMember map[string]string, err error) {
	for _, v := range vals {
		member, branch, ok := strings.Cut(v, "=")
		if !ok {
			if all != "" {
				return "", nil, fmt.Errorf("%s names two branches for every member (%q and %q); use MEMBER=BRANCH for one member", flag, all, v)
			}
			all = v
			continue
		}
		if member == "" || branch == "" {
			return "", nil, fmt.Errorf("%s %q: expected MEMBER=BRANCH", flag, v)
		}
		if byMember == nil {
			byMember = map[string]string{}
		}
		byMember[member] = branch
	}
	return all, byMember, nil
}

// packageArg returns the optional package argument.
func packageArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// readTextFlag returns the content of a file flag, "-" meaning stdin.
func readTextFlag(cmd *cobra.Command, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}
