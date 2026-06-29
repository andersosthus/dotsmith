package encrypt

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"golang.org/x/crypto/ssh"
)

// ---- fixtures ---------------------------------------------------------------

// fakePrompter is an injected Prompter that records calls and returns a fixed
// passphrase (or error). It never touches a real terminal.
type fakePrompter struct {
	interactive bool
	passphrase  []byte
	err         error
	calls       int
	// labels records the keyLabel passed to each Prompt call, so tests can assert
	// the prompt named the matched key.
	labels []string
}

func (p *fakePrompter) Interactive() bool { return p.interactive }

func (p *fakePrompter) Prompt(label string) ([]byte, error) {
	p.calls++
	p.labels = append(p.labels, label)
	if p.err != nil {
		return nil, p.err
	}
	return p.passphrase, nil
}

// seqPrompter returns a different passphrase on each call, in order, so tests can
// exercise the wrong-then-right retry path. It is always interactive.
type seqPrompter struct {
	passphrases [][]byte
	calls       int
}

func (p *seqPrompter) Interactive() bool { return true }

func (p *seqPrompter) Prompt(_ string) ([]byte, error) {
	idx := p.calls
	p.calls++
	if idx >= len(p.passphrases) {
		return p.passphrases[len(p.passphrases)-1], nil
	}
	return p.passphrases[idx], nil
}

// generateAgeIdentity returns a fresh native age identity.
func generateAgeIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	return id
}

// writeAgeKeyFile writes a native age identity file and returns its path.
func writeAgeKeyFile(t *testing.T, dir string, id *age.X25519Identity) string {
	t.Helper()
	path := filepath.Join(dir, "age-key.txt")
	content := "# created by dotsmith test\n" + id.String() + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write age key: %v", err)
	}
	return path
}

// writeEd25519Key generates an ssh-ed25519 key pair, writing the private key
// (optionally passphrase-protected) to name in dir. Returns the private key path
// and the ssh.PublicKey.
func writeEd25519Key(t *testing.T, dir, name string, passphrase []byte) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 GenerateKey: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	path := writePrivateKey(t, dir, name, priv, passphrase)
	return path, sshPub
}

// writeRSAKey generates an ssh-rsa key of the given bit size, writing it to name
// in dir (optionally passphrase-protected). Returns the private key path and the
// ssh.PublicKey.
func writeRSAKey(t *testing.T, dir, name string, bits int, passphrase []byte) (string, ssh.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("rsa GenerateKey: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	path := writePrivateKey(t, dir, name, key, passphrase)
	return path, sshPub
}

// writePrivateKey marshals key to an OpenSSH PEM file at dir/name.
func writePrivateKey(t *testing.T, dir, name string, key any, passphrase []byte) string {
	t.Helper()
	var block *pem.Block
	var err error
	if len(passphrase) > 0 {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "dotsmith-test", passphrase)
	} else {
		block, err = ssh.MarshalPrivateKey(key, "dotsmith-test")
	}
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	path := filepath.Join(dir, name)
	if err = os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return path
}

// encryptToRecipients armors plaintext encrypted to the given recipients.
func encryptToRecipients(t *testing.T, plaintext string, recipients ...age.Recipient) []byte {
	t.Helper()
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, recipients...)
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

// encryptBinaryToRecipients mirrors encryptToRecipients but omits the armor
// writer, producing age's default binary encoding (intro line
// "age-encryption.org/v1"). It is the binary counterpart of the existing armor
// fixture helper, making file encoding a first-class test axis.
func encryptBinaryToRecipients(t *testing.T, plaintext string, recipients ...age.Recipient) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		t.Fatalf("age.Encrypt: %v", err)
	}
	if _, err = io.WriteString(w, plaintext); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close age writer: %v", err)
	}
	return buf.Bytes()
}

// sshRecipient builds an age recipient from an SSH public key.
func sshRecipient(t *testing.T, pub ssh.PublicKey) age.Recipient {
	t.Helper()
	rec, err := agessh.NewEd25519Recipient(pub)
	if err == nil {
		return rec
	}
	rrec, rerr := agessh.NewRSARecipient(pub)
	if rerr != nil {
		t.Fatalf("build ssh recipient: ed25519 err=%v rsa err=%v", err, rerr)
	}
	return rrec
}

// withSSHDir points the discovery seams at dir for the duration of the test.
func withSSHDir(t *testing.T, dir string) {
	t.Helper()
	origDir, origList := sshDirFunc, listDirFunc
	t.Cleanup(func() { sshDirFunc, listDirFunc = origDir, origList })
	sshDirFunc = func() (string, error) { return dir, nil }
	listDirFunc = func(d string) ([]os.DirEntry, error) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return nil, err //nolint:wrapcheck // test seam
		}
		return entries, nil
	}
}

// ---- Resolve: native identities --------------------------------------------

func TestResolve_NativeOnly(t *testing.T) {
	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)

	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1", len(set.identities))
	}

	ct := encryptToRecipients(t, "native secret", id.Recipient())
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != "native secret" {
		t.Errorf("Decrypt = %q", out)
	}
}

