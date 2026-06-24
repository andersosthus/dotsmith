package encrypt

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// TerminalPrompter reads passphrases from the controlling terminal. When no
// terminal is available, Interactive reports false and Prompt returns a hard
// error naming the key — the documented no-TTY behavior for a file that needs a
// passphrase-protected key.
type TerminalPrompter struct{}

// Interactive reports whether stdin is a terminal.
func (TerminalPrompter) Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// Prompt reads a passphrase for keyLabel from the terminal without echoing it.
// With no terminal it returns a hard error so non-interactive compiles (e.g.
// git hooks) fail clearly rather than hanging.
func (TerminalPrompter) Prompt(keyLabel string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf(
			"passphrase required for %s but no terminal is available — "+
				"run interactively or use an unencrypted key", keyLabel,
		)
	}
	fmt.Fprintf(os.Stderr, "Enter passphrase for %s: ", keyLabel)
	pass, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read passphrase for %s: %w", keyLabel, err)
	}
	return pass, nil
}
