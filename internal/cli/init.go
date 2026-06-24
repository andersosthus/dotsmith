package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/config"
)

// defaultDotsmithYML is the content written to the user config file on init.
const defaultDotsmithYML = `# dotsmith configuration
# dotfiles_dir: ~/.dotfiles  # defaults to ~/.dotfiles
# compile_dir: ~/.dotcompiled
# target_dir: ~
# age:
#   identity_file: ~/.dotsmith-age-key  # native age key (optional)
#   identities:                         # extra identity paths (age or SSH, auto-detected)
#     - ~/.ssh/id_ed25519
#   ssh_discovery: true                 # scan ~/.ssh for usable SSH keys (default: true)
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

			// Config lives outside the repo, at the user-level location.
			cfgPath := config.UserConfigPath()
			if cfg.DryRun {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "would create: %s\n", cfgPath)
				return nil
			}
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				if err = osMkdirAllInitFunc(filepath.Dir(cfgPath), 0o755); err != nil {
					return fmt.Errorf("init: create %s: %w", filepath.Dir(cfgPath), err)
				}
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
