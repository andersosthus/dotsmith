package encrypt

import (
	"strings"
	"testing"
)

// In the test environment stdin is not a terminal, so Interactive reports false
// and Prompt fails fast with a guidance error rather than blocking on input.

func TestTerminalPrompter_NotInteractive(t *testing.T) {
	if (TerminalPrompter{}).Interactive() {
		t.Skip("stdin is a terminal in this environment; cannot assert non-interactive path")
	}
}

func TestTerminalPrompter_PromptNoTTY(t *testing.T) {
	if (TerminalPrompter{}).Interactive() {
		t.Skip("stdin is a terminal in this environment; cannot assert no-TTY path")
	}
	_, err := (TerminalPrompter{}).Prompt("~/.ssh/id_ed25519")
	if err == nil {
		t.Fatal("expected error prompting with no terminal, got nil")
	}
	if !strings.Contains(err.Error(), "~/.ssh/id_ed25519") {
		t.Errorf("error %q should name the key", err)
	}
}
