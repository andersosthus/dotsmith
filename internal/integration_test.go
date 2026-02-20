//go:build integration

// Package internal_test contains integration tests for the full dotsmith workflow.
// Run with: go test -tags integration ./internal/...
package internal_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
	"github.com/andersosthus/dotsmith/internal/linker"
)

// scenario bundles directories for an integration test.
type scenario struct {
	dotfiles   string
	compileDir string
	targetDir  string
}

// newScenario creates a fresh test environment. compileDir is NOT pre-created
// so the compiler can set its own permissions via MkdirAll.
func newScenario(t *testing.T) scenario {
	t.Helper()
	dotfiles := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dotfiles, "base"), 0o755); err != nil {
		t.Fatalf("MkdirAll base: %v", err)
	}
	return scenario{
		dotfiles:   dotfiles,
		compileDir: filepath.Join(t.TempDir(), "compile"), // not pre-created
		targetDir:  t.TempDir(),
	}
}

// writeBase writes a file into the base layer.
func (s scenario) writeBase(t *testing.T, name, content string) {
	t.Helper()
	p := filepath.Join(s.dotfiles, "base", name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

// writeLayer writes a file into a named sublayer (e.g. "os/linux", "hostname/mymachine").
func (s scenario) writeLayer(t *testing.T, layer, name, content string) {
	t.Helper()
	dir := filepath.Join(s.dotfiles, layer)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", layer, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s/%s: %v", layer, name, err)
	}
}

// compile runs compiler.Compile and fatals on error.
func (s scenario) compile(t *testing.T, ctx context.Context) *compiler.CompileResult {
	t.Helper()
	result, err := compiler.Compile(ctx, compiler.CompileConfig{
		DotfilesDir: s.dotfiles,
		Identity:    mustDetect(t),
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return result
}

// compileAndWrite compiles and writes to disk, fataling on error.
func (s scenario) compileAndWrite(t *testing.T, ctx context.Context) compiler.WriteStats {
	t.Helper()
	result := s.compile(t, ctx)
	stats, err := compiler.WriteCompiled(ctx, result, compiler.WriteConfig{
		CompileDir: s.compileDir,
	})
	if err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}
	return stats
}

// link runs linker.Link and fatals on error.
func (s scenario) link(t *testing.T, ctx context.Context) *linker.LinkResult {
	t.Helper()
	refs, err := compiledRefs(s.compileDir)
	if err != nil {
		t.Fatalf("compiledRefs: %v", err)
	}
	result, err := linker.Link(ctx, linker.LinkConfig{
		CompileDir: s.compileDir,
		TargetDir:  s.targetDir,
	}, refs)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	return result
}

func (s scenario) linkCfg() linker.LinkConfig {
	return linker.LinkConfig{CompileDir: s.compileDir, TargetDir: s.targetDir}
}

// compiledRefs walks compileDir and returns a FileRef for every file except the state file.
func compiledRefs(compileDir string) ([]linker.FileRef, error) {
	var refs []linker.FileRef
	err := filepath.WalkDir(compileDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(compileDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == ".dotsmith.state" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		refs = append(refs, linker.FileRef{
			RelPath:     rel,
			ContentHash: hex.EncodeToString(sum[:]),
		})
		return nil
	})
	return refs, err
}

// mustDetect returns auto-detected identity or fatals.
func mustDetect(t *testing.T) identity.Identity {
	t.Helper()
	id, err := identity.Detect()
	if err != nil {
		t.Fatalf("identity.Detect: %v", err)
	}
	return id
}

// generateKey creates a temporary age identity file.
func generateKey(t *testing.T) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "key.txt")
	if err = os.WriteFile(keyPath, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	return keyPath
}

// --- Integration tests ---

// TestIntegration_FullCycle: compile → link → status → clean.
func TestIntegration_FullCycle(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, ".bashrc.subfile-010.sh", "export PATH=/usr/bin\n")
	s.writeBase(t, ".bashrc.subfile-020.sh", "alias ll='ls -la'\n")

	stats := s.compileAndWrite(t, ctx)
	if stats.Written != 1 {
		t.Errorf("Written = %d, want 1", stats.Written)
	}
	if _, statErr := os.Stat(filepath.Join(s.compileDir, ".bashrc")); statErr != nil {
		t.Fatalf("compiled .bashrc missing: %v", statErr)
	}

	lResult := s.link(t, ctx)
	if lResult.Created != 1 {
		t.Errorf("Created = %d, want 1", lResult.Created)
	}

	symlink := filepath.Join(s.targetDir, ".bashrc")
	if _, statErr := os.Lstat(symlink); statErr != nil {
		t.Fatalf("symlink missing: %v", statErr)
	}

	entries, err := linker.Status(ctx, s.linkCfg())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != linker.StatusCorrect {
		t.Errorf("Status = %v, want [{.bashrc correct}]", entries)
	}

	data, _ := os.ReadFile(filepath.Join(s.compileDir, ".bashrc"))
	content := string(data)
	if !strings.Contains(content, "export PATH") || !strings.Contains(content, "alias ll") {
		t.Errorf("content missing expected subfiles: %q", content)
	}

	if err = linker.Clean(ctx, s.linkCfg()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, statErr := os.Lstat(symlink); !os.IsNotExist(statErr) {
		t.Error("expected symlink removed after clean")
	}
}

