package cli

import (
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// splitPkgArgs separates an optional leading package name from the
// remaining positional arguments. With the full count the first argument is
// the package; with one fewer the package is inferred from the working
// directory.
func splitPkgArgs(args []string, withPkg int) (pkg string, rest []string) {
	if len(args) == withPkg {
		return args[0], args[1:]
	}
	return "", args
}

// resolve loads a Core and the package named by arg (or the cwd's).
func resolve(arg string) (*core.Core, *core.Package, error) {
	c, err := loadCore()
	if err != nil {
		return nil, nil, err
	}
	p, err := c.ResolvePackage(arg)
	if err != nil {
		return nil, nil, err
	}
	return c, p, nil
}

// newShowCmd builds `vmono show`. Inside a session (or given pkg/session)
// each member also carries its git status in that session.
func newShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show [pkg[/session]] [--json]",
		Short: "Show the package's purpose, context, notes, sessions and members",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			p, sess, err := c.ResolveContext(packageArg(args))
			if err != nil {
				return err
			}
			s := c.Show(p, sess)
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, s)
			}
			title := s.Name
			if s.Session != "" {
				title += "/" + s.Session + "  (" + s.Branch + ")"
			}
			fprintf(out, "%s\n%s\n\n", title, s.Path)
			section(out, "Purpose", s.Purpose)
			section(out, "Context", s.Context)
			if len(s.Sessions) > 0 {
				fprintf(out, "Sessions\n")
				for _, ss := range s.Sessions {
					mark := " "
					if ss.Name == s.Session {
						mark = "*"
					}
					fprintf(out, "  %s %-16s %s\n", mark, ss.Name, ss.Branch)
				}
				fprintf(out, "\n")
			}
			fprintf(out, "Members\n")
			for _, m := range s.Members {
				if s.Session != "" {
					fprintf(out, "  %-16s %-9s %-22s %s\n", m.Name, m.Mode, where(m.MemberStatus), cleanliness(m.MemberStatus))
				} else {
					fprintf(out, "  %-16s %-9s\n", m.Name, m.Mode)
				}
				if m.Role != "" {
					fprintf(out, "      %s\n", m.Role)
				}
			}
			if len(s.Notes) > 0 {
				fprintf(out, "\nNotes\n")
				for _, n := range s.Notes {
					fprintf(out, "  %s (%s): %s\n", n.At, n.By, n.Text)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// section prints a titled text block, skipping empty ones.
func section(w io.Writer, title, text string) {
	if text == "" {
		return
	}
	fprintf(w, "%s\n  %s\n\n", title, strings.ReplaceAll(text, "\n", "\n  "))
}

// newDescribeCmd builds `vmono describe`.
func newDescribeCmd() *cobra.Command {
	var purpose, purposeFile, ctx, ctxFile string
	cmd := &cobra.Command{
		Use:   "describe [pkg] (--purpose TEXT | --purpose-file F) (--context TEXT | --context-file F)",
		Short: "Replace the package purpose and/or context",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if f.Changed("purpose") && f.Changed("purpose-file") || f.Changed("context") && f.Changed("context-file") {
				return errors.New("give either the text flag or its -file flag, not both")
			}
			var opts core.DescribeOptions
			switch {
			case f.Changed("purpose"):
				opts.Purpose = &purpose
			case f.Changed("purpose-file"):
				text, err := readTextFlag(cmd, purposeFile)
				if err != nil {
					return err
				}
				opts.Purpose = &text
			}
			switch {
			case f.Changed("context"):
				opts.Context = &ctx
			case f.Changed("context-file"):
				text, err := readTextFlag(cmd, ctxFile)
				if err != nil {
					return err
				}
				opts.Context = &text
			}
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			return c.Describe(p, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&purpose, "purpose", "", "replace the purpose")
	f.StringVar(&purposeFile, "purpose-file", "", "replace the purpose from a file (- for stdin)")
	f.StringVar(&ctx, "context", "", "replace the context")
	f.StringVar(&ctxFile, "context-file", "", "replace the context from a file (- for stdin)")
	return cmd
}

// newRoleCmd builds `vmono role`.
func newRoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "role [pkg] <member> TEXT",
		Short: "Set a member's role",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 3)
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			return c.SetRole(p, rest[0], rest[1])
		},
	}
}

// newNoteCmd builds `vmono note`.
func newNoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "note [pkg] TEXT",
		Short: "Append a note stamped with the time and author",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			return c.AddNote(p, rest[0])
		},
	}
}

// newAddCmd builds `vmono add`.
func newAddCmd() *cobra.Command {
	var reference, reuse bool
	var role, name, base string
	var copyGlobs []string
	cmd := &cobra.Command{
		Use:   "add [pkg] <repo> [--ref] [--role TEXT]",
		Short: "Add a member to the package, and so to every session (a known repo name or a path to any git repo)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			r, err := c.ResolveRepo(rest[0])
			if err != nil {
				return err
			}
			opts := core.AddOptions{
				Source: r.Path, Name: name, Reference: reference, Role: role,
				Copy: copyGlobs, ReuseBranch: reuse,
			}
			if reference {
				opts.Ref = base
			} else {
				opts.Base = base
			}
			var added string
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				m, aerr := c.AddMember(p, opts)
				added = m.Path
				return aerr
			})
			if err != nil {
				return err
			}
			names, _ := p.Sessions()
			fprintf(cmd.OutOrStdout(), "added %s (%s) to %d session(s)\n", r.Name, added, len(names))
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&reference, "ref", false, "add as a read-only reference member")
	f.StringVar(&role, "role", "", "one-line role of the member")
	f.StringVar(&name, "name", "", "member name (default: the repo directory name)")
	f.StringVar(&base, "base", "", "base ref for an active member, or the ref for a reference member (default origin/HEAD)")
	f.StringSliceVar(&copyGlobs, "copy", nil, "untracked file globs to copy from the source repo")
	f.BoolVar(&reuse, "reuse-branch", false, "check out each session branch if it already exists in the repo")
	return cmd
}

// newRmCmd builds `vmono rm`.
func newRmCmd() *cobra.Command {
	var force, deleteBranch bool
	cmd := &cobra.Command{
		Use:   "rm [pkg] <member> [--force] [--delete-branch]",
		Short: "Remove a member from the package, and so from every session",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			var res core.DeleteResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var rerr error
				res, rerr = c.RemoveMember(p, rest[0], core.RemoveOptions{Force: force, DeleteBranch: deleteBranch})
				return rerr
			})
			if err != nil {
				return err
			}
			fprintf(cmd.OutOrStdout(), "removed %s\n", rest[0])
			for _, b := range res.BranchesKept {
				fprintf(cmd.OutOrStdout(), "kept branch %s\n", b)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if the member has unsaved work in any session")
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false, "also delete each session branch if merged into its base")
	return cmd
}
