package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
)

// TestCompile_DryRun_ReportsMatchingIdentity verifies a dry-run compile reports
// the identity that would decrypt each .age source, without decrypting it.
func TestCompile_DryRun_ReportsMatchingIdentity(t *testing.T) {
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".subfile-010.bashrc"), "export SECRET=hi\n", keyPath)

	result, err := Compile(context.Background(), CompileConfig{
		DotfilesDir: root,
		Identity:    identity.Identity{},
		Identities:  set,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Compile (dry-run): %v", err)
	}
	if len(result.DryRunReports) != 1 {
		t.Fatalf("len(DryRunReports) = %d, want 1", len(result.DryRunReports))
	}
	r := result.DryRunReports[0]
	if !r.Matched {
		t.Fatal("expected the .age source to match the resolved identity")
	}
	if r.IdentityPath != keyPath {
		t.Errorf("IdentityPath = %q, want %q", r.IdentityPath, keyPath)
	}
	if r.IdentityKind != "age" {
		t.Errorf("IdentityKind = %q, want %q", r.IdentityKind, "age")
	}
	// In dry-run the encrypted content is not decrypted, so it stays empty.
	if len(result.Files) != 1 || len(result.Files[0].Content) != 0 {
		t.Errorf("dry-run should not decrypt content, got %q", result.Files[0].Content)
	}
}

// TestCompile_DryRun_NoMatchReported verifies a file encrypted to a key this
// machine does not hold is reported as a non-match in dry-run, with no error.
func TestCompile_DryRun_NoMatchReported(t *testing.T) {
	keyPath1, _ := generateKey(t) // file encrypted to this key
	_, set2 := generateKey(t)     // but we only hold this key

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".subfile-010.bashrc"), "secret\n", keyPath1)

	result, err := Compile(context.Background(), CompileConfig{
		DotfilesDir: root,
		Identity:    identity.Identity{},
		Identities:  set2,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Compile (dry-run) should not error on a no-match: %v", err)
	}
	if len(result.DryRunReports) != 1 {
		t.Fatalf("len(DryRunReports) = %d, want 1", len(result.DryRunReports))
	}
	if result.DryRunReports[0].Matched {
		t.Error("expected a no-match report, got a match")
	}
}

// TestCompile_DryRun_RegularEncryptedFile covers the regular (non-subfile)
// encrypted source path in dry-run.
func TestCompile_DryRun_RegularEncryptedFile(t *testing.T) {
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".secret"), "data\n", keyPath)

	result, err := Compile(context.Background(), CompileConfig{
		DotfilesDir: root,
		Identity:    identity.Identity{},
		Identities:  set,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Compile (dry-run): %v", err)
	}
	if len(result.DryRunReports) != 1 || !result.DryRunReports[0].Matched {
		t.Fatalf("expected one matching dry-run report, got %+v", result.DryRunReports)
	}
}

// TestCompile_DryRun_NoEncryptedSources verifies a dry-run compile of only
// plaintext sources produces no dry-run reports and full content.
func TestCompile_DryRun_NoEncryptedSources(t *testing.T) {
	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, base, ".subfile-010.bashrc", "export PATH=/usr/bin\n")

	result, err := Compile(context.Background(), CompileConfig{
		DotfilesDir: root,
		Identity:    identity.Identity{},
		Identities:  encrypt.IdentitySet{},
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Compile (dry-run): %v", err)
	}
	if len(result.DryRunReports) != 0 {
		t.Errorf("len(DryRunReports) = %d, want 0 (no encrypted sources)", len(result.DryRunReports))
	}
	if len(result.Files[0].Content) == 0 {
		t.Error("plaintext content should be read even in dry-run")
	}
}

// TestCompile_DryRun_ProbeError surfaces a probe error (missing/corrupt .age)
// as a compile error.
func TestCompile_DryRun_ProbeError(t *testing.T) {
	_, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	// Write a .age file that is not valid age ciphertext.
	writeFile(t, base, ".subfile-010.bashrc.age", "not a valid age file\n")

	_, err := Compile(context.Background(), CompileConfig{
		DotfilesDir: root,
		Identity:    identity.Identity{},
		Identities:  set,
		DryRun:      true,
	})
	if err == nil {
		t.Fatal("expected error probing a corrupt .age file in dry-run, got nil")
	}
}

