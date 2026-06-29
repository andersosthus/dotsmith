package linker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andersosthus/dotsmith/internal/state"
)

// ---- helpers ----------------------------------------------------------------

// writeCompiled writes content to compileDir/relPath and returns its hash.
func writeCompiled(t *testing.T, compileDir, relPath, content string) string {
	t.Helper()
	p := filepath.Join(compileDir, relPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return hashBytes([]byte(content))
}

// writeState writes a state file with the given single entry.
func writeState(t *testing.T, compileDir, relPath, hash string) {
	t.Helper()
	s := state.New()
	s.Symlinks[relPath] = state.SymlinkEntry{Source: relPath, Target: relPath, ContentHash: hash}
	if err := state.Save(context.Background(), s, compileDir); err != nil {
		t.Fatalf("Save state: %v", err)
	}
}

// writeStateMulti writes a state file with multiple relPath→hash entries.
func writeStateMulti(t *testing.T, compileDir string, entries map[string]string) {
	t.Helper()
	s := state.New()
	for relPath, hash := range entries {
		s.Symlinks[relPath] = state.SymlinkEntry{Source: relPath, Target: relPath, ContentHash: hash}
	}
	if err := state.Save(context.Background(), s, compileDir); err != nil {
		t.Fatalf("Save state: %v", err)
	}
}

// writeStateWithManifest writes a state file with the given symlink entries and
// compile manifest entries (relPath→hash for each).
func writeStateWithManifest(t *testing.T, compileDir string, symlinks, compiled map[string]string) {
	t.Helper()
	s := state.New()
	for relPath, hash := range symlinks {
		s.Symlinks[relPath] = state.SymlinkEntry{Source: relPath, Target: relPath, ContentHash: hash}
	}
	for relPath, hash := range compiled {
		s.Compiled[relPath] = state.CompiledEntry{ContentHash: hash}
	}
	if err := state.Save(context.Background(), s, compileDir); err != nil {
		t.Fatalf("Save state: %v", err)
	}
}

// writeCorruptState writes a file that is not valid JSON.
func writeCorruptState(t *testing.T, compileDir string) {
	t.Helper()
	p := filepath.Join(compileDir, ".dotsmith.state")
	if err := os.WriteFile(p, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// makeSymlink creates targetDir/relPath as a symlink to compileDir/relPath.
func makeSymlink(t *testing.T, compileDir, targetDir, relPath string) {
	t.Helper()
	src := filepath.Join(compileDir, relPath)
	tgt := filepath.Join(targetDir, relPath)
	if err := os.MkdirAll(filepath.Dir(tgt), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(src, tgt); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
}

// injectLstat replaces osLstatFunc and restores it on cleanup.
func injectLstat(t *testing.T, fn func(string) (os.FileInfo, error)) {
	t.Helper()
	orig := osLstatFunc
	t.Cleanup(func() { osLstatFunc = orig })
	osLstatFunc = fn
}

// injectReadlink replaces osReadlinkFunc and restores it on cleanup.
func injectReadlink(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := osReadlinkFunc
	t.Cleanup(func() { osReadlinkFunc = orig })
	osReadlinkFunc = fn
}

// makeDirs creates each given directory (and any parents), failing the test on
// error.
func makeDirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}
}

// ---- Link tests -------------------------------------------------------------

func TestLink_Create(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "export PATH=/usr/bin\n")

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Created != 1 || result.Unchanged != 0 || result.Updated != 0 {
		t.Errorf("result = %+v, want Created=1", result)
	}

	// Verify the symlink points to the right place.
	target := filepath.Join(targetDir, ".bashrc")
	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if dest != filepath.Join(compileDir, ".bashrc") {
		t.Errorf("symlink dest = %q, want %q", dest, filepath.Join(compileDir, ".bashrc"))
	}

	// Verify state was written.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if _, ok := s.Symlinks[".bashrc"]; !ok {
		t.Error("expected .bashrc in state")
	}
}

func TestLink_Correct_Unchanged(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "export A=1\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Unchanged != 1 || result.Created != 0 || result.Updated != 0 {
		t.Errorf("result = %+v, want Unchanged=1", result)
	}
}

func TestLink_Stale_Updated(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash1 := writeCompiled(t, compileDir, ".bashrc", "old content\n")
	hash2 := hashBytes([]byte("new content\n"))
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash1)

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash2}})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Updated != 1 || result.Created != 0 || result.Unchanged != 0 {
		t.Errorf("result = %+v, want Updated=1", result)
	}

	// State should reflect new hash.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if s.Symlinks[".bashrc"].ContentHash != hash2 {
		t.Errorf("state hash = %q, want %q", s.Symlinks[".bashrc"].ContentHash, hash2)
	}
}

func TestLink_Stale_DryRun(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash1 := writeCompiled(t, compileDir, ".bashrc", "old content\n")
	hash2 := hashBytes([]byte("new content\n"))
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash1)

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash2}})
	if err != nil {
		t.Fatalf("Link dry-run: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("result.Updated = %d, want 1", result.Updated)
	}

	// State should still have old hash (dry-run).
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if s.Symlinks[".bashrc"].ContentHash != hash1 {
		t.Error("expected state hash unchanged in dry-run")
	}
}

func TestLink_Conflict_RegularFile(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	// Place a regular file at the target path.
	if err := os.WriteFile(filepath.Join(targetDir, ".bashrc"), []byte("regular"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected conflict error for regular file, got nil")
	}
}

func TestLink_Conflict_WrongSymlink(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	// Symlink to a completely different path.
	if err := os.Symlink("/dev/null", filepath.Join(targetDir, ".bashrc")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected conflict error for wrong symlink, got nil")
	}
}

func TestLink_DryRun_NoCreate(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err != nil {
		t.Fatalf("Link dry-run: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("dry-run Created = %d, want 1 (would-create count)", result.Created)
	}

	// Nothing should actually be created.
	if _, err = os.Lstat(filepath.Join(targetDir, ".bashrc")); !os.IsNotExist(err) {
		t.Error("expected no symlink to exist after dry-run")
	}
}

func TestLink_NestedPath(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".config/git/config", "[core]\n")

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".config/git/config", ContentHash: hash}})
	if err != nil {
		t.Fatalf("Link nested: %v", err)
	}

	tgt := filepath.Join(targetDir, ".config", "git", "config")
	if _, err = os.Lstat(tgt); err != nil {
		t.Errorf("expected symlink at %s, got error: %v", tgt, err)
	}
}

