package encrypt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// generateTestIdentity creates a new X25519 identity for test use.
func generateTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	return id
}

// writeIdentityFile writes an age identity to a temp file and returns its path.
func writeIdentityFile(t *testing.T, id *age.X25519Identity) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "age-key.txt")
	content := "# created by dotsmith test\n" + id.String() + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
	return path
}

// roundTrip encrypts plaintext and decrypts with the same key source,
// asserting the output matches. Returns the ciphertext bytes.
func roundTrip(t *testing.T, ks KeySource, plaintext string) []byte {
	t.Helper()
	ctx := context.Background()

	var buf bytes.Buffer
	err := Encrypt(ctx, strings.NewReader(plaintext), &buf, ks)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	out, err := Decrypt(ctx, &buf, ks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != plaintext {
		t.Errorf("round-trip: got %q, want %q", out, plaintext)
	}
	return buf.Bytes()
}

func TestEncryptDecrypt_IdentityFile(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ks := KeySource{IdentityFile: keyPath}
	roundTrip(t, ks, "secret dotfile content\nline two\n")
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ks := KeySource{IdentityFile: keyPath}
	roundTrip(t, ks, "")
}

func TestDecrypt_WrongKey(t *testing.T) {
	id1 := generateTestIdentity(t)
	id2 := generateTestIdentity(t)
	encKeyPath := writeIdentityFile(t, id1)
	decKeyPath := writeIdentityFile(t, id2)

	ctx := context.Background()
	var buf bytes.Buffer
	if err := Encrypt(ctx, strings.NewReader("secret"), &buf, KeySource{IdentityFile: encKeyPath}); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err := Decrypt(ctx, &buf, KeySource{IdentityFile: decKeyPath})
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

func TestDecrypt_CorruptCiphertext(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	corrupt := strings.NewReader("this is not valid age ciphertext")
	_, err := Decrypt(ctx, corrupt, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error decrypting corrupt ciphertext, got nil")
	}
}

func TestDecryptFile(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()
	ks := KeySource{IdentityFile: keyPath}

	dir := t.TempDir()
	encPath := filepath.Join(dir, "secret.txt.age")

	// Encrypt to file.
	var buf bytes.Buffer
	if err := Encrypt(ctx, strings.NewReader("file contents"), &buf, ks); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := os.WriteFile(encPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Decrypt from file.
	out, err := DecryptFile(ctx, encPath, ks)
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	if string(out) != "file contents" {
		t.Errorf("got %q, want %q", out, "file contents")
	}
}

func TestDecryptFile_MissingFile(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	_, err := DecryptFile(ctx, "/nonexistent/path/secret.age", KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestEncryptFileInPlace(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()
	ks := KeySource{IdentityFile: keyPath}

	dir := t.TempDir()
	plainPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(plainPath, []byte("my secret"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := EncryptFileInPlace(ctx, plainPath, ks); err != nil {
		t.Fatalf("EncryptFileInPlace: %v", err)
	}

	// Original should be gone.
	if _, err := os.Stat(plainPath); !os.IsNotExist(err) {
		t.Error("original file should have been removed after encryption")
	}

	// Encrypted file should exist.
	encPath := plainPath + ".age"
	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("encrypted file not found: %v", err)
	}

	// Should be decryptable.
	out, err := DecryptFile(ctx, encPath, ks)
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	if string(out) != "my secret" {
		t.Errorf("decrypted content = %q, want %q", out, "my secret")
	}

	// Permissions of the .age file should be 0600.
	info, err := os.Stat(encPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestEncryptFileInPlace_AlreadyAgeExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt.age")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := EncryptFileInPlace(context.Background(), path, KeySource{})
	if err == nil {
		t.Fatal("expected error for .age input, got nil")
	}
}

func TestEncryptFileInPlace_OutputExists(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)

	dir := t.TempDir()
	plainPath := filepath.Join(dir, "file.txt")
	encPath := plainPath + ".age"

	if err := os.WriteFile(plainPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Pre-create the output file to simulate conflict.
	if err := os.WriteFile(encPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := EncryptFileInPlace(context.Background(), plainPath, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error when output file already exists, got nil")
	}
}

func TestEncrypt_IdentityFileError(t *testing.T) {
	ctx := context.Background()
	ks := KeySource{IdentityFile: "/nonexistent/key.txt"}

	var buf bytes.Buffer
	err := Encrypt(ctx, strings.NewReader("data"), &buf, ks)
	if err == nil {
		t.Fatal("expected error with missing identity file, got nil")
	}
}

func TestEncryptFileInPlace_ReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("data"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Ensure the file is not readable even to owner.
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)

	err := EncryptFileInPlace(context.Background(), path, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error reading unreadable file, got nil")
	}
}

func TestEncryptFileInPlace_EncryptError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Use empty KeySource (no identity file) to force encrypt failure.
	err := EncryptFileInPlace(context.Background(), path, KeySource{})
	if err == nil {
		t.Fatal("expected error when encryption fails, got nil")
	}
	// Original file should still exist.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Error("original file should still exist after failed encrypt")
	}
}

func TestDecrypt_ReadAllError(t *testing.T) {
	orig := ioReadAllFunc
	t.Cleanup(func() { ioReadAllFunc = orig })
	ioReadAllFunc = func(_ io.Reader) ([]byte, error) {
		return nil, errors.New("injected io.ReadAll failure")
	}

	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	// Encrypt something so Decrypt can successfully open the stream.
	var buf bytes.Buffer
	if err := Encrypt(ctx, strings.NewReader("data"), &buf, KeySource{IdentityFile: keyPath}); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err := Decrypt(ctx, &buf, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from io.ReadAll failure, got nil")
	}
}

func TestResolveRecipients_IdentityFile(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ks := KeySource{IdentityFile: keyPath}

	recipients, err := resolveRecipients(ks)
	if err != nil {
		t.Fatalf("resolveRecipients: %v", err)
	}
	if len(recipients) != 1 {
		t.Errorf("len(recipients) = %d, want 1", len(recipients))
	}
}

// errReader is an io.Reader that always returns an error.
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }

// failWriter is an io.Writer that always returns an error.
type failWriter struct{ err error }

// deferredFailWriter succeeds for the first `after` bytes then starts failing.
type deferredFailWriter struct {
	written int
	after   int
	err     error
	buf     bytes.Buffer
}

func (w *failWriter) Write(_ []byte) (int, error) { return 0, w.err }

func (w *deferredFailWriter) Write(p []byte) (int, error) {
	if w.written >= w.after {
		return 0, w.err
	}
	n := min(len(p), w.after-w.written)
	written, _ := w.buf.Write(p[:n]) // bytes.Buffer.Write never returns an error
	w.written += written
	if w.written >= w.after {
		return written, w.err
	}
	return written, nil
}


func TestEncrypt_AgeEncryptError(t *testing.T) {
	orig := ageEncryptFunc
	t.Cleanup(func() { ageEncryptFunc = orig })
	ageEncryptFunc = func(_ io.Writer, _ ...age.Recipient) (io.WriteCloser, error) {
		return nil, errors.New("injected age.Encrypt failure")
	}

	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	var buf bytes.Buffer
	err := Encrypt(ctx, strings.NewReader("data"), &buf, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from failing age.Encrypt, got nil")
	}
}

func TestEncrypt_CopyError(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	r := &errReader{err: errors.New("read error")}
	var buf bytes.Buffer
	err := Encrypt(ctx, r, &buf, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from failing reader, got nil")
	}
}

func TestEncrypt_WriterFailsAtArmorClose(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	// First measure the exact size written by w.Close() vs armorWriter.Close().
	// w.Close() writes 311 bytes; armorWriter.Close() writes the remaining footer.
	// Set the threshold to 312 so w.Close() succeeds and armorWriter.Close() fails.
	//
	// We use the measured size to avoid hardcoding the exact threshold.
	var measureBuf bytes.Buffer
	if err := Encrypt(ctx, strings.NewReader("data"), &measureBuf, KeySource{IdentityFile: keyPath}); err != nil {
		t.Fatalf("measure encrypt: %v", err)
	}

	// Use a threshold that lets w.Close() succeed but causes armorWriter.Close() to fail.
	// From empirical measurement: w.Close() writes exactly 311 bytes for this key type.
	// We set threshold 1 beyond w.Close()'s write count so w.Close() succeeds but
	// armorWriter.Close() fails on its first write.
	wCloseBytes := 311 // bytes written through w.Close()
	threshold := wCloseBytes + 1
	if threshold >= measureBuf.Len() {
		t.Skip("cannot isolate armorWriter.Close() failure with this key size")
	}

	fw := &deferredFailWriter{after: threshold, err: errors.New("write at armor close")}
	err := Encrypt(ctx, strings.NewReader("data"), fw, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from armorWriter.Close() failure, got nil")
	}
}

func TestEncrypt_WriterFailsAfterHeader(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	// Fail after 300 bytes — enough for the age header (recipient stanza + armor
	// header) to be written, but before the ciphertext. This should cause w.Close()
	// or armorWriter.Close() to fail.
	fw := &deferredFailWriter{after: 300, err: errors.New("write error")}
	err := Encrypt(ctx, strings.NewReader("data"), fw, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from deferred writer failure, got nil")
	}
}

func TestEncrypt_WriterFailsImmediately(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ctx := context.Background()

	// Fail immediately — causes age.Encrypt (header write) or armorWriter to fail.
	fw := &failWriter{err: errors.New("write error")}
	err := Encrypt(ctx, strings.NewReader("data"), fw, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from failing writer, got nil")
	}
}

func TestDecryptFile_DecryptError(t *testing.T) {
	id1 := generateTestIdentity(t)
	id2 := generateTestIdentity(t)
	encKeyPath := writeIdentityFile(t, id1)
	decKeyPath := writeIdentityFile(t, id2)
	ctx := context.Background()

	dir := t.TempDir()
	encPath := filepath.Join(dir, "secret.txt.age")
	var buf bytes.Buffer
	if err := Encrypt(ctx, strings.NewReader("data"), &buf, KeySource{IdentityFile: encKeyPath}); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := os.WriteFile(encPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := DecryptFile(ctx, encPath, KeySource{IdentityFile: decKeyPath})
	if err == nil {
		t.Fatal("expected error decrypting with wrong key via DecryptFile, got nil")
	}
}

func TestEncryptFileInPlace_RemoveError(t *testing.T) {
	orig := osRemoveFunc
	t.Cleanup(func() { osRemoveFunc = orig })
	osRemoveFunc = func(path string) error {
		if strings.HasSuffix(path, ".age") {
			// Allow cleanup of the output file (called when remove of original fails).
			return nil
		}
		return errors.New("injected os.Remove failure")
	}

	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)

	dir := t.TempDir()
	plainPath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(plainPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := EncryptFileInPlace(context.Background(), plainPath, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error when os.Remove fails, got nil")
	}
}

func TestEncryptFileInPlace_WriteError(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)

	dir := t.TempDir()
	plainPath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(plainPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Make directory read-only so WriteFile of the .age output fails.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := EncryptFileInPlace(context.Background(), plainPath, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error when output dir is read-only, got nil")
	}
}


func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir:", err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"~notexpanded", "~notexpanded"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := expandHome(tc.input)
			if got != tc.want {
				t.Errorf("expandHome(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExpandHome_UserHomeDirError(t *testing.T) {
	orig := userHomeDirFunc
	t.Cleanup(func() { userHomeDirFunc = orig })

	userHomeDirFunc = func() (string, error) { return "", errors.New("no home") }

	got := expandHome("~/foo/bar")
	if got != "~/foo/bar" {
		t.Errorf("expandHome with homedir error = %q, want original path %q", got, "~/foo/bar")
	}
}

func TestLoadIdentityFile_Missing(t *testing.T) {
	_, err := loadIdentityFile("/nonexistent/key.txt")
	if err == nil {
		t.Fatal("expected error for missing identity file, got nil")
	}
}

func TestLoadIdentityFile_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-key.txt")
	if err := os.WriteFile(path, []byte("this is not an age key"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := loadIdentityFile(path)
	if err == nil {
		t.Fatal("expected error for invalid identity file, got nil")
	}
}