// dryRunReuse runs a dry-run compile against compileDir and returns the result,
// mirroring what the CLI does (without writing).
func dryRunReuse(
	t *testing.T, ctx context.Context, root, compileDir string, set encrypt.IdentitySet,
) *CompileResult {
	t.Helper()
	result, err := Compile(ctx, CompileConfig{
		DotfilesDir: root,
		CompileDir:  compileDir,
		Identity:    identity.Identity{},
		Identities:  set,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Compile (dry-run): %v", err)
	}
	return result
}

// TestCompile_DryRun_AnnotatesReuseAndStillProbes is the decisive dry-run test:
// after a real compile records the signature, a dry-run of the unchanged repo
// annotates the target as would-reuse AND still probes the encrypted source for
// its matching identity (the unconditional key-health probe, ADR 0004), without
// writing anything or saving state.
func TestCompile_DryRun_AnnotatesReuseAndStillProbes(t *testing.T) {
	ctx := context.Background()
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".subfile-010.bashrc"), "export SECRET=hi\n", keyPath)
	compileDir := t.TempDir()

	// First, a real compile records the signature and writes the output.
	compileAndWrite(t, ctx, root, compileDir, set)

	// Snapshot the compile dir so we can prove the dry-run touches nothing.
	before := snapshotDir(t, compileDir)

	result := dryRunReuse(t, ctx, root, compileDir, set)

	bashrc := fileFor(t, result, ".bashrc")
	if !bashrc.WouldReuse {
		t.Error("unchanged target should be annotated WouldReuse=true under dry-run")
	}
	if bashrc.Reused {
		t.Error("dry-run must never set Reused (it still probes, not reuses)")
	}
	// The unconditional probe still ran for the would-be-reused target.
	if len(result.DryRunReports) != 1 {
		t.Fatalf("len(DryRunReports) = %d, want 1 (probe must run even when reusable)", len(result.DryRunReports))
	}
	if !result.DryRunReports[0].Matched || result.DryRunReports[0].IdentityPath != keyPath {
		t.Errorf("probe report = %+v, want a match on %q", result.DryRunReports[0], keyPath)
	}

	// Zero filesystem side effects.
	after := snapshotDir(t, compileDir)
	if before != after {
		t.Error("dry-run changed the compile directory; expected zero side effects")
	}
}

// TestCompile_DryRun_AnnotatesRecompile verifies that after a source changes,
// a dry-run annotates the target as would-recompile (WouldReuse=false).
func TestCompile_DryRun_AnnotatesRecompile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, base, ".subfile-010.bashrc", "v1\n")
	compileDir := t.TempDir()

	compileAndWrite(t, ctx, root, compileDir, encrypt.IdentitySet{})

	// Change the source so the signature no longer matches.
	writeFile(t, base, ".subfile-010.bashrc", "v2-changed\n")

	result := dryRunReuse(t, ctx, root, compileDir, encrypt.IdentitySet{})
	bashrc := fileFor(t, result, ".bashrc")
	if bashrc.WouldReuse {
		t.Error("changed source should be annotated WouldReuse=false under dry-run")
	}
}

// TestCompile_DryRun_FirstRunRecompiles verifies that with no recorded state the
// dry-run annotates every target as would-recompile.
func TestCompile_DryRun_FirstRunRecompiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, base, ".subfile-010.bashrc", "v1\n")
	compileDir := t.TempDir()

	result := dryRunReuse(t, ctx, root, compileDir, encrypt.IdentitySet{})
	if fileFor(t, result, ".bashrc").WouldReuse {
		t.Error("first run (no state) should annotate WouldReuse=false")
	}
}

// TestCompile_DryRun_TwoRunsIdentical verifies two consecutive dry-runs report
// identical reuse annotations and probe reports, and that neither writes state.
func TestCompile_DryRun_TwoRunsIdentical(t *testing.T) {
	ctx := context.Background()
	keyPath, set := generateKey(t)

	root := t.TempDir()
	base := makeDir(t, root, "base")
	encryptFile(t, filepath.Join(base, ".subfile-010.bashrc"), "secret\n", keyPath)
	writeFile(t, base, ".subfile-010.vimrc", "set nocompatible\n")
	compileDir := t.TempDir()

	compileAndWrite(t, ctx, root, compileDir, set)
	before := snapshotDir(t, compileDir)

	first := dryRunReuse(t, ctx, root, compileDir, set)
	second := dryRunReuse(t, ctx, root, compileDir, set)

	if len(first.Files) != len(second.Files) {
		t.Fatalf("file counts differ: %d vs %d", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].RelPath != second.Files[i].RelPath ||
			first.Files[i].WouldReuse != second.Files[i].WouldReuse {
			t.Errorf("run %d differs: %+v vs %+v", i, first.Files[i], second.Files[i])
		}
	}
	if len(first.DryRunReports) != len(second.DryRunReports) {
		t.Errorf("probe report counts differ: %d vs %d",
			len(first.DryRunReports), len(second.DryRunReports))
	}
	if after := snapshotDir(t, compileDir); before != after {
		t.Error("dry-runs changed the compile directory; expected zero side effects")
	}
}

// snapshotDir returns a deterministic fingerprint of every file under dir (path,
// size, mode, content hash) so a test can assert the directory was untouched.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, e := range entries {
		info, statErr := e.Info()
		if statErr != nil {
			t.Fatalf("stat %s: %v", e.Name(), statErr)
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("read %s: %v", e.Name(), readErr)
		}
		fmt.Fprintf(&b, "%s|%d|%v|%s\n", e.Name(), info.Size(), info.Mode(), hashContent(data))
	}
	return b.String()
}