func TestLink_LoadStateError(t *testing.T) {
	compileDir := t.TempDir()
	writeCorruptState(t, compileDir)

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: t.TempDir()},
		[]FileRef{{RelPath: ".bashrc", ContentHash: "abc"}})
	if err == nil {
		t.Fatal("expected error from corrupt state, got nil")
	}
}

func TestLink_SaveStateError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")

	// Make compileDir read-only so state.Save fails.
	if err := os.Chmod(compileDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(compileDir, 0o755) })

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected error saving state, got nil")
	}
}

func TestLink_LstatError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")

	injectLstat(t, func(string) (os.FileInfo, error) {
		return nil, fmt.Errorf("forced lstat error")
	})

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected error from lstat, got nil")
	}
}

func TestLink_MkdirError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")

	orig := osMkdirAllFunc
	t.Cleanup(func() { osMkdirAllFunc = orig })
	osMkdirAllFunc = func(string, os.FileMode) error { return fmt.Errorf("forced mkdir error") }

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected error from mkdir, got nil")
	}
}

func TestLink_SymlinkError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")

	orig := osSymlinkFunc
	t.Cleanup(func() { osSymlinkFunc = orig })
	osSymlinkFunc = func(string, string) error { return fmt.Errorf("forced symlink error") }

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected error from symlink, got nil")
	}
}

func TestLink_ReadlinkError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")

	injectReadlink(t, func(string) (string, error) {
		return "", fmt.Errorf("forced readlink error")
	})

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected error from readlink, got nil")
	}
}

func TestLink_StaleNoStateEntry(t *testing.T) {
	// Symlink exists, points to right source, but no state entry yet — treated as stale.
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	// No state written → state is empty.

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash}})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("result.Updated = %d, want 1", result.Updated)
	}
}

// ---- removeOrphans tests ----------------------------------------------------

func TestLink_RemoveOrphan(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash1 := writeCompiled(t, compileDir, ".bashrc", "export A=1\n")
	hash2 := writeCompiled(t, compileDir, ".vimrc", "set noswap\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	makeSymlink(t, compileDir, targetDir, ".vimrc")
	writeStateMulti(t, compileDir, map[string]string{".bashrc": hash1, ".vimrc": hash2})

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash1}})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("result.Removed = %d, want 1", result.Removed)
	}

	// .vimrc symlink should be gone.
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".vimrc")); !os.IsNotExist(statErr) {
		t.Error("expected .vimrc symlink to be removed")
	}
	// .vimrc compiled file should be gone.
	if _, statErr := os.Lstat(filepath.Join(compileDir, ".vimrc")); !os.IsNotExist(statErr) {
		t.Error("expected .vimrc compiled file to be removed")
	}
	// State should not contain .vimrc.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if _, ok := s.Symlinks[".vimrc"]; ok {
		t.Error("expected .vimrc removed from state")
	}
}

func TestLink_RemoveOrphan_DryRun(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash1 := writeCompiled(t, compileDir, ".bashrc", "export A=1\n")
	hash2 := writeCompiled(t, compileDir, ".vimrc", "set noswap\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	makeSymlink(t, compileDir, targetDir, ".vimrc")
	writeStateMulti(t, compileDir, map[string]string{".bashrc": hash1, ".vimrc": hash2})

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true},
		[]FileRef{{RelPath: ".bashrc", ContentHash: hash1}})
	if err != nil {
		t.Fatalf("Link dry-run: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("result.Removed = %d, want 1", result.Removed)
	}

	// .vimrc symlink should still exist.
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".vimrc")); statErr != nil {
		t.Errorf("expected .vimrc symlink to remain after dry-run, got: %v", statErr)
	}
	// .vimrc compiled file should still exist.
	if _, statErr := os.Lstat(filepath.Join(compileDir, ".vimrc")); statErr != nil {
		t.Errorf("expected .vimrc compiled file to remain after dry-run, got: %v", statErr)
	}
	// State should still contain .vimrc (dry-run: no state save).
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if _, ok := s.Symlinks[".vimrc"]; !ok {
		t.Error("expected .vimrc to remain in state after dry-run")
	}
}

func TestLink_RemoveOrphan_NestedPath(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".config/git/config", "[core]\n")
	makeSymlink(t, compileDir, targetDir, ".config/git/config")
	writeState(t, compileDir, ".config/git/config", hash)

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("result.Removed = %d, want 1", result.Removed)
	}

	// Symlink and empty parent dirs should be removed.
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".config", "git", "config")); !os.IsNotExist(statErr) {
		t.Error("expected symlink to be removed")
	}
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".config", "git")); !os.IsNotExist(statErr) {
		t.Error("expected empty dir .config/git to be removed")
	}
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".config")); !os.IsNotExist(statErr) {
		t.Error("expected empty dir .config to be removed")
	}
	// Compiled file should be removed.
	if _, statErr := os.Lstat(filepath.Join(compileDir, ".config", "git", "config")); !os.IsNotExist(statErr) {
		t.Error("expected compiled file to be removed")
	}
}

func TestLink_RemoveOrphan_AlreadyGone(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	// State has an entry, but no files exist on disk.
	writeState(t, compileDir, ".bashrc", "somehash")

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("result.Removed = %d, want 1", result.Removed)
	}

	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if len(s.Symlinks) != 0 {
		t.Errorf("state has %d entries after orphan removal, want 0", len(s.Symlinks))
	}
}

func TestLink_RemoveOrphan_SymlinkRemoveError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	orig := osRemoveFunc
	t.Cleanup(func() { osRemoveFunc = orig })
	osRemoveFunc = func(path string) error {
		if filepath.Dir(path) == targetDir {
			return fmt.Errorf("forced remove error")
		}
		return orig(path)
	}

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{})
	if err == nil {
		t.Fatal("expected error removing orphan symlink, got nil")
	}
}

func TestLink_RemoveOrphan_CompiledRemoveError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	orig := osRemoveFunc
	t.Cleanup(func() { osRemoveFunc = orig })
	osRemoveFunc = func(path string) error {
		if filepath.Dir(path) == compileDir {
			return fmt.Errorf("forced remove error")
		}
		return orig(path)
	}

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{})
	if err == nil {
		t.Fatal("expected error removing orphan compiled file, got nil")
	}
}

