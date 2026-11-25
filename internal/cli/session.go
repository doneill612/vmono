package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// newSessionCmd builds `vmono session`, the commands that start, list, update,
// finish, push and remove the sessions of a package. A session is one piece of work on a
// package: its own branch, and its own worktree of every member.
func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start, list, update, finish, push and remove the sessions of a package",
	}
	cmd.AddCommand(newSessionNewCmd(), newSessionLsCmd(), newSessionIntoCmd(), newSessionUpdateCmd(), newSessionFinishCmd(), newSessionReviewCmd(), newSessionPushCmd(), newSessionRmCmd())
	return cmd
}

// newSessionNewCmd builds `vmono session new`.
func newSessionNewCmd() *cobra.Command {
	var branch string
	var into, track []string
	var reuse bool
	cmd := &cobra.Command{
		Use:   "new [pkg] <name> [--branch B] [--into [MEMBER=]B]... [--track [MEMBER=]B]... [--reuse-branch]",
		Short: "Start a session: a worktree of every member on a new branch",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			intoAll, intoBy, err := parseBranches("--into", into)
			if err != nil {
				return err
			}
			trackAll, trackBy, err := parseBranches("--track", track)
			if err != nil {
				return err
			}
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			var s *core.Session
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var cerr error
				s, cerr = c.CreateSession(p, rest[0], core.SessionOptions{Branch: branch, Into: intoAll, IntoByMember: intoBy, Track: trackAll, TrackByMember: trackBy, ReuseBranch: reuse})
				return cerr
			})
			if err != nil {
				return err
			}
			fprintf(cmd.OutOrStdout(), "%s\n", s.Dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "session branch (default: <branch_prefix><pkg>/<session>)")
	cmd.Flags().StringArrayVar(&into, "into", nil, intoHelp+"; default is the branch each source has checked out. A session starts from its landing branch when that is not the member's base branch and exists")
	cmd.Flags().StringArrayVar(&track, "track", nil, trackHelp)
	cmd.Flags().BoolVar(&reuse, "reuse-branch", false, "check out the branch if it already exists in a source repo")
	return cmd
}

// newSessionLsCmd builds `vmono session ls`.
func newSessionLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls [pkg] [--json]",
		Short: "List a package's sessions",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			rows, err := c.ListSessions(p, true)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, rows)
			}
			if len(rows) == 0 {
				fprintf(out, "no sessions; start one with `vmono session new %s NAME`\n", p.Name)
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SESSION\tBRANCH\tSTATE\tDIRTY\tLAST OPENED")
			for _, r := range rows {
				dirty := ""
				if r.Dirty {
					dirty = "*"
				}
				opened := r.LastOpened
				if opened == "" {
					opened = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Branch, r.State, dirty, opened)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newSessionIntoCmd builds `vmono session into`: show where each member's work
// will land, or change it.
func newSessionIntoCmd() *cobra.Command {
	var all string
	var members []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "into [pkg/session] [--all B] [--member NAME=B]... [--json]",
		Short: "Show, or change, the branch each member's work lands on",
		Long: `Without flags, list each active member's landing branch in the session.
--all sets one branch for every active member and --member NAME=BRANCH sets one
member's, which wins over --all. Members you do not name keep theirs. A branch that
does not exist yet is created when the session is finished.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			byMember, err := parseMembers(members)
			if err != nil {
				return err
			}
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(args))
			if err != nil {
				return err
			}
			if all != "" || len(byMember) > 0 {
				if err := c.SetInto(s, all, byMember); err != nil {
					return err
				}
				if s, err = c.ResolveSession(s.Label()); err != nil {
					return err
				}
			}
			type row struct {
				Member string `json:"member"`
				Into   string `json:"into"`
			}
			var rows []row
			for _, m := range c.Status(s).Members {
				if m.Mode == manifest.ModeActive {
					rows = append(rows, row{m.Name, m.Into})
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, rows)
			}
			for _, r := range rows {
				fprintf(out, "%-16s %s\n", r.Member, r.Into)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&all, "all", "", "land every active member on this branch")
	f.StringArrayVar(&members, "member", nil, "land one member on a branch: NAME=BRANCH (repeatable)")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// parseMembers reads NAME=BRANCH pairs.
func parseMembers(vals []string) (map[string]string, error) {
	_, by, err := parseBranches("--member", vals)
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		if !strings.Contains(v, "=") {
			return nil, fmt.Errorf("--member %q: expected NAME=BRANCH", v)
		}
	}
	return by, nil
}

// newSessionUpdateCmd builds `vmono session update`.
func newSessionUpdateCmd() *cobra.Command {
	var members []string
	var merge, noFetch, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "update [pkg/session] [--member NAME]... [--merge] [--no-fetch] [--dry-run] [--json]",
		Short: "Rebase the session's commits onto the latest of each member's landing branch",
		Long: `Bring a session up to date. The sources are fetched, then each active member's own
commits are rebased, inside the session, onto its landing branch or that branch's
copy on the remote, whichever is further ahead, or onto the member's base when the
landing branch does not exist yet. Only the session branch moves.

Members are independent. One with uncommitted work, or whose landing branch has
diverged from its remote copy, is skipped; --merge merges both sides of a diverged
branch into the session instead, and the merge commit lands with the work. One that
conflicts is put back as it was. The command exits with 2 when any member was skipped or conflicted. A failed
fetch only warns. --dry-run fetches, then shows what would be updated.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(args))
			if err != nil {
				return err
			}
			var res core.UpdateResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var uerr error
				res, uerr = c.Update(s, core.UpdateOptions{Members: members, Merge: merge, NoFetch: noFetch, DryRun: dryRun})
				return uerr
			})
			out := cmd.OutOrStdout()
			if len(res.Members) > 0 || err == nil {
				if asJSON {
					if perr := printJSON(out, res); perr != nil {
						return perr
					}
				} else {
					printUpdate(out, res, dryRun)
				}
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&members, "member", nil, "update only this member (repeatable)")
	f.BoolVar(&merge, "merge", false, "merge both sides of a landing branch that has diverged from its remote copy")
	f.BoolVar(&noFetch, "no-fetch", false, "update to the refs already on disk")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be updated without moving any branch")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// printUpdate writes one line per member for an update, or a dry run of one.