func TestResolve_MissingDefaultNativeTolerated(t *testing.T) {
	// Default (non-explicit) native key absent → silently skipped, empty set.
	set, err := Resolve(context.Background(), KeySource{
		IdentityFile:         filepath.Join(t.TempDir(), "does-not-exist"),
		IdentityFileExplicit: false,
	}, nil)
	if err != nil {
		t.Fatalf("Resolve should tolerate missing default key: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0", len(set.identities))
	}
}

func TestResolve_MissingExplicitNativeIsError(t *testing.T) {
	_, err := Resolve(context.Background(), KeySource{
		IdentityFile:         filepath.Join(t.TempDir(), "does-not-exist"),
		IdentityFileExplicit: true,
	}, nil)
	if err == nil {
		t.Fatal("expected hard error for missing explicit native key, got nil")
	}
}

func TestResolve_InvalidNativeKeyIsError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad-key")
	if err := os.WriteFile(bad, []byte("not an age key"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Resolve(context.Background(), KeySource{IdentityFile: bad, IdentityFileExplicit: true}, nil)
	if err == nil {
		t.Fatal("expected error for invalid native key, got nil")
	}
}

func TestResolve_EmptyIdentityFileSkipped(t *testing.T) {
	set, err := Resolve(context.Background(), KeySource{IdentityFile: ""}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0", len(set.identities))
	}
}

// ---- Resolve: explicit identities ------------------------------------------

func TestResolve_ExplicitIdentities_NativeAndSSH(t *testing.T) {
	dir := t.TempDir()
	ageID := generateAgeIdentity(t)
	agePath := writeAgeKeyFile(t, dir, ageID)
	sshPath, sshPub := writeEd25519Key(t, dir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{
		Identities: []string{agePath, sshPath},
	}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 2 {
		t.Fatalf("got %d identities, want 2", len(set.identities))
	}

	// File encrypted to the SSH recipient opens via the explicit SSH identity.
	ct := encryptToRecipients(t, "ssh secret", sshRecipient(t, sshPub))
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != "ssh secret" {
		t.Errorf("Decrypt = %q", out)
	}
}

func TestResolve_ExplicitMissingIsError(t *testing.T) {
	_, err := Resolve(context.Background(), KeySource{
		Identities: []string{filepath.Join(t.TempDir(), "nope")},
	}, nil)
	if err == nil {
		t.Fatal("expected hard error for missing explicit identity, got nil")
	}
}

func TestResolve_ExplicitUnparseableIsError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("garbage that is neither age nor ssh"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Resolve(context.Background(), KeySource{Identities: []string{bad}}, nil)
	if err == nil {
		t.Fatal("expected error for unparseable explicit identity, got nil")
	}
}

// ---- Resolve: SSH discovery -------------------------------------------------

func TestResolve_SSHDiscovery_Ed25519(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1", len(set.identities))
	}

	ct := encryptToRecipients(t, "discovered", sshRecipient(t, pub))
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != "discovered" {
		t.Errorf("Decrypt = %q", out)
	}
}

func TestResolve_SSHDiscovery_RSA(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeRSAKey(t, sshDir, "id_rsa", 2048, nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1", len(set.identities))
	}

	ct := encryptToRecipients(t, "rsa secret", sshRecipient(t, pub))
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != "rsa secret" {
		t.Errorf("Decrypt = %q", out)
	}
}

func TestResolve_SSHDiscovery_MultiRecipientPicksMatch(t *testing.T) {
	// File encrypted to two SSH recipients; this machine only holds the second.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	otherDir := t.TempDir()
	_, otherPub := writeEd25519Key(t, otherDir, "other", nil)
	_, myPub := writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "multi", sshRecipient(t, otherPub), sshRecipient(t, myPub))
	out, err := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(out) != "multi" {
		t.Errorf("Decrypt = %q", out)
	}
}

func TestResolve_SSHDiscovery_Disabled(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: false}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("discovery disabled but got %d identities", len(set.identities))
	}
}

func TestResolve_SSHDiscovery_Denylist(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)

	// Denylisted files must never be parsed as candidate keys.
	for _, name := range []string{"config", "authorized_keys", "known_hosts", "known_hosts.old"} {
		if err := os.WriteFile(filepath.Join(sshDir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// A .pub sibling must be excluded too.
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", nil)
	pubBytes := ssh.MarshalAuthorizedKey(pub)
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), pubBytes, 0o600); err != nil {
		t.Fatalf("write .pub: %v", err)
	}

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Only the real private key should be added (not config/.pub/etc).
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1 (denylist + .pub excluded)", len(set.identities))
	}
	if set.candidates[0].kind != typeSSHEd25519 {
		t.Errorf("candidate kind = %q, want ssh-ed25519", set.candidates[0].kind)
	}
}