func TestLink_RemoveOrphan_Mixed(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash1 := writeCompiled(t, compileDir, ".bashrc", "export A=1\n")
	hash2 := writeCompiled(t, compileDir, ".vimrc", "set noswap\n")
	hash3 := writeCompiled(t, compileDir, ".gitconfig", "[user]\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	makeSymlink(t, compileDir, targetDir, ".vimrc")
	makeSymlink(t, compileDir, targetDir, ".gitconfig")
	writeStateMulti(t, compileDir, map[string]string{
		".bashrc": hash1, ".vimrc": hash2, ".gitconfig": hash3,
	})

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{
			{RelPath: ".bashrc", ContentHash: hash1},
			{RelPath: ".gitconfig", ContentHash: hash3},
		})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("result.Removed = %d, want 1", result.Removed)
	}

	// .vimrc should be gone.
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".vimrc")); !os.IsNotExist(statErr) {
		t.Error("expected .vimrc symlink to be removed")
	}
	// .bashrc and .gitconfig should still be present.
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".bashrc")); statErr != nil {
		t.Errorf("expected .bashrc symlink to remain, got: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".gitconfig")); statErr != nil {
		t.Errorf("expected .gitconfig symlink to remain, got: %v", statErr)
	}
	// State should have 2 entries.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if len(s.Symlinks) != 2 {
		t.Errorf("state has %d entries, want 2", len(s.Symlinks))
	}
}

// ---- verify-then-disown tests -----------------------------------------------

// writeFile writes a plain regular file at dir/relPath.
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	p := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// assertFileContent fails unless the file at path has exactly want.
func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file %s removed or unreadable: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("file %s content = %q, want %q", path, string(got), want)
	}
}

// assertGone fails unless path does not exist.
func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s to be gone, lstat err = %v", path, err)
	}
}

// assertNoStateEntry fails unless the state at compileDir has no entry for relPath.
func assertNoStateEntry(t *testing.T, compileDir, relPath string) {
	t.Helper()
	s, err := state.Load(context.Background(), compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if _, ok := s.Symlinks[relPath]; ok {
		t.Errorf("expected %s dropped from state", relPath)
	}
}

// TestLink_RemoveOrphan_DisownsReplacedFile verifies that when a managed
// symlink's source is gone but the user has replaced the target with a real file
// of their own, Link leaves the user's file untouched, removes the compiled
// artifact, drops the state entry, and surfaces the disowned path.
func TestLink_RemoveOrphan_DisownsReplacedFile(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "compiled\n")
	// User replaced the managed symlink with a real file of their own.
	writeFile(t, targetDir, ".bashrc", "my own content\n")
	writeState(t, compileDir, ".bashrc", "somehash")

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir}, []FileRef{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Removed != 0 {
		t.Errorf("result.Removed = %d, want 0", result.Removed)
	}
	if len(result.Disowned) != 1 || result.Disowned[0] != ".bashrc" {
		t.Errorf("result.Disowned = %v, want [.bashrc]", result.Disowned)
	}

	assertFileContent(t, filepath.Join(targetDir, ".bashrc"), "my own content\n")
	assertGone(t, filepath.Join(compileDir, ".bashrc"))
	assertNoStateEntry(t, compileDir, ".bashrc")
}

// TestLink_RemoveOrphan_DisownsForeignSymlink verifies that a target which is a
// symlink pointing somewhere other than the expected compiled source is treated
// as foreign and disowned (left untouched).
func TestLink_RemoveOrphan_DisownsForeignSymlink(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "compiled\n")
	// Target is a symlink the user created pointing elsewhere.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(elsewhere, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(targetDir, ".bashrc")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	writeState(t, compileDir, ".bashrc", "somehash")

	ctx := context.Background()
	result, err := Link(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir}, []FileRef{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(result.Disowned) != 1 {
		t.Fatalf("result.Disowned = %v, want one entry", result.Disowned)
	}
	// Foreign symlink left in place.
	dst, err := os.Readlink(filepath.Join(targetDir, ".bashrc"))
	if err != nil {
		t.Fatalf("foreign symlink removed or unreadable: %v", err)
	}
	if dst != elsewhere {
		t.Errorf("foreign symlink target = %q, want %q", dst, elsewhere)
	}
}

// TestLink_RemoveOrphan_LstatError verifies an unexpected lstat error during
// orphan removal is surfaced rather than silently disowning the path.
func TestLink_RemoveOrphan_LstatError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	injectLstat(t, func(string) (os.FileInfo, error) {
		return nil, fmt.Errorf("forced lstat error")
	})

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir}, []FileRef{})
	if err == nil {
		t.Fatal("expected error from lstat during orphan removal, got nil")
	}
}

// TestLink_RemoveOrphan_ReadlinkError verifies an unexpected readlink error
// during orphan removal is surfaced rather than silently disowning the path.
func TestLink_RemoveOrphan_ReadlinkError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	injectReadlink(t, func(string) (string, error) {
		return "", fmt.Errorf("forced readlink error")
	})

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir}, []FileRef{})
	if err == nil {
		t.Fatal("expected error from readlink during orphan removal, got nil")
	}
}

// TestClean_DisownsReplacedFile verifies Clean leaves a user-substituted real
// file untouched, removes the compiled artifact, and reports the disowned path.
func TestClean_DisownsReplacedFile(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "compiled\n")
	writeFile(t, targetDir, ".bashrc", "my own content\n")
	writeState(t, compileDir, ".bashrc", "somehash")

	ctx := context.Background()
	result, err := Clean(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Disowned) != 1 || result.Disowned[0] != ".bashrc" {
		t.Errorf("result.Disowned = %v, want [.bashrc]", result.Disowned)
	}
	got, err := os.ReadFile(filepath.Join(targetDir, ".bashrc"))
	if err != nil {
		t.Fatalf("user file was removed or unreadable: %v", err)
	}
	if string(got) != "my own content\n" {
		t.Errorf("user file content = %q, want unchanged", string(got))
	}
	if _, statErr := os.Lstat(filepath.Join(compileDir, ".bashrc")); !os.IsNotExist(statErr) {
		t.Error("expected compiled artifact to be removed")
	}
}

// TestClean_LstatError verifies an unexpected lstat error during clean is
// surfaced rather than silently disowning the path.
func TestClean_LstatError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	injectLstat(t, func(string) (os.FileInfo, error) {
		return nil, fmt.Errorf("forced lstat error")
	})

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error from lstat during clean, got nil")
	}
}

// TestClean_ReadlinkError verifies an unexpected readlink error during clean is
// surfaced rather than silently disowning the path.
func TestClean_ReadlinkError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	injectReadlink(t, func(string) (string, error) {
		return "", fmt.Errorf("forced readlink error")
	})

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error from readlink during clean, got nil")
	}
}

// ---- Status tests -----------------------------------------------------------

func TestStatus_Empty(t *testing.T) {
	compileDir := t.TempDir()
	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}

func TestStatus_Missing(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	writeState(t, compileDir, ".bashrc", hash)
	// No symlink created.

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusMissing {
		t.Errorf("entries = %+v, want one StatusMissing", entries)
	}
}

