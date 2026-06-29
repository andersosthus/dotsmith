package encrypt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"
)

// writeEncFile writes ciphertext encrypted to recipients into dir/name and
// returns its path.
func writeEncFile(t *testing.T, dir, name, plaintext string, recipients ...age.Recipient) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, encryptToRecipients(t, plaintext, recipients...), 0o600); err != nil {
		t.Fatalf("write enc file: %v", err)
	}
	return path
}

// ---- SSH dry-run probe: matches without unlocking or prompting --------------

func TestDryRunProbeFile_Ed25519Match_NoPrompt(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("passphrase"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("passphrase")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encDir := t.TempDir()
	encPath := writeEncFile(t, encDir, "secret.age", "x", sshRecipient(t, pub))

	res, err := set.DryRunProbeFile(context.Background(), encPath)
	if err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}
	if !res.Matched {
		t.Fatal("expected a match for the file's recipient, got none")
	}
	if res.Path != keyPath {
		t.Errorf("matched path = %q, want %q", res.Path, keyPath)
	}
	if res.Kind != string(typeSSHEncrypted) {
		t.Errorf("matched kind = %q, want %q", res.Kind, typeSSHEncrypted)
	}
	// The whole point: a passphrase-protected matched key was reported WITHOUT
	// ever prompting or unlocking.
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times during dry-run probe, want 0", prompter.calls)
	}
}

func TestDryRunProbeFile_UnencryptedEd25519Match(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, pub := writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encPath := writeEncFile(t, t.TempDir(), "secret.age", "x", sshRecipient(t, pub))
	res, err := set.DryRunProbeFile(context.Background(), encPath)
	if err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}
	if !res.Matched || res.Path != keyPath || res.Kind != string(typeSSHEd25519) {
		t.Errorf("got %+v, want match on %s [ssh-ed25519]", res, keyPath)
	}
}

func TestDryRunProbeFile_RSAMatch(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	keyPath, pub := writeRSAKey(t, sshDir, "id_rsa", 2048, nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encPath := writeEncFile(t, t.TempDir(), "secret.age", "x", sshRecipient(t, pub))
	res, err := set.DryRunProbeFile(context.Background(), encPath)
	if err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}
	if !res.Matched || res.Path != keyPath || res.Kind != string(typeSSHRSA) {
		t.Errorf("got %+v, want match on %s [ssh-rsa]", res, keyPath)
	}
}

// TestDryRunProbeFile_NativeAgeMatch proves the dry-run stanza-capture probe
// reports the matching identity for both age encodings: armored (age -a) and
// binary (the age CLI default). Parametrizing across encodings makes the binary
// path — which the probe previously could not handle because captureStanzas
// hardcoded the armor reader — a first-class case rather than an untested
// dimension.
func TestDryRunProbeFile_NativeAgeMatch(t *testing.T) {
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
			set, err := Resolve(context.Background(),
				KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			encPath := filepath.Join(t.TempDir(), "secret.age")
			if werr := os.WriteFile(encPath, enc.encrypt(t, "x", id.Recipient()), 0o600); werr != nil {
				t.Fatalf("WriteFile: %v", werr)
			}
			res, err := set.DryRunProbeFile(context.Background(), encPath)
			if err != nil {
				t.Fatalf("DryRunProbeFile: %v", err)
			}
			if !res.Matched || res.Path != keyPath || res.Kind != string(typeNativeAge) {
				t.Errorf("got %+v, want match on %s [age]", res, keyPath)
			}
		})
	}
}

// ---- no-match reporting in dry-run ------------------------------------------

func TestDryRunProbeFile_NoMatch_NoPrompt(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	// The machine holds one encrypted key; the file is encrypted to a different
	// key. The probe must report no-match without prompting.
	writeEd25519Key(t, sshDir, "id_ed25519", []byte("pw"))

	prompter := &fakePrompter{interactive: true, passphrase: []byte("pw")}
	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, prompter)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	otherDir := t.TempDir()
	_, otherPub := writeEd25519Key(t, otherDir, "other", nil)
	encPath := writeEncFile(t, t.TempDir(), "secret.age", "x", sshRecipient(t, otherPub))

	res, err := set.DryRunProbeFile(context.Background(), encPath)
	if err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}
	if res.Matched {
		t.Errorf("expected no match, got match on %s", res.Path)
	}
	if prompter.calls != 0 {
		t.Errorf("prompter called %d times for a non-matching file, want 0", prompter.calls)
	}
}

func TestDryRunProbeFile_NativeNoMatch(t *testing.T) {
	dir := t.TempDir()
	mine := generateAgeIdentity(t)
	keyPath := writeAgeKeyFile(t, dir, mine)
	set, err := Resolve(context.Background(),
		KeySource{IdentityFile: keyPath, IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	other := generateAgeIdentity(t)
	encPath := writeEncFile(t, t.TempDir(), "secret.age", "x", other.Recipient())
	res, err := set.DryRunProbeFile(context.Background(), encPath)
	if err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}
	if res.Matched {
		t.Errorf("expected no match for native key, got match on %s", res.Path)
	}
}

// ---- zero side effects ------------------------------------------------------

func TestDryRunProbeFile_NoFilesystemWrites(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	_, pub := writeEd25519Key(t, sshDir, "id_ed25519", []byte("pw"))

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true},
		&fakePrompter{interactive: true, passphrase: []byte("pw")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	encDir := t.TempDir()
	encPath := writeEncFile(t, encDir, "secret.age", "x", sshRecipient(t, pub))
	before := dirSnapshot(t, encDir)

	if _, err := set.DryRunProbeFile(context.Background(), encPath); err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}

	after := dirSnapshot(t, encDir)
	if before != after {
		t.Errorf("dry-run probe changed the directory:\nbefore=%s\nafter =%s", before, after)
	}
}

