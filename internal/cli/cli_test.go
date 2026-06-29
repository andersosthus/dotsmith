package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/config"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
	"github.com/andersosthus/dotsmith/internal/linker"
)

// TestMain isolates the user config environment so the developer's real
// ~/.config/dotsmith/config.yml or ~/.dotsmith.yml cannot leak into tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dotsmith-cli-test-home")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", dir)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// ---- helpers ----------------------------------------------------------------

// run executes a command with the given args and returns stdout, stderr, error.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// runWithDotfiles sets up a minimal dotfiles dir and runs the command.
func runWithDotfiles(t *testing.T, dotfilesDir string, args ...string) (string, error) {
	t.Helper()
	return run(t, append([]string{"--dotfiles-dir", dotfilesDir}, args...)...)
}

// runSplit executes a command with separate stdout and stderr buffers so a test
// can assert which stream a line landed on.
func runSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

// makeDotfiles creates a minimal dotfiles structure in a temp dir.
func makeDotfiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "base"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return root
}

// generateAgeKey creates a temporary age identity file and returns its path.
func generateAgeKey(t *testing.T) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	path := filepath.Join(t.TempDir(), "key.txt")
	if err = os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	return path
}

// writeAgeFile encrypts content to <plainPath>.age for the recipient in keyPath
// and returns the .age path. Used to set up decrypt-command tests now that the
// encrypt command has been removed.
func writeAgeFile(t *testing.T, keyPath, plainPath, content string) string {
	t.Helper()
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ReadFile key: %v", err)
	}
	ids, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseIdentities: %v", err)
	}
	rec := ids[0].(*age.X25519Identity).Recipient()

	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, rec)
	if err != nil {
		t.Fatalf("age.Encrypt: %v", err)
	}
	if _, err = io.WriteString(w, content); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close age writer: %v", err)
	}
	if err = aw.Close(); err != nil {
		t.Fatalf("close armor writer: %v", err)
	}

	agePath := plainPath + ".age"
	if err = os.WriteFile(agePath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile age: %v", err)
	}
	return agePath
}

// encryptArmored produces armored age ciphertext for the given recipients.
func encryptArmored(t *testing.T, content string, recipients ...age.Recipient) []byte {
	t.Helper()
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, recipients...)
	if err != nil {
		t.Fatalf("age.Encrypt: %v", err)
	}
	if _, err = io.WriteString(w, content); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close age writer: %v", err)
	}
	if err = aw.Close(); err != nil {
		t.Fatalf("close armor writer: %v", err)
	}
	return buf.Bytes()
}