func TestStatus_Correct(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusCorrect {
		t.Errorf("entries = %+v, want one StatusCorrect", entries)
	}
}

func TestStatus_Stale(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "original\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	// Modify compiled file so hash differs from state.
	if err := os.WriteFile(filepath.Join(compileDir, ".bashrc"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusStale {
		t.Errorf("entries = %+v, want one StatusStale", entries)
	}
}

func TestStatus_ConflictRegularFile(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	writeState(t, compileDir, ".bashrc", hash)
	// Place a regular file instead of a symlink.
	if err := os.WriteFile(filepath.Join(targetDir, ".bashrc"), []byte("regular"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusConflict {
		t.Errorf("entries = %+v, want one StatusConflict", entries)
	}
}

func TestStatus_LstatNonExistError(t *testing.T) {
	// Non-ErrNotExist lstat error → StatusConflict.
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	writeState(t, compileDir, ".bashrc", hash)

	injectLstat(t, func(string) (os.FileInfo, error) {
		return nil, fmt.Errorf("forced lstat error")
	})

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusConflict {
		t.Errorf("entries = %+v, want one StatusConflict", entries)
	}
}

func TestStatus_ReadlinkError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	injectReadlink(t, func(string) (string, error) {
		return "", fmt.Errorf("forced readlink error")
	})

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusConflict {
		t.Errorf("entries = %+v, want one StatusConflict", entries)
	}
}

func TestStatus_WrongSymlink(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	writeState(t, compileDir, ".bashrc", hash)
	// Symlink to wrong destination.
	if err := os.Symlink("/dev/null", filepath.Join(targetDir, ".bashrc")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusConflict {
		t.Errorf("entries = %+v, want one StatusConflict", entries)
	}
}

func TestStatus_UnreadableSource(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", hash)

	// Make the compiled source unreadable.
	if err := os.Chmod(filepath.Join(compileDir, ".bashrc"), 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(compileDir, ".bashrc"), 0o644) })

	entries, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != StatusStale {
		t.Errorf("entries = %+v, want one StatusStale", entries)
	}
}

func TestStatus_LoadStateError(t *testing.T) {
	compileDir := t.TempDir()
	writeCorruptState(t, compileDir)

	_, err := Status(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from corrupt state, got nil")
	}
}

// ---- Clean tests ------------------------------------------------------------

func TestClean_Basic(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	ctx := context.Background()
	if _, err := Clean(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Symlink should be gone.
	if _, err := os.Lstat(filepath.Join(targetDir, ".bashrc")); !os.IsNotExist(err) {
		t.Error("expected symlink to be removed")
	}
	// Compiled file should be gone.
	if _, err := os.Lstat(filepath.Join(compileDir, ".bashrc")); !os.IsNotExist(err) {
		t.Error("expected compiled file to be removed")
	}
	// State should be empty.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if len(s.Symlinks) != 0 {
		t.Errorf("state has %d entries after clean, want 0", len(s.Symlinks))
	}
}

func TestClean_DryRun(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	if _, err := Clean(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true}); err != nil {
		t.Fatalf("Clean dry-run: %v", err)
	}

	// Symlink should still exist.
	if _, err := os.Lstat(filepath.Join(targetDir, ".bashrc")); err != nil {
		t.Errorf("expected symlink to remain after dry-run, got: %v", err)
	}
}

func TestClean_NestedPathEmptyDirRemoval(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".config/git/config", "data\n")
	makeSymlink(t, compileDir, targetDir, ".config/git/config")
	writeState(t, compileDir, ".config/git/config", "somehash")

	if _, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Empty parent dirs should be removed.
	gitDir := filepath.Join(targetDir, ".config", "git")
	if _, err := os.Lstat(gitDir); !os.IsNotExist(err) {
		t.Errorf("expected empty dir %s to be removed", gitDir)
	}
	configDir := filepath.Join(targetDir, ".config")
	if _, err := os.Lstat(configDir); !os.IsNotExist(err) {
		t.Errorf("expected empty dir %s to be removed", configDir)
	}
}

func TestClean_NonEmptyDirPreserved(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".config/git/config", "data\n")
	makeSymlink(t, compileDir, targetDir, ".config/git/config")
	writeState(t, compileDir, ".config/git/config", "somehash")

	// Create a second file in the same dir so it's not empty after clean.
	gitDir := filepath.Join(targetDir, ".config", "git")
	if err := os.WriteFile(filepath.Join(gitDir, "other"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Non-empty dir should remain.
	if _, err := os.Lstat(gitDir); err != nil {
		t.Errorf("expected non-empty dir %s to be preserved, got: %v", gitDir, err)
	}
}

// TestClean_RemovesUnlinkedCompiledFile verifies that a compiled file recorded
// in the manifest but never linked (no symlink entry) is removed by clean, and
// that both state fields are zeroed afterwards.
func TestClean_RemovesUnlinkedCompiledFile(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	// .bashrc is linked; .vimrc was compiled but never linked.
	hashBash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	hashVim := writeCompiled(t, compileDir, ".vimrc", "set noswap\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeStateWithManifest(t, compileDir,
		map[string]string{".bashrc": hashBash},
		map[string]string{".bashrc": hashBash, ".vimrc": hashVim})

	ctx := context.Background()
	if _, err := Clean(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Both compiled files must be gone — including the never-linked .vimrc.
	assertGone(t, filepath.Join(compileDir, ".bashrc"))
	assertGone(t, filepath.Join(compileDir, ".vimrc"))
	// The linked symlink must be gone.
	assertGone(t, filepath.Join(targetDir, ".bashrc"))

	// Both state fields must be zeroed.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load state: %v", err)
	}
	if len(s.Symlinks) != 0 {
		t.Errorf("Symlinks has %d entries after clean, want 0", len(s.Symlinks))
	}
	if len(s.Compiled) != 0 {
		t.Errorf("Compiled has %d entries after clean, want 0", len(s.Compiled))
	}
}

// TestClean_RemovesUnlinkedNestedCompiledFile verifies that a never-linked
// compiled file in a nested path is removed and its now-empty parent
// directories within the compile directory are cleaned up.
func TestClean_RemovesUnlinkedNestedCompiledFile(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compileDir, ".config/nvim/init.lua", "vim.opt\n")
	writeStateWithManifest(t, compileDir,
		nil,
		map[string]string{".config/nvim/init.lua": hash})

	if _, err := Clean(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	assertGone(t, filepath.Join(compileDir, ".config", "nvim", "init.lua"))
	assertGone(t, filepath.Join(compileDir, ".config", "nvim"))
	assertGone(t, filepath.Join(compileDir, ".config"))
}

// TestClean_UnlinkedCompiledAlreadyGone verifies a manifest entry whose compiled
// file is already absent does not cause an error.
func TestClean_UnlinkedCompiledAlreadyGone(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeStateWithManifest(t, compileDir,
		nil,
		map[string]string{".vimrc": "somehash"})

	if _, err := Clean(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean with already-gone compiled file: %v", err)
	}
}

// TestClean_UnlinkedCompiledRemoveError verifies an os.Remove failure while
// removing a never-linked compiled file is surfaced.
func TestClean_UnlinkedCompiledRemoveError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".vimrc", "set noswap\n")
	writeStateWithManifest(t, compileDir,
		nil,
		map[string]string{".vimrc": "somehash"})

	orig := osRemoveFunc
	t.Cleanup(func() { osRemoveFunc = orig })
	osRemoveFunc = func(path string) error {
		if filepath.Base(path) == ".vimrc" {
			return fmt.Errorf("forced remove error")
		}
		return orig(path)
	}

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error removing unlinked compiled file, got nil")
	}
}

