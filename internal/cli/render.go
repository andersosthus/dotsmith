package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/encrypt"
)

func newRenderCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "render <relpath>",
		Short: "Compile a single dotfile and print it to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustGetCfg(cmd)
			relPath := args[0]

			result, err := compileFunc(cmd.Context(), compiler.CompileConfig{
				DotfilesDir: cfg.DotfilesDir,
				Identity:    cfg.Identity,
				KeySource:   encrypt.KeySource{IdentityFile: cfg.AgeIdentity},
			})
			if err != nil {
				return fmt.Errorf("render: %w", err)
			}

			for _, f := range result.Files {
				if f.RelPath == relPath {
					_, err = fmt.Fprint(cmd.OutOrStdout(), string(f.Content))
					return err //nolint:wrapcheck // propagates stdout write error verbatim
				}
			}
			return fmt.Errorf("render: %q not found in dotfiles", relPath)
		},
	}
}
