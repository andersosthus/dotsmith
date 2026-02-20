package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Injectable for testing.
var (
	genBashCompletionFunc = func(root *cobra.Command, w io.Writer) error {
		return root.GenBashCompletionV2(w, true)
	}
	genZshCompletionFunc = func(root *cobra.Command, w io.Writer) error {
		return root.GenZshCompletion(w)
	}
	genFishCompletionFunc = func(root *cobra.Command, w io.Writer) error {
		return root.GenFishCompletion(w, true)
	}
)

func newShellCmd() *cobra.Command {
	shell := &cobra.Command{
		Use:   "shell",
		Short: "Generate shell completion scripts",
		// Override PersistentPreRunE so shell works without a valid config.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
	}
	shell.AddCommand(newShellBashCmd(), newShellZshCmd(), newShellFishCmd())
	return shell
}

func newShellBashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bash",
		Short: "Generate bash completion script",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := genBashCompletionFunc(cmd.Root(), cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("shell bash: %w", err)
			}
			return nil
		},
	}
}

func newShellZshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "zsh",
		Short: "Generate zsh completion script",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := genZshCompletionFunc(cmd.Root(), cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("shell zsh: %w", err)
			}
			return nil
		},
	}
}

func newShellFishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fish",
		Short: "Generate fish completion script",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := genFishCompletionFunc(cmd.Root(), cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("shell fish: %w", err)
			}
			return nil
		},
	}
}
