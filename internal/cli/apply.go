package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/linker"
)

func newApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Compile dotfiles and link them to the target directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := mustGetCfg(cmd)
			ctx := cmd.Context()

			set, err := resolveIdentitySet(ctx, cfg)
			if err != nil {
				return fmt.Errorf("apply: %w", err)
			}

			compileCfg := compiler.CompileConfig{
				DotfilesDir: cfg.DotfilesDir,
				Identity:    cfg.Identity,
				Identities:  set,
				DryRun:      cfg.DryRun,
			}
			result, err := compileFunc(ctx, compileCfg)
			if err != nil {
				return fmt.Errorf("apply: compile: %w", err)
			}

			printDryRunReports(cmd.OutOrStdout(), result.DryRunReports)

			writeCfg := compiler.WriteConfig{
				CompileDir: cfg.CompileDir,
				DryRun:     cfg.DryRun,
			}
			stats, err := writeCompiledFunc(ctx, result, writeCfg)
			if err != nil {
				return fmt.Errorf("apply: write: %w", err)
			}

			// Build FileRefs from the compile result.
			files := make([]linker.FileRef, 0, len(result.Files))
			for _, f := range result.Files {
				files = append(files, linker.FileRef{
					RelPath:     f.RelPath,
					ContentHash: f.ContentHash,
				})
			}

			linkResult, err := linkFunc(ctx, linker.LinkConfig{
				CompileDir: cfg.CompileDir,
				TargetDir:  cfg.TargetDir,
				DryRun:     cfg.DryRun,
			}, files)
			if err != nil {
				return fmt.Errorf("apply: link: %w", err)
			}

			// apply runs link immediately after compile, so any pruned file's
			// dangling symlink is resolved before control returns. The
			// dangling-symlink warning is therefore deliberately not printed here.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"compiled: %d written, %d unchanged, %d pruned; "+
					"linked: %d created, %d updated, %d unchanged, %d removed\n",
				stats.Written, stats.Unchanged, len(stats.Pruned),
				linkResult.Created, linkResult.Updated, linkResult.Unchanged, linkResult.Removed)
			return nil
		},
	}
}
