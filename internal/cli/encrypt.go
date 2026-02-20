package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/encrypt"
)

// Injectable for testing.
var encryptFileInPlaceFunc = encrypt.EncryptFileInPlace

func newEncryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "encrypt <file>",
		Short: "Encrypt a dotfile with age, writing <file>.age and removing the original",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := mustGetCfg(cmd)
			path := args[0]

			if strings.HasSuffix(path, ".age") {
				return fmt.Errorf("encrypt: %s already has .age extension", path)
			}
			if cfg.AgeIdentity == "" {
				return fmt.Errorf("encrypt: age_identity not configured (set age_identity in .dotsmith.yml)")
			}

			ks := encrypt.KeySource{IdentityFile: cfg.AgeIdentity}
			if err := encryptFileInPlaceFunc(cmd.Context(), path, ks); err != nil {
				return fmt.Errorf("encrypt %s: %w", path, err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "encrypted: %s → %s.age\n", path, path)
			return nil
		},
	}
}