// TestCleanCompiled_NonLocalManifest_Refused verifies cleanCompiled refuses a
// manifest key that escapes the compile directory and performs no out-of-tree
// deletion. state.Load already rejects such a key, so this exercises the
// belt-and-suspenders guard at the deletion sink directly.
func TestCleanCompiled_NonLocalManifest_Refused(t *testing.T) {
	compileDir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile victim: %v", err)
	}

	rel, err := filepath.Rel(compileDir, victim)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	s := state.New()
	s.Compiled[rel] = state.CompiledEntry{ContentHash: "h"}

	cfg := LinkConfig{CompileDir: compileDir, TargetDir: t.TempDir()}
	if err := cleanCompiled(cfg, s, map[string]struct{}{}); err == nil {
		t.Fatal("expected cleanCompiled to refuse non-local manifest key, got nil")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("victim file was deleted or unreadable: %v", statErr)
	}
}

func TestClean_AlreadyGone(t *testing.T) {
	// Files already removed from disk should not cause errors.
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeState(t, compileDir, ".bashrc", "somehash")
	// No symlink or compiled file created.

	if _, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean with already-gone files: %v", err)
	}
}

func TestClean_LoadStateError(t *testing.T) {
	compileDir := t.TempDir()
	writeCorruptState(t, compileDir)

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from corrupt state, got nil")
	}
}

func TestClean_SaveStateError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	// Remove the state file and make compileDir read-only so Save fails.
	if err := os.Remove(filepath.Join(compileDir, ".dotsmith.state")); err != nil {
		t.Fatalf("Remove state: %v", err)
	}
	if err := os.Chmod(compileDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(compileDir, 0o755) })

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error saving state, got nil")
	}
}

func TestClean_RemoveSymlinkError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	orig := osRemoveFunc
	t.Cleanup(func() { osRemoveFunc = orig })
	osRemoveFunc = func(path string) error {
		// Fail on the targetDir path (symlink), succeed on others.
		if filepath.Dir(path) == targetDir {
			return fmt.Errorf("forced remove error")
		}
		return orig(path)
	}

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error removing symlink, got nil")
	}
}

func TestClean_RemoveSourceError(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeCompiled(t, compileDir, ".bashrc", "data\n")
	makeSymlink(t, compileDir, targetDir, ".bashrc")
	writeState(t, compileDir, ".bashrc", "somehash")

	orig := osRemoveFunc
	t.Cleanup(func() { osRemoveFunc = orig })
	osRemoveFunc = func(path string) error {
		// Fail on the compileDir path (source), succeed on others.
		if filepath.Dir(path) == compileDir {
			return fmt.Errorf("forced remove error")
		}
		return orig(path)
	}

	_, err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error removing compiled file, got nil")
	}
}

// ---- containment guard tests ------------------------------------------------

// nonLocalState builds a State whose single entry escapes its directory. It
// bypasses state.Load (which would reject such an entry) to simulate a future
// producer that populates State without going through Load.
func nonLocalState(rel string) *state.State {
	s := state.New()
	s.Symlinks["evil"] = state.SymlinkEntry{Source: rel, Target: rel, ContentHash: "h"}
	return s
}

// TestRemoveOrphans_NonLocalTargetRefused verifies removeOrphans refuses a
// non-local Target and performs no out-of-tree deletion.
func TestRemoveOrphans_NonLocalTarget_Refused(t *testing.T) {
	targetDir := t.TempDir()
	compileDir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile victim: %v", err)
	}

	// Relative path from targetDir back out to the victim file.
	rel, err := filepath.Rel(targetDir, victim)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	s := nonLocalState(rel)

	cfg := LinkConfig{CompileDir: compileDir, TargetDir: targetDir}
	err = removeOrphans(cfg, s, map[string]struct{}{}, &LinkResult{})
	if err == nil {
		t.Fatal("expected removeOrphans to refuse non-local target, got nil")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("victim file was deleted or unreadable: %v", statErr)
	}
}

// TestRemoveOrphans_NonLocalSource_Refused verifies removeOrphans refuses a
// non-local Source and performs no out-of-tree deletion.
func TestRemoveOrphans_NonLocalSource_Refused(t *testing.T) {
	targetDir := t.TempDir()
	compileDir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile victim: %v", err)
	}

	rel, err := filepath.Rel(compileDir, victim)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	// Local Target so the check that fails is the Source check.
	s := state.New()
	s.Symlinks["evil"] = state.SymlinkEntry{Source: rel, Target: ".bashrc", ContentHash: "h"}

	cfg := LinkConfig{CompileDir: compileDir, TargetDir: targetDir}
	err = removeOrphans(cfg, s, map[string]struct{}{}, &LinkResult{})
	if err == nil {
		t.Fatal("expected removeOrphans to refuse non-local source, got nil")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("victim file was deleted or unreadable: %v", statErr)
	}
}

// TestCleanSymlinks_NonLocalTarget_Refused verifies cleanSymlinks refuses a
// non-local Target and performs no out-of-tree deletion.
func TestCleanSymlinks_NonLocalTarget_Refused(t *testing.T) {
	targetDir := t.TempDir()
	compileDir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile victim: %v", err)
	}

	rel, err := filepath.Rel(targetDir, victim)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	s := nonLocalState(rel)

	cfg := LinkConfig{CompileDir: compileDir, TargetDir: targetDir}
	if _, err := cleanSymlinks(cfg, s, &CleanResult{}); err == nil {
		t.Fatal("expected cleanSymlinks to refuse non-local target, got nil")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("victim file was deleted or unreadable: %v", statErr)
	}
}