func TestResolve_SSHDiscovery_SkipsDirAndSocketAndUnsupported(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)

	// Subdirectory: skipped.
	if err := os.Mkdir(filepath.Join(sshDir, "sub"), 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// Unix socket: skipped (not a regular file).
	sockPath := filepath.Join(sshDir, "agent.sock")
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Skipf("cannot create unix socket: %v", err)
	}
	defer l.Close() //nolint:errcheck // best-effort
	// Undersized RSA: agessh rejects it → skipped silently.
	writeRSAKey(t, sshDir, "id_rsa_small", 1024, nil)
	// A usable key so the set is non-empty.
	writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1 (only the ed25519 key is usable)", len(set.identities))
	}
}

func TestResolve_SSHDiscovery_DirMissing(t *testing.T) {
	// sshDirFunc errors → discovery is skipped, no error.
	origDir, origList := sshDirFunc, listDirFunc
	t.Cleanup(func() { sshDirFunc, listDirFunc = origDir, origList })
	sshDirFunc = func() (string, error) { return "", errors.New("no home") }

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0", len(set.identities))
	}
}

func TestResolve_SSHDiscovery_ListError(t *testing.T) {
	origDir, origList := sshDirFunc, listDirFunc
	t.Cleanup(func() { sshDirFunc, listDirFunc = origDir, origList })
	sshDirFunc = func() (string, error) { return "/no/such/dir", nil }
	listDirFunc = func(_ string) ([]os.DirEntry, error) { return nil, errors.New("read error") }

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0", len(set.identities))
	}
}

func TestResolve_SSHDiscovery_ReadFileError(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeEd25519Key(t, sshDir, "id_ed25519", nil)

	origRead := readKeyFileFunc
	t.Cleanup(func() { readKeyFileFunc = origRead })
	readKeyFileFunc = func(_ string) ([]byte, error) { return nil, errors.New("read denied") }

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0", len(set.identities))
	}
}

// ---- Resolve: encrypted SSH keys (lazy unlock) ------------------------------

func TestResolve_EncryptedSSHKey_UnlockOnce(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	const pass = "hunter2"
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte(pass))

	prompter := &fakePrompter{interactive: true, passphrase: []byte(pass)}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1", len(set.identities))
	}
	// Building the set must not have prompted (lazy).
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times during resolve, want 0 (lazy)", prompter.calls)
	}

	// Decrypt two files; the key unlocks and is cached so the passphrase is
	// requested once.
	rec := sshRecipient(t, pub)
	for i := 0; i < 2; i++ {
		ct := encryptToRecipients(t, "encsecret", rec)
		out, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
		if derr != nil {
			t.Fatalf("Decrypt #%d: %v", i, derr)
		}
		if string(out) != "encsecret" {
			t.Errorf("Decrypt #%d = %q", i, out)
		}
	}
	if prompter.calls != 1 {
		t.Errorf("prompter called %d times, want exactly 1 (unlock once per run)", prompter.calls)
	}
}

func TestDecryptFile_EncryptedSSHKey_PromptNoticeNamesSource(t *testing.T) {
	// When a passphrase prompt fires for a discovered SSH key, DecryptFile must
	// print a one-line notice naming the .age file that triggered it, so a
	// repo-induced prompt is explainable (security finding #1).
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	const pass = "hunter2"
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte(pass))

	var notice bytes.Buffer
	prev := promptNoticeWriter
	promptNoticeWriter = &notice
	t.Cleanup(func() { promptNoticeWriter = prev })

	prompter := &fakePrompter{interactive: true, passphrase: []byte(pass)}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "secret", sshRecipient(t, pub))
	agePath := filepath.Join(t.TempDir(), "gitconfig.age")
	if werr := os.WriteFile(agePath, ct, 0o600); werr != nil {
		t.Fatalf("write age file: %v", werr)
	}

	out, derr := DecryptFile(context.Background(), agePath, set)
	if derr != nil {
		t.Fatalf("DecryptFile: %v", derr)
	}
	if string(out) != "secret" {
		t.Errorf("DecryptFile = %q, want %q", out, "secret")
	}
	if got := notice.String(); !strings.Contains(got, agePath) ||
		!strings.Contains(got, filepath.Join(sshDir, "id_ed25519")) {
		t.Errorf("notice %q should name both the source file %q and the SSH key", got, agePath)
	}
}

func TestDecryptFile_EncryptedSSHKey_PromptNoticeSanitizesSource(t *testing.T) {
	// The .age basename is repo-controlled and may embed terminal control
	// sequences. The prompt notice must render it with %q so escapes, CRs, and
	// newlines cannot spoof or manipulate the terminal right before the genuine
	// passphrase prompt (security finding run-5 / #42).
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	const pass = "hunter2"
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte(pass))

	var notice bytes.Buffer
	prev := promptNoticeWriter
	promptNoticeWriter = &notice
	t.Cleanup(func() { promptNoticeWriter = prev })

	prompter := &fakePrompter{interactive: true, passphrase: []byte(pass)}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "secret", sshRecipient(t, pub))
	// Basename embeds an ANSI clear-line, a CR, a forged prompt, and a newline.
	evilName := "a\x1b[2K\rEnter passphrase for x: \n.age"
	agePath := filepath.Join(t.TempDir(), evilName)
	if werr := os.WriteFile(agePath, ct, 0o600); werr != nil {
		t.Fatalf("write age file: %v", werr)
	}

	if _, derr := DecryptFile(context.Background(), agePath, set); derr != nil {
		t.Fatalf("DecryptFile: %v", derr)
	}
	got := notice.String()
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("prompt notice leaks raw control characters: %q", got)
	}
	// A single notice line: an injected newline would have split it.
	if c := strings.Count(got, "\n"); c != 1 {
		t.Errorf("prompt notice has %d lines, want 1: %q", c, got)
	}
	// The escaped (%q) form of the path is what should be emitted.
	if !strings.Contains(got, strconv.Quote(agePath)) {
		t.Errorf("prompt notice should contain the %%q-rendered path %q, got %q",
			strconv.Quote(agePath), got)
	}
}

