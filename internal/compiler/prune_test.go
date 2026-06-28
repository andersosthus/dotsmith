package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/andersosthus/dotsmith/internal/state"
)

// entry is a tiny helper to build a manifest map from relative paths.
func manifest(paths ...string) map[string]state.CompiledEntry {
	m := make(map[string]state.CompiledEntry, len(paths))
	for _, p := range paths {
		m[p] = state.CompiledEntry{ContentHash: "h-" + p}
	}
	return m
}

func TestPruneSet(t *testing.T) {
	tests := []struct {
		name     string
		previous map[string]state.CompiledEntry
		current  map[string]state.CompiledEntry
		want     []string
	}{
		{
			name:     "empty manifest first run prunes nothing",
			previous: manifest(),
			current:  manifest(".bashrc", ".vimrc"),
			want:     nil,
		},
		{
			name:     "removed file is pruned",
			previous: manifest(".bashrc", ".vimrc"),
			current:  manifest(".bashrc"),
			want:     []string{".vimrc"},
		},
		{
			name:     "added file prunes nothing",
			previous: manifest(".bashrc"),
			current:  manifest(".bashrc", ".vimrc"),
			want:     nil,
		},
		{
			name:     "unchanged file prunes nothing",
			previous: manifest(".bashrc"),
			current:  manifest(".bashrc"),
			want:     nil,
		},
		{
			name:     "rename prunes old keeps new",
			previous: manifest(".oldrc"),
			current:  manifest(".newrc"),
			want:     []string{".oldrc"},
		},
		{
			name:     "nested paths pruned and sorted",
			previous: manifest(".config/b/x", ".config/a/y", ".bashrc"),
			current:  manifest(".bashrc"),
			want:     []string{".config/a/y", ".config/b/x"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pruneSet(tc.previous, tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("pruneSet = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDanglingPaths(t *testing.T) {
	symlinks := map[string]state.SymlinkEntry{
		".bashrc": {Source: ".bashrc", Target: ".bashrc", ContentHash: "h"},
		".vimrc":  {Source: ".vimrc", Target: ".vimrc", ContentHash: "h"},
	}
	tests := []struct {
		name     string
		pruned   []string
		symlinks map[string]state.SymlinkEntry
		want     []string
	}{
		{
			name:     "pruned with symlink dangles",
			pruned:   []string{".bashrc"},
			symlinks: symlinks,
			want:     []string{".bashrc"},
		},
		{
			name:     "pruned without symlink does not dangle",
			pruned:   []string{".unlinked"},
			symlinks: symlinks,
			want:     nil,
		},
		{
			name:     "mixed keeps order and filters",
			pruned:   []string{".bashrc", ".unlinked", ".vimrc"},
			symlinks: symlinks,
			want:     []string{".bashrc", ".vimrc"},
		},
		{
			name:     "no symlinks at all",
			pruned:   []string{".bashrc"},
			symlinks: map[string]state.SymlinkEntry{},
			want:     nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := danglingPaths(tc.pruned, tc.symlinks)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("danglingPaths = %v, want %v", got, tc.want)
			}
		})
	}
}

// resultWith builds a CompileResult with one plaintext file per relPath.
func resultWith(relPaths ...string) *CompileResult {
	r := &CompileResult{}
	for _, p := range relPaths {
		content := []byte("content of " + p + "\n")
		r.Files = append(r.Files, CompiledFile{
			RelPath:     p,
			Content:     content,
			ContentHash: hashContent(content),
		})
	}
	return r
}

// TestWriteCompiled_PrunesOrphanedFile establishes a manifest, then compiles a
// reduced result and asserts the orphaned compiled file is removed while the
// live file and the state file survive.
func TestWriteCompiled_PrunesOrphanedFile(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	cfg := WriteConfig{CompileDir: compileDir}

	// First run produces two files and the baseline manifest.
	if _, err := WriteCompiled(ctx, resultWith(".bashrc", ".vimrc"), cfg); err != nil {
		t.Fatalf("first WriteCompiled: %v", err)
	}

	// Second run drops .vimrc.
	stats, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg)
	if err != nil {
		t.Fatalf("second WriteCompiled: %v", err)
	}
	if want := []string{".vimrc"}; !reflect.DeepEqual(stats.Pruned, want) {
		t.Errorf("Pruned = %v, want %v", stats.Pruned, want)
	}
	if len(stats.Dangling) != 0 {
		t.Errorf("Dangling = %v, want empty (no symlink state)", stats.Dangling)
	}

	assertAbsent(t, filepath.Join(compileDir, ".vimrc"))
	assertPresent(t, filepath.Join(compileDir, ".bashrc"))
	assertPresent(t, filepath.Join(compileDir, state.FileName))

	// Manifest now reflects only the surviving file.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s.Compiled[".vimrc"]; ok {
		t.Error("manifest should no longer contain .vimrc")
	}
	if _, ok := s.Compiled[".bashrc"]; !ok {
		t.Error("manifest should still contain .bashrc")
	}
}

// assertAbsent fails if path exists.
func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s should be absent, stat err = %v", path, err)
	}
}

