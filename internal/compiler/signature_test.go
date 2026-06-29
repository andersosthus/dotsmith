package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andersosthus/dotsmith/internal/hash"
	"github.com/andersosthus/dotsmith/internal/identity"
)

// hashOfSingle returns the source signature a single subfile with the given
// bytes must produce: the digest of that subfile's content hash. It mirrors the
// SourceSignature construction for the single-subfile case so the test asserts
// the actual value, not just that something changed.
func hashOfSingle(content []byte) string {
	return hash.Sum([]byte(hash.Sum(content)))
}

// sigSubfile is a small helper that writes content to a file under dir and
// returns a SubfileDesc pointing at it. Encrypted marks whether the bytes are
// ciphertext, but SourceSignature hashes the raw bytes either way.
func sigSubfile(t *testing.T, dir, name, content string, number string, encrypted bool) SubfileDesc {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return SubfileDesc{
		Number:     number,
		SourcePath: path,
		Encrypted:  encrypted,
		Layer:      "base",
		SourceName: name,
	}
}

func mustSignature(t *testing.T, subfiles []SubfileDesc) string {
	t.Helper()
	sig, err := SourceSignature(context.Background(), subfiles)
	if err != nil {
		t.Fatalf("SourceSignature: %v", err)
	}
	return sig
}

func TestSourceSignature_Deterministic(t *testing.T) {
	dir := t.TempDir()
	subfiles := []SubfileDesc{
		sigSubfile(t, dir, "a.subfile-010", "alpha\n", "010", false),
		sigSubfile(t, dir, "b.subfile-020", "beta\n", "020", false),
	}
	first := mustSignature(t, subfiles)
	second := mustSignature(t, subfiles)
	if first != second {
		t.Errorf("signature not deterministic: %q != %q", first, second)
	}
	if first == "" {
		t.Error("signature should not be empty for non-empty subfiles")
	}
}

