package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/compiler"
)

// Injectable for testing.
var (
	compileFunc       = compiler.Compile
	writeCompiledFunc = compiler.WriteCompiled
)

func newCompileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compile",
		Short: "Discover, decrypt, and assemble dotfiles into the compile directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := mustGetCfg(cmd)
			ctx := cmd.Context()

			set, err := resolveIdentitySet(ctx, cfg)
			if err != nil {
				return fmt.Errorf("compile: %w", err)
			}

			compileCfg := compiler.CompileConfig{
				DotfilesDir: cfg.DotfilesDir,
				CompileDir:  cfg.CompileDir,
				Identity:    cfg.Identity,
				Identities:  set,
				DryRun:      cfg.DryRun,
			}
			result, err := compileFunc(ctx, compileCfg)
			if err != nil {
				return fmt.Errorf("compile: %w", err)
			}

			printDryRunReports(cmd.OutOrStdout(), result.DryRunReports)

			writeCfg := compiler.WriteConfig{
				CompileDir: cfg.CompileDir,
				DryRun:     cfg.DryRun,
			}
			stats, err := writeCompiledFunc(ctx, result, writeCfg)
			if err != nil {
				return fmt.Errorf("write compiled: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"compiled: %d written, %d reused, %d unchanged, %d pruned\n",
				stats.Written, stats.Reused, stats.Unchanged, len(stats.Pruned))
			printDanglingWarning(cmd.ErrOrStderr(), stats.Dangling)
			return nil
		},
	}
}
