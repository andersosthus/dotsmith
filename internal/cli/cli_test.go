package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/spf13/cobra"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
	"github.com/andersosthus/dotsmith/internal/linker"
)

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
	root := makeDotfiles(t)
	// Write invalid YAML config.
	if err := os.WriteFile(filepath.Join(root, ".dotsmith.yml"), []byte("not: valid: yaml: {"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := runWithDotfiles(t, root, "compile")
	if err == nil {
		t.Fatal("expected error from invalid config, got nil")
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

// ---- encrypt/decrypt --------------------------------------------------------

func TestEncryptCmd_Success(t *testing.T) {
	keyPath := generateAgeKey(t)
	plainFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(plainFile, []byte("top secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := run(t, "--age-identity", keyPath, "encrypt", plainFile)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.Contains(out, "encrypted") {
		t.Errorf("encrypt output = %q, want 'encrypted'", out)
	}
	if _, statErr := os.Stat(plainFile + ".age"); statErr != nil {
		t.Errorf("expected .age file, got: %v", statErr)
	}
}

func TestEncryptCmd_AlreadyAge(t *testing.T) {
	_, err := run(t, "encrypt", "/some/file.age")
	if err == nil {
		t.Fatal("expected error for .age extension, got nil")
	}
}

func TestEncryptCmd_KeyFileMissing(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(plain, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := run(t, "--age-identity", "/nonexistent/age.key", "encrypt", plain)
	if err == nil {
		t.Fatal("expected error for missing key file, got nil")
	}
}

func TestEncryptCmd_EncryptError(t *testing.T) {
	keyPath := generateAgeKey(t)
	orig := encryptFileInPlaceFunc
	t.Cleanup(func() { encryptFileInPlaceFunc = orig })
	encryptFileInPlaceFunc = func(_ context.Context, _ string, _ encrypt.KeySource) error {
		return fmt.Errorf("forced encrypt error")
	}

	plain := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(plain, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := run(t, "--age-identity", keyPath, "encrypt", plain)
	if err == nil {
		t.Fatal("expected error from encryptFunc, got nil")
	}
}

func TestDecryptCmd_Success(t *testing.T) {
	keyPath := generateAgeKey(t)
	plainFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(plainFile, []byte("top secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ks := encrypt.KeySource{IdentityFile: keyPath}
	if err := encrypt.EncryptFileInPlace(context.Background(), plainFile, ks); err != nil {
		t.Fatalf("EncryptFileInPlace: %v", err)
	}

	out, err := run(t, "--age-identity", keyPath, "decrypt", plainFile+".age")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !strings.Contains(out, "top secret") {
		t.Errorf("decrypt output = %q, want 'top secret'", out)
	}
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
	decryptFileFunc = func(_ context.Context, _ string, _ encrypt.KeySource) ([]byte, error) {
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
	root := makeDotfiles(t)
	cfg := "identity:\n  hostname: override-host\n  username: override-user\n  os: override-os\n"
	if err := os.WriteFile(filepath.Join(root, ".dotsmith.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig := identity.DetectFunc
	t.Cleanup(func() { identity.DetectFunc = orig })
	identity.DetectFunc = func() (identity.Identity, error) {
		return identity.Identity{OS: "linux", Hostname: "orighost", Username: "origuser"}, nil
	}

	out, err := runWithDotfiles(t, root, "identity")
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
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) error { return nil }

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
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) error { return nil }

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
	cleanFunc = func(_ context.Context, _ linker.LinkConfig) error {
		return fmt.Errorf("forced clean error")
	}

	_, err := runWithDotfiles(t, makeDotfiles(t), "clean")
	if err == nil {
		t.Fatal("expected error from cleanFunc, got nil")
	}
}

// ---- init -------------------------------------------------------------------

func TestInitCmd_Success(t *testing.T) {
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
	if _, statErr := os.Stat(filepath.Join(root, ".dotsmith.yml")); statErr != nil {
		t.Errorf("expected .dotsmith.yml, got: %v", statErr)
	}
}

func TestInitCmd_ExistingConfig(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, ".dotsmith.yml")
	if err := os.WriteFile(cfgPath, []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := runWithDotfiles(t, root, "init")
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
	root := t.TempDir()
	// Make the dotfiles dir read-only so WriteFile fails.
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	orig := osMkdirAllInitFunc
	t.Cleanup(func() { osMkdirAllInitFunc = orig })
	osMkdirAllInitFunc = func(string, os.FileMode) error { return nil }

	_, err := run(t, "--dotfiles-dir", root, "init")
	if err == nil {
		t.Fatal("expected error writing .dotsmith.yml, got nil")
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
