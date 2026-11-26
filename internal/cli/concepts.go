package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/terms"
)

// conceptsWidth is the column the text of `vmono help concepts` wraps at.
const conceptsWidth = 78

// newConceptsTopic builds `vmono help concepts`. It has nothing to run, which
// makes cobra list it as a help topic rather than as a command.
func newConceptsTopic() *cobra.Command {
	return &cobra.Command{
		Use:   "concepts",
		Short: "What package, session, member and vmono's other terms mean",
		Long:  conceptsHelp(),
	}
}

// conceptsHelp sets out every term with its explanation indented under it.
func conceptsHelp() string {
	var b strings.Builder
	b.WriteString("The terms vmono uses, in its commands and in the terminal UI.")
	for _, e := range terms.Entries {
		b.WriteString("\n\n" + e.Term)
		line := ""
		for _, word := range strings.Fields(e.Long) {
			if line != "" && len(line)+1+len(word) > conceptsWidth {
				b.WriteString("\n" + line)
				line = ""
			}
			if line == "" {
				line = "  " + word
			} else {
				line += " " + word
			}
		}
		b.WriteString("\n" + line)
	}
	return b.String()
}
