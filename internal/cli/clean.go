package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/linker"
)

// Injectable for testing.
var cleanFunc = linker.Clean

func newCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove managed symlinks and compiled files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := mustGetCfg(cmd)

			result, err := cleanFunc(cmd.Context(), linker.LinkConfig{
				CompileDir: cfg.CompileDir,
				TargetDir:  cfg.TargetDir,
				DryRun:     cfg.DryRun,
			})
			if err != nil {
				return fmt.Errorf("clean: %w", err)
			}

			if cfg.DryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "clean: dry-run complete (no changes made)")
			} else {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "clean: done")
			}
			warnDisowned(cmd.ErrOrStderr(), result.Disowned)
			return nil
		},
	}
}
