package cli

import (
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/integrations"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/paths"
)

// integrationEnv builds the integrations.Env for the real user: vmono's
// config directory, the home directory and $XDG_CONFIG_HOME.
func integrationEnv() (integrations.Env, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return integrations.Env{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return integrations.Env{}, err
	}
	return integrations.Env{ConfigDir: dir, Home: home, XDGConfigHome: os.Getenv("XDG_CONFIG_HOME")}, nil
}

// newIntegrationCmd builds `vmono integration`, which installs vmono's
// integrations with other tools. The files each one writes live in the
// repository's integrations/ directory and are embedded in the binary; see
// package integrations.
func newIntegrationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Install, remove and list integrations with other tools, such as tmux",
	}
	cmd.AddCommand(newIntegrationLsCmd(), newIntegrationInstallCmd(), newIntegrationUninstallCmd(), newIntegrationPrintCmd())
	return cmd
}

// newIntegrationLsCmd builds `vmono integration ls`.
func newIntegrationLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls [--json]",
		Short: "List integrations and whether each is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := integrationEnv()
			if err != nil {
				return err
			}
			var all []integrations.Status
			for _, i := range integrations.All() {
				all = append(all, i.Status(e))
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, all)
			}
			for _, st := range all {
				state := "not installed"
				switch {
				case st.Outdated:
					state = "outdated (run install again)"
				case st.Installed:
					state = "installed"
				}
				if !st.ToolFound {
					state += ", not on PATH"
				}
				fprintf(out, "%-8s %-30s %s\n", st.Name, state, st.Summary)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newIntegrationInstallCmd builds `vmono integration install`. Besides
// writing the integration's files, it turns on the launcher the integration
// pairs with (for tmux, "auto"), unless --no-launcher is given or a launcher
// other than direct is already set.
func newIntegrationInstallCmd() *cobra.Command {
	var noLauncher bool
	cmd := &cobra.Command{
		Use:       "install <name> [--no-launcher]",
		Short:     "Install an integration: write its files and wire them into the tool",
		Args:      cobra.ExactArgs(1),
		ValidArgs: integrationNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := integrations.Get(args[0])
			if err != nil {
				return core.NotFound("%s", err.Error())
			}
			e, err := integrationEnv()
			if err != nil {
				return err
			}
			changes, err := i.Install(e)
			out := cmd.OutOrStdout()
			for _, c := range changes {
				fprintf(out, "%-8s %s\n", c.Action, paths.Contract(c.Path))
			}
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				fprintf(out, "%s integration already installed and up to date\n", i.Name)
			}
			if i.Launcher != "" && !noLauncher {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				if cfg.Launcher == "" || cfg.Launcher == config.LauncherDirect {
					cfg.Launcher = i.Launcher
					if err := cfg.Save(); err != nil {
						return err
					}
					fprintf(out, "set      launcher = %q in %s\n", i.Launcher, configFileForDisplay())
				}
			}
			if st := i.Status(e); st.Wired != "" && i.Name == "tmux" {
				fprintf(out, "\nReload tmux to pick it up: tmux source-file %s\n", paths.Contract(st.Wired))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noLauncher, "no-launcher", false, "only install the files; leave the launcher setting alone")
	return cmd
}

// newIntegrationUninstallCmd builds `vmono integration uninstall`. It
// removes what install added and, when the launcher setting is the one the
// integration turns on (or names its tool), puts it back to direct.
func newIntegrationUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "uninstall <name>",
		Short:     "Remove what an integration's install added",
		Args:      cobra.ExactArgs(1),
		ValidArgs: integrationNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := integrations.Get(args[0])
			if err != nil {
				return core.NotFound("%s", err.Error())
			}
			e, err := integrationEnv()
			if err != nil {
				return err
			}
			changes, err := i.Uninstall(e)
			out := cmd.OutOrStdout()
			for _, c := range changes {
				fprintf(out, "%-8s %s\n", c.Action, paths.Contract(c.Path))
			}
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				fprintf(out, "%s integration was not installed\n", i.Name)
			}
			if i.Launcher == "" {
				return nil
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if slices.Contains([]string{i.Launcher, i.Tool}, cfg.Launcher) && cfg.Launcher != config.LauncherDirect {
				cfg.Launcher = config.LauncherDirect
				if err := cfg.Save(); err != nil {
					return err
				}
				fprintf(out, "set      launcher = %q in %s\n", config.LauncherDirect, configFileForDisplay())
			}
			return nil
		},
	}
}

// newIntegrationPrintCmd builds `vmono integration print`, which writes an
// integration's files to stdout for people who manage their dotfiles by hand,
// in the spirit of `vmono shell-init`.
func newIntegrationPrintCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "print <name>",
		Short:     "Print an integration's files instead of installing them",
		Args:      cobra.ExactArgs(1),
		ValidArgs: integrationNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := integrations.Get(args[0])
			if err != nil {
				return core.NotFound("%s", err.Error())
			}
			for _, f := range i.Files {
				b, err := i.Content(f)
				if err != nil {
					return err
				}
				fprintf(cmd.OutOrStdout(), "%s", b)
			}
			return nil
		},
	}
}

// integrationNames lists the integrations for shell completion.
func integrationNames() []string {
	var out []string
	for _, i := range integrations.All() {
		out = append(out, i.Name)
	}
	return out
}

// configFileForDisplay returns the config file path with the home directory
// shortened to "~".
func configFileForDisplay() string {
	file, err := paths.ConfigFile()
	if err != nil {
		return "config.toml"
	}
	return paths.Contract(file)
}