// assertPresent fails if path does not exist.
func assertPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s should be present, stat err = %v", path, err)
	}
}

// TestWriteCompiled_PruneCleansEmptyParents asserts pruning the last file in a
// nested directory removes the now-empty parent directories up to the compile
// dir.
func TestWriteCompiled_PruneCleansEmptyParents(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	cfg := WriteConfig{CompileDir: compileDir}

	if _, err := WriteCompiled(ctx, resultWith(".config/nvim/init.vim", ".bashrc"), cfg); err != nil {
		t.Fatalf("first WriteCompiled: %v", err)
	}
	if _, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg); err != nil {
		t.Fatalf("second WriteCompiled: %v", err)
	}

	if _, err := os.Stat(filepath.Join(compileDir, ".config")); !os.IsNotExist(err) {
		t.Errorf("empty parent .config should have been removed, stat err = %v", err)
	}
}

// TestWriteCompiled_PruneIdempotent asserts a second identical run prunes
// nothing and writes nothing.
func TestWriteCompiled_PruneIdempotent(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	cfg := WriteConfig{CompileDir: compileDir}

	if _, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg); err != nil {
		t.Fatalf("first WriteCompiled: %v", err)
	}
	stats, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg)
	if err != nil {
		t.Fatalf("second WriteCompiled: %v", err)
	}
	if len(stats.Pruned) != 0 {
		t.Errorf("Pruned = %v, want empty", stats.Pruned)
	}
	if stats.Written != 0 {
		t.Errorf("Written = %d, want 0", stats.Written)
	}
	if stats.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1", stats.Unchanged)
	}
}

// TestWriteCompiled_FirstRunNoManifestPrunesNothing asserts the very first run
// (no Compiled field in state) prunes nothing and establishes the baseline.
func TestWriteCompiled_FirstRunNoManifestPrunesNothing(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()

	// Seed a legacy state file with only symlinks (no compiled field), as an
	// upgrade from a pre-manifest version would have.
	legacy := `{"symlinks":{".bashrc":{"source":".bashrc","target":".bashrc","content_hash":"old"}}}`
	if err := os.WriteFile(filepath.Join(compileDir, state.FileName), []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	stats, err := WriteCompiled(ctx, resultWith(".bashrc"), WriteConfig{CompileDir: compileDir})
	if err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}
	if len(stats.Pruned) != 0 {
		t.Errorf("first run Pruned = %v, want empty", stats.Pruned)
	}

	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s.Compiled[".bashrc"]; !ok {
		t.Error("baseline manifest should contain .bashrc after first run")
	}
	// Symlinks must be preserved across the compile-only save.
	if _, ok := s.Symlinks[".bashrc"]; !ok {
		t.Error("compile must preserve the existing Symlinks entry")
	}
}

// TestWriteCompiled_DanglingReportedWhenSymlinkExists asserts that pruning a
// compiled file whose symlink state still exists reports it as dangling.
func TestWriteCompiled_DanglingReportedWhenSymlinkExists(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	cfg := WriteConfig{CompileDir: compileDir}

	if _, err := WriteCompiled(ctx, resultWith(".bashrc", ".vimrc"), cfg); err != nil {
		t.Fatalf("first WriteCompiled: %v", err)
	}

	// Simulate that .vimrc was linked: add a Symlinks entry to state.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s.Symlinks[".vimrc"] = state.SymlinkEntry{Source: ".vimrc", Target: ".vimrc", ContentHash: "h"}
	if err := state.Save(ctx, s, compileDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	stats, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg)
	if err != nil {
		t.Fatalf("second WriteCompiled: %v", err)
	}
	if want := []string{".vimrc"}; !reflect.DeepEqual(stats.Dangling, want) {
		t.Errorf("Dangling = %v, want %v", stats.Dangling, want)
	}

	// The Symlinks entry must be preserved (read-only peek) — only link removes it.
	after, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load after: %v", err)
	}
	if _, ok := after.Symlinks[".vimrc"]; !ok {
		t.Error("compile must not mutate Symlinks; .vimrc entry should remain")
	}
}