func TestDecrypt_EncryptedSSHKey_StreamPromptNoticeOmitsFile(t *testing.T) {
	// Decrypt operates on an unnamed stream, so the prompt notice must not claim a
	// source filename (it stays silent rather than printing a stale or empty one).
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	const pass = "hunter2"
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte(pass))

	var notice bytes.Buffer
	prev := promptNoticeWriter
	promptNoticeWriter = &notice
	t.Cleanup(func() { promptNoticeWriter = prev })

	prompter := &fakePrompter{interactive: true, passphrase: []byte(pass)}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "secret", sshRecipient(t, pub))
	if _, derr := Decrypt(context.Background(), bytes.NewReader(ct), set); derr != nil {
		t.Fatalf("Decrypt: %v", derr)
	}
	if notice.Len() != 0 {
		t.Errorf("stream decrypt printed a source-naming notice %q, want none", notice.String())
	}
}

func TestResolve_EncryptedUndersizedRSA_Skipped(t *testing.T) {
	// An encrypted, undersized RSA key takes the PassphraseMissingError path. Its
	// public key is recoverable (OpenSSH format embeds it), but age could never
	// match a recipient to a sub-2048-bit key, so it must be skipped silently —
	// and crucially without ever prompting for the passphrase.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeRSAKey(t, sshDir, "id_rsa", 1024, []byte("pw"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Fatalf("got %d identities, want 0 (encrypted undersized RSA skipped)", len(set.identities))
	}
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times, want 0 (undersized key skipped before any prompt)", prompter.calls)
	}
}

func TestResolve_EncryptedSSHKey_NoPrompterSkipped(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeEd25519Key(t, sshDir, "id_ed25519", []byte("pw"))

	// nil prompter → encrypted key is unusable and skipped.
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0 (encrypted key skipped without prompter)", len(set.identities))
	}
}

func TestResolve_EncryptedSSHKey_WrongPassphrase(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("correct"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("wrong")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "x", sshRecipient(t, pub))
	if _, derr := Decrypt(context.Background(), bytes.NewReader(ct), set); derr == nil {
		t.Fatal("expected decrypt error with wrong passphrase, got nil")
	}
}

func TestResolve_EncryptedSSHKey_PromptError(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("correct"))

	prompter := &fakePrompter{interactive: false, err: errors.New("no tty")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "x", sshRecipient(t, pub))
	if _, derr := Decrypt(context.Background(), bytes.NewReader(ct), set); derr == nil {
		t.Fatal("expected decrypt error when prompter fails, got nil")
	}
}

func TestResolve_EncryptedSSHKey_InteractivePromptError(t *testing.T) {
	// A terminal is present but reading the passphrase fails (e.g. read error).
	// The error must propagate immediately, naming the key, without retrying.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("pw"))

	prompter := &fakePrompter{interactive: true, err: errors.New("read failure")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "x", sshRecipient(t, pub))
	_, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr == nil {
		t.Fatal("expected error when interactive prompt fails, got nil")
	}
	if prompter.calls != 1 {
		t.Errorf("prompter called %d times, want 1 (prompt error is terminal, no retry)", prompter.calls)
	}
	if !bytes.Contains([]byte(derr.Error()), []byte(keyPath)) {
		t.Errorf("error %q should name the key", derr)
	}
}

func TestResolve_EncryptedSSHKey_NonMatchNeverPrompts(t *testing.T) {
	// An encrypted key in the set whose tag does not match the file must return
	// age.ErrIncorrectIdentity without prompting. With no other matching identity
	// the decrypt fails with a no-match error, and the encrypted key is never
	// unlocked. This exercises the non-match branch of the retrying wrapper.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeEd25519Key(t, sshDir, "id_ed25519", []byte("pw"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// File encrypted to a different SSH key the machine does not hold: the
	// encrypted candidate's tag does not match, so it must not prompt.
	otherDir := t.TempDir()
	_, otherPub := writeEd25519Key(t, otherDir, "other", nil)
	ct := encryptToRecipients(t, "x", sshRecipient(t, otherPub))
	_, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr == nil {
		t.Fatal("expected no-match error, got nil")
	}
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times, want 0 (encrypted key did not match)", prompter.calls)
	}
}

