package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is injected at build time via ldflags:
//
//	-ldflags "-X github.com/andersosthus/dotsmith/internal/cli.Version=1.2.3"
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the dotsmith version",
		// Override PersistentPreRunE so version works without a valid config.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dotsmith %s\n", Version)
			return nil
		},
	}
}
