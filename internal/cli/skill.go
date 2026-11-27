package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/render"
)

// agentHelp is the line of the root help that points a coding agent at the
// skill, for a harness that has none installed.
const agentHelp = "Coding agents: unless a vmono skill is already loaded, run `vmono skill` first."

// newSkillCmd builds `vmono skill`, which prints the skills that teach a
// coding agent to use vmono, as this binary has them. With no argument it is
// the app skill's SKILL.md, for use outside a session; a reference name prints
// that file, and --session prints the skill every session carries.
//
// It reads no config and touches nothing, so it works before vmono is set up.
func newSkillCmd() *cobra.Command {
	var session, list bool
	cmd := &cobra.Command{
		Use:   "skill [reference] [--list | --session]",
		Short: "Print the skill that teaches a coding agent to use vmono",
		Long: `Print the skill that teaches a coding agent to use vmono from outside a session,
as this version of vmono has it. The skill lists references with more on each
subject; name one to print it. Inside a session the agent has a skill of its
own, which --session prints.`,
		Example: `  vmono skill
  vmono skill sessions
  vmono skill --session`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: render.AppSkillReferences(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (session || list) {
				return errors.New("name a reference, or pass --list or --session, not both")
			}
			out := cmd.OutOrStdout()
			switch {
			case session:
				fprintf(out, "%s", render.SkillMarkdown())
			case list:
				fprintf(out, "%s\n", strings.Join(render.AppSkillReferences(), "\n"))
			case len(args) > 0:
				body, ok := render.AppSkillReference(args[0])
				if !ok {
					return core.NotFound("no skill reference %q (references: %s)", args[0], strings.Join(render.AppSkillReferences(), ", "))
				}
				fprintf(out, "%s", body)
			default:
				fprintf(out, "%s", render.AppSkillMarkdown())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list the references")
	cmd.Flags().BoolVar(&session, "session", false, "print the skill used inside a session")
	cmd.MarkFlagsMutuallyExclusive("list", "session")
	return cmd
}