func TestResolve_EncryptedSSHKey_WrongThenRight(t *testing.T) {
	// A wrong passphrase is retried; the correct one on the second attempt
	// unlocks the key (≤3 attempts).
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	const pass = "correct-horse"
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte(pass))

	prompter := &seqPrompter{passphrases: [][]byte{[]byte("wrong"), []byte(pass)}}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "retry secret", sshRecipient(t, pub))
	out, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr != nil {
		t.Fatalf("Decrypt after retry: %v", derr)
	}
	if string(out) != "retry secret" {
		t.Errorf("Decrypt = %q, want %q", out, "retry secret")
	}
	if prompter.calls != 2 {
		t.Errorf("prompter called %d times, want 2 (one wrong, one right)", prompter.calls)
	}
}

func TestResolve_EncryptedSSHKey_ExhaustsAttempts(t *testing.T) {
	// Three wrong passphrases exhaust the attempt budget and fail with a clear
	// error; the prompter is asked exactly maxPassphraseAttempts times.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("correct"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("always-wrong")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "x", sshRecipient(t, pub))
	_, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr == nil {
		t.Fatal("expected error after exhausting passphrase attempts, got nil")
	}
	if prompter.calls != maxPassphraseAttempts {
		t.Errorf("prompter called %d times, want %d", prompter.calls, maxPassphraseAttempts)
	}
	if !bytes.Contains([]byte(derr.Error()), []byte("attempts")) {
		t.Errorf("error %q should mention exhausted attempts", derr)
	}
}

func TestResolve_EncryptedSSHKey_PromptsOnlyMatchedKey(t *testing.T) {
	// Two encrypted keys in the set; only the key that matches the file is ever
	// prompted for. The non-matching key must not prompt.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	matchedPath, matchedPub := writeEd25519Key(t, sshDir, "id_matched", []byte("pw-matched"))
	writeEd25519Key(t, sshDir, "id_other", []byte("pw-other"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw-matched")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 2 {
		t.Fatalf("got %d identities, want 2", len(set.identities))
	}

	ct := encryptToRecipients(t, "matched", sshRecipient(t, matchedPub))
	out, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr != nil {
		t.Fatalf("Decrypt: %v", derr)
	}
	if string(out) != "matched" {
		t.Errorf("Decrypt = %q", out)
	}
	if prompter.calls != 1 {
		t.Errorf("prompter called %d times, want 1 (only matched key prompts)", prompter.calls)
	}
	for _, label := range prompter.labels {
		if label != matchedPath {
			t.Errorf("prompted for %q, want only matched key %q", label, matchedPath)
		}
	}
}

func TestResolve_EncryptedSSHKey_NoTTYHardError(t *testing.T) {
	// A matched encrypted key with no terminal must fail with a hard error naming
	// the file and the key — never prompting and never hanging.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("pw"))

	prompter := &fakePrompter{interactive: false, passphrase: []byte("pw")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encPath := filepath.Join(t.TempDir(), "secret.age")
	if werr := os.WriteFile(encPath, encryptToRecipients(t, "x", sshRecipient(t, pub)), 0o600); werr != nil {
		t.Fatalf("WriteFile: %v", werr)
	}

	_, derr := DecryptFile(context.Background(), encPath, set)
	if derr == nil {
		t.Fatal("expected no-TTY hard error, got nil")
	}
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times with no TTY, want 0", prompter.calls)
	}
	if !bytes.Contains([]byte(derr.Error()), []byte(encPath)) {
		t.Errorf("error %q should name the file", derr)
	}
	if !bytes.Contains([]byte(derr.Error()), []byte(keyPath)) {
		t.Errorf("error %q should name the key", derr)
	}
	if !bytes.Contains([]byte(derr.Error()), []byte("no terminal")) {
		t.Errorf("error %q should explain no terminal is available", derr)
	}
}

func TestResolve_UnencryptedKey_DecryptsSilentlyNoTTY(t *testing.T) {
	// The non-interactive (git-hook) path: an unencrypted matched key decrypts
	// with no terminal and no prompt.
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", nil)

	prompter := &fakePrompter{interactive: false}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "silent", sshRecipient(t, pub))
	out, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr != nil {
		t.Fatalf("Decrypt (no TTY, unencrypted): %v", derr)
	}
	if string(out) != "silent" {
		t.Errorf("Decrypt = %q", out)
	}
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times for unencrypted key, want 0", prompter.calls)
	}
}

func TestResolve_EncryptedRSA_OldPEM_SiblingPub(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)

	// Old-PEM (PKCS#1) encrypted RSA: the PassphraseMissingError carries no
	// public key, so dotsmith falls back to the sibling .pub file.
	const pass = "rsa-pass"
	keyPath, sshPub := writeOldPEMEncryptedRSA(t, sshDir, "id_rsa", pass)
	if werr := os.WriteFile(keyPath+".pub", ssh.MarshalAuthorizedKey(sshPub), 0o600); werr != nil {
		t.Fatalf("write pub: %v", werr)
	}

	prompter := &fakePrompter{interactive: true, passphrase: []byte(pass)}
	set, rerr := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	if len(set.identities) != 1 {
		t.Fatalf("got %d identities, want 1 (old-PEM RSA via sibling .pub)", len(set.identities))
	}

	ct := encryptToRecipients(t, "old-rsa", sshRecipient(t, sshPub))
	out, derr := Decrypt(context.Background(), bytes.NewReader(ct), set)
	if derr != nil {
		t.Fatalf("Decrypt: %v", derr)
	}
	if string(out) != "old-rsa" {
		t.Errorf("Decrypt = %q", out)
	}
}

