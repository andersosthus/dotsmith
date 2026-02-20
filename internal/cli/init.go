package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// defaultDotsmithYML is the content written to .dotsmith.yml on init.
const defaultDotsmithYML = `# dotsmith configuration
# dotfiles_dir: ~/dotfiles  # defaults to current directory
# compile_dir: ~/.dotsmith/compiled
# target_dir: ~
# age_identity: ~/.age/key.txt
`

// Injectable for testing.
var osMkdirAllInitFunc = os.MkdirAll

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new dotfiles repository structure",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := mustGetCfg(cmd)
			dotfilesDir := cfg.DotfilesDir

			layers := []string{"base", "os", "hostname", "username", "userhost"}
			for _, layer := range layers {
				dir := filepath.Join(dotfilesDir, layer)
				if cfg.DryRun {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "would create: %s\n", dir)
					continue
				}
				if err := osMkdirAllInitFunc(dir, 0o755); err != nil {
					return fmt.Errorf("init: create %s: %w", dir, err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created: %s\n", dir)
			}

			cfgPath := filepath.Join(dotfilesDir, ".dotsmith.yml")
			if cfg.DryRun {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "would create: %s\n", cfgPath)
				return nil
			}
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				if err = os.WriteFile(cfgPath, []byte(defaultDotsmithYML), 0o644); err != nil {
					return fmt.Errorf("init: create %s: %w", cfgPath, err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created: %s\n", cfgPath)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "exists:  %s (not overwritten)\n", cfgPath)
			}
			return nil
		},
	}
}
