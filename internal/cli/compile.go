package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/encrypt"
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

			compileCfg := compiler.CompileConfig{
				DotfilesDir: cfg.DotfilesDir,
				Identity:    cfg.Identity,
				KeySource:   encrypt.KeySource{IdentityFile: cfg.AgeIdentity},
			}
			result, err := compileFunc(ctx, compileCfg)
			if err != nil {
				return fmt.Errorf("compile: %w", err)
			}

			writeCfg := compiler.WriteConfig{
				CompileDir: cfg.CompileDir,
				DryRun:     cfg.DryRun,
			}
			stats, err := writeCompiledFunc(ctx, result, writeCfg)
			if err != nil {
				return fmt.Errorf("write compiled: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "compiled: %d written, %d unchanged\n",
				stats.Written, stats.Unchanged)
			return nil
		},
	}
}
