package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/encrypt"
)

// Injectable for testing.
var decryptFileFunc = encrypt.DecryptFile

func newDecryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "decrypt <file.age>",
		Short: "Decrypt an age-encrypted dotfile and print it to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustGetCfg(cmd)
			path := args[0]

			if !strings.HasSuffix(path, ".age") {
				return fmt.Errorf("decrypt: %s does not have .age extension", path)
			}

			ks := encrypt.KeySource{IdentityFile: cfg.AgeIdentity}
			content, err := decryptFileFunc(cmd.Context(), path, ks)
			if err != nil {
				return fmt.Errorf("decrypt %s: %w", path, err)
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), string(content))
			return err //nolint:wrapcheck // propagates stdout write error verbatim
		},
	}
}
