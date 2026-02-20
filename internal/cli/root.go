// Package cli defines all Cobra commands for the dotsmith CLI.
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/config"
)

// cfgKey is the context key for the loaded Config.
type cfgKey struct{}

// mustGetCfg retrieves the loaded Config from the command context.
// PersistentPreRunE always runs before RunE, so the config is always present.
func mustGetCfg(cmd *cobra.Command) config.Config {
	return cmd.Context().Value(cfgKey{}).(config.Config)
}

// NewRootCmd constructs the root cobra.Command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	var flags config.Flags

	root := &cobra.Command{
		Use:           "dotsmith",
		Short:         "Manage dotfiles with overlay-based composition",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cmd.Context(), flags)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cmd.SetContext(context.WithValue(cmd.Context(), cfgKey{}, cfg))
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&flags.ConfigPath, "config", "", "path to .dotsmith.yml")
	pf.StringVar(&flags.DotfilesDir, "dotfiles-dir", "", "path to dotfiles repository")
	pf.StringVar(&flags.CompileDir, "compile-dir", "", "path to compiled output directory")
	pf.StringVar(&flags.TargetDir, "target-dir", "", "path to symlink target directory")
	pf.StringVar(&flags.AgeIdentity, "age-identity", "", "path to age identity file")
	pf.BoolVar(&flags.Verbose, "verbose", false, "enable verbose output")
	pf.BoolVar(&flags.DryRun, "dry-run", false, "print actions without writing files")

	root.AddCommand(
		newVersionCmd(),
		newCompileCmd(),
		newLinkCmd(),
		newApplyCmd(),
		newRenderCmd(),
		newEncryptCmd(),
		newDecryptCmd(),
		newStatusCmd(),
		newIdentityCmd(),
		newCleanCmd(),
		newInitCmd(),
		newGitCmd(),
		newShellCmd(),
	)

	return root
}

// Execute runs the root command using a background context.
func Execute() error {
	return NewRootCmd().ExecuteContext(context.Background()) //nolint:wrapcheck // cobra is top-level runner
}