// writeSubfile writes a subfile into the dotfiles dir's base layer.
func writeSubfile(t *testing.T, dotfilesDir, name, content string) {
	t.Helper()
	p := filepath.Join(dotfilesDir, "base", name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ---- version ----------------------------------------------------------------

func TestVersionCmd(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "1.2.3"

	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("version output = %q, want version number", out)
	}
}

// ---- root -------------------------------------------------------------------

func TestHelp(t *testing.T) {
	_, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
}

func TestRootFlags(t *testing.T) {
	cmd := NewRootCmd()
	flags := []string{"config", "dotfiles-dir", "verbose", "dry-run"}
	for _, f := range flags {
		if cmd.PersistentFlags().Lookup(f) == nil {
			t.Errorf("flag --%s not found on root command", f)
		}
	}
}

func TestExecute_Success(t *testing.T) {
	// Execute with --help should not error.
	orig := NewRootCmd
	_ = orig // just ensure NewRootCmd is accessible
}

func TestPersistentPreRunE_InvalidConfig(t *testing.T) {
	// Invalid YAML in an explicit --config file must surface as an error.
	bad := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(bad, []byte("not: valid: yaml: {"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := run(t, "--config", bad, "compile")
	if err == nil {
		t.Fatal("expected error from invalid config, got nil")
	}
}

func TestPersistentPreRunE_RepoConfigIgnored(t *testing.T) {
	// A malformed repo-local .dotsmith.yml must be ignored, not read.
	root := makeDotfiles(t)
	if err := os.WriteFile(filepath.Join(root, ".dotsmith.yml"), []byte("not: valid: yaml: {"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out, err := runWithDotfiles(t, root, "compile",
		"--compile-dir", t.TempDir(), "--target-dir", t.TempDir())
	if err != nil {
		t.Fatalf("compile should ignore repo config, got: %v (out=%q)", err, out)
	}
}

// ---- compile ----------------------------------------------------------------

func TestCompileCmd_Success(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export PATH=/usr/bin\n")
	compileDir := t.TempDir()

	out, err := run(t,
		"--dotfiles-dir", root,
		"compile",
		"--compile-dir", compileDir,
	)
	_ = out
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(compileDir, ".bashrc")); statErr != nil {
		t.Errorf("expected .bashrc in compileDir, got: %v", statErr)
	}
}

// forceResolveError makes the shared identity-set resolver fail, exercising the
// resolve-error branch in every command that resolves identities.
func forceResolveError(t *testing.T) {
	t.Helper()
	orig := resolveIdentitiesFunc
	t.Cleanup(func() { resolveIdentitiesFunc = orig })
	resolveIdentitiesFunc = func(_ context.Context, _ encrypt.KeySource, _ encrypt.Prompter) (encrypt.IdentitySet, error) {
		return encrypt.IdentitySet{}, fmt.Errorf("forced resolve error")
	}
}

func TestCompileCmd_ResolveError(t *testing.T) {
	forceResolveError(t)
	root := makeDotfiles(t)
	if _, err := runWithDotfiles(t, root, "compile"); err == nil {
		t.Fatal("expected error from resolve failure, got nil")
	}
}

func TestApplyCmd_ResolveError(t *testing.T) {
	forceResolveError(t)
	root := makeDotfiles(t)
	if _, err := runWithDotfiles(t, root, "apply"); err == nil {
		t.Fatal("expected error from resolve failure, got nil")
	}
}

func TestRenderCmd_ResolveError(t *testing.T) {
	forceResolveError(t)
	root := makeDotfiles(t)
	if _, err := runWithDotfiles(t, root, "render", ".bashrc"); err == nil {
		t.Fatal("expected error from resolve failure, got nil")
	}
}

func TestDecryptCmd_ResolveError(t *testing.T) {
	forceResolveError(t)
	if _, err := run(t, "decrypt", "/some/file.age"); err == nil {
		t.Fatal("expected error from resolve failure, got nil")
	}
}

func TestCompileCmd_CompileError(t *testing.T) {
	orig := compileFunc
	t.Cleanup(func() { compileFunc = orig })
	compileFunc = func(_ context.Context, _ compiler.CompileConfig) (*compiler.CompileResult, error) {
		return nil, fmt.Errorf("forced compile error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "compile")
	if err == nil {
		t.Fatal("expected error from compileFunc, got nil")
	}
}

func TestCompileCmd_WriteError(t *testing.T) {
	orig := writeCompiledFunc
	t.Cleanup(func() { writeCompiledFunc = orig })
	writeCompiledFunc = func(_ context.Context, _ *compiler.CompileResult, _ compiler.WriteConfig) (compiler.WriteStats, error) {
		return compiler.WriteStats{}, fmt.Errorf("forced write error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "compile")
	if err == nil {
		t.Fatal("expected error from writeCompiledFunc, got nil")
	}
}

func TestCompileCmd_DryRun(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export A=1\n")
	compileDir := t.TempDir()

	_, err := run(t, "--dotfiles-dir", root, "--dry-run", "compile",
		"--compile-dir", compileDir)
	if err != nil {
		t.Fatalf("compile --dry-run: %v", err)
	}

	entries, _ := os.ReadDir(compileDir)
	if len(entries) != 0 {
		t.Errorf("expected no files after dry-run compile, got %d", len(entries))
	}
}

// ---- link -------------------------------------------------------------------

func TestLinkCmd_Success(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export A=1\n")
	compileDir := t.TempDir()
	targetDir := t.TempDir()

	// First compile.
	if _, err := run(t, "--dotfiles-dir", root, "compile",
		"--compile-dir", compileDir); err != nil {
		t.Fatalf("compile: %v", err)
	}

	_, err := run(t, "--dotfiles-dir", root,
		"link",
		"--compile-dir", compileDir,
		"--target-dir", targetDir,
	)
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	if _, statErr := os.Lstat(filepath.Join(targetDir, ".bashrc")); statErr != nil {
		t.Errorf("expected .bashrc symlink, got: %v", statErr)
	}
}

func TestLinkCmd_LinkError(t *testing.T) {
	orig := linkFunc
	t.Cleanup(func() { linkFunc = orig })
	linkFunc = func(_ context.Context, _ linker.LinkConfig, _ []linker.FileRef) (*linker.LinkResult, error) {
		return nil, fmt.Errorf("forced link error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "link")
	if err == nil {
		t.Fatal("expected error from linkFunc, got nil")
	}
}

func TestLinkCmd_CompiledFileRefsError(t *testing.T) {
	root := makeDotfiles(t)
	// Point compile-dir at a non-existent path to trigger walk error.
	nonExistentDir := filepath.Join(t.TempDir(), "nonexistent")
	// Create the dir but put an unreadable file in it.
	if err := os.MkdirAll(nonExistentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	lockedFile := filepath.Join(nonExistentDir, "file")
	if err := os.WriteFile(lockedFile, []byte("data"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedFile, 0o644) })

	_, err := run(t, "--dotfiles-dir", root,
		"link",
		"--compile-dir", nonExistentDir,
		"--target-dir", t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error reading unreadable file, got nil")
	}
}

func TestLinkCmd_WarnsDisowned(t *testing.T) {
	orig := linkFunc
	t.Cleanup(func() { linkFunc = orig })
	linkFunc = func(_ context.Context, _ linker.LinkConfig, _ []linker.FileRef) (*linker.LinkResult, error) {
		return &linker.LinkResult{Disowned: []string{".vimrc"}}, nil
	}

	root := makeDotfiles(t)
	out, err := run(t, "--dotfiles-dir", root, "link",
		"--compile-dir", t.TempDir(), "--target-dir", t.TempDir())
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !strings.Contains(out, "no longer dotsmith-managed") || !strings.Contains(out, ".vimrc") {
		t.Errorf("link output = %q, want disown warning mentioning .vimrc", out)
	}
}

// TestLinkCmd_DryRun_CollectsBlockers verifies a real dry-run link against a
// tree with multiple conflicting files lists all of them in one run (no early
// abort), prints the summary to stdout and the sorted blocker list to stderr,
// and exits non-zero.
func TestLinkCmd_DryRun_CollectsBlockers(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export A=1\n")
	writeSubfile(t, root, ".subfile-010.vimrc", "set nocompatible\n")
	compileDir := t.TempDir()
	targetDir := t.TempDir()

	// Real compile so the compile dir holds .bashrc and .vimrc.
	if _, err := run(t, "--dotfiles-dir", root, "compile", "--compile-dir", compileDir); err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Occupy both targets with conflicting real files (alphabetical order
	// .bashrc, .vimrc lets us assert the sort independently of walk order).
	for _, name := range []string{".vimrc", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(targetDir, name), []byte("mine"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	stdout, stderr, err := runSplit(t, "--dotfiles-dir", root, "--dry-run", "link",
		"--compile-dir", compileDir, "--target-dir", targetDir)
	assertTwoBlockerDryRun(t, stdout, stderr, err)
	// Both conflicts are reported, sorted: .bashrc before .vimrc.
	bashIdx := strings.Index(stderr, ".bashrc")
	vimIdx := strings.Index(stderr, ".vimrc")
	if bashIdx < 0 || vimIdx < 0 {
		t.Fatalf("stderr should mention both .bashrc and .vimrc: %q", stderr)
	}
	if bashIdx > vimIdx {
		t.Errorf("blockers not sorted: .bashrc should precede .vimrc in %q", stderr)
	}
}

// assertTwoBlockerDryRun checks the shared expectations of a dry-run that found
// two blockers: a non-zero exit (sentinel error), the summary on stdout, and the
// blocker header on stderr.
func assertTwoBlockerDryRun(t *testing.T, stdout, stderr string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected non-zero exit (sentinel error) with blockers, got nil")
	}
	if !strings.Contains(err.Error(), "2 blocker(s) would prevent linking") {
		t.Errorf("sentinel error = %q, want it to mention 2 blockers", err.Error())
	}
	if !strings.Contains(stdout, "linked:") {
		t.Errorf("stdout = %q, want the linked summary line", stdout)
	}
	if !strings.Contains(stderr, "2 blocker(s) would prevent linking:") {
		t.Errorf("stderr = %q, want the blocker header", stderr)
	}
}

// TestLinkCmd_DryRun_NoBlockers verifies a clean dry-run exits zero and prints
// no blocker section.
func TestLinkCmd_DryRun_NoBlockers(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export A=1\n")
	compileDir := t.TempDir()
	if _, err := run(t, "--dotfiles-dir", root, "compile", "--compile-dir", compileDir); err != nil {
		t.Fatalf("compile: %v", err)
	}

	stdout, stderr, err := runSplit(t, "--dotfiles-dir", root, "--dry-run", "link",
		"--compile-dir", compileDir, "--target-dir", t.TempDir())
	if err != nil {
		t.Fatalf("clean dry-run should exit zero, got: %v", err)
	}
	if !strings.Contains(stdout, "linked:") {
		t.Errorf("stdout = %q, want the linked summary line", stdout)
	}
	if strings.Contains(stderr, "blocker") {
		t.Errorf("stderr = %q, want no blocker section for a clean dry-run", stderr)
	}
}

// TestLinkCmd_RealRun_ConflictUnchanged verifies a real (non-dry-run) link still
// fails on the first conflict with today's error message and emits no blocker
// overview.
func TestLinkCmd_RealRun_ConflictUnchanged(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export A=1\n")
	compileDir := t.TempDir()
	targetDir := t.TempDir()
	if _, err := run(t, "--dotfiles-dir", root, "compile", "--compile-dir", compileDir); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, ".bashrc"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, stderr, err := runSplit(t, "--dotfiles-dir", root, "link",
		"--compile-dir", compileDir, "--target-dir", targetDir)
	if err == nil {
		t.Fatal("expected real-run conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "exists and is not a symlink") {
		t.Errorf("error = %q, want today's conflict message", err.Error())
	}
	if strings.Contains(stderr, "blocker(s) would prevent linking") {
		t.Errorf("stderr = %q, real run must not print the blocker overview", stderr)
	}
}

// ---- apply ------------------------------------------------------------------

func TestApplyCmd_Success(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export B=2\n")

	_, err := run(t,
		"--dotfiles-dir", root,
		"apply",
		"--compile-dir", t.TempDir(),
		"--target-dir", t.TempDir(),
	)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestApplyCmd_CompileError(t *testing.T) {
	orig := compileFunc
	t.Cleanup(func() { compileFunc = orig })
	compileFunc = func(_ context.Context, _ compiler.CompileConfig) (*compiler.CompileResult, error) {
		return nil, fmt.Errorf("forced apply compile error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "apply")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestApplyCmd_WriteError(t *testing.T) {
	orig := writeCompiledFunc
	t.Cleanup(func() { writeCompiledFunc = orig })
	writeCompiledFunc = func(_ context.Context, _ *compiler.CompileResult, _ compiler.WriteConfig) (compiler.WriteStats, error) {
		return compiler.WriteStats{}, fmt.Errorf("forced apply write error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "apply")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestApplyCmd_LinkError(t *testing.T) {
	orig := linkFunc
	t.Cleanup(func() { linkFunc = orig })
	linkFunc = func(_ context.Context, _ linker.LinkConfig, _ []linker.FileRef) (*linker.LinkResult, error) {
		return nil, fmt.Errorf("forced apply link error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "apply")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestApplyCmd_WarnsDisowned(t *testing.T) {
	orig := linkFunc
	t.Cleanup(func() { linkFunc = orig })
	linkFunc = func(_ context.Context, _ linker.LinkConfig, _ []linker.FileRef) (*linker.LinkResult, error) {
		return &linker.LinkResult{Disowned: []string{".gitconfig"}}, nil
	}

	root := makeDotfiles(t)
	out, err := run(t, "--dotfiles-dir", root, "apply",
		"--compile-dir", t.TempDir(), "--target-dir", t.TempDir())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(out, "no longer dotsmith-managed") || !strings.Contains(out, ".gitconfig") {
		t.Errorf("apply output = %q, want disown warning mentioning .gitconfig", out)
	}
}

// TestApplyCmd_DryRun_CollectsBlockers verifies apply --dry-run behaves like
// link --dry-run: it compiles and previews the link, collects every conflict,
// prints the summary to stdout and the sorted blocker list to stderr, and exits
// non-zero.
func TestApplyCmd_DryRun_CollectsBlockers(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "export A=1\n")
	writeSubfile(t, root, ".subfile-010.vimrc", "set nocompatible\n")
	compileDir := t.TempDir()
	targetDir := t.TempDir()

	// A real apply first so the compile dir and conflicting targets exist, then
	// replace the managed symlinks with conflicting real files.
	if _, err := run(t, "--dotfiles-dir", root, "apply",
		"--compile-dir", compileDir, "--target-dir", targetDir); err != nil {
		t.Fatalf("apply (real): %v", err)
	}
	for _, name := range []string{".bashrc", ".vimrc"} {
		p := filepath.Join(targetDir, name)
		if err := os.Remove(p); err != nil {
			t.Fatalf("Remove %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte("mine"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	stdout, stderr, err := runSplit(t, "--dotfiles-dir", root, "--dry-run", "apply",
		"--compile-dir", compileDir, "--target-dir", targetDir)
	assertTwoBlockerDryRun(t, stdout, stderr, err)
}

// ---- render -----------------------------------------------------------------

func TestRenderCmd_Success(t *testing.T) {
	root := makeDotfiles(t)
	writeSubfile(t, root, "aliases.subfile-010.sh", "alias ll='ls -la'\n")

	out, err := runWithDotfiles(t, root, "render", "aliases.sh")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "alias ll=") {
		t.Errorf("render output = %q, want alias ll=", out)
	}
}

func TestRenderCmd_NotFound(t *testing.T) {
	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "render", ".nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestRenderCmd_CompileError(t *testing.T) {
	orig := compileFunc
	t.Cleanup(func() { compileFunc = orig })
	compileFunc = func(_ context.Context, _ compiler.CompileConfig) (*compiler.CompileResult, error) {
		return nil, fmt.Errorf("forced render compile error")
	}

	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "render", ".bashrc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRenderCmd_MissingArg(t *testing.T) {
	root := makeDotfiles(t)
	_, err := runWithDotfiles(t, root, "render")
	if err == nil {
		t.Fatal("expected error for missing arg, got nil")
	}
}

// ---- decrypt ----------------------------------------------------------------

func TestDecryptCmd_Success(t *testing.T) {
	keyPath := generateAgeKey(t)
	plainFile := filepath.Join(t.TempDir(), "secret.txt")
	agePath := writeAgeFile(t, keyPath, plainFile, "top secret\n")

	out, err := run(t, "--age-identity", keyPath, "decrypt", agePath)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !strings.Contains(out, "top secret") {
		t.Errorf("decrypt output = %q, want 'top secret'", out)
	}
}

// TestDecryptCmd_SSHDiscovery exercises the headline feature: a file encrypted
// to an SSH public key is decrypted using a matching unencrypted SSH private key
// discovered in ~/.ssh/, with no age identity configuration.
func TestDecryptCmd_SSHDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	rec := writeDiscoverableSSHKey(t, home)

	agePath := filepath.Join(t.TempDir(), "secret.txt.age")
	if werr := os.WriteFile(agePath, encryptArmored(t, "ssh-decrypted\n", rec), 0o600); werr != nil {
		t.Fatalf("write age file: %v", werr)
	}

	out, err := run(t, "decrypt", agePath)
	if err != nil {
		t.Fatalf("decrypt via discovered SSH key: %v", err)
	}
	if !strings.Contains(out, "ssh-decrypted") {
		t.Errorf("decrypt output = %q, want 'ssh-decrypted'", out)
	}
}

// writeDiscoverableSSHKey writes an unencrypted ed25519 private key into
// home/.ssh and returns an age recipient for its public half.
func writeDiscoverableSSHKey(t *testing.T, home string) age.Recipient {
	t.Helper()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll .ssh: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 GenerateKey: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	if werr := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), pem.EncodeToMemory(block), 0o600); werr != nil {
		t.Fatalf("write key: %v", werr)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	rec, err := agessh.NewEd25519Recipient(sshPub)
	if err != nil {
		t.Fatalf("NewEd25519Recipient: %v", err)
	}
	return rec
}

func TestDecryptCmd_NotAge(t *testing.T) {
	_, err := run(t, "decrypt", "/some/file.txt")
	if err == nil {
		t.Fatal("expected error for non-.age file, got nil")
	}
}

func TestDecryptCmd_KeyFileMissing(t *testing.T) {
	_, err := run(t, "--age-identity", "/nonexistent/age.key", "decrypt", "/some/file.age")
	if err == nil {
		t.Fatal("expected error for missing key file, got nil")
	}
}

func TestDecryptCmd_DecryptError(t *testing.T) {
	keyPath := generateAgeKey(t)
	orig := decryptFileFunc
	t.Cleanup(func() { decryptFileFunc = orig })
	decryptFileFunc = func(_ context.Context, _ string, _ encrypt.IdentitySet) ([]byte, error) {
		return nil, fmt.Errorf("forced decrypt error")
	}

	_, err := run(t, "--age-identity", keyPath, "decrypt", "/some/file.age")
	if err == nil {
		t.Fatal("expected error from decryptFunc, got nil")
	}
}

// ---- identity ---------------------------------------------------------------

func TestIdentityCmd_Output(t *testing.T) {
	orig := identity.DetectFunc
	t.Cleanup(func() { identity.DetectFunc = orig })
	identity.DetectFunc = func() (identity.Identity, error) {
		return identity.Identity{OS: "linux", Hostname: "myhost", Username: "grapz"}, nil
	}

	out, err := runWithDotfiles(t, makeDotfiles(t), "identity")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	for _, want := range []string{"os:", "linux", "hostname:", "myhost", "username:", "grapz", "userhost:", "grapz@myhost"} {
		if !strings.Contains(out, want) {
			t.Errorf("identity output = %q, want %q", out, want)
		}
	}
}

func TestIdentityCmd_ConfigOverride(t *testing.T) {
	cfg := "identity:\n  hostname: override-host\n  username: override-user\n  os: override-os\n"
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig := identity.DetectFunc
	t.Cleanup(func() { identity.DetectFunc = orig })
	identity.DetectFunc = func() (identity.Identity, error) {
		return identity.Identity{OS: "linux", Hostname: "orighost", Username: "origuser"}, nil
	}

	out, err := run(t, "--config", cfgPath, "identity")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	for _, want := range []string{"override-os", "override-host", "override-user", "override-user@override-host"} {
		if !strings.Contains(out, want) {
			t.Errorf("identity output = %q, want %q", out, want)
		}
	}
}

// ---- status -----------------------------------------------------------------

func TestStatusCmd_Empty(t *testing.T) {
	out, err := runWithDotfiles(t, makeDotfiles(t), "status",
		"--compile-dir", t.TempDir(), "--target-dir", t.TempDir())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "no managed") {
		t.Errorf("status output = %q, want 'no managed'", out)
	}
}

func TestStatusCmd_WithEntries(t *testing.T) {
	orig := statusFunc
	t.Cleanup(func() { statusFunc = orig })
	statusFunc = func(_ context.Context, _ linker.LinkConfig) ([]linker.StatusEntry, error) {
		return []linker.StatusEntry{
			{RelPath: ".bashrc", Kind: linker.StatusCorrect},
			{RelPath: ".vimrc", Kind: linker.StatusMissing},
		}, nil
	}

	out, err := runWithDotfiles(t, makeDotfiles(t), "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, ".bashrc") || !strings.Contains(out, "correct") {
		t.Errorf("status output = %q, want .bashrc correct", out)
	}
}

func TestStatusCmd_Error(t *testing.T) {
	orig := statusFunc
	t.Cleanup(func() { statusFunc = orig })
	statusFunc = func(_ context.Context, _ linker.LinkConfig) ([]linker.StatusEntry, error) {
		return nil, fmt.Errorf("forced status error")
	}

	_, err := runWithDotfiles(t, makeDotfiles(t), "status")
	if err == nil {
		t.Fatal("expected error from statusFunc, got nil")
	}
}

// ---- clean ------------------------------------------------------------------

func TestCleanCmd_Success(t *testing.T) {
	orig := cleanFunc
	t.Cleanup(func() { cleanFunc = orig })
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) (*linker.CleanResult, error) {
		return &linker.CleanResult{}, nil
	}

	out, err := runWithDotfiles(t, makeDotfiles(t), "clean")
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("clean output = %q, want 'done'", out)
	}
}

func TestCleanCmd_DryRun(t *testing.T) {
	orig := cleanFunc
	t.Cleanup(func() { cleanFunc = orig })
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) (*linker.CleanResult, error) {
		return &linker.CleanResult{}, nil
	}

	out, err := runWithDotfiles(t, makeDotfiles(t), "--dry-run", "clean")
	if err != nil {
		t.Fatalf("clean dry-run: %v", err)
	}
	if !strings.Contains(out, "dry-run") {
		t.Errorf("clean dry-run output = %q, want 'dry-run'", out)
	}
}

func TestCleanCmd_Error(t *testing.T) {
	orig := cleanFunc
	t.Cleanup(func() { cleanFunc = orig })
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) (*linker.CleanResult, error) {
		return nil, fmt.Errorf("forced clean error")
	}

	_, err := runWithDotfiles(t, makeDotfiles(t), "clean")
	if err == nil {
		t.Fatal("expected error from cleanFunc, got nil")
	}
}

func TestCleanCmd_WarnsDisowned(t *testing.T) {
	orig := cleanFunc
	t.Cleanup(func() { cleanFunc = orig })
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) (*linker.CleanResult, error) {
		return &linker.CleanResult{Disowned: []string{".bashrc"}}, nil
	}

	out, err := runWithDotfiles(t, makeDotfiles(t), "clean")
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if !strings.Contains(out, "no longer dotsmith-managed") || !strings.Contains(out, ".bashrc") {
		t.Errorf("clean output = %q, want disown warning mentioning .bashrc", out)
	}
}

// ---- init -------------------------------------------------------------------

func TestInitCmd_Success(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	out, err := runWithDotfiles(t, root, "init")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("init output = %q, want 'created'", out)
	}

	for _, layer := range []string{"base", "os", "hostname", "username", "userhost"} {
		dir := filepath.Join(root, layer)
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Errorf("expected dir %s, got: %v", dir, statErr)
		}
	}
	// Config is written to the user-level location, not inside the repo.
	if _, statErr := os.Stat(config.UserConfigPath()); statErr != nil {
		t.Errorf("expected user config %s, got: %v", config.UserConfigPath(), statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".dotsmith.yml")); statErr == nil {
		t.Error("init must not write config inside the repo")
	}
}

func TestInitCmd_ExistingConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfgPath := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := runWithDotfiles(t, t.TempDir(), "init")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "exists") {
		t.Errorf("init output = %q, want 'exists'", out)
	}
	// Existing config should not be overwritten.
	data, _ := os.ReadFile(cfgPath)
	if string(data) != "# existing\n" {
		t.Error("expected existing config to be preserved")
	}
}

func TestInitCmd_DryRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	out, err := run(t, "--dotfiles-dir", root, "--dry-run", "init")
	if err != nil {
		t.Fatalf("init dry-run: %v", err)
	}
	if !strings.Contains(out, "would create") {
		t.Errorf("init dry-run output = %q, want 'would create'", out)
	}
	// Nothing should be created.
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("expected empty dir after dry-run init, got %d entries", len(entries))
	}
	if _, statErr := os.Stat(config.UserConfigPath()); statErr == nil {
		t.Error("dry-run init must not write the user config")
	}
}

func TestInitCmd_MkdirError(t *testing.T) {
	orig := osMkdirAllInitFunc
	t.Cleanup(func() { osMkdirAllInitFunc = orig })
	osMkdirAllInitFunc = func(string, os.FileMode) error {
		return fmt.Errorf("forced mkdir error")
	}

	_, err := runWithDotfiles(t, t.TempDir(), "init")
	if err == nil {
		t.Fatal("expected error from mkdir, got nil")
	}
}

func TestInitCmd_WriteConfigError(t *testing.T) {
	// Point the config at a temp XDG dir but stub MkdirAll to a no-op so the
	// parent directory is never created; WriteFile then fails.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	orig := osMkdirAllInitFunc
	t.Cleanup(func() { osMkdirAllInitFunc = orig })
	osMkdirAllInitFunc = func(string, os.FileMode) error { return nil }

	_, err := run(t, "--dotfiles-dir", t.TempDir(), "init")
	if err == nil {
		t.Fatal("expected error writing user config, got nil")
	}
}

func TestInitCmd_ConfigMkdirError(t *testing.T) {
	// Let the repo layer dirs be created, but fail creating the config's parent.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	orig := osMkdirAllInitFunc
	t.Cleanup(func() { osMkdirAllInitFunc = orig })
	osMkdirAllInitFunc = func(path string, _ os.FileMode) error {
		if strings.Contains(path, "dotsmith") {
			return fmt.Errorf("forced config mkdir error")
		}
		return nil
	}

	_, err := run(t, "--dotfiles-dir", t.TempDir(), "init")
	if err == nil {
		t.Fatal("expected error creating config parent dir, got nil")
	}
}

// ---- git --------------------------------------------------------------------

func TestGitInstallCmd(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Change working directory to the fake git repo.
	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	out, err := run(t, "git", "install")
	if err != nil {
		t.Fatalf("git install: %v", err)
	}
	if !strings.Contains(out, "installed hook") {
		t.Errorf("git install output = %q, want 'installed hook'", out)
	}

	// Verify hook file was created.
	hookPath := filepath.Join(hooksDir, "post-merge")
	data, readErr := os.ReadFile(hookPath)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if !strings.Contains(string(data), "dotsmith apply") {
		t.Errorf("hook content = %q, want 'dotsmith apply'", string(data))
	}
}

func TestGitInstallCmd_AlreadyInstalled(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Pre-write the hook block.
	hookPath := filepath.Join(hooksDir, "post-merge")
	if err := os.WriteFile(hookPath, []byte(hookBlock), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Only create post-checkout as well.
	if err := os.WriteFile(filepath.Join(hooksDir, "post-checkout"), []byte(hookBlock), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	out, err := run(t, "git", "install")
	if err != nil {
		t.Fatalf("git install: %v", err)
	}
	if !strings.Contains(out, "already present") {
		t.Errorf("git install output = %q, want 'already present'", out)
	}
}

func TestGitRemoveCmd(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "post-merge")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\n"+hookBlock), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "post-checkout"), []byte("#!/bin/sh\n"+hookBlock), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	out, err := run(t, "git", "remove")
	if err != nil {
		t.Fatalf("git remove: %v", err)
	}
	if !strings.Contains(out, "removed hook") {
		t.Errorf("git remove output = %q, want 'removed hook'", out)
	}

	data, _ := os.ReadFile(hookPath)
	if strings.Contains(string(data), "dotsmith apply") {
		t.Error("expected hook to be removed from file")
	}
}

func TestGitRemoveCmd_NotInstalled(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "post-merge"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "post-checkout"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	out, err := run(t, "git", "remove")
	if err != nil {
		t.Fatalf("git remove: %v", err)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("git remove output = %q, want 'not found'", out)
	}
}

func TestGitRemoveCmd_NoHookFile(t *testing.T) {
	// Hook file doesn't exist — should succeed silently.
	gitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	_, err := run(t, "git", "remove")
	if err != nil {
		t.Fatalf("git remove (no hook file): %v", err)
	}
}

func TestGitCmd_NoGitDir(t *testing.T) {
	// No .git dir — should error.
	tmpDir := t.TempDir()
	origCwd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	_, err := run(t, "git", "install")
	if err == nil {
		t.Fatal("expected error for missing .git dir, got nil")
	}
}

func TestGitInstallCmd_Branch(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	out, err := run(t, "git", "install", "--branch", "main")
	if err != nil {
		t.Fatalf("git install --branch main: %v", err)
	}
	if !strings.Contains(out, "installed hook") {
		t.Errorf("git install output = %q, want 'installed hook'", out)
	}

	hookPath := filepath.Join(hooksDir, "post-merge")
	data, readErr := os.ReadFile(hookPath)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	content := string(data)
	if !strings.Contains(content, "git branch --show-current") {
		t.Errorf("hook content = %q, want 'git branch --show-current'", content)
	}
	if !strings.Contains(content, `= 'main'`) {
		t.Errorf("hook content = %q, want branch guard for 'main'", content)
	}
	if !strings.Contains(content, "dotsmith apply") {
		t.Errorf("hook content = %q, want 'dotsmith apply'", content)
	}
}

func TestGitInstallCmd_BranchEmpty(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	_, err := run(t, "git", "install")
	if err != nil {
		t.Fatalf("git install: %v", err)
	}

	hookPath := filepath.Join(hooksDir, "post-merge")
	data, readErr := os.ReadFile(hookPath)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	content := string(data)
	if strings.Contains(content, "git branch --show-current") {
		t.Errorf("hook content = %q, want no branch guard when --branch is not set", content)
	}
	if !strings.Contains(content, "dotsmith apply") {
		t.Errorf("hook content = %q, want 'dotsmith apply'", content)
	}
}

func TestGitInstallCmd_MkdirError(t *testing.T) {
	gitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	orig := osMkdirAllGitFunc
	t.Cleanup(func() { osMkdirAllGitFunc = orig })
	osMkdirAllGitFunc = func(string, os.FileMode) error {
		return fmt.Errorf("forced mkdir error")
	}

	_, err := run(t, "git", "install")
	if err == nil {
		t.Fatal("expected error from mkdir, got nil")
	}
}

func TestGitInstallCmd_ReadError(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	orig := osReadFileGitFunc
	t.Cleanup(func() { osReadFileGitFunc = orig })
	osReadFileGitFunc = func(string) ([]byte, error) {
		return nil, fmt.Errorf("forced read error")
	}

	_, err := run(t, "git", "install")
	if err == nil {
		t.Fatal("expected error from read, got nil")
	}
}

func TestGitInstallCmd_WriteError(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	orig := osWriteFileGitFunc
	t.Cleanup(func() { osWriteFileGitFunc = orig })
	osWriteFileGitFunc = func(string, []byte, os.FileMode) error {
		return fmt.Errorf("forced write error")
	}

	_, err := run(t, "git", "install")
	if err == nil {
		t.Fatal("expected error from write, got nil")
	}
}

func TestGitRemoveCmd_ReadError(t *testing.T) {
	gitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	orig := osReadFileGitFunc
	t.Cleanup(func() { osReadFileGitFunc = orig })
	// Return non-ErrNotExist error.
	osReadFileGitFunc = func(string) ([]byte, error) {
		return nil, fmt.Errorf("forced read error")
	}

	_, err := run(t, "git", "remove")
	if err == nil {
		t.Fatal("expected error from read, got nil")
	}
}

func TestGitRemoveCmd_WriteError(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Write hook with block so strip happens.
	for _, name := range gitHookFiles {
		if err := os.WriteFile(filepath.Join(hooksDir, name), []byte("#!/bin/sh\n"+hookBlock), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	orig := osWriteFileGitFunc
	t.Cleanup(func() { osWriteFileGitFunc = orig })
	osWriteFileGitFunc = func(string, []byte, os.FileMode) error {
		return fmt.Errorf("forced write error")
	}

	_, err := run(t, "git", "remove")
	if err == nil {
		t.Fatal("expected error from write, got nil")
	}
}

// ---- shell ------------------------------------------------------------------

func TestShellBashCmd(t *testing.T) {
	out, err := run(t, "shell", "bash")
	if err != nil {
		t.Fatalf("shell bash: %v", err)
	}
	if !strings.Contains(out, "bash") && !strings.Contains(out, "complete") {
		t.Errorf("shell bash output = %q, want completion content", out)
	}
}

func TestShellZshCmd(t *testing.T) {
	out, err := run(t, "shell", "zsh")
	if err != nil {
		t.Fatalf("shell zsh: %v", err)
	}
	if len(out) == 0 {
		t.Error("shell zsh output is empty")
	}
}

func TestShellFishCmd(t *testing.T) {
	out, err := run(t, "shell", "fish")
	if err != nil {
		t.Fatalf("shell fish: %v", err)
	}
	if len(out) == 0 {
		t.Error("shell fish output is empty")
	}
}

// ---- helpers coverage -------------------------------------------------------

func TestCompiledFileRefs_WalkError(t *testing.T) {
	// An unreadable subdirectory causes WalkDir to pass err != nil to the callback.
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	_, err := compiledFileRefs(dir)
	if err == nil {
		t.Fatal("expected error for unreadable subdir, got nil")
	}
}

func TestCompiledFileRefs_RelError(t *testing.T) {
	orig := filepathRelHelpersFunc
	t.Cleanup(func() { filepathRelHelpersFunc = orig })
	filepathRelHelpersFunc = func(string, string) (string, error) {
		return "", fmt.Errorf("forced rel error")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := compiledFileRefs(dir)
	if err == nil {
		t.Fatal("expected error from relErr, got nil")
	}
}

func TestCompiledFileRefs_EmptyDir(t *testing.T) {
	refs, err := compiledFileRefs(t.TempDir())
	if err != nil {
		t.Fatalf("compiledFileRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("len(refs) = %d, want 0", len(refs))
	}
}

func TestCompiledFileRefs_SkipsStateFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".dotsmith.state"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".bashrc"), []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	refs, err := compiledFileRefs(dir)
	if err != nil {
		t.Fatalf("compiledFileRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].RelPath != ".bashrc" {
		t.Errorf("refs = %v, want [{.bashrc ...}]", refs)
	}
}

// stubWriteWithDangling makes writeCompiledFunc report a dangling set so the
// CLI warning path can be exercised without staging a full prune on disk.
func stubWriteWithDangling(t *testing.T, dangling []string) {
	t.Helper()
	orig := writeCompiledFunc
	t.Cleanup(func() { writeCompiledFunc = orig })
	writeCompiledFunc = func(_ context.Context, _ *compiler.CompileResult, _ compiler.WriteConfig) (compiler.WriteStats, error) {
		return compiler.WriteStats{
			Pruned:   dangling,
			Dangling: dangling,
		}, nil
	}
}

// TestCompileCmd_DanglingWarning asserts a bare compile prints the
// dangling-symlink warning, lists the paths, and advises running link.
func TestCompileCmd_DanglingWarning(t *testing.T) {
	stubWriteWithDangling(t, []string{".vimrc"})
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "x\n")

	out, err := runWithDotfiles(t, root, "compile")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(out, "dangle") {
		t.Errorf("expected dangling warning, got: %q", out)
	}
	if !strings.Contains(out, ".vimrc") {
		t.Errorf("expected dangling path listed, got: %q", out)
	}
	if !strings.Contains(out, "dotsmith link") {
		t.Errorf("expected advice to run link, got: %q", out)
	}
}

// TestCompileCmd_NoDanglingWarningWhenNone asserts no warning is printed when
// nothing dangles.
func TestCompileCmd_NoDanglingWarningWhenNone(t *testing.T) {
	stubWriteWithDangling(t, nil)
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "x\n")

	out, err := runWithDotfiles(t, root, "compile")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if strings.Contains(out, "dangle") {
		t.Errorf("did not expect dangling warning, got: %q", out)
	}
}

// TestApplyCmd_SuppressesDanglingWarning asserts apply does not print the
// dangling warning even when files were pruned, since it links immediately after.
func TestApplyCmd_SuppressesDanglingWarning(t *testing.T) {
	stubWriteWithDangling(t, []string{".vimrc"})
	root := makeDotfiles(t)
	writeSubfile(t, root, ".subfile-010.bashrc", "x\n")
	compileDir := t.TempDir()
	targetDir := t.TempDir()

	out, err := run(t, "--dotfiles-dir", root, "apply",
		"--compile-dir", compileDir, "--target-dir", targetDir)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if strings.Contains(out, "dangle") {
		t.Errorf("apply should suppress dangling warning, got: %q", out)
	}
}

// TestPrintDanglingWarning_CapsLongList asserts the warning caps the listed
// paths and summarises the remainder when the list is long.
func TestPrintDanglingWarning_CapsLongList(t *testing.T) {
	var dangling []string
	for i := 0; i < danglingWarnCap+5; i++ {
		dangling = append(dangling, fmt.Sprintf(".f%d", i))
	}
	var buf bytes.Buffer
	printDanglingWarning(&buf, dangling)
	out := buf.String()
	if !strings.Contains(out, "... and 5 more") {
		t.Errorf("expected capped summary, got: %q", out)
	}
	// The first capped entries are listed; entries past the cap are not listed
	// individually.
	if !strings.Contains(out, ".f0") {
		t.Errorf("expected first entry listed, got: %q", out)
	}
	if strings.Contains(out, fmt.Sprintf(".f%d\n", danglingWarnCap+4)) {
		t.Errorf("entry past the cap should not be listed individually, got: %q", out)
	}
}

// TestWarnDisowned_Empty asserts no output is written when nothing was disowned.
func TestWarnDisowned_Empty(t *testing.T) {
	var buf bytes.Buffer
	warnDisowned(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty disowned list, got: %q", buf.String())
	}
}

// TestWarnDisowned_CapsLongList asserts the warning caps the listed paths and
// summarises the remainder when the list is long.
func TestWarnDisowned_CapsLongList(t *testing.T) {
	var disowned []string
	for i := 0; i < danglingWarnCap+5; i++ {
		disowned = append(disowned, fmt.Sprintf(".f%d", i))
	}
	var buf bytes.Buffer
	warnDisowned(&buf, disowned)
	out := buf.String()
	if !strings.Contains(out, "... and 5 more") {
		t.Errorf("expected capped summary, got: %q", out)
	}
	if !strings.Contains(out, ".f0") {
		t.Errorf("expected first entry listed, got: %q", out)
	}
	if strings.Contains(out, fmt.Sprintf(".f%d\n", danglingWarnCap+4)) {
		t.Errorf("entry past the cap should not be listed individually, got: %q", out)
	}
}

// TestCompiledFileRefs_SkipsStateFileCaseVariant verifies a case variant of the
// state filename is skipped too. On case-insensitive filesystems (APFS/HFS+,
// NTFS) such a name folds onto the real state file, so it must never be treated
// as a compiled dotfile.
func TestCompiledFileRefs_SkipsStateFileCaseVariant(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".DOTSMITH.STATE"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".bashrc"), []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	refs, err := compiledFileRefs(dir)
	if err != nil {
		t.Fatalf("compiledFileRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].RelPath != ".bashrc" {
		t.Errorf("refs = %v, want [{.bashrc ...}]", refs)
	}
}

// ---- shellQuote / hookBlockForBranch ---------------------------------------

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "main", want: `'main'`},
		{name: "empty", in: "", want: `''`},
		{name: "double quote", in: `fea"ture`, want: `'fea"ture'`},
		{name: "single quote", in: "fea'ture", want: `'fea'\''ture'`},
		{name: "metacharacters", in: "a; rm -rf /", want: `'a; rm -rf /'`},
		{name: "subshell", in: "$(touch x)", want: `'$(touch x)'`},
		{name: "backtick", in: "`id`", want: "'`id`'"},
		{name: "only single quote", in: "'", want: `''\'''`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHookBlockForBranch_Quoting(t *testing.T) {
	// A branch name containing a double quote must not break out of the test
	// expression; the value is single-quoted.
	block := hookBlockForBranch(`fea"ture`)
	if !strings.Contains(block, `= 'fea"ture' ]`) {
		t.Errorf("hook block = %q, want single-quoted branch guard", block)
	}
	// An embedded single quote is escaped via the '\'' sequence.
	block = hookBlockForBranch("o'brien")
	if !strings.Contains(block, `= 'o'\''brien' ]`) {
		t.Errorf("hook block = %q, want escaped single quote", block)
	}
}

func TestHookBlockForBranch_Empty(t *testing.T) {
	if got := hookBlockForBranch(""); got != hookBlock {
		t.Errorf("hookBlockForBranch(\"\") = %q, want plain hookBlock %q", got, hookBlock)
	}
}

// ---- Execute (root) ---------------------------------------------------------

func TestExecute_Help(t *testing.T) {
	// Verify Execute runs without panicking on --help.
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--help"})
	var buf strings.Builder
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// Execute returns nil for --help.
	_ = cmd.ExecuteContext(context.Background())
}

// ---- shell error paths ------------------------------------------------------

func TestShellBashCmd_Error(t *testing.T) {
	orig := genBashCompletionFunc
	t.Cleanup(func() { genBashCompletionFunc = orig })
	genBashCompletionFunc = func(_ *cobra.Command, _ io.Writer) error {
		return fmt.Errorf("forced bash completion error")
	}

	_, err := run(t, "shell", "bash")
	if err == nil {
		t.Fatal("expected error from genBashCompletionFunc, got nil")
	}
}

func TestShellZshCmd_Error(t *testing.T) {
	orig := genZshCompletionFunc
	t.Cleanup(func() { genZshCompletionFunc = orig })
	genZshCompletionFunc = func(_ *cobra.Command, _ io.Writer) error {
		return fmt.Errorf("forced zsh completion error")
	}

	_, err := run(t, "shell", "zsh")
	if err == nil {
		t.Fatal("expected error from genZshCompletionFunc, got nil")
	}
}

func TestShellFishCmd_Error(t *testing.T) {
	orig := genFishCompletionFunc
	t.Cleanup(func() { genFishCompletionFunc = orig })
	genFishCompletionFunc = func(_ *cobra.Command, _ io.Writer) error {
		return fmt.Errorf("forced fish completion error")
	}

	_, err := run(t, "shell", "fish")
	if err == nil {
		t.Fatal("expected error from genFishCompletionFunc, got nil")
	}
}

// ---- git uncovered paths ----------------------------------------------------

func TestGitRemoveCmd_NoGitDir(t *testing.T) {
	tmpDir := t.TempDir()
	origCwd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	_, err := run(t, "git", "remove")
	if err == nil {
		t.Fatal("expected error for missing .git dir, got nil")
	}
}

func TestGitInstallCmd_ChmodError(t *testing.T) {
	gitDir := t.TempDir()
	hooksDir := filepath.Join(gitDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origCwd, _ := os.Getwd()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	orig := osChmodGitFunc
	t.Cleanup(func() { osChmodGitFunc = orig })
	osChmodGitFunc = func(string, os.FileMode) error {
		return fmt.Errorf("forced chmod error")
	}

	_, err := run(t, "git", "install")
	if err == nil {
		t.Fatal("expected error from chmod, got nil")
	}
}

func TestFindHooksDir_GetWdError(t *testing.T) {
	orig := osGetWdFunc
	t.Cleanup(func() { osGetWdFunc = orig })
	osGetWdFunc = func() (string, error) {
		return "", fmt.Errorf("forced getwd error")
	}

	_, err := run(t, "git", "install")
	if err == nil {
		t.Fatal("expected error from getwd, got nil")
	}
}

func TestStripHookBlock_NoEnd(t *testing.T) {
	// Content with begin marker but no end marker — should return unchanged.
	content := "#!/bin/sh\n" + hookBegin + "\norphaned content"
	got := stripHookBlock(content)
	if got != content {
		t.Errorf("stripHookBlock with no end = %q, want unchanged %q", got, content)
	}
}

func TestStripHookBlock_NoTrailingNewline(t *testing.T) {
	// hookEnd at the very end with no trailing newline — exercises end > len(content) guard.
	content := "#!/bin/sh\n" + hookBegin + "\n" + hookBody + "\n" + hookEnd
	got := stripHookBlock(content)
	if strings.Contains(got, hookBegin) {
		t.Errorf("stripHookBlock should have removed hook block, got %q", got)
	}
}

// ---- Execute ----------------------------------------------------------------

func TestExecute_Args(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = []string{"dotsmith", "version"}
	_ = Execute()
}