// TestWriteCompiled_DryRunPrunesNothingAndIsStable asserts dry-run reports the
// prune set without touching disk or state, and two dry-runs report identically.
func TestWriteCompiled_DryRunPrunesNothingAndIsStable(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()

	if _, err := WriteCompiled(ctx, resultWith(".bashrc", ".vimrc"), WriteConfig{CompileDir: compileDir}); err != nil {
		t.Fatalf("baseline WriteCompiled: %v", err)
	}

	dry := WriteConfig{CompileDir: compileDir, DryRun: true}
	stats1, err := WriteCompiled(ctx, resultWith(".bashrc"), dry)
	if err != nil {
		t.Fatalf("dry-run 1: %v", err)
	}
	stats2, err := WriteCompiled(ctx, resultWith(".bashrc"), dry)
	if err != nil {
		t.Fatalf("dry-run 2: %v", err)
	}
	if !reflect.DeepEqual(stats1.Pruned, stats2.Pruned) {
		t.Errorf("two dry-runs differ: %v vs %v", stats1.Pruned, stats2.Pruned)
	}
	if want := []string{".vimrc"}; !reflect.DeepEqual(stats1.Pruned, want) {
		t.Errorf("dry-run Pruned = %v, want %v", stats1.Pruned, want)
	}

	// Disk untouched: .vimrc still present.
	if _, err := os.Stat(filepath.Join(compileDir, ".vimrc")); err != nil {
		t.Errorf("dry-run must not prune .vimrc, stat err = %v", err)
	}
	// State untouched: manifest still has both.
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s.Compiled[".vimrc"]; !ok {
		t.Error("dry-run must not rewrite the manifest")
	}
}

// TestWriteCompiled_DeleteThenRecreateRoundTrips asserts a file removed and then
// brought back compiles correctly each time.
func TestWriteCompiled_DeleteThenRecreateRoundTrips(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	cfg := WriteConfig{CompileDir: compileDir}

	if _, err := WriteCompiled(ctx, resultWith(".bashrc", ".vimrc"), cfg); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	// Delete .vimrc.
	if _, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(compileDir, ".vimrc")); !os.IsNotExist(err) {
		t.Fatal(".vimrc should be pruned after run 2")
	}
	// Recreate .vimrc.
	stats, err := WriteCompiled(ctx, resultWith(".bashrc", ".vimrc"), cfg)
	if err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if len(stats.Pruned) != 0 {
		t.Errorf("run 3 Pruned = %v, want empty", stats.Pruned)
	}
	if _, err := os.Stat(filepath.Join(compileDir, ".vimrc")); err != nil {
		t.Errorf(".vimrc should be recreated, stat err = %v", err)
	}
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s.Compiled[".vimrc"]; !ok {
		t.Error("manifest should contain .vimrc again after recreate")
	}
}

// TestWriteCompiled_LoadStateError surfaces a corrupt state file as a write
// error before any file is touched.
func TestWriteCompiled_LoadStateError(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(compileDir, state.FileName), []byte("not json {{"), 0o600); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}
	_, err := WriteCompiled(ctx, resultWith(".bashrc"), WriteConfig{CompileDir: compileDir})
	if err == nil {
		t.Fatal("expected error loading corrupt state, got nil")
	}
}

// TestWriteCompiled_PruneRemoveError surfaces an os.Remove failure during
// pruning as a write error. The compiled file to prune lives in a subdirectory
// made read-only so its removal fails.
func TestWriteCompiled_PruneRemoveError(t *testing.T) {
	ctx := context.Background()
	compileDir := t.TempDir()
	cfg := WriteConfig{CompileDir: compileDir}

	// Establish a manifest containing sub/orphan and a top-level survivor.
	if _, err := WriteCompiled(ctx, resultWith("sub/orphan", ".bashrc"), cfg); err != nil {
		t.Fatalf("baseline WriteCompiled: %v", err)
	}

	// Make the sub directory read-only so removing sub/orphan fails.
	subDir := filepath.Join(compileDir, "sub")
	if err := os.Chmod(subDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

	_, err := WriteCompiled(ctx, resultWith(".bashrc"), cfg)
	if err == nil {
		t.Fatal("expected error pruning a file in a read-only directory, got nil")
	}
}

// TestPrune_NonLocalPathRejected exercises the containment guard in prune
// directly: a manifest path that escapes the compile directory must be refused
// rather than joined and deleted. state.Load normally rejects such a path, so
// this calls the helper directly as a belt-and-suspenders check.
func TestPrune_NonLocalPathRejected(t *testing.T) {
	compileDir := t.TempDir()
	err := prune(compileDir, []string{"../escape"})
	if err == nil {
		t.Fatal("expected error pruning a non-local path, got nil")
	}
}

// TestWriteCompiled_SaveStateError surfaces a state.Save failure as a write
// error. ensureCompileDir always restores the compile dir to a writable 0700,
// so the save sink is injected to fail instead.
func TestWriteCompiled_SaveStateError(t *testing.T) {
	orig := stateSaveFunc
	t.Cleanup(func() { stateSaveFunc = orig })
	stateSaveFunc = func(_ context.Context, _ *state.State, _ string) error {
		return errors.New("injected save error")
	}

	ctx := context.Background()
	compileDir := t.TempDir()
	_, err := WriteCompiled(ctx, resultWith(".bashrc"), WriteConfig{CompileDir: compileDir})
	if err == nil {
		t.Fatal("expected error from failing state save, got nil")
	}
}
