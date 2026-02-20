package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newIdentityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "identity",
		Short: "Print the resolved OS, hostname, username, and user@host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := mustGetCfg(cmd).Identity
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "os:        %s\n", id.OS)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hostname:  %s\n", id.Hostname)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "username:  %s\n", id.Username)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "userhost:  %s\n", id.Userhost())
			return nil
		},
	}
}