func printUpdate(w io.Writer, res core.UpdateResult, dryRun bool) {
	for _, m := range res.Members {
		text := m.Status + ": " + m.Reason
		switch m.Status {
		case core.UpdateCurrent:
			text = "up to date with " + m.Onto
		case core.UpdateDone:
			text = fmt.Sprintf("updated onto %s (%d behind, %d of its own commits replayed)", m.Onto, m.Behind, m.Commits)
			if m.Merge {
				text = fmt.Sprintf("merged %s (%d commits brought in)", m.Onto, m.Behind)
			}
		case core.UpdateReady:
			text = fmt.Sprintf("would update onto %s (%d behind, %d of its own commits to replay)", m.Onto, m.Behind, m.Commits)
			if m.Merge {
				text = fmt.Sprintf("would merge %s (%d commits to bring in)", m.Onto, m.Behind)
			}
		}
		fprintf(w, "%-16s %s\n", m.Name, text)
	}
	if dryRun {
		fprintf(w, "dry run: no branch moved\n")
	}
}

// newSessionFinishCmd builds `vmono session finish`.
func newSessionFinishCmd() *cobra.Command {
	var into []string
	var closeAfter, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "finish [pkg/session] [--into [MEMBER=]B]... [--close] [--dry-run] [--json]",
		Short: "Land the session's commits on each member's branch in its source repo",
		Long: `Land the session's work. Each active member's own commits are rebased onto its
landing branch inside the session, then the landing branch in the member's source
repo is moved to the result: fast-forwarded when it is checked out in a clean
worktree, otherwise updated in place, and created when it does not exist yet.
Members with no commits of their own are skipped.

Each member lands on its own landing branch: the one recorded when the session was
created (by default the branch its source had checked out), else the branch its
base names (origin/main lands on main). --into overrides and records it. Nothing moves unless every member can land: commit
or stash uncommitted work first. If a rebase stops on conflicts, resolve them in
that member, run git add and git rebase --continue, then run this command again.

The session, its worktrees and its branch stay in place and the session is marked
finished; remove it with ` + "`vmono session rm`" + ` or pass --close. Nothing is pushed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFinish(cmd, args, into, closeAfter, dryRun, asJSON)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&into, "into", nil, intoHelp+"; overrides what is recorded, and is recorded")
	f.BoolVar(&closeAfter, "close", false, "remove the session once its work has landed")
	f.BoolVar(&dryRun, "dry-run", false, "show what would land without changing anything")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newSessionReviewCmd builds `vmono session review`, the read-only form of
// `vmono session finish`: it plans the finish and prints what would land on
// each member's branch, and changes nothing. It exists as its own command,
// rather than as `finish --dry-run`, so a harness permission rule can allow
// it while `vmono session finish` keeps asking: Claude Code lets an ask rule
// beat any allow rule, however specific, so a dry-run flag on a command that
// asks can never be exempted. Output, JSON and exit codes are those of
// `finish --dry-run`, and an --into given here is checked but never recorded.
func newSessionReviewCmd() *cobra.Command {
	var into []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "review [pkg/session] [--into [MEMBER=]B]... [--json]",
		Short: "Show what finishing the session would land, changing nothing",
		Long: `Show what ` + "`vmono session finish`" + ` would do, without doing it. Each active member's
commits that would be rebased onto its landing branch are listed with the branch
they would land on, and any member that is blocked or would conflict is shown with
the reason. Nothing is rebased, moved, recorded or marked finished.

--into previews a different landing branch; unlike finish, it is not recorded.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFinish(cmd, args, into, false, true, asJSON)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&into, "into", nil, intoHelp+"; previews only, and is not recorded")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// runFinish is the body shared by `vmono session finish` and `vmono session
// review`. It resolves the session, plans or performs the finish with the
// given options, and prints the result as text or JSON. A refused finish still
// prints its per-member reasons before the error is returned, so the caller
// sees why and the exit status stays non-zero. With dryRun set nothing is
// changed, which is the only way review calls it.
func runFinish(cmd *cobra.Command, args, into []string, closeAfter, dryRun, asJSON bool) error {
	intoAll, intoBy, err := parseBranches("--into", into)
	if err != nil {
		return err
	}
	c, err := loadCore()
	if err != nil {
		return err
	}
	s, err := c.ResolveSession(packageArg(args))
	if err != nil {
		return err
	}
	var res core.FinishResult
	err = withEvents(cmd.ErrOrStderr(), c, func() error {
		var ferr error
		res, ferr = c.Finish(s, core.FinishOptions{Into: intoAll, IntoByMember: intoBy, Close: closeAfter, DryRun: dryRun})
		return ferr
	})
	out := cmd.OutOrStdout()
	switch {
	case asJSON && (err == nil || errors.Is(err, core.ErrRefused)):
		if perr := printJSON(out, res); perr != nil {
			return perr
		}
	case err == nil || errors.Is(err, core.ErrRefused):
		printFinish(out, res, dryRun)
	}
	return err
}