// TestCleanSymlinks_NonLocalSource_Refused verifies cleanSymlinks refuses a
// non-local Source and performs no out-of-tree deletion.
func TestCleanSymlinks_NonLocalSource_Refused(t *testing.T) {
	targetDir := t.TempDir()
	compileDir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile victim: %v", err)
	}

	rel, err := filepath.Rel(compileDir, victim)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	s := state.New()
	s.Symlinks["evil"] = state.SymlinkEntry{Source: rel, Target: ".bashrc", ContentHash: "h"}

	cfg := LinkConfig{CompileDir: compileDir, TargetDir: targetDir}
	if _, err := cleanSymlinks(cfg, s, &CleanResult{}); err == nil {
		t.Fatal("expected cleanSymlinks to refuse non-local source, got nil")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("victim file was deleted or unreadable: %v", statErr)
	}
}

// ---- hashBytes test ---------------------------------------------------------

func TestHashBytes_Deterministic(t *testing.T) {
	h1 := hashBytes([]byte("hello"))
	h2 := hashBytes([]byte("hello"))
	h3 := hashBytes([]byte("world"))
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
	if len(h1) != 32 {
		t.Errorf("hash length = %d, want 32 (hex XXH3-128)", len(h1))
	}
}

// ---- symlinked-parent safety tests -----------------------------------------

// TestClean_PreservesSymlinkedParent is the end-to-end reproduction from issue
// #41: a user's non-managed symlinked parent dir (e.g. ~/.config -> a
// cloud-synced location) must survive Clean's empty-parent cleanup. Without the
// guard, os.Remove unlinks the symlink itself rather than rmdir'ing its
// (non-empty) target, silently deleting the user's link.
//
// To reproduce the exact climb the bug exercises, the managed link is planted
// directly inside the symlinked directory and recorded in state, bypassing
// linkNew's new guard (which now refuses to create such a link in the first
// place). This isolates the RemoveEmptyParents behaviour against the real Clean.
func TestClean_PreservesSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "home")
	compile := filepath.Join(root, "compiled")
	real := filepath.Join(root, "cloud")
	makeDirs(t, target, compile, real)
	if err := os.WriteFile(filepath.Join(real, "precious.txt"), []byte("data\n"), 0o644); err != nil {
		t.Fatalf("WriteFile precious: %v", err)
	}
	// The user's NON-managed symlinked parent dir under target.
	link := filepath.Join(target, ".config")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// A compiled source and a managed leaf planted inside the symlinked dir,
	// recorded in state as Target=.config/app.conf.
	hash := writeCompiled(t, compile, ".config/app.conf", "managed\n")
	if err := os.Symlink(filepath.Join(compile, ".config/app.conf"),
		filepath.Join(real, "app.conf")); err != nil {
		t.Fatalf("Symlink leaf: %v", err)
	}
	writeState(t, compile, ".config/app.conf", hash)

	if _, err := Clean(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// The user's symlink must still exist and still be a symlink.
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("user symlink %s was deleted: %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected %s to remain a symlink", link)
	}
	// The data behind it must remain.
	if _, err := os.Stat(filepath.Join(real, "precious.txt")); err != nil {
		t.Errorf("data behind symlink was lost: %v", err)
	}
}

// TestLink_RefusesSymlinkedParent verifies linkNew treats a symlinked parent
// component as a conflict, refusing to plant a managed link inside a user's
// symlinked directory and leaving the symlink and its contents untouched.
func TestLink_RefusesSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "home")
	compile := filepath.Join(root, "compiled")
	real := filepath.Join(root, "cloud")
	makeDirs(t, target, compile, real)
	link := filepath.Join(target, ".config")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	hash := writeCompiled(t, compile, ".config/app.conf", "managed\n")

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target},
		[]FileRef{{RelPath: ".config/app.conf", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected conflict error for symlinked parent, got nil")
	}
	if !strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("error %q should explain the symlinked-parent conflict", err.Error())
	}
	// No managed link must have been planted inside the symlinked dir.
	if _, statErr := os.Lstat(filepath.Join(real, "app.conf")); !os.IsNotExist(statErr) {
		t.Error("expected no managed link planted inside the symlinked dir")
	}
	// The user's symlink survives.
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Errorf("user symlink was disturbed: %v", statErr)
	}
}

// TestLink_RefusesSymlinkedParent_Nested verifies the guard inspects every
// ancestor between TargetDir and the leaf, not just the immediate parent.
func TestLink_RefusesSymlinkedParent_Nested(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "home")
	compile := filepath.Join(root, "compiled")
	real := filepath.Join(root, "cloud")
	makeDirs(t, target, compile, real)
	// Symlink a higher ancestor (.config), with a deeper real path below it.
	if err := os.Symlink(real, filepath.Join(target, ".config")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	hash := writeCompiled(t, compile, ".config/app/sub.conf", "managed\n")

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target},
		[]FileRef{{RelPath: ".config/app/sub.conf", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected conflict error for symlinked ancestor, got nil")
	}
	if !strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("error %q should explain the symlinked-parent conflict", err.Error())
	}
}

// TestLink_RefusesSymlinkedParent_DryRun verifies the guard still detects a
// symlinked parent in dry run, but now collects it as a conflict blocker (with a
// nil error) rather than aborting — so a dry run faithfully reports the conflict
// it would hit alongside any others.
func TestLink_RefusesSymlinkedParent_DryRun(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "home")
	compile := filepath.Join(root, "compiled")
	real := filepath.Join(root, "cloud")
	makeDirs(t, target, compile, real)
	if err := os.Symlink(real, filepath.Join(target, ".config")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	hash := writeCompiled(t, compile, ".config/app.conf", "managed\n")

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target, DryRun: true},
		[]FileRef{{RelPath: ".config/app.conf", ContentHash: hash}})
	if err != nil {
		t.Fatalf("dry-run Link returned error, want nil: %v", err)
	}
	b := blockerByRel(t, result.Blockers, ".config/app.conf")
	if b.Kind != BlockerConflict {
		t.Errorf("Kind = %q, want %q", b.Kind, BlockerConflict)
	}
}

// TestLink_RealNestedParent_NoConflict verifies the guard does not fire for an
// ordinary nested target whose parents are real directories (or do not exist
// yet), so normal nested linking is unaffected.
func TestLink_RealNestedParent_NoConflict(t *testing.T) {
	compile, target := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compile, ".config/app/sub.conf", "data\n")

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target},
		[]FileRef{{RelPath: ".config/app/sub.conf", ContentHash: hash}})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("result = %+v, want Created=1", result)
	}
}