func TestResolve_EncryptedRSA_OldPEM_NoSiblingSkipped(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeOldPEMEncryptedRSA(t, sshDir, "id_rsa", "pw") // no sibling .pub written

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw")}
	set, rerr := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, prompter)
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0 (no sibling .pub → skipped)", len(set.identities))
	}
}

// TestDecrypt_RoundTrip proves the streaming Decrypt path decrypts to the
// correct plaintext for both age encodings: armored (age -a) and binary (the age
// CLI default). Parametrizing across encodings makes the binary path — which the
// stream path previously could not handle because it hardcoded the armor reader —
// a first-class case rather than an untested dimension.
func TestDecrypt_RoundTrip(t *testing.T) {
	encodings := []struct {
		name    string
		encrypt func(*testing.T, string, ...age.Recipient) []byte
	}{
		{name: "armor", encrypt: encryptToRecipients},
		{name: "binary", encrypt: encryptBinaryToRecipients},
	}
	for _, enc := range encodings {
		t.Run(enc.name, func(t *testing.T) {
			dir := t.TempDir()
			id := generateAgeIdentity(t)
			keyPath := writeAgeKeyFile(t, dir, id)
			set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			ct := enc.encrypt(t, "stream contents", id.Recipient())
			out, err := Decrypt(context.Background(), bytes.NewReader(ct), set)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(out) != "stream contents" {
				t.Errorf("got %q, want %q", out, "stream contents")
			}
		})
	}
}

// ---- Decrypt errors ---------------------------------------------------------

func TestDecrypt_EmptySet(t *testing.T) {
	_, err := Decrypt(context.Background(), bytes.NewReader([]byte("x")), IdentitySet{})
	if err == nil {
		t.Fatal("expected guidance error for empty candidate set, got nil")
	}
}

func TestDecryptFile_EmptySet(t *testing.T) {
	_, err := DecryptFile(context.Background(), "/some/file.age", IdentitySet{})
	if err == nil {
		t.Fatal("expected guidance error for empty candidate set, got nil")
	}
}

func TestDecrypt_NoTagMatch(t *testing.T) {
	dir := t.TempDir()
	mine := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, mine)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	other := generateAgeIdentity(t)
	ct := encryptToRecipients(t, "secret", other.Recipient())
	_, err = Decrypt(context.Background(), bytes.NewReader(ct), set)
	if err == nil {
		t.Fatal("expected no-match error, got nil")
	}
	// Error must name the tried identity (path + type) and guide re-encryption.
	if !bytes.Contains([]byte(err.Error()), []byte(keyPath)) {
		t.Errorf("error %q does not name the tried identity path", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("[age]")) {
		t.Errorf("error %q does not include the identity type label", err)
	}
}

func TestDecrypt_CorruptCiphertext(t *testing.T) {
	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, err = Decrypt(context.Background(), bytes.NewReader([]byte("not valid age")), set)
	if err == nil {
		t.Fatal("expected error for corrupt ciphertext, got nil")
	}
}

func TestDecrypt_ReadAllError(t *testing.T) {
	orig := ioReadAllFunc
	t.Cleanup(func() { ioReadAllFunc = orig })
	ioReadAllFunc = func(_ io.Reader) ([]byte, error) { return nil, errors.New("injected read failure") }

	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ct := encryptToRecipients(t, "data", id.Recipient())
	if _, derr := Decrypt(context.Background(), bytes.NewReader(ct), set); derr == nil {
		t.Fatal("expected error from io.ReadAll failure, got nil")
	}
}

// ---- DecryptFile ------------------------------------------------------------

// TestIsArmored exercises the pure encoding predicate in isolation: the armor
// marker boundary, short/empty inputs, and a binary intro line — no ciphertext
// construction needed.
func TestIsArmored(t *testing.T) {
	tests := []struct {
		name    string
		leading []byte
		want    bool
	}{
		{name: "exact marker", leading: []byte(armor.Header), want: true},
		{name: "marker prefix of longer input", leading: []byte(armor.Header + "\nrest of armor envelope"), want: true},
		{name: "marker missing trailing dash", leading: []byte(armor.Header[:len(armor.Header)-1]), want: false},
		{name: "binary intro line", leading: []byte("age-encryption.org/v1\n"), want: false},
		{name: "empty input", leading: []byte{}, want: false},
		{name: "nil input", leading: nil, want: false},
		{name: "short read shorter than marker", leading: []byte("-----BEGIN"), want: false},
		{name: "leading whitespace before marker not tolerated", leading: []byte("\n" + armor.Header), want: false},
		{name: "garbage", leading: []byte("not an age file at all"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isArmored(tt.leading); got != tt.want {
				t.Errorf("isArmored(%q) = %v, want %v", tt.leading, got, tt.want)
			}
		})
	}
}