// TestIntegration_OverrideCycle tests OS override layer adds subfiles.
// Override directories follow the structure: dotfiles/<layer>/<key>/
func TestIntegration_OverrideCycle(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	id := mustDetect(t)

	s.writeBase(t, ".vimrc.subfile-010.vim", "set nocompatible\n")
	s.writeBase(t, ".vimrc.subfile-020.vim", "set number\n")
	// OS layer: dotfiles/os/<osname>/
	s.writeLayer(t, filepath.Join("os", id.OS), ".vimrc.subfile-030.vim", "\" OS-specific\n")

	result, err := compiler.Compile(ctx, compiler.CompileConfig{
		DotfilesDir: s.dotfiles,
		Identity:    id,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(result.Files))
	}
	content := string(result.Files[0].Content)
	for _, want := range []string{"set nocompatible", "set number", "OS-specific"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q", want)
		}
	}
}

// TestIntegration_IgnoreMarker verifies .ignore removes a base subfile.
func TestIntegration_IgnoreMarker(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	id := mustDetect(t)

	s.writeBase(t, ".bashrc.subfile-010.sh", "export A=1\n")
	s.writeBase(t, ".bashrc.subfile-020.sh", "export B=2\n")
	// OS layer ignores subfile-010: marker must include the subfile extension.
	s.writeLayer(t, filepath.Join("os", id.OS), ".bashrc.subfile-010.sh.ignore", "")

	result, err := compiler.Compile(ctx, compiler.CompileConfig{
		DotfilesDir: s.dotfiles,
		Identity:    id,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(result.Files))
	}
	content := string(result.Files[0].Content)
	if strings.Contains(content, "export A=1") {
		t.Error("ignored subfile-010 should not appear in output")
	}
	if !strings.Contains(content, "export B=2") {
		t.Error("subfile-020 should still be present")
	}
}