// TestLink_SymlinkedParentGuard_LstatError verifies an unexpected lstat error on
// a parent component (other than not-exist) is surfaced rather than swallowed.
func TestLink_SymlinkedParentGuard_LstatError(t *testing.T) {
	compile, target := t.TempDir(), t.TempDir()
	hash := writeCompiled(t, compile, ".config/app.conf", "data\n")
	parent := filepath.Join(target, ".config")
	injectLstat(t, func(p string) (os.FileInfo, error) {
		if p == parent {
			return nil, fmt.Errorf("forced lstat error")
		}
		return os.Lstat(p)
	})

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target},
		[]FileRef{{RelPath: ".config/app.conf", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected error from parent lstat, got nil")
	}
}

// TestGuardSymlinkedParents_RootBackstop verifies the climb halts at the
// filesystem root when targetDir is never an ancestor of the target, exercising
// the dir == filepath.Dir(dir) backstop. Lstat is stubbed to report every
// component as a non-existent real path so the walk runs to the root
// deterministically, independent of the host filesystem layout.
func TestGuardSymlinkedParents_RootBackstop(t *testing.T) {
	injectLstat(t, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist })
	// targetDir is never an ancestor of the target path, so the loop walks up to
	// the filesystem root and stops at the backstop rather than at targetDir.
	if err := guardSymlinkedParents(
		filepath.Join("never", "an", "ancestor"),
		filepath.Join("a", "b", "c"),
	); err != nil {
		t.Fatalf("expected nil error at root backstop, got %v", err)
	}
}

// TestLink_RealIntermediateThenSymlinkedAncestor verifies the guard climbs past
// a real, existing intermediate directory before catching a symlinked ancestor
// higher up, covering the multi-level real-then-symlink walk.
func TestLink_RealIntermediateThenSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "home")
	compile := filepath.Join(root, "compiled")
	real := filepath.Join(root, "cloud")
	makeDirs(t, target, compile, real)
	// .config is a symlink; below it, a real "app" subdir exists in the target
	// (created through the link so the immediate parent .config/app is a real,
	// existing directory the climb walks past before reaching the .config symlink).
	if err := os.Symlink(real, filepath.Join(target, ".config")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(real, "app"), 0o755); err != nil {
		t.Fatalf("MkdirAll real/app: %v", err)
	}
	hash := writeCompiled(t, compile, ".config/app/sub.conf", "managed\n")

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compile, TargetDir: target},
		[]FileRef{{RelPath: ".config/app/sub.conf", ContentHash: hash}})
	if err == nil {
		t.Fatal("expected conflict error for symlinked ancestor, got nil")
	}
	if !strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("error %q should explain the symlinked-parent conflict", err.Error())
	}
}

// ---- JSON-round-trip guard --------------------------------------------------

func TestStateJSON_RoundTrip(t *testing.T) {
	// Guard: ensure state.SymlinkEntry fields match JSON tags expected by linker.
	entry := state.SymlinkEntry{Source: "src", Target: "tgt", ContentHash: "abc"}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got state.SymlinkEntry
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Source != "src" || got.Target != "tgt" || got.ContentHash != "abc" {
		t.Errorf("round-trip = %+v, want {src tgt abc}", got)
	}
}


// ---- dry-run blocker collection tests ---------------------------------------

// blockerByRel returns the blocker for relPath, or fails the test if absent.
func blockerByRel(t *testing.T, blockers []Blocker, relPath string) Blocker {
	t.Helper()
	for _, b := range blockers {
		if b.RelPath == relPath {
			return b
		}
	}
	t.Fatalf("no blocker for %q in %+v", relPath, blockers)
	return Blocker{}
}

// setupThreeConflicts populates compileDir with three compiled files and
// targetDir with the three conflict sub-cases (occupied target, wrong-pointing
// symlink, symlinked parent directory). It returns the FileRefs to link.
func setupThreeConflicts(t *testing.T, compileDir, targetDir string) []FileRef {
	t.Helper()
	// Occupied target: a real (non-symlink) file at .bashrc.
	hBash := writeCompiled(t, compileDir, ".bashrc", "data\n")
	if err := os.WriteFile(filepath.Join(targetDir, ".bashrc"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Wrong-pointing symlink at .vimrc.
	hVim := writeCompiled(t, compileDir, ".vimrc", "set nocompatible\n")
	if err := os.Symlink("/dev/null", filepath.Join(targetDir, ".vimrc")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// Symlinked parent directory: .config -> elsewhere, target .config/git/config.
	hGit := writeCompiled(t, compileDir, ".config/git/config", "[core]\n")
	if err := os.Symlink(t.TempDir(), filepath.Join(targetDir, ".config")); err != nil {
		t.Fatalf("Symlink parent: %v", err)
	}
	return []FileRef{
		{RelPath: ".bashrc", ContentHash: hBash},
		{RelPath: ".vimrc", ContentHash: hVim},
		{RelPath: ".config/git/config", ContentHash: hGit},
	}
}

// TestLink_DryRun_CollectsAllConflictSubCases verifies that a dry-run continues
// past every conflict and collects all three conflict sub-cases in one call with
// Kind == conflict, returning a nil error.
func TestLink_DryRun_CollectsAllConflictSubCases(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	files := setupThreeConflicts(t, compileDir, targetDir)

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true}, files)
	if err != nil {
		t.Fatalf("dry-run Link returned error: %v", err)
	}
	if len(result.Blockers) != 3 {
		t.Fatalf("got %d blockers, want 3: %+v", len(result.Blockers), result.Blockers)
	}
	for _, rel := range []string{".bashrc", ".vimrc", ".config/git/config"} {
		assertConflictBlocker(t, result.Blockers, rel)
	}

	// Blocked files are excluded from the change counts.
	if result.Created != 0 || result.Updated != 0 || result.Unchanged != 0 {
		t.Errorf("blocked files leaked into counts: created=%d updated=%d unchanged=%d",
			result.Created, result.Updated, result.Unchanged)
	}

	// Nothing was written.
	if _, statErr := os.Lstat(filepath.Join(targetDir, ".config", "git")); !os.IsNotExist(statErr) {
		t.Error("dry-run created files under the symlinked parent")
	}
}

// assertConflictBlocker fails the test unless relPath has a conflict blocker with
// a non-empty Detail.
func assertConflictBlocker(t *testing.T, blockers []Blocker, relPath string) {
	t.Helper()
	b := blockerByRel(t, blockers, relPath)
	if b.Kind != BlockerConflict {
		t.Errorf("%s: Kind = %q, want %q", relPath, b.Kind, BlockerConflict)
	}
	if b.Detail == "" {
		t.Errorf("%s: Detail is empty", relPath)
	}
}

// TestLink_DryRun_BlockersSorted verifies blockers are sorted by RelPath
// regardless of the input order.
func TestLink_DryRun_BlockersSorted(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	rels := []string{"z", "a", "m"}
	files := make([]FileRef, 0, len(rels))
	for _, rel := range rels {
		h := writeCompiled(t, compileDir, rel, rel+"\n")
		if err := os.WriteFile(filepath.Join(targetDir, rel), []byte("mine"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
		files = append(files, FileRef{RelPath: rel, ContentHash: h})
	}

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true}, files)
	if err != nil {
		t.Fatalf("dry-run Link: %v", err)
	}
	got := make([]string, len(result.Blockers))
	for i, b := range result.Blockers {
		got[i] = b.RelPath
	}
	want := []string{"a", "m", "z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("blocker order = %v, want %v", got, want)
	}
}

// TestLink_DryRun_NonBlockedFilesStillCounted verifies that a dry-run with a mix
// of blocked and clean files counts only the non-blocked files.
func TestLink_DryRun_NonBlockedFilesStillCounted(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	hOK := writeCompiled(t, compileDir, ".okrc", "ok\n")
	hBad := writeCompiled(t, compileDir, ".badrc", "bad\n")
	if err := os.WriteFile(filepath.Join(targetDir, ".badrc"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true},
		[]FileRef{
			{RelPath: ".okrc", ContentHash: hOK},
			{RelPath: ".badrc", ContentHash: hBad},
		})
	if err != nil {
		t.Fatalf("dry-run Link: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1 (the non-blocked file)", result.Created)
	}
	if len(result.Blockers) != 1 {
		t.Errorf("Blockers = %d, want 1", len(result.Blockers))
	}
}

// TestLink_DryRun_NoBlockers verifies a clean dry-run returns an empty Blockers
// slice and a nil error.
func TestLink_DryRun_NoBlockers(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	h := writeCompiled(t, compileDir, ".bashrc", "data\n")

	result, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir, DryRun: true},
		[]FileRef{{RelPath: ".bashrc", ContentHash: h}})
	if err != nil {
		t.Fatalf("dry-run Link: %v", err)
	}
	if len(result.Blockers) != 0 {
		t.Errorf("Blockers = %d, want 0", len(result.Blockers))
	}
}

