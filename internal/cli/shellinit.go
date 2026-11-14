package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// shellFunctions maps each supported shell to a `vcd <pkg>` function that
// changes into a package directory. A child process cannot change its
// parent's directory, so this has to be sourced into the shell.
var shellFunctions = map[string]string{
	"zsh": `vcd() {
  local dir
  dir="$(vmono path "$@")" || return $?
  cd "$dir"
}
`,
	"bash": `vcd() {
  local dir
  dir="$(vmono path "$@")" || return $?
  cd "$dir"
}
`,
	"fish": `function vcd
    set -l dir (vmono path $argv)
    or return $status
    cd $dir
end
`,
	"pwsh": `function vcd {
    $dir = vmono path @args
    if ($LASTEXITCODE -ne 0) { return }
    Set-Location $dir
}
`,
}

// newShellInitCmd builds `vmono shell-init`.
func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "shell-init zsh|bash|fish|pwsh",
		Short:     "Print a vcd <pkg> shell function; eval its output in your shell rc",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"zsh", "bash", "fish", "pwsh"},
		RunE: func(cmd *cobra.Command, args []string) error {
			fn, ok := shellFunctions[strings.ToLower(args[0])]
			if !ok {
				return fmt.Errorf("unsupported shell %q (use zsh, bash, fish or pwsh)", args[0])
			}
			fprintf(cmd.OutOrStdout(), "%s", fn)
			return nil
		},
	}
}