func TestSourceSignature_MovesOnContentChange(t *testing.T) {
	dir := t.TempDir()
	subfiles := []SubfileDesc{
		sigSubfile(t, dir, "a.subfile-010", "alpha\n", "010", false),
	}
	before := mustSignature(t, subfiles)

	// Rewrite the same subfile with different content.
	if err := os.WriteFile(subfiles[0].SourcePath, []byte("alpha-changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	after := mustSignature(t, subfiles)
	if before == after {
		t.Error("signature should move when a subfile's content changes")
	}
}

func TestSourceSignature_MovesOnAddRemove(t *testing.T) {
	dir := t.TempDir()
	a := sigSubfile(t, dir, "a.subfile-010", "alpha\n", "010", false)
	b := sigSubfile(t, dir, "b.subfile-020", "beta\n", "020", false)

	one := mustSignature(t, []SubfileDesc{a})
	two := mustSignature(t, []SubfileDesc{a, b})
	if one == two {
		t.Error("signature should move when a subfile is added")
	}
	// Removing b returns to the single-subfile signature.
	if got := mustSignature(t, []SubfileDesc{a}); got != one {
		t.Errorf("removing the added subfile should restore the signature: %q != %q", got, one)
	}
}

func TestSourceSignature_MovesOnReorder(t *testing.T) {
	dir := t.TempDir()
	a := sigSubfile(t, dir, "a.subfile-010", "alpha\n", "010", false)
	b := sigSubfile(t, dir, "b.subfile-020", "beta\n", "020", false)

	forward := mustSignature(t, []SubfileDesc{a, b})
	reversed := mustSignature(t, []SubfileDesc{b, a})
	if forward == reversed {
		t.Error("signature should move when subfiles are reordered")
	}
}

func TestSourceSignature_StableOnHeaderOnlyChange(t *testing.T) {
	dir := t.TempDir()
	sf := sigSubfile(t, dir, "a.subfile-010", "alpha\n", "010", false)
	before := mustSignature(t, []SubfileDesc{sf})

	// A rename or layer move changes only the provenance-header inputs
	// (SourceName, Layer), not the bytes — the signature must not move.
	moved := sf
	moved.SourceName = "renamed.subfile-010"
	moved.Layer = "hostname/workstation"
	moved.Number = "999" // reordering metadata, but it is the only subfile

	after := mustSignature(t, []SubfileDesc{moved})
	if before != after {
		t.Errorf("signature should be stable across a header-only change: %q != %q", before, after)
	}
}

func TestSourceSignature_MovesOnReencryptedSecret(t *testing.T) {
	root := t.TempDir()
	keyPath, _ := generateKey(t)

	// Encrypt the same plaintext twice; age produces different ciphertext each
	// time. The signature hashes ciphertext, so it must move.
	encryptFile(t, filepath.Join(root, ".subfile-010.bashrc"), "export SECRET=hi\n", keyPath)
	first := mustSignature(t, []SubfileDesc{{
		Number:     "010",
		SourcePath: filepath.Join(root, ".subfile-010.bashrc.age"),
		Encrypted:  true,
		Layer:      "base",
		SourceName: ".subfile-010.bashrc.age",
	}})

	encryptFile(t, filepath.Join(root, ".subfile-010.bashrc"), "export SECRET=hi\n", keyPath)
	second := mustSignature(t, []SubfileDesc{{
		Number:     "010",
		SourcePath: filepath.Join(root, ".subfile-010.bashrc.age"),
		Encrypted:  true,
		Layer:      "base",
		SourceName: ".subfile-010.bashrc.age",
	}})

	if first == second {
		t.Error("re-encrypting an unchanged secret should move the signature (new ciphertext)")
	}
}

func TestSourceSignature_ComputedWithoutDecrypting(t *testing.T) {
	root := t.TempDir()
	keyPath, _ := generateKey(t)
	encryptFile(t, filepath.Join(root, ".subfile-010.bashrc"), "export SECRET=hi\n", keyPath)
	agePath := filepath.Join(root, ".subfile-010.bashrc.age")

	sf := SubfileDesc{
		Number:     "010",
		SourcePath: agePath,
		Encrypted:  true,
		Layer:      "base",
		SourceName: ".subfile-010.bashrc.age",
	}
	// SourceSignature takes no identity set; it can only be reading the raw
	// ciphertext. Confirm it equals the hash of the ciphertext bytes on disk.
	ciphertext, err := os.ReadFile(agePath)
	if err != nil {
		t.Fatalf("ReadFile ciphertext: %v", err)
	}
	want := hashOfSingle(ciphertext)
	if got := mustSignature(t, []SubfileDesc{sf}); got != want {
		t.Errorf("signature = %q, want digest over ciphertext %q", got, want)
	}
}

func TestSourceSignature_EmptyForNoSubfiles(t *testing.T) {
	sig := mustSignature(t, nil)
	if sig == "" {
		t.Error("signature of an empty subfile list should be a stable non-empty digest")
	}
	if again := mustSignature(t, []SubfileDesc{}); again != sig {
		t.Errorf("empty-list signature should be stable: %q != %q", again, sig)
	}
}

func TestSourceSignature_ReadError(t *testing.T) {
	sf := SubfileDesc{
		Number:     "010",
		SourcePath: filepath.Join(t.TempDir(), "does-not-exist"),
		Layer:      "base",
		SourceName: "missing",
	}
	if _, err := SourceSignature(context.Background(), []SubfileDesc{sf}); err == nil {
		t.Fatal("expected error for a missing source file, got nil")
	}
}

func TestSourceSignature_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	sf := sigSubfile(t, dir, "a.subfile-010", "alpha\n", "010", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SourceSignature(ctx, []SubfileDesc{sf}); err == nil {
		t.Fatal("expected error for a cancelled context, got nil")
	}
}

// TestCompile_SignatureError exercises the compileEntry path where a target's
// content assembles successfully but computing its source signature fails.
func TestCompile_SignatureError(t *testing.T) {
	orig := sourceSignatureFunc
	t.Cleanup(func() { sourceSignatureFunc = orig })
	sourceSignatureFunc = func(_ context.Context, _ []SubfileDesc) (string, error) {
		return "", errors.New("injected signature error")
	}

	root := t.TempDir()
	base := makeDir(t, root, "base")
	writeFile(t, base, ".subfile-010.bashrc", "export A=1\n")

	cfg := CompileConfig{DotfilesDir: root, Identity: identity.Identity{}}
	if _, err := Compile(context.Background(), cfg); err == nil {
		t.Fatal("expected error from a failed source-signature computation, got nil")
	}
}