// TestIntegration_EncryptionCycle: encrypt subfile → compile → verify decrypted content.
func TestIntegration_EncryptionCycle(t *testing.T) {
	ctx := context.Background()
	keyPath := generateKey(t)
	s := newScenario(t)

	plainFile := filepath.Join(s.dotfiles, "base", "secrets.sh.subfile-010.sh")
	if err := os.WriteFile(plainFile, []byte("export SECRET=hunter2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ks := encrypt.KeySource{IdentityFile: keyPath}
	if err := encrypt.EncryptFileInPlace(ctx, plainFile, ks); err != nil {
		t.Fatalf("EncryptFileInPlace: %v", err)
	}

	result, err := compiler.Compile(ctx, compiler.CompileConfig{
		DotfilesDir: s.dotfiles,
		Identity:    mustDetect(t),
		KeySource:   ks,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(result.Files))
	}
	if !strings.Contains(string(result.Files[0].Content), "export SECRET=hunter2") {
		t.Error("decrypted content missing from compiled output")
	}
}

// TestIntegration_Idempotency verifies two runs produce no unnecessary writes.
func TestIntegration_Idempotency(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, ".bashrc.subfile-010.sh", "export A=1\n")
	id := mustDetect(t)

	compileCfg := compiler.CompileConfig{DotfilesDir: s.dotfiles, Identity: id}
	writeCfg := compiler.WriteConfig{CompileDir: s.compileDir}

	r1, _ := compiler.Compile(ctx, compileCfg)
	stats1, _ := compiler.WriteCompiled(ctx, r1, writeCfg)
	refs, _ := compiledRefs(s.compileDir)
	linker.Link(ctx, s.linkCfg(), refs) //nolint:errcheck

	r2, _ := compiler.Compile(ctx, compileCfg)
	stats2, _ := compiler.WriteCompiled(ctx, r2, writeCfg)
	if stats1.Written != 1 || stats2.Written != 0 || stats2.Unchanged != 1 {
		t.Errorf("idempotency: run1=%+v run2=%+v", stats1, stats2)
	}

	refs2, _ := compiledRefs(s.compileDir)
	lr2, _ := linker.Link(ctx, s.linkCfg(), refs2)
	if lr2.Created != 0 || lr2.Updated != 0 || lr2.Unchanged != 1 {
		t.Errorf("link idempotency: %+v", lr2)
	}
}

// TestIntegration_DryRun verifies dry-run makes no filesystem changes.
// Note: WriteCompiled dry-run reports Written=N (files "would write"), not 0.
func TestIntegration_DryRun(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, ".bashrc.subfile-010.sh", "export A=1\n")

	result, _ := compiler.Compile(ctx, compiler.CompileConfig{
		DotfilesDir: s.dotfiles,
		Identity:    mustDetect(t),
	})

	// Dry-run write — nothing written to disk.
	_, err := compiler.WriteCompiled(ctx, result, compiler.WriteConfig{
		CompileDir: s.compileDir,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("WriteCompiled dry-run: %v", err)
	}

	// compileDir was not created.
	if _, statErr := os.Stat(s.compileDir); !os.IsNotExist(statErr) {
		t.Error("compileDir should not exist after dry-run")
	}

	// Dry-run link — nothing created.
	refs := []linker.FileRef{{RelPath: ".bashrc", ContentHash: "abc123"}}
	lResult, err := linker.Link(ctx, linker.LinkConfig{
		CompileDir: s.compileDir,
		TargetDir:  s.targetDir,
		DryRun:     true,
	}, refs)
	if err != nil {
		t.Fatalf("Link dry-run: %v", err)
	}
	_ = lResult
	entries, _ := os.ReadDir(s.targetDir)
	if len(entries) != 0 {
		t.Errorf("targetDir has %d entries after dry-run link, want 0", len(entries))
	}
}

// TestIntegration_FilePermissions verifies compile dir is 0700, regular files 0644.
func TestIntegration_FilePermissions(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, ".bashrc.subfile-010.sh", "export A=1\n")

	s.compileAndWrite(t, ctx)

	// compileDir was created fresh by WriteCompiled with 0700.
	dirInfo, err := os.Stat(s.compileDir)
	if err != nil {
		t.Fatalf("Stat compileDir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("compileDir perm = %04o, want 0700", dirInfo.Mode().Perm())
	}

	fileInfo, err := os.Stat(filepath.Join(s.compileDir, ".bashrc"))
	if err != nil {
		t.Fatalf("Stat .bashrc: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o644 {
		t.Errorf(".bashrc perm = %04o, want 0644", fileInfo.Mode().Perm())
	}
}

// TestIntegration_ConflictError verifies link errors on regular file at target path.
func TestIntegration_ConflictError(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, ".vimrc.subfile-010.vim", "set nocompatible\n")
	s.compileAndWrite(t, ctx)

	if err := os.WriteFile(filepath.Join(s.targetDir, ".vimrc"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	refs, _ := compiledRefs(s.compileDir)
	_, err := linker.Link(ctx, s.linkCfg(), refs)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

// TestIntegration_StalenessDetection verifies status shows stale after content change.
func TestIntegration_StalenessDetection(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, ".bashrc.subfile-010.sh", "export A=1\n")

	s.compileAndWrite(t, ctx)
	refs, _ := compiledRefs(s.compileDir)
	linker.Link(ctx, s.linkCfg(), refs) //nolint:errcheck

	if err := os.WriteFile(filepath.Join(s.compileDir, ".bashrc"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := linker.Status(ctx, s.linkCfg())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.RelPath == ".bashrc" {
			found = true
			if e.Kind != linker.StatusStale {
				t.Errorf("status = %s, want stale", e.Kind)
			}
		}
	}
	if !found {
		t.Error(".bashrc not found in status entries")
	}
}

// TestIntegration_DirectoryCleanup verifies clean removes empty parent dirs.
func TestIntegration_DirectoryCleanup(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)

	// Use a subfile with a known extension (.ini) so ParseSubfileName matches.
	nestedBase := filepath.Join(s.dotfiles, "base", ".config", "git")
	if err := os.MkdirAll(nestedBase, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedBase, "config.subfile-010.ini"),
		[]byte("[user]\n  name = Test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s.compileAndWrite(t, ctx)
	s.link(t, ctx)

	symlink := filepath.Join(s.targetDir, ".config", "git", "config")
	if _, statErr := os.Lstat(symlink); statErr != nil {
		t.Fatalf("expected nested symlink %s: %v", symlink, statErr)
	}

	if err := linker.Clean(ctx, s.linkCfg()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, statErr := os.Lstat(symlink); !os.IsNotExist(statErr) {
		t.Error("symlink should be removed after clean")
	}
	if _, statErr := os.Lstat(filepath.Join(s.targetDir, ".config", "git")); !os.IsNotExist(statErr) {
		t.Error(".config/git dir should be removed after clean")
	}
}

// TestIntegration_CommentHeaders verifies comment headers are inserted for known extensions.
func TestIntegration_CommentHeaders(t *testing.T) {
	ctx := context.Background()
	s := newScenario(t)
	s.writeBase(t, "aliases.sh.subfile-010.sh", "alias ll='ls -la'\n")
	s.writeBase(t, "aliases.sh.subfile-020.sh", "alias la='ls -a'\n")

	result, err := compiler.Compile(ctx, compiler.CompileConfig{
		DotfilesDir: s.dotfiles,
		Identity:    mustDetect(t),
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(result.Files))
	}
	content := string(result.Files[0].Content)
	if !strings.Contains(content, "# ---") || !strings.Contains(content, "dotsmith") {
		t.Errorf("expected dotsmith comment header, got: %q", content)
	}
}
