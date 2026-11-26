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
	"github.com/doneill612/vmono/internal/launcher"
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

// noHooks holds the --no-hooks flag.
var noHooks bool

// loadCore reads config and builds a Core. It is a variable so tests could
// substitute it, but commands normally just call it.
func loadCore() (*core.Core, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	for _, n := range cfg.Notices {
		fprintf(os.Stderr, "vmono: %s\n", n)
	}
	cfg.NoHooks = cfg.NoHooks || noHooks
	c, err := core.New(cfg)
	if err != nil {
		return nil, err
	}
	c.FindTerminal = launcher.FindSession
	return c, nil
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

// sessionFlags are the flags that say which branches a new session works on,
// shared by the commands that start one.
type sessionFlags struct {
	prefix              string
	branch, track, from []string
	reuse               bool
}

// add defines the flags on cmd, their help starting with intro.
func (f *sessionFlags) add(cmd *cobra.Command, intro string) {
	fs := cmd.Flags()
	fs.StringVar(&f.prefix, "prefix", "", intro+"put PREFIX/ before the session's name in its branch: feat gives feat/SESSION")
	fs.StringArrayVar(&f.branch, "branch", nil, intro+"name the branch to work on: BRANCH for every active member, or MEMBER=BRANCH for one (repeatable; default [PREFIX/]SESSION)")
	fs.StringArrayVar(&f.track, "track", nil, intro+"work on a branch already on the remote: BRANCH for every member, starting it where a remote lacks it, or MEMBER=BRANCH for one (repeatable); fetches first")
	fs.StringArrayVar(&f.from, "from", nil, intro+"build on this instead of the member's base: REF for every active member that has it, or MEMBER=REF for one; for a read-only member, what to check out (repeatable); fetches first")
	fs.BoolVar(&f.reuse, "reuse-branch", false, intro+"check out the branch if it already exists in a source repo")
}

// given reports whether any of the flags was used.
func (f *sessionFlags) given() bool {
	return f.prefix != "" || len(f.branch) > 0 || len(f.track) > 0 || len(f.from) > 0 || f.reuse
}

// options turns the flags into what core takes.
func (f *sessionFlags) options() (core.SessionOptions, error) {
	opts := core.SessionOptions{Prefix: f.prefix, ReuseBranch: f.reuse}
	var err error
	if opts.Branch, opts.BranchByMember, err = parseBranches("--branch", f.branch); err != nil {
		return opts, err
	}
	if opts.Track, opts.TrackByMember, err = parseBranches("--track", f.track); err != nil {
		return opts, err
	}
	opts.From, opts.FromByMember, err = parseBranches("--from", f.from)
	return opts, err
}

// printBranches writes what became of the branches of what was removed, one line
// each, saying why.
func printBranches(w io.Writer, res core.DeleteResult) {
	for _, b := range res.BranchesDeleted {
		fprintf(w, "deleted branch %s (%s): %s\n", b.Branch, b.Member, b.Reason)
	}
	for _, b := range res.BranchesKept {
		fprintf(w, "kept branch %s (%s): %s\n", b.Branch, b.Member, b.Reason)
	}
}

// branchesHelp explains what removing a worktree does with its branch.
const branchesHelp = `Branches are kept, so committed work is never lost, except that a branch vmono made
and nothing was committed to goes too. --delete-branches also deletes the branches
whose work has been merged. A merge commit, a rebase merge and a squash merge all
count, and each kept branch is listed with the reason. A squash merge that was edited
on the way in, or that resolved a conflict, cannot be told from work that was never
merged, so its branch is kept: check, then delete it with git branch -D in the source
repo. What counts as merged is as of the last fetch.`

// parseBranches splits the values of flag into the branch for every member and
// the branches for individual members. A value is MEMBER=BRANCH when it has an
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
