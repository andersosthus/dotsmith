package compiler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
	"github.com/andersosthus/dotsmith/internal/state"
)

// compileAndWrite runs a full compile-then-write cycle against compileDir and
// returns the write stats, mirroring what `compile`/`apply` do at the CLI layer.
func compileAndWrite(
	t *testing.T, ctx context.Context, root, compileDir string, set encrypt.IdentitySet,
) (*CompileResult, WriteStats) {
	t.Helper()
	result, err := Compile(ctx, CompileConfig{
		DotfilesDir: root,
		CompileDir:  compileDir,
		Identity:    identity.Identity{},
		Identities:  set,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	stats, err := WriteCompiled(ctx, result, WriteConfig{CompileDir: compileDir})
	if err != nil {
		t.Fatalf("WriteCompiled: %v", err)
	}
	return result, stats
}

// fileFor returns the compiled file for relPath from a result, failing if absent.
func fileFor(t *testing.T, result *CompileResult, relPath string) CompiledFile {
	t.Helper()
	for _, f := range result.Files {
		if f.RelPath == relPath {
			return f
		}
	}
	t.Fatalf("compiled file %q not found in result", relPath)
	return CompiledFile{}
}

// --- reuse gate (isolated unit) ---

func TestReuseGate(t *testing.T) {
	compileDir := t.TempDir()
	const target = ".bashrc"
	content := []byte("export PATH=/usr/bin\n")
	contentHash := hashContent(content)
	if err := os.WriteFile(filepath.Join(compileDir, target), content, 0o644); err != nil {
		t.Fatalf("seed compiled file: %v", err)
	}

	tests := []struct {
		name      string
		prior     map[string]state.CompiledEntry
		freshSig  string
		removeOut bool
		wantReuse bool
	}{
		{
			name:      "both gates pass reuses",
			prior:     map[string]state.CompiledEntry{target: {ContentHash: contentHash, SourceSignature: "sig-1"}},
			freshSig:  "sig-1",
			wantReuse: true,
		},
		{
			name:      "signature mismatch recompiles",
			prior:     map[string]state.CompiledEntry{target: {ContentHash: contentHash, SourceSignature: "sig-1"}},
			freshSig:  "sig-2",
			wantReuse: false,
		},
		{
			name:      "absent prior signature recompiles",
			prior:     map[string]state.CompiledEntry{target: {ContentHash: contentHash, SourceSignature: ""}},
			freshSig:  "sig-1",
			wantReuse: false,
		},
		{
			name:      "no prior entry recompiles",
			prior:     map[string]state.CompiledEntry{},
			freshSig:  "sig-1",
			wantReuse: false,
		},
		{
			name:      "missing compiled output recompiles",
			prior:     map[string]state.CompiledEntry{target: {ContentHash: contentHash, SourceSignature: "sig-1"}},
			freshSig:  "sig-1",
			removeOut: true,
			wantReuse: false,
		},
		{
			name:      "altered compiled output recompiles",
			prior:     map[string]state.CompiledEntry{target: {ContentHash: "stale-hash", SourceSignature: "sig-1"}},
			freshSig:  "sig-1",
			wantReuse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := compileDir
			if tt.removeOut {
				dir = t.TempDir() // no compiled file present
			}
			d := reuseGate(tt.prior, dir, target, tt.freshSig)
			if d.Reuse != tt.wantReuse {
				t.Fatalf("Reuse = %v, want %v", d.Reuse, tt.wantReuse)
			}
			if d.Reuse && d.ContentHash != contentHash {
				t.Errorf("ContentHash = %q, want %q", d.ContentHash, contentHash)
			}
		})
	}
}

// TestReuseGate_ReadErrorRecompiles exercises gate 2's read-error branch via the
// injectable read sink — a path a real readable file otherwise hides.
func TestReuseGate_ReadErrorRecompiles(t *testing.T) {
	orig := osReadFileFunc
	osReadFileFunc = func(string) ([]byte, error) { return nil, os.ErrPermission }
	defer func() { osReadFileFunc = orig }()

	prior := map[string]state.CompiledEntry{".bashrc": {ContentHash: "h", SourceSignature: "sig"}}
	d := reuseGate(prior, t.TempDir(), ".bashrc", "sig")
	if d.Reuse {
		t.Fatal("expected no reuse on read error")
	}
}

// --- end-to-end: the decisive test ---

// TestReuse_SecondCompileSkipsDecryption is the core promise: after one compile
// with a valid identity, a second compile with the identity removed succeeds
// with every target reused, proving decryption was genuinely skipped.
func TestReuse_SecondCompileSkipsDecryption(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	keyPath, set := generateKey(t)
	encryptFile(t, filepath.Join(root, "base", "secret.subfile-010.sh"), "export TOKEN=abc\n", keyPath)
	writeFile(t, filepath.Join(root, "base"), "secret.subfile-020.sh", "echo done\n")

	ctx := context.Background()
	r1, s1 := compileAndWrite(t, ctx, root, compileDir, set)
	if s1.Written != 1 || s1.Reused != 0 {
		t.Fatalf("first compile stats = %+v, want 1 written / 0 reused", s1)
	}
	if fileFor(t, r1, "secret.sh").Reused {
		t.Fatal("first compile should not reuse")
	}

	// Remove the identity entirely: a real decrypt would now fail.
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	emptySet := encrypt.IdentitySet{}

	r2, s2 := compileAndWrite(t, ctx, root, compileDir, emptySet)
	if s2.Reused != 1 || s2.Written != 0 || s2.Unchanged != 0 {
		t.Fatalf("second compile stats = %+v, want 1 reused / 0 written / 0 unchanged", s2)
	}
	cf := fileFor(t, r2, "secret.sh")
	assertReusedFile(t, cf, fileFor(t, r1, "secret.sh").ContentHash)
}

// assertReusedFile verifies a CompiledFile is flagged reused, carries nil
// content, the prior ContentHash, and the encrypted-derived flag.
func assertReusedFile(t *testing.T, cf CompiledFile, wantHash string) {
	t.Helper()
	if !cf.Reused {
		t.Error("file should be reused")
	}
	if cf.Content != nil {
		t.Errorf("reused file Content = %q, want nil", cf.Content)
	}
	if cf.ContentHash != wantHash {
		t.Errorf("reused file ContentHash = %q, want %q", cf.ContentHash, wantHash)
	}
	if !cf.FromEncrypted {
		t.Error("reused encrypted-derived file should report FromEncrypted")
	}
}

// TestReuse_EditRecompilesOnlyThatTarget asserts editing one source recompiles
// only its target while the untouched target is reused.
func TestReuse_EditRecompilesOnlyThatTarget(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.vimrc", "set nu\n")

	ctx := context.Background()
	set := encrypt.IdentitySet{}
	compileAndWrite(t, ctx, root, compileDir, set)

	// Edit only .bashrc's source.
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a-changed\n")

	r2, s2 := compileAndWrite(t, ctx, root, compileDir, set)
	if s2.Written != 1 || s2.Reused != 1 {
		t.Fatalf("stats = %+v, want 1 written / 1 reused", s2)
	}
	if fileFor(t, r2, ".bashrc").Reused {
		t.Error(".bashrc should have been recompiled")
	}
	if !fileFor(t, r2, ".vimrc").Reused {
		t.Error(".vimrc should have been reused")
	}
}

// TestReuse_ReEncryptRecompilesTarget asserts re-encrypting a secret (new
// ciphertext, identical plaintext) recompiles its target.
func TestReuse_ReEncryptRecompilesTarget(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	keyPath, set := generateKey(t)
	plain := filepath.Join(root, "base", "secret.subfile-010.sh")
	encryptFile(t, plain, "export TOKEN=abc\n", keyPath)

	ctx := context.Background()
	compileAndWrite(t, ctx, root, compileDir, set)

	// Re-encrypt the same plaintext: new ciphertext bytes, so the signature moves.
	encryptFile(t, plain, "export TOKEN=abc\n", keyPath)

	_, s2 := compileAndWrite(t, ctx, root, compileDir, set)
	// Plaintext is identical so the assembled output is byte-identical: it is
	// decrypted and reassembled (not reused), so the disk write is skipped and
	// it counts as Unchanged, never Reused.
	if s2.Reused != 0 || s2.Written != 0 || s2.Unchanged != 1 {
		t.Fatalf("re-encrypt stats = %+v, want 0 reused / 0 written / 1 unchanged", s2)
	}
}

// TestReuse_FirstRunNoSignatureRecompilesEverything asserts the first compile
// after upgrade (state has no recorded signatures) recompiles every target.
func TestReuse_FirstRunNoSignatureRecompilesEverything(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")

	ctx := context.Background()
	set := encrypt.IdentitySet{}

	// Seed a pre-reuse manifest: a ContentHash entry but no SourceSignature.
	r0, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("seed Compile: %v", err)
	}
	cf := fileFor(t, r0, ".bashrc")
	if err = os.WriteFile(filepath.Join(compileDir, ".bashrc"), cf.Content, 0o644); err != nil {
		t.Fatalf("seed compiled file: %v", err)
	}
	s := state.New()
	s.Compiled[".bashrc"] = state.CompiledEntry{ContentHash: cf.ContentHash} // no signature
	if err = state.Save(ctx, s, compileDir); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	_, stats := compileAndWrite(t, ctx, root, compileDir, set)
	if stats.Reused != 0 {
		t.Fatalf("first post-upgrade compile must not reuse; stats = %+v", stats)
	}
	// The on-disk content was already identical, so it is Unchanged, not reused.
	if stats.Unchanged != 1 {
		t.Fatalf("stats = %+v, want 1 unchanged", stats)
	}

	// The signature is now recorded; the next run reuses.
	_, stats2 := compileAndWrite(t, ctx, root, compileDir, set)
	if stats2.Reused != 1 {
		t.Fatalf("second run should reuse; stats = %+v", stats2)
	}
}

