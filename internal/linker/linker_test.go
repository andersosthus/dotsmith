package linker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if err := Clean(ctx, LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
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

	if err := Clean(context.Background(),
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

	if err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
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

	if err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Non-empty dir should remain.
	if _, err := os.Lstat(gitDir); err != nil {
		t.Errorf("expected non-empty dir %s to be preserved, got: %v", gitDir, err)
	}
}

func TestClean_AlreadyGone(t *testing.T) {
	// Files already removed from disk should not cause errors.
	compileDir, targetDir := t.TempDir(), t.TempDir()
	writeState(t, compileDir, ".bashrc", "somehash")
	// No symlink or compiled file created.

	if err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir}); err != nil {
		t.Fatalf("Clean with already-gone files: %v", err)
	}
}

func TestClean_LoadStateError(t *testing.T) {
	compileDir := t.TempDir()
	writeCorruptState(t, compileDir)

	err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: t.TempDir()})
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

	err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
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

	err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
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

	err := Clean(context.Background(), LinkConfig{CompileDir: compileDir, TargetDir: targetDir})
	if err == nil {
		t.Fatal("expected error removing compiled file, got nil")
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
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64", len(h1))
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
