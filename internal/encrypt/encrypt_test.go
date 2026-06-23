package encrypt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
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

// encryptForTest produces armored age ciphertext for the recipient derived from
// id. The encrypt code path was removed from the package, so tests roll their
// own ciphertext to exercise decryption.
func encryptForTest(t *testing.T, id *age.X25519Identity, plaintext string) []byte {
	t.Helper()
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, id.Recipient())
	if err != nil {
		t.Fatalf("age.Encrypt: %v", err)
	}
	if _, err = io.WriteString(w, plaintext); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close age writer: %v", err)
	}
	if err = aw.Close(); err != nil {
		t.Fatalf("close armor writer: %v", err)
	}
	return buf.Bytes()
}

func TestDecrypt_IdentityFile(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ks := KeySource{IdentityFile: keyPath}

	const plaintext = "secret dotfile content\nline two\n"
	ct := encryptForTest(t, id, plaintext)
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), ks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != plaintext {
		t.Errorf("Decrypt = %q, want %q", out, plaintext)
	}
}

func TestDecrypt_EmptyPlaintext(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ks := KeySource{IdentityFile: keyPath}

	ct := encryptForTest(t, id, "")
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), ks)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Decrypt = %q, want empty", out)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	id1 := generateTestIdentity(t)
	id2 := generateTestIdentity(t)
	decKeyPath := writeIdentityFile(t, id2)

	ct := encryptForTest(t, id1, "secret")
	_, err := Decrypt(context.Background(), bytes.NewReader(ct), KeySource{IdentityFile: decKeyPath})
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

func TestDecrypt_CorruptCiphertext(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)

	corrupt := bytes.NewReader([]byte("this is not valid age ciphertext"))
	_, err := Decrypt(context.Background(), corrupt, KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error decrypting corrupt ciphertext, got nil")
	}
}

func TestDecrypt_NoIdentityFile(t *testing.T) {
	ct := encryptForTest(t, generateTestIdentity(t), "data")
	_, err := Decrypt(context.Background(), bytes.NewReader(ct), KeySource{})
	if err == nil {
		t.Fatal("expected error with no identity file configured, got nil")
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
	ct := encryptForTest(t, id, "data")

	_, err := Decrypt(context.Background(), bytes.NewReader(ct), KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error from io.ReadAll failure, got nil")
	}
}

func TestDecryptFile(t *testing.T) {
	id := generateTestIdentity(t)
	keyPath := writeIdentityFile(t, id)
	ks := KeySource{IdentityFile: keyPath}

	dir := t.TempDir()
	encPath := filepath.Join(dir, "secret.txt.age")
	if err := os.WriteFile(encPath, encryptForTest(t, id, "file contents"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := DecryptFile(context.Background(), encPath, ks)
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

	_, err := DecryptFile(context.Background(), "/nonexistent/path/secret.age", KeySource{IdentityFile: keyPath})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestDecryptFile_DecryptError(t *testing.T) {
	id1 := generateTestIdentity(t)
	id2 := generateTestIdentity(t)
	decKeyPath := writeIdentityFile(t, id2)

	dir := t.TempDir()
	encPath := filepath.Join(dir, "secret.txt.age")
	if err := os.WriteFile(encPath, encryptForTest(t, id1, "data"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := DecryptFile(context.Background(), encPath, KeySource{IdentityFile: decKeyPath})
	if err == nil {
		t.Fatal("expected error decrypting with wrong key via DecryptFile, got nil")
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