// TestDecryptFile_RoundTrip proves DecryptFile decrypts to the correct plaintext
// for both age encodings: armored (age -a) and binary (the age CLI default).
// Parametrizing across encodings makes the binary path — the one that was
// previously broken — a first-class case rather than an untested dimension.
func TestDecryptFile_RoundTrip(t *testing.T) {
	encodings := []struct {
		name    string
		encrypt func(*testing.T, string, ...age.Recipient) []byte
	}{
		{name: "armor", encrypt: encryptToRecipients},
		{name: "binary", encrypt: encryptBinaryToRecipients},
	}
	for _, enc := range encodings {
		t.Run(enc.name, func(t *testing.T) {
			dir := t.TempDir()
			id := generateAgeIdentity(t)
			keyPath := writeAgeKeyFile(t, dir, id)
			set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			encPath := filepath.Join(dir, "secret.txt.age")
			if werr := os.WriteFile(encPath, enc.encrypt(t, "file contents", id.Recipient()), 0o600); werr != nil {
				t.Fatalf("WriteFile: %v", werr)
			}

			out, err := DecryptFile(context.Background(), encPath, set)
			if err != nil {
				t.Fatalf("DecryptFile: %v", err)
			}
			if string(out) != "file contents" {
				t.Errorf("got %q", out)
			}
		})
	}
}

// TestDecryptFile_BinaryRegression reproduces the reported bug directly: a
// binary-format .age file (as produced by the default `age` CLI) decrypts
// cleanly through DecryptFile, where it previously failed with a misleading
// "invalid armor" error.
func TestDecryptFile_BinaryRegression(t *testing.T) {
	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ciphertext := encryptBinaryToRecipients(t, "authorized_keys body", id.Recipient())
	// Guard the fixture: it must be true binary, starting with age's intro line
	// rather than the armor marker, or the regression would not be exercised.
	if isArmored(ciphertext) {
		t.Fatal("fixture is armored; expected binary encoding")
	}

	encPath := filepath.Join(dir, "authorized_keys.age")
	if werr := os.WriteFile(encPath, ciphertext, 0o600); werr != nil {
		t.Fatalf("WriteFile: %v", werr)
	}

	out, err := DecryptFile(context.Background(), encPath, set)
	if err != nil {
		t.Fatalf("DecryptFile on binary .age file: %v", err)
	}
	if string(out) != "authorized_keys body" {
		t.Errorf("got %q, want %q", out, "authorized_keys body")
	}
}

func TestDecryptFile_MissingFile(t *testing.T) {
	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, derr := DecryptFile(context.Background(), "/nonexistent/secret.age", set); derr == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestDecryptFile_NoTagMatch(t *testing.T) {
	dir := t.TempDir()
	mine := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, mine)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	other := generateAgeIdentity(t)
	encPath := filepath.Join(dir, "secret.age")
	if werr := os.WriteFile(encPath, encryptToRecipients(t, "x", other.Recipient()), 0o600); werr != nil {
		t.Fatalf("WriteFile: %v", werr)
	}

	_, err = DecryptFile(context.Background(), encPath, set)
	if err == nil {
		t.Fatal("expected no-match error, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte(encPath)) {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestDecryptFile_ReadAllError(t *testing.T) {
	orig := ioReadAllFunc
	t.Cleanup(func() { ioReadAllFunc = orig })
	ioReadAllFunc = func(_ io.Reader) ([]byte, error) { return nil, errors.New("injected read failure") }

	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encPath := filepath.Join(dir, "secret.age")
	if werr := os.WriteFile(encPath, encryptToRecipients(t, "data", id.Recipient()), 0o600); werr != nil {
		t.Fatalf("WriteFile: %v", werr)
	}
	if _, derr := DecryptFile(context.Background(), encPath, set); derr == nil {
		t.Fatal("expected error from io.ReadAll failure, got nil")
	}
}

func TestDecryptFile_CorruptCiphertext(t *testing.T) {
	dir := t.TempDir()
	id := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, id)
	set, err := Resolve(context.Background(), KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encPath := filepath.Join(dir, "corrupt.age")
	if werr := os.WriteFile(encPath, []byte("not valid age ciphertext"), 0o600); werr != nil {
		t.Fatalf("WriteFile: %v", werr)
	}
	_, derr := DecryptFile(context.Background(), encPath, set)
	if derr == nil {
		t.Fatal("expected error for corrupt ciphertext via DecryptFile, got nil")
	}
	// A file matching neither marker takes the binary path, so age surfaces its
	// own accurate binary-header error rather than the misleading "invalid armor"
	// message that defaulting to the armor reader would produce.
	if strings.Contains(derr.Error(), "invalid armor") {
		t.Errorf("error misleadingly blames armor for a non-armored file: %v", derr)
	}
}

func TestResolve_EncryptedKey_BadSiblingPub(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, _ := writeOldPEMEncryptedRSA(t, sshDir, "id_rsa", "pw")
	// Sibling .pub exists but is not a valid authorized key → parse fails → skip.
	if werr := os.WriteFile(keyPath+".pub", []byte("garbage"), 0o600); werr != nil {
		t.Fatalf("write pub: %v", werr)
	}

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw")}
	set, rerr := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, prompter)
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0 (bad sibling .pub → skipped)", len(set.identities))
	}
}