// TestLink_RealRun_FailsFast_ByteIdenticalMessages verifies a real (non-dry-run)
// run still returns on the first conflict with today's exact error message for
// each sub-case, and that Blockers stays empty.
func TestLink_RealRun_FailsFast_ByteIdenticalMessages(t *testing.T) {
	tests := []struct {
		name    string
		relPath string
		setup   func(t *testing.T, targetDir string)
		wantErr func(targetDir string) string
	}{
		{
			name:    "occupied target",
			relPath: ".bashrc",
			setup: func(t *testing.T, targetDir string) {
				if err := os.WriteFile(filepath.Join(targetDir, ".bashrc"), []byte("mine"), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			},
			wantErr: func(targetDir string) string {
				return fmt.Sprintf("link .bashrc: conflict: %s exists and is not a symlink",
					filepath.Join(targetDir, ".bashrc"))
			},
		},
		{
			name:    "wrong symlink",
			relPath: ".vimrc",
			setup: func(t *testing.T, targetDir string) {
				if err := os.Symlink("/dev/null", filepath.Join(targetDir, ".vimrc")); err != nil {
					t.Fatalf("Symlink: %v", err)
				}
			},
			wantErr: func(targetDir string) string {
				return fmt.Sprintf("link .vimrc: conflict: %s points to /dev/null, expected %s",
					filepath.Join(targetDir, ".vimrc"), "EXPECT_SOURCE")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compileDir, targetDir := t.TempDir(), t.TempDir()
			h := writeCompiled(t, compileDir, tc.relPath, "data\n")
			tc.setup(t, targetDir)

			result, err := Link(context.Background(),
				LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
				[]FileRef{{RelPath: tc.relPath, ContentHash: h}})
			if err == nil {
				t.Fatal("expected real-run conflict error, got nil")
			}
			want := strings.ReplaceAll(tc.wantErr(targetDir), "EXPECT_SOURCE",
				filepath.Join(compileDir, tc.relPath))
			if err.Error() != want {
				t.Errorf("error message:\n got: %q\nwant: %q", err.Error(), want)
			}
			if result != nil {
				t.Errorf("real-run result should be nil on error, got %+v", result)
			}
		})
	}
}

// TestLink_RealRun_FailsFast_SymlinkedParent verifies the symlinked-parent
// conflict in a real run returns today's exact message.
func TestLink_RealRun_FailsFast_SymlinkedParent(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	h := writeCompiled(t, compileDir, ".config/git/config", "[core]\n")
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(targetDir, ".config")); err != nil {
		t.Fatalf("Symlink parent: %v", err)
	}

	_, err := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".config/git/config", ContentHash: h}})
	if err == nil {
		t.Fatal("expected symlinked-parent conflict error, got nil")
	}
	want := fmt.Sprintf(
		"link .config/git/config: conflict: parent %s is a symlink — "+
			"refusing to plant a managed link inside it; "+
			"replace the symlink with a real directory or move the target out from under it",
		filepath.Join(targetDir, ".config"))
	if err.Error() != want {
		t.Errorf("error message:\n got: %q\nwant: %q", err.Error(), want)
	}
}

// TestLink_RealRun_ConflictBlockersEmpty verifies a real-run conflict leaves
// no Blockers (it aborts before collecting).
func TestLink_RealRun_ConflictBlockersEmpty(t *testing.T) {
	compileDir, targetDir := t.TempDir(), t.TempDir()
	h := writeCompiled(t, compileDir, ".bashrc", "data\n")
	if err := os.WriteFile(filepath.Join(targetDir, ".bashrc"), []byte("mine"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, _ := Link(context.Background(),
		LinkConfig{CompileDir: compileDir, TargetDir: targetDir},
		[]FileRef{{RelPath: ".bashrc", ContentHash: h}})
	if result != nil && len(result.Blockers) != 0 {
		t.Errorf("real-run Blockers = %+v, want empty", result.Blockers)
	}
}

// TestBlockerError_Unwrap verifies blockerError preserves a wrapped OS error so
// errors.Is checks still hold.
func TestBlockerError_Unwrap(t *testing.T) {
	wrapped := os.ErrPermission
	be := &blockerError{kind: BlockerConflict, msg: "boom", err: wrapped}
	if be.Error() != "boom" {
		t.Errorf("Error() = %q, want %q", be.Error(), "boom")
	}
	if !errors.Is(be, wrapped) {
		t.Error("errors.Is should find the wrapped error")
	}
}
