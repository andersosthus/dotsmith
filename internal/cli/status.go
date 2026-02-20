package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/linker"
)

// Injectable for testing.
var statusFunc = linker.Status

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the status of managed symlinks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := mustGetCfg(cmd)

			entries, err := statusFunc(cmd.Context(), linker.LinkConfig{
				CompileDir: cfg.CompileDir,
				TargetDir:  cfg.TargetDir,
			})
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}

			if len(entries) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no managed symlinks")
				return nil
			}

			// Sort for deterministic output.
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].RelPath < entries[j].RelPath
			})

			for _, e := range entries {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-40s %s\n", e.RelPath, e.Kind)
			}
			return nil
		},
	}
}
