package encrypt

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
	"filippo.io/age/armor"
	"golang.org/x/crypto/ssh"
)

// prober reports, without unlocking or prompting, whether a resolved candidate
// identity would decrypt a file given its recipient stanzas. It is the heart of
// --dry-run reporting: matching is decided from public-key data (or, for native
// age, an in-memory unwrap of an already-loaded key) so no passphrase callback
// ever fires and nothing is written to disk.
type prober interface {
	// matches reports whether this candidate would decrypt the stanzas. It must
	// never prompt, unlock a passphrase-protected key, or touch the filesystem.
	matches(stanzas []*age.Stanza) bool
}

// sshTagFor computes the recipient tag age embeds in an ssh-ed25519 / ssh-rsa
// stanza: the first four bytes of the SHA-256 of the marshalled public key,
// raw-std-base64 encoded. This mirrors agessh.sshFingerprint exactly and depends
// only on the public key, so it can be evaluated without the private key or a
// passphrase.
func sshTagFor(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return base64.RawStdEncoding.EncodeToString(sum[:4])
}

// sshProbe matches an SSH identity by comparing its public-key tag against each
// stanza's recipient tag — the same comparison agessh performs before it ever
// uses the private key. It works identically for encrypted and unencrypted SSH
// keys because the tag lives in the public key.
type sshProbe struct {
	// stanzaType is the age stanza type this key can satisfy ("ssh-ed25519" or
	// "ssh-rsa").
	stanzaType string
	// tag is the recipient tag (sshFingerprint) of the public key.
	tag string
}

// newSSHProbe builds an SSH tag probe from a public key.
func newSSHProbe(pub ssh.PublicKey) sshProbe {
	return sshProbe{stanzaType: pub.Type(), tag: sshTagFor(pub)}
}

// matches reports whether any stanza is an SSH stanza of this key's type whose
// recipient tag equals this key's tag.
func (p sshProbe) matches(stanzas []*age.Stanza) bool {
	for _, s := range stanzas {
		if s.Type == p.stanzaType && len(s.Args) >= 1 && s.Args[0] == p.tag {
			return true
		}
	}
	return false
}

// nativeProbe matches a native age (X25519) identity. Native age stanzas carry
// no public-key tag — the only way to tell a match is the ECDH-and-AEAD unwrap
// itself. That unwrap is a pure in-memory operation on an already-loaded,
// never-passphrase-protected key, so running it during dry-run prompts nothing
// and writes nothing.
type nativeProbe struct {
	id age.Identity
}

// matches reports whether the native identity unwraps any stanza. A nil identity
// (defensive) never matches.
func (p nativeProbe) matches(stanzas []*age.Stanza) bool {
	if p.id == nil {
		return false
	}
	_, err := p.id.Unwrap(stanzas)
	return err == nil
}

// captureIdentity is the sentinel age.Identity supplied to age.Decrypt during a
// dry-run probe. age calls its Unwrap once with the file's recipient stanzas;
// captureIdentity records them and returns age.ErrIncorrectIdentity so age never
// invokes any real identity (and thus never prompts or decrypts). The captured
// stanzas are then matched against each candidate's probe.
type captureIdentity struct {
	stanzas []*age.Stanza
}

var _ age.Identity = (*captureIdentity)(nil)

// Unwrap records the stanzas and declines, so age moves on without unlocking.
func (c *captureIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	c.stanzas = stanzas
	return nil, age.ErrIncorrectIdentity //nolint:wrapcheck // sentinel must propagate unwrapped for age
}

// DryRunResult reports the outcome of probing one .age file in --dry-run.
type DryRunResult struct {
	// Matched is true when a candidate identity would decrypt the file.
	Matched bool
	// Path is the source file of the matching identity (empty when no match).
	Path string
	// Kind is the type label of the matching identity (empty when no match).
	Kind string
}

// DryRunProbeFile reports which candidate identity would decrypt the .age file
// at path, without unlocking any key, prompting for a passphrase, or writing
// anything. It reads the file's header to recover the recipient stanzas (via a
// sentinel capturing identity, since age's header parser is internal) and then
// matches each candidate's probe against those stanzas. A file matched by no
// candidate returns a result with Matched == false.
func (s IdentitySet) DryRunProbeFile(_ context.Context, path string) (DryRunResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return DryRunResult{}, fmt.Errorf("dry-run probe %s: open: %w — check the path exists and is readable", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	stanzas, err := captureStanzas(f)
	if err != nil {
		return DryRunResult{}, fmt.Errorf("dry-run probe %s: %w", path, err)
	}
	return s.probeStanzas(stanzas), nil
}

// captureStanzas recovers a file's recipient stanzas by handing age a single
// sentinel identity that records the stanzas and declines to unwrap. age always
// fails here — the sentinel matches nothing — so a NoIdentityMatchError is the
// success case (the header parsed and the sentinel's Unwrap ran, capturing the
// stanzas). Any other error means the header itself could not be parsed and is
// surfaced.
func captureStanzas(r io.Reader) ([]*age.Stanza, error) {
	sentinel := &captureIdentity{}
	armorReader := armor.NewReader(r)
	_, err := age.Decrypt(armorReader, sentinel)
	var noMatch *age.NoIdentityMatchError
	if !errors.As(err, &noMatch) {
		return nil, fmt.Errorf("read age header: %w — file may be corrupt or not an age file", err)
	}
	return sentinel.stanzas, nil
}

// probeStanzas matches each candidate's probe against the stanzas in candidate
// order, returning the first match.
func (s IdentitySet) probeStanzas(stanzas []*age.Stanza) DryRunResult {
	for _, c := range s.candidates {
		if c.probe != nil && c.probe.matches(stanzas) {
			return DryRunResult{Matched: true, Path: c.path, Kind: string(c.kind)}
		}
	}
	return DryRunResult{Matched: false}
}

// Empty reports whether the candidate identity set holds no identities. Dry-run
// reporting uses this to surface the empty-set guidance before probing files.
func (s IdentitySet) Empty() bool {
	return len(s.identities) == 0
}
