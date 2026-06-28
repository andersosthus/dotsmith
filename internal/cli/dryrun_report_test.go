package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andersosthus/dotsmith/internal/compiler"
)

// isolateHome points HOME and XDG at empty temp dirs so SSH discovery (on by
// default) finds nothing and the test relies solely on the configured age
// identity. Returns the home dir.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	return home
}

func TestCompileCmd_DryRun_ReportsWouldDecrypt(t *testing.T) {
	isolateHome(t)
	keyPath := generateAgeKey(t)

	root := makeDotfiles(t)
	plain := filepath.Join(root, "base", ".subfile-010.bashrc")
	writeAgeFile(t, keyPath, plain, "export SECRET=1\n")

	compileDir := t.TempDir()
	out, err := run(t,
		"--age-identity", keyPath,
		"--dotfiles-dir", root,
		"--dry-run",
		"compile",
		"--compile-dir", compileDir,
	)
	if err != nil {
		t.Fatalf("compile --dry-run: %v", err)
	}
	if !strings.Contains(out, "would decrypt") {
		t.Errorf("output %q should report 'would decrypt'", out)
	}
	if !strings.Contains(out, keyPath) {
		t.Errorf("output %q should name the resolving identity path", out)
	}
	if !strings.Contains(out, "[age]") {
		t.Errorf("output %q should name the identity kind", out)
	}
	// Zero filesystem side effects: nothing written to the compile dir.
	entries, _ := os.ReadDir(compileDir)
	if len(entries) != 0 {
		t.Errorf("dry-run wrote %d files to compile dir, want 0", len(entries))
	}
}

func TestCompileCmd_DryRun_ReportsNoMatch(t *testing.T) {
	isolateHome(t)
	fileKey := generateAgeKey(t) // file encrypted to this key
	myKey := generateAgeKey(t)   // but we only hold this key

	root := makeDotfiles(t)
	plain := filepath.Join(root, "base", ".subfile-010.bashrc")
	writeAgeFile(t, fileKey, plain, "secret\n")

	out, err := run(t,
		"--age-identity", myKey,
		"--dotfiles-dir", root,
		"--dry-run",
		"compile",
		"--compile-dir", t.TempDir(),
	)
	if err != nil {
		t.Fatalf("compile --dry-run (no match): %v", err)
	}
	if !strings.Contains(out, "would NOT decrypt") {
		t.Errorf("output %q should report 'would NOT decrypt'", out)
	}
}

func TestApplyCmd_DryRun_ReportsWouldDecrypt(t *testing.T) {
	isolateHome(t)
	keyPath := generateAgeKey(t)

	root := makeDotfiles(t)
	plain := filepath.Join(root, "base", ".subfile-010.bashrc")
	writeAgeFile(t, keyPath, plain, "export SECRET=1\n")

	out, err := run(t,
		"--age-identity", keyPath,
		"--dotfiles-dir", root,
		"--dry-run",
		"apply",
		"--compile-dir", t.TempDir(),
		"--target-dir", t.TempDir(),
	)
	if err != nil {
		t.Fatalf("apply --dry-run: %v", err)
	}
	if !strings.Contains(out, "would decrypt") {
		t.Errorf("output %q should report 'would decrypt'", out)
	}
}

func TestPrintDryRunReports_MatchAndNoMatch(t *testing.T) {
	var buf bytes.Buffer
	printDryRunReports(&buf, []compiler.DryRunReport{
		{SourcePath: "/d/a.age", Matched: true, IdentityPath: "/k/id", IdentityKind: "ssh-ed25519"},
		{SourcePath: "/d/b.age", Matched: false},
	})
	out := buf.String()
	if !strings.Contains(out, `would decrypt "/d/a.age" -> "/k/id" [ssh-ed25519]`) {
		t.Errorf("match line missing/incorrect: %q", out)
	}
	if !strings.Contains(out, `would NOT decrypt "/d/b.age"`) {
		t.Errorf("no-match line missing/incorrect: %q", out)
	}
}

// TestPrintDryRunReports_SanitizesControlChars verifies a repo-controlled
// .age path containing terminal control sequences (ANSI escapes, CR, newline)
// is rendered with %q so it cannot manipulate or spoof the terminal.
func TestPrintDryRunReports_SanitizesControlChars(t *testing.T) {
	const evil = "/d/a\x1b[2K\rEnter passphrase: \n.age"
	var buf bytes.Buffer
	printDryRunReports(&buf, []compiler.DryRunReport{
		{SourcePath: evil, Matched: true, IdentityPath: "/k/id", IdentityKind: "age"},
		{SourcePath: evil, Matched: false},
	})
	out := buf.String()
	// The raw escape, CR, and embedded newline must not appear verbatim.
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("output leaks raw control characters: %q", out)
	}
	// Exactly two report lines (one per report) plus a trailing newline; an
	// injected newline would add a third.
	if got := strings.Count(out, "\n"); got != 2 {
		t.Errorf("expected 2 lines, got %d: %q", got, out)
	}
	// The escaped form (Go-quoted) is what should be emitted.
	if !strings.Contains(out, `"/d/a\x1b[2K\rEnter passphrase: \n.age"`) {
		t.Errorf("path not rendered in escaped %%q form: %q", out)
	}
}

func TestPrintDryRunReports_Empty(t *testing.T) {
	var buf bytes.Buffer
	printDryRunReports(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for no reports, got %q", buf.String())
	}
}
