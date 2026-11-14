package cli

import (
	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/paths"
)

// newInitCmd builds `vmono init`, which writes the global config without
// prompting. Existing config values are kept unless overridden by a flag.
func newInitCmd() *cobra.Command {
	var sourceRoots []string
	var packageRoot string
	cmd := &cobra.Command{
		Use:   "init [--source-root P]... [--package-root P]",
		Short: "Write the global config without prompts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if len(sourceRoots) > 0 {
				cfg.SourceRoots = sourceRoots
			}
			if packageRoot != "" {
				cfg.PackageRoot = packageRoot
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			file, _ := paths.ConfigFile()
			fprintf(cmd.OutOrStdout(), "wrote %s\n", file)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&sourceRoots, "source-root", nil, "directory scanned for repos (repeatable)")
	cmd.Flags().StringVar(&packageRoot, "package-root", "", "directory holding packages")
	return cmd
}
