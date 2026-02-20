package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/linker"
)

// Injectable for testing.
var linkFunc = linker.Link

func newLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link",
		Short: "Create symlinks from the compile directory to the target directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := mustGetCfg(cmd)
			ctx := cmd.Context()

			// Build FileRef list from compiled files on disk.
			files, err := compiledFileRefs(cfg.CompileDir)
			if err != nil {
				return fmt.Errorf("link: %w", err)
			}

			result, err := linkFunc(ctx, linker.LinkConfig{
				CompileDir: cfg.CompileDir,
				TargetDir:  cfg.TargetDir,
				DryRun:     cfg.DryRun,
			}, files)
			if err != nil {
				return fmt.Errorf("link: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"linked: %d created, %d updated, %d unchanged\n",
				result.Created, result.Updated, result.Unchanged)
			return nil
		},
	}
}