// printFinish writes one line per member for a finish, or a dry run of one,
// then a summary line. Refused finishes print the same lines with the reason
// each member is stopped.
func printFinish(w io.Writer, res core.FinishResult, dryRun bool) {
	label := res.Package + "/" + res.Session
	for _, m := range res.Members {
		fprintf(w, "%-16s %s\n", m.Name, describeLanding(m, dryRun))
		for _, n := range m.Notes {
			fprintf(w, "%-16s note: %s\n", "", n)
		}
	}
	switch {
	case res.Closed:
		fprintf(w, "finished and removed session %s\n", label)
	case res.Finished:
		fprintf(w, "finished session %s; remove it with `vmono session rm %s` when you are done\n", label, label)
	case dryRun:
		fprintf(w, "dry run: nothing changed\n")
	}
}

// describeLanding words one member's outcome.
func describeLanding(m core.MemberLanding, dryRun bool) string {
	commits := fmt.Sprintf("%d commit", m.Commits)
	if m.Commits != 1 {
		commits += "s"
	}
	verb := "landed"
	if dryRun || m.Status == core.LandReady {
		verb = "would land"
	}
	switch m.Status {
	case core.LandNothing:
		return "nothing to land"
	case core.LandBlocked:
		return "blocked: " + m.Reason
	case core.LandConflict:
		return fmt.Sprintf("conflict: rebase onto %s stopped in %s; %s", m.Into, strings.Join(m.Conflicts, ", "), m.Reason)
	}
	text := fmt.Sprintf("%s %s on %s (%s)", verb, commits, m.Into, m.Via)
	if m.Create {
		text += ", creating the branch"
	}
	return text
}

