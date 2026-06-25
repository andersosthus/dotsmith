package compiler

import (
	"context"
	"path/filepath"
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