// dirSnapshot returns a stable string of the dir's entries and sizes for
// comparing before/after a no-write operation.
func dirSnapshot(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var buf bytes.Buffer
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			t.Fatalf("Info: %v", ierr)
		}
		buf.WriteString(e.Name())
		buf.WriteByte(':')
		buf.WriteString(info.Mode().String())
		buf.WriteByte(':')
		fmt.Fprintf(&buf, "%d", info.Size())
		buf.WriteByte('\n')
	}
	return buf.String()
}

// ---- error paths ------------------------------------------------------------

func TestDryRunProbeFile_MissingFile(t *testing.T) {
	set, err := Resolve(context.Background(), KeySource{}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, derr := set.DryRunProbeFile(context.Background(), filepath.Join(t.TempDir(), "nope.age"))
	if derr == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestDryRunProbeFile_CorruptFile feeds the probe garbage that matches neither
// the armor marker nor a valid age header. It must surface an error, and that
// error must be age's own accurate binary-header error rather than the
// misleading "invalid armor" message — guarding the positive-detect-armor,
// default-to-binary decision in the shared reader helper.
func TestDryRunProbeFile_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "corrupt.age")
	if err := os.WriteFile(bad, []byte("not an age file at all"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	set, err := Resolve(context.Background(), KeySource{}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, derr := set.DryRunProbeFile(context.Background(), bad)
	if derr == nil {
		t.Fatal("expected error for corrupt age file, got nil")
	}
	// A file matching neither marker takes the binary path, so age surfaces its
	// own accurate binary-header error rather than the misleading "invalid armor"
	// message that defaulting to the armor reader would produce.
	if strings.Contains(derr.Error(), "invalid armor") {
		t.Errorf("error misleadingly blames armor for a non-armored file: %v", derr)
	}
}

func TestDryRunProbeFile_MultiRecipientReportsTheMatchingOne(t *testing.T) {
	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	myPath, myPub := writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	otherDir := t.TempDir()
	_, otherPub := writeEd25519Key(t, otherDir, "other", nil)
	// Encrypted to a key we do not hold AND to our key.
	encPath := writeEncFile(t, t.TempDir(), "multi.age", "x",
		sshRecipient(t, otherPub), sshRecipient(t, myPub))

	res, err := set.DryRunProbeFile(context.Background(), encPath)
	if err != nil {
		t.Fatalf("DryRunProbeFile: %v", err)
	}
	if !res.Matched || res.Path != myPath {
		t.Errorf("got %+v, want match on our key %s", res, myPath)
	}
}

// ---- IdentitySet.Empty ------------------------------------------------------

func TestIdentitySet_Empty(t *testing.T) {
	if !(IdentitySet{}).Empty() {
		t.Error("zero IdentitySet should be Empty")
	}
	dir := t.TempDir()
	id := generateAgeIdentity(t)
	set, err := Resolve(context.Background(),
		KeySource{IdentityFile: writeAgeKeyFile(t, dir, id), IdentityFileExplicit: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if set.Empty() {
		t.Error("set with one identity should not be Empty")
	}
}

// ---- nativeProbe nil-guard --------------------------------------------------

func TestNativeProbe_NilIdentity(t *testing.T) {
	if (nativeProbe{}).matches(nil) {
		t.Error("nativeProbe with nil identity should never match")
	}
}

// ---- probe derivation failure (defensive) -----------------------------------

func TestResolve_SSHProbeDerivationFails_KeySkipped(t *testing.T) {
	// If recovering the public key for the dry-run probe fails (agessh accepted
	// the key but ssh.ParsePrivateKey did not), the key is treated as unusable and
	// skipped rather than added without a probe.
	orig := sshParsePrivateKey
	t.Cleanup(func() { sshParsePrivateKey = orig })
	sshParsePrivateKey = func(_ []byte) (ssh.Signer, error) {
		return nil, errors.New("forced probe-derivation failure")
	}

	sshDir := t.TempDir()
	withSSHDir(t, sshDir)
	writeEd25519Key(t, sshDir, "id_ed25519", nil)

	set, err := Resolve(context.Background(), KeySource{SSHDiscovery: true, Verbose: true}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(set.identities) != 0 {
		t.Errorf("got %d identities, want 0 (probe derivation failed → skipped)", len(set.identities))
	}
}

func TestResolve_ExplicitSSH_ProbeDerivationFails_Error(t *testing.T) {
	// For an explicitly configured SSH identity, a probe-derivation failure is a
	// hard error (explicit paths must not be silently dropped).
	orig := sshParsePrivateKey
	t.Cleanup(func() { sshParsePrivateKey = orig })
	sshParsePrivateKey = func(_ []byte) (ssh.Signer, error) {
		return nil, errors.New("forced probe-derivation failure")
	}

	dir := t.TempDir()
	keyPath, _ := writeEd25519Key(t, dir, "id_ed25519", nil)
	if _, err := Resolve(context.Background(), KeySource{Identities: []string{keyPath}}, nil); err == nil {
		t.Fatal("expected hard error when explicit SSH identity probe derivation fails, got nil")
	}
}
