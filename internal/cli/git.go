package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	hookBegin = "# --- dotsmith hook begin ---"
	hookEnd   = "# --- dotsmith hook end ---"
	hookBody  = "dotsmith apply --verbose || true"
	hookBlock = hookBegin + "\n" + hookBody + "\n" + hookEnd + "\n"
)

var gitHookFiles = []string{"post-merge", "post-checkout"}

// Injectable for testing.
var osReadFileGitFunc = os.ReadFile
var osWriteFileGitFunc = os.WriteFile
var osMkdirAllGitFunc = os.MkdirAll
var osChmodGitFunc = os.Chmod
var osGetWdFunc = os.Getwd

func newGitCmd() *cobra.Command {
	git := &cobra.Command{
		Use:   "git",
		Short: "Manage git hook integration",
	}
	git.AddCommand(newGitInstallCmd(), newGitRemoveCmd())
	return git
}

func newGitInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Append dotsmith hook to post-merge and post-checkout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			branch, err := cmd.Flags().GetString("branch")
			if err != nil {
				return fmt.Errorf("git install: get --branch flag: %w", err)
			}
			hooksDir, err := findHooksDir()
			if err != nil {
				return err
			}
			if err = osMkdirAllGitFunc(hooksDir, 0o755); err != nil {
				return fmt.Errorf("git install: create hooks dir: %w", err)
			}
			for _, name := range gitHookFiles {
				if err = installHook(hooksDir, name, branch, cmd); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().String("branch", "", "only run dotsmith apply when the current branch matches this name")
	return cmd
}

func newGitRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove dotsmith hook from post-merge and post-checkout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hooksDir, err := findHooksDir()
			if err != nil {
				return err
			}
			for _, name := range gitHookFiles {
				if err = removeHook(hooksDir, name, cmd); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func installHook(hooksDir, name, branch string, cmd *cobra.Command) error {
	path := filepath.Join(hooksDir, name)
	existing, err := osReadFileGitFunc(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("git install: read %s: %w", path, err)
	}
	content := string(existing)
	if strings.Contains(content, hookBegin) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hook already present: %s\n", path)
		return nil
	}
	if content == "" {
		content = "#!/bin/sh\n"
	}
	content += hookBlockForBranch(branch)
	if err = osWriteFileGitFunc(path, []byte(content), 0o755); err != nil {
		return fmt.Errorf("git install: write %s: %w", path, err)
	}
	if err = osChmodGitFunc(path, 0o755); err != nil {
		return fmt.Errorf("git install: chmod %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "installed hook: %s\n", path)
	return nil
}

// hookBlockForBranch returns the hook block to insert. When branch is empty, the plain
// unconditional block is returned. When branch is set, the apply command is wrapped in a
// branch guard that checks git branch --show-current.
func hookBlockForBranch(branch string) string {
	if branch == "" {
		return hookBlock
	}
	body := "if [ \"$(git branch --show-current)\" = \"" + branch + "\" ]; then\n" +
		"  " + hookBody + "\n" +
		"fi"
	return hookBegin + "\n" + body + "\n" + hookEnd + "\n"
}

func removeHook(hooksDir, name string, cmd *cobra.Command) error {
	path := filepath.Join(hooksDir, name)
	data, err := osReadFileGitFunc(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to remove
		}
		return fmt.Errorf("git remove: read %s: %w", path, err)
	}
	updated := stripHookBlock(string(data))
	if updated == string(data) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hook not found: %s\n", path)
		return nil
	}
	if err = osWriteFileGitFunc(path, []byte(updated), 0o755); err != nil {
		return fmt.Errorf("git remove: write %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed hook: %s\n", path)
	return nil
}

// stripHookBlock removes the dotsmith hook block from content.
func stripHookBlock(content string) string {
	begin := strings.Index(content, hookBegin)
	if begin == -1 {
		return content
	}
	end := strings.Index(content[begin:], hookEnd)
	if end == -1 {
		return content
	}
	end += begin + len(hookEnd) + 1 // +1 for trailing newline
	if end > len(content) {
		end = len(content)
	}
	return content[:begin] + content[end:]
}

// findHooksDir returns the path to .git/hooks in the current directory tree.
func findHooksDir() (string, error) {
	cwd, err := osGetWdFunc()
	if err != nil {
		return "", fmt.Errorf("git: get working directory: %w", err)
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".git", "hooks")
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("git: no .git directory found in %s or its parents", cwd)
}