func TestResolve_EncryptedKey_UnsupportedSiblingPubType(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, _ := writeOldPEMEncryptedRSA(t, sshDir, "id_rsa", "pw")
	// Sibling .pub parses as a valid SSH key but of an unsupported type (ecdsa),
	// so NewEncryptedSSHIdentity rejects it and the key is skipped.
	if werr := os.WriteFile(keyPath+".pub", ecdsaAuthorizedKey(t), 0o600); werr != nil {
		t.Fatalf("write pub: %v", werr)
	}

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw")}
	set, rerr := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, prompter)
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0 (unsupported sibling .pub type → skipped)", len(set.identities))
	}
}

// ecdsaAuthorizedKey returns an authorized-keys line for a fresh ecdsa key — a
// valid SSH public key of a type age does not support.
func ecdsaAuthorizedKey(t *testing.T) []byte {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa GenerateKey: %v", err)
	}
	ecPub, err := ssh.NewPublicKey(&ecKey.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	return ssh.MarshalAuthorizedKey(ecPub)
}

func TestUsableSSHIdentity_NonKeyBytes(t *testing.T) {
	// ParseRawPrivateKey fails → usableSSHIdentity defers to agessh (returns nil).
	if err := usableSSHIdentity([]byte("not a key")); err != nil {
		t.Errorf("usableSSHIdentity on non-key bytes = %v, want nil", err)
	}
}

func TestSSHKindFor_RSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa GenerateKey: %v", err)
	}
	id, err := agessh.NewRSAIdentity(key)
	if err != nil {
		t.Fatalf("NewRSAIdentity: %v", err)
	}
	if got := sshKindFor(id); got != typeSSHRSA {
		t.Errorf("sshKindFor(RSA) = %q, want ssh-rsa", got)
	}
}

// ---- helpers / misc ---------------------------------------------------------

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
			if got := expandHome(tc.input); got != tc.want {
				t.Errorf("expandHome(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExpandHome_UserHomeDirError(t *testing.T) {
	orig := userHomeDirFunc
	t.Cleanup(func() { userHomeDirFunc = orig })
	userHomeDirFunc = func() (string, error) { return "", errors.New("no home") }

	if got := expandHome("~/foo/bar"); got != "~/foo/bar" {
		t.Errorf("expandHome with homedir error = %q, want original", got)
	}
}

func TestDefaultSSHDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir, err := defaultSSHDir()
	if err != nil {
		t.Fatalf("defaultSSHDir: %v", err)
	}
	if dir != filepath.Join(home, ".ssh") {
		t.Errorf("defaultSSHDir = %q, want %q", dir, filepath.Join(home, ".ssh"))
	}
}

func TestDefaultSSHDir_HomeError(t *testing.T) {
	orig := userHomeDirFunc
	t.Cleanup(func() { userHomeDirFunc = orig })
	userHomeDirFunc = func() (string, error) { return "", errors.New("no home") }

	if _, err := defaultSSHDir(); err == nil {
		t.Fatal("expected error when home dir lookup fails, got nil")
	}
}

func TestDefaultListDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := defaultListDir(dir)
	if err != nil {
		t.Fatalf("defaultListDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if _, err := defaultListDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error listing missing dir, got nil")
	}
}

// writeOldPEMEncryptedRSA writes a legacy (old-PEM, PKCS#1) passphrase-encrypted
// RSA key at dir/name. The legacy format carries no embedded public key, so the
// caller relies on a sibling .pub. The underlying x509 helpers are deprecated but
// remain the only way to produce this historical format, which dotsmith must
// still handle. Returns the key path and the ssh.PublicKey.
func writeOldPEMEncryptedRSA(t *testing.T, dir, name, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa GenerateKey: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	//nolint:staticcheck // legacy encrypted PEM is exactly what we are testing
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", der, []byte(passphrase), x509.PEMCipherAES256)
	if err != nil {
		t.Fatalf("EncryptPEMBlock: %v", err)
	}
	path := filepath.Join(dir, name)
	if werr := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); werr != nil {
		t.Fatalf("write key: %v", werr)
	}
	return path, sshPub
}

func TestIsDeniedSSHName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"config", true},
		{"authorized_keys", true},
		{"known_hosts", true},
		{"known_hosts.old", true},
		{"id_ed25519.pub", true},
		{"id_ed25519", false},
		{"id_rsa", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeniedSSHName(tc.name); got != tc.want {
				t.Errorf("isDeniedSSHName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