// newSessionPushCmd builds `vmono session push`, which publishes the branches a
// session's work is on. It is the only vmono command that pushes, and it never
// forces.
func newSessionPushCmd() *cobra.Command {
	var members []string
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "push [pkg/session] [--member NAME]... [--dry-run] [--json]",
		Short: "Push the session's branches to their remotes",
		Long: `Push the branch each active member is on: to the remote branch it already tracks,
or, when it tracks none, to a branch of the same name on origin, which it then
tracks. Members with no commits of their own, or whose remote already has every
one, are skipped, and so are members whose work has been merged. Uncommitted work
is not pushed, and is pointed out.

Nothing is forced: a remote branch that has commits yours lacks is refused, so run
vmono session update first. A branch is never pushed to the branch it is meant to
merge into, and one whose remote branch was deleted, as it is once a pull request
is merged, is not pushed again. Nothing prompts for credentials, so load your key
into ssh-agent or sign in through your git credential helper beforehand. Whether a
branch is behind is judged from the last fetch; f in the TUI, or git fetch in the
member's source repo, refreshes it.

--member limits the push to the named members (repeatable). Members are
independent: one that cannot be pushed does not stop the others, and the command
exits with 2 when any is blocked, 1 when git refused one. --dry-run shows the plan
and pushes nothing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(args))
			if err != nil {
				return err
			}
			var res core.PushResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var perr error
				res, perr = c.Push(s, core.PushOptions{Members: members, DryRun: dryRun})
				return perr
			})
			out := cmd.OutOrStdout()
			if len(res.Members) > 0 || err == nil {
				if asJSON {
					if perr := printJSON(out, res); perr != nil {
						return perr
					}
				} else {
					printPush(out, res, dryRun)
				}
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&members, "member", nil, "push only this member (repeatable)")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be pushed without pushing")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// printPush writes one line per member for a push, or a dry run of one.
func printPush(w io.Writer, res core.PushResult, dryRun bool) {
	for _, m := range res.Members {
		fprintf(w, "%-16s %s\n", m.Name, describePush(m, dryRun))
		for _, n := range m.Notes {
			fprintf(w, "%-16s note: %s\n", "", n)
		}
	}
	if dryRun {
		fprintf(w, "dry run: nothing pushed\n")
	}
}

// describePush words one member's outcome.
func describePush(m core.MemberPush, dryRun bool) string {
	switch m.Status {
	case core.PushNothing:
		return "nothing to push"
	case core.PushMerged:
		return m.Reason
	case core.PushBlocked:
		return "blocked: " + m.Reason
	case core.PushFailed:
		return "failed: " + m.Reason
	}
	verb := "pushed"
	if dryRun || m.Status == core.PushReady {
		verb = "would push"
	}
	commits := fmt.Sprintf("%d commit", m.Commits)
	if m.Commits != 1 {
		commits += "s"
	}
	text := fmt.Sprintf("%s %s of %s to %s/%s", verb, commits, m.Branch, m.Remote, m.Dest)
	if m.New {
		text += ", creating the branch"
	}
	if m.SetsUpstream {
		text += ", now tracking it"
		if verb == "would push" {
			text = strings.Replace(text, "now tracking", "to track", 1)
		}
	}
	return text
}

// newSessionRmCmd builds `vmono session rm`.
func newSessionRmCmd() *cobra.Command {
	var force, deleteBranches bool
	cmd := &cobra.Command{
		Use:   "rm <pkg>/<session> [--force] [--delete-branches]",
		Short: "Remove a session and its worktrees; the package and other sessions stay",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !strings.Contains(args[0], "/") {
				return fmt.Errorf("name the session as PACKAGE/SESSION, got %q", args[0])
			}
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(args[0])
			if err != nil {
				return err
			}
			var res core.DeleteResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var derr error
				res, derr = c.DeleteSession(s, core.DeleteOptions{Force: force, DeleteBranches: deleteBranches})
				return derr
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fprintf(out, "removed session %s\n", s.Label())
			for _, b := range res.BranchesKept {
				fprintf(out, "kept branch %s\n", b)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if members have unsaved work")
	cmd.Flags().BoolVar(&deleteBranches, "delete-branches", false, "also delete the session branch if already merged into its base")
	return cmd
}