// --- mode on reuse ---

// TestReuse_RepairsModeWithoutRewrite asserts a reused encrypted-derived file
// loosened on disk is repaired to 0600 without a content rewrite.
func TestReuse_RepairsModeWithoutRewrite(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	keyPath, set := generateKey(t)
	encryptFile(t, filepath.Join(root, "base", "secret.subfile-010.sh"), "export TOKEN=abc\n", keyPath)

	ctx := context.Background()
	compileAndWrite(t, ctx, root, compileDir, set)

	out := filepath.Join(compileDir, "secret.sh")
	before := mustReadFile(t, out)
	beforeInfo := mustStat(t, out)

	// Loosen the mode, then recompile — gate 2 still matches the recorded hash.
	if err := os.Chmod(out, 0o644); err != nil {
		t.Fatalf("chmod loosen: %v", err)
	}

	_, stats := compileAndWrite(t, ctx, root, compileDir, set)
	if stats.Reused != 1 {
		t.Fatalf("stats = %+v, want 1 reused", stats)
	}

	info := mustStat(t, out)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
	if !info.ModTime().Equal(beforeInfo.ModTime()) {
		t.Error("content was rewritten on reuse (mtime changed)")
	}
	if string(mustReadFile(t, out)) != string(before) {
		t.Error("reused content changed")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}

// TestReuse_PlainFileRepairsTo0644 asserts a reused non-encrypted file is
// re-asserted to 0644 on the reuse path.
func TestReuse_PlainFileRepairsTo0644(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")

	ctx := context.Background()
	set := encrypt.IdentitySet{}
	compileAndWrite(t, ctx, root, compileDir, set)

	out := filepath.Join(compileDir, ".bashrc")
	if err := os.Chmod(out, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, stats := compileAndWrite(t, ctx, root, compileDir, set)
	if stats.Reused != 1 {
		t.Fatalf("stats = %+v, want 1 reused", stats)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 0644", info.Mode().Perm())
	}
}

// --- reporting distinctness ---

// TestReuse_StatsDistinct asserts Reused is distinct from Written/Unchanged/
// Pruned across reuse, recompile, and re-encrypt in a single scenario.
func TestReuse_StatsDistinct(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	keyPath, set := generateKey(t)
	secret := filepath.Join(root, "base", "secret.subfile-010.sh")
	encryptFile(t, secret, "export TOKEN=abc\n", keyPath)
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.vimrc", "set nu\n")

	ctx := context.Background()
	compileAndWrite(t, ctx, root, compileDir, set)

	// Edit .bashrc (recompile → written), re-encrypt secret (recompile → unchanged
	// output since plaintext identical), leave .vimrc untouched (reuse).
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a-changed\n")
	encryptFile(t, secret, "export TOKEN=abc\n", keyPath)

	_, stats := compileAndWrite(t, ctx, root, compileDir, set)
	if stats.Written != 1 {
		t.Errorf("Written = %d, want 1 (.bashrc)", stats.Written)
	}
	if stats.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1 (secret.sh re-encrypted, identical output)", stats.Unchanged)
	}
	if stats.Reused != 1 {
		t.Errorf("Reused = %d, want 1 (.vimrc)", stats.Reused)
	}
	if len(stats.Pruned) != 0 {
		t.Errorf("Pruned = %v, want none", stats.Pruned)
	}
}

// TestReuse_DisabledWhenNoCompileDir asserts that with no CompileDir configured
// (e.g. render) reuse never fires even when a matching artifact exists.
func TestReuse_DisabledWhenNoCompileDir(t *testing.T) {
	root := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")

	ctx := context.Background()
	result, err := Compile(ctx, CompileConfig{DotfilesDir: root, Identity: identity.Identity{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fileFor(t, result, ".bashrc").Reused {
		t.Error("reuse must be disabled when CompileDir is empty")
	}
}

// TestReuse_LoadStateErrorPropagates asserts a corrupt prior state file makes
// Compile fail rather than silently disabling reuse.
func TestReuse_LoadStateErrorPropagates(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")

	// Write a corrupt state file so state.Load errors.
	if err := os.WriteFile(filepath.Join(compileDir, ".dotsmith.state"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}

	_, err := Compile(context.Background(), CompileConfig{
		DotfilesDir: root,
		CompileDir:  compileDir,
		Identity:    identity.Identity{},
	})
	if err == nil {
		t.Fatal("expected Compile to fail on corrupt prior state")
	}
}

// TestReuse_ChmodErrorOnReuse exercises the reuse-path chmod-error branch of
// writeCompiledFile: a reused file whose mode cannot be re-asserted surfaces an
// error rather than being silently left loose.
func TestReuse_ChmodErrorOnReuse(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	writeFile(t, filepath.Join(root, "base"), ".subfile-010.bashrc", "a\n")

	ctx := context.Background()
	set := encrypt.IdentitySet{}
	result, _ := compileAndWrite(t, ctx, root, compileDir, set)

	// Recompile produces a reused entry; remove the file between compile and
	// write so the reuse-path chmod fails. The gate already passed in Compile,
	// so the result still flags the target reused.
	r2, err := Compile(ctx, CompileConfig{
		DotfilesDir: root, CompileDir: compileDir, Identity: identity.Identity{}, Identities: set,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !fileFor(t, r2, ".bashrc").Reused {
		t.Fatal("expected .bashrc to be reused")
	}
	_ = result
	if err = os.Remove(filepath.Join(compileDir, ".bashrc")); err != nil {
		t.Fatalf("remove compiled file: %v", err)
	}
	if _, err = WriteCompiled(ctx, r2, WriteConfig{CompileDir: compileDir}); err == nil {
		t.Fatal("expected chmod error when reused file is missing at write time")
	}
}

// TestReuse_DryRunNeverReuses asserts a dry-run compile probes (does not reuse)
// even when a valid prior artifact and matching signature exist.
func TestReuse_DryRunNeverReuses(t *testing.T) {
	root := t.TempDir()
	compileDir := t.TempDir()
	makeDir(t, root, "base")
	keyPath, set := generateKey(t)
	encryptFile(t, filepath.Join(root, "base", "secret.subfile-010.sh"), "export TOKEN=abc\n", keyPath)

	ctx := context.Background()
	compileAndWrite(t, ctx, root, compileDir, set)

	// Dry-run after a successful compile: must still probe, never reuse.
	result, err := Compile(ctx, CompileConfig{
		DotfilesDir: root,
		CompileDir:  compileDir,
		Identity:    identity.Identity{},
		Identities:  set,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("dry-run Compile: %v", err)
	}
	if fileFor(t, result, "secret.sh").Reused {
		t.Error("dry-run must not reuse")
	}
	if len(result.DryRunReports) != 1 {
		t.Errorf("dry-run reports = %d, want 1 (source probed unconditionally)", len(result.DryRunReports))
	}
}
