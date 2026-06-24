package encrypt

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

// identityType labels the format of a resolved candidate identity, used in the
// no-match error so the user can see exactly what dotsmith tried.
type identityType string

const (
	typeNativeAge    identityType = "age"
	typeSSHEd25519   identityType = "ssh-ed25519"
	typeSSHRSA       identityType = "ssh-rsa"
	typeSSHEncrypted identityType = "ssh-encrypted"
)

// candidate is one resolved identity plus the metadata used for reporting and
// the no-match error.
type candidate struct {
	identity age.Identity
	// path is the source file the identity came from (for error reporting).
	path string
	// kind labels the identity format.
	kind identityType
	// probe reports, without unlocking or prompting, whether this candidate would
	// decrypt a file given its recipient stanzas. It powers --dry-run reporting.
	probe prober
}

// IdentitySet is the resolved candidate identity set for one dotsmith run. It is
// built once via Resolve and consumed by every Decrypt/DecryptFile call so a
// passphrase-protected key is unlocked at most once per run. The zero value
// holds no candidates and decrypting against it returns a guidance error.
type IdentitySet struct {
	identities []age.Identity
	candidates []candidate
}

// Prompter supplies passphrases for passphrase-protected SSH keys and reports
// whether an interactive terminal is available. It is injected so resolution is
// testable without a real terminal.
type Prompter interface {
	// Interactive reports whether a terminal is available for prompting.
	Interactive() bool
	// Prompt requests the passphrase for the key labelled keyLabel. It may be
	// called multiple times (age retries up to three attempts on a wrong
	// passphrase) and is invoked lazily, only when a key's tag matches a file.
	Prompt(keyLabel string) ([]byte, error)
}

// Injectable seams for testing discovery without touching the real machine.
var (
	sshDirFunc         = defaultSSHDir
	listDirFunc        = defaultListDir
	readKeyFileFunc    = os.ReadFile
	sshParsePrivateKey = ssh.ParsePrivateKey
)

// Resolve builds the candidate identity set from ks: the native age identity
// (when present), every explicitly configured extra identity path, and SSH keys
// discovered in ~/.ssh/ (when ks.SSHDiscovery is set). The prompter is wired
// into any passphrase-protected SSH key so it is unlocked lazily, at most once
// per run. A missing default native key is tolerated; a missing explicitly
// configured path is a hard error.
func Resolve(_ context.Context, ks KeySource, prompter Prompter) (IdentitySet, error) {
	var set IdentitySet

	if err := set.addNativeIdentity(ks); err != nil {
		return IdentitySet{}, err
	}
	if err := set.addExplicitIdentities(ks); err != nil {
		return IdentitySet{}, err
	}
	if ks.SSHDiscovery {
		set.discoverSSH(ks, prompter)
	}

	return set, nil
}

// addNativeIdentity loads the native age identity file. A missing default file
// (IdentityFileExplicit == false) is silently skipped to unblock SSH-only users;
// a missing explicitly configured file is a hard error.
func (s *IdentitySet) addNativeIdentity(ks KeySource) error {
	if ks.IdentityFile == "" {
		return nil
	}
	expanded := expandHome(ks.IdentityFile)

	ids, err := loadNativeIdentities(expanded)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !ks.IdentityFileExplicit {
			return nil // default key absent: tolerated
		}
		return err
	}
	for _, id := range ids {
		s.add(id, ks.IdentityFile, typeNativeAge, nativeProbe{id: id})
	}
	return nil
}

// addExplicitIdentities loads every path in ks.Identities. Each is explicit, so
// a missing or unparseable file is a hard error.
func (s *IdentitySet) addExplicitIdentities(ks KeySource) error {
	for _, path := range ks.Identities {
		expanded := expandHome(path)
		data, err := readKeyFileFunc(expanded)
		if err != nil {
			return fmt.Errorf(
				"load identity %s: %w — fix the path in age.identities or remove the entry",
				path, err,
			)
		}
		id, kind, probe, err := parseIdentity(data, expanded, nil)
		if err != nil {
			return fmt.Errorf(
				"parse identity %s: %w — must be a native age or SSH (ed25519/rsa) key",
				path, err,
			)
		}
		s.add(id, path, kind, probe)
	}
	return nil
}

// discoverSSH enumerates ~/.ssh/ and adds every usable SSH private key. Files
// that are not usable keys (wrong type, undersized RSA, denylisted names,
// sockets, directories) are skipped; the reason is logged only under Verbose.
func (s *IdentitySet) discoverSSH(ks KeySource, prompter Prompter) {
	dir, err := sshDirFunc()
	if err != nil {
		s.logSkip(ks, "~/.ssh", fmt.Sprintf("locate ssh dir: %v", err))
		return
	}

	entries, err := listDirFunc(dir)
	if err != nil {
		s.logSkip(ks, dir, fmt.Sprintf("read ssh dir: %v", err))
		return
	}

	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)

		if !e.Type().IsRegular() {
			s.logSkip(ks, path, "not a regular file")
			continue
		}
		if isDeniedSSHName(name) {
			continue
		}
		s.tryAddSSHKey(ks, path, prompter)
	}
}

// tryAddSSHKey reads and classifies one ~/.ssh/ file via agessh, adding it to
// the set when it is a usable SSH identity (encrypted or not) and skipping it
// otherwise.
func (s *IdentitySet) tryAddSSHKey(ks KeySource, path string, prompter Prompter) {
	data, err := readKeyFileFunc(path)
	if err != nil {
		s.logSkip(ks, path, fmt.Sprintf("read: %v", err))
		return
	}

	id, kind, probe, err := parseIdentity(data, path, prompter)
	if err != nil {
		s.logSkip(ks, path, err.Error())
		return
	}
	s.add(id, path, kind, probe)
}

// parseIdentity classifies key bytes. A native age identity is detected via
// age's parser; otherwise the bytes are delegated to agessh, whose result is the
// classifier — dotsmith does no PEM-sniffing or key-type/size validation of its
// own. A passphrase-protected SSH key becomes a lazily-unlocked identity when a
// prompter is supplied.
func parseIdentity(data []byte, path string, prompter Prompter) (age.Identity, identityType, prober, error) {
	if id, ok := tryNativeAge(data); ok {
		return id, typeNativeAge, nativeProbe{id: id}, nil
	}

	id, err := agessh.ParseIdentity(data)
	if err == nil {
		if usableErr := usableSSHIdentity(data); usableErr != nil {
			return nil, "", nil, usableErr
		}
		probe, perr := sshProbeFromKey(data)
		if perr != nil {
			return nil, "", nil, perr
		}
		return id, sshKindFor(id), probe, nil
	}

	var pmErr *ssh.PassphraseMissingError
	if errors.As(err, &pmErr) {
		return encryptedSSHIdentity(data, path, pmErr, prompter)
	}

	return nil, "", nil, fmt.Errorf("not a usable age or SSH identity: %w", err)
}

// sshProbeFromKey recovers the SSH public key from an unencrypted private key's
// bytes and builds the dry-run tag probe for it. The public key is the only
// material the probe needs, so this performs no decryption.
func sshProbeFromKey(data []byte) (prober, error) {
	signer, err := sshParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("recover ssh public key for dry-run probe: %w", err)
	}
	return newSSHProbe(signer.PublicKey()), nil
}

// encryptedSSHIdentity builds a lazily-unlocked identity for a
// passphrase-protected SSH key. The public key comes from the
// PassphraseMissingError, or from a sibling .pub file for old-PEM encrypted RSA;
// without either, or without a prompter, the key is unusable and skipped.
func encryptedSSHIdentity(
	data []byte, path string, pmErr *ssh.PassphraseMissingError, prompter Prompter,
) (age.Identity, identityType, prober, error) {
	if prompter == nil {
		return nil, "", nil, errors.New("passphrase-protected key with no prompter available")
	}

	pub := pmErr.PublicKey
	if pub == nil {
		var err error
		pub, err = readSiblingPub(path)
		if err != nil {
			return nil, "", nil, fmt.Errorf("encrypted key without recoverable public key: %w", err)
		}
	}

	if err := usableSSHPublicKey(pub); err != nil {
		return nil, "", nil, err
	}

	// pendingErr lets the passphrase callback surface a no-TTY hard error through
	// agessh, which only propagates errors returned by the callback itself.
	var pendingErr error
	inner, err := agessh.NewEncryptedSSHIdentity(pub, data, func() ([]byte, error) {
		if !prompter.Interactive() {
			pendingErr = fmt.Errorf(
				"passphrase required for %s but no terminal is available — "+
					"run dotsmith interactively or use an unencrypted key", path,
			)
			return nil, pendingErr
		}
		pass, perr := prompter.Prompt(path)
		if perr != nil {
			pendingErr = fmt.Errorf("prompt passphrase for %s: %w", path, perr)
			return nil, pendingErr
		}
		return pass, nil
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("build encrypted ssh identity: %w", err)
	}

	return &retryingEncryptedIdentity{
		inner:      inner,
		keyLabel:   path,
		pendingErr: &pendingErr,
	}, typeSSHEncrypted, newSSHProbe(pub), nil
}

// maxPassphraseAttempts is the number of times dotsmith prompts for an encrypted
// key's passphrase before giving up.
const maxPassphraseAttempts = 3

// retryingEncryptedIdentity wraps an agessh.EncryptedSSHIdentity to add
// dotsmith's interactivity policy: up to maxPassphraseAttempts passphrase
// prompts, and a clear hard error when a matched encrypted key cannot be
// unlocked (wrong passphrase exhausted, or no terminal available).
//
// agessh.EncryptedSSHIdentity.Unwrap calls the passphrase callback at most once
// and aborts the whole decrypt on a wrong passphrase, so the retry loop lives
// here: each Unwrap attempt re-runs the (lazy) callback. After the first
// successful unlock agessh caches the decrypted key, so a key shared by many
// files in one run prompts exactly once.
type retryingEncryptedIdentity struct {
	inner    age.Identity
	keyLabel string
	// pendingErr receives a hard error (no TTY, or prompt I/O failure) set by the
	// passphrase callback; such errors must not be retried.
	pendingErr *error
}

var _ age.Identity = (*retryingEncryptedIdentity)(nil)

// Unwrap implements age.Identity. If the key's tag does not match any stanza it
// returns age.ErrIncorrectIdentity unchanged (no prompt, letting age try the
// next candidate). On a tag match it drives the prompt-and-unlock loop.
func (r *retryingEncryptedIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxPassphraseAttempts; attempt++ {
		*r.pendingErr = nil
		fileKey, err := r.inner.Unwrap(stanzas)
		if err == nil {
			return fileKey, nil
		}
		// Non-matching key: hand age.ErrIncorrectIdentity straight back so age
		// moves on to the next candidate without prompting.
		if errors.Is(err, age.ErrIncorrectIdentity) {
			return nil, err //nolint:wrapcheck // sentinel must propagate unwrapped for age
		}
		// A no-TTY or prompt-failure error from the callback is terminal — there
		// is no point re-prompting.
		if *r.pendingErr != nil {
			return nil, *r.pendingErr
		}
		// Otherwise the passphrase was wrong (or the key failed to decrypt); retry.
		lastErr = err
	}
	return nil, fmt.Errorf(
		"incorrect passphrase for %s after %d attempts — verify the passphrase or use an unencrypted key: %w",
		r.keyLabel, maxPassphraseAttempts, lastErr,
	)
}

// readSiblingPub reads and parses the <path>.pub file next to an encrypted key.
func readSiblingPub(path string) (ssh.PublicKey, error) {
	data, err := readKeyFileFunc(path + ".pub")
	if err != nil {
		return nil, fmt.Errorf("read sibling .pub: %w", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse sibling .pub: %w", err)
	}
	return pub, nil
}

// tryNativeAge attempts to parse data as one or more native age identities.
func tryNativeAge(data []byte) (age.Identity, bool) {
	ids, err := age.ParseIdentities(strings.NewReader(string(data)))
	if err != nil || len(ids) == 0 {
		return nil, false
	}
	return ids[0], true
}

// loadNativeIdentities parses a native age identity file at expanded.
func loadNativeIdentities(expanded string) ([]age.Identity, error) {
	f, err := os.Open(expanded)
	if err != nil {
		return nil, fmt.Errorf("open identity file %s: %w", expanded, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	ids, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("parse identity file %s: %w", expanded, err)
	}
	return ids, nil
}

// minRSABits is age's minimum supported ssh-rsa key size; smaller keys are
// rejected at recipient-build time inside agessh and so could never match a real
// .age file.
const minRSABits = 2048

// usableSSHIdentity rejects keys age can parse but cannot actually use as a
// recipient match. agessh.ParseIdentity accepts an undersized RSA key, but age's
// recipient build (and thus any real encryption to it) enforces a 2048-bit
// minimum, so such a key could never match a file — treat it as unsupported.
func usableSSHIdentity(data []byte) error {
	raw, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		return nil // not classifiable here; agessh already accepted it
	}
	rsaKey, ok := raw.(*rsa.PrivateKey)
	if !ok {
		return nil
	}
	if rsaKey.N.BitLen() < minRSABits {
		return fmt.Errorf("RSA key is %d-bit, below age's %d-bit minimum", rsaKey.N.BitLen(), minRSABits)
	}
	return nil
}

// usableSSHPublicKey applies the same RSA-size floor as usableSSHIdentity but to
// an already-recovered public key. It is used on the encrypted-key path, where
// the private key cannot be inspected without the passphrase: an undersized
// encrypted RSA key must be skipped before it can ever prompt, since age could
// never match a recipient to it.
func usableSSHPublicKey(pub ssh.PublicKey) error {
	if pub.Type() != "ssh-rsa" {
		return nil
	}
	cpk, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return nil
	}
	rsaPub, ok := cpk.CryptoPublicKey().(*rsa.PublicKey)
	if !ok {
		return nil
	}
	if rsaPub.N.BitLen() < minRSABits {
		return fmt.Errorf("RSA key is %d-bit, below age's %d-bit minimum", rsaPub.N.BitLen(), minRSABits)
	}
	return nil
}

// sshKindFor maps a resolved agessh identity to its type label.
func sshKindFor(id age.Identity) identityType {
	if _, ok := id.(*agessh.RSAIdentity); ok {
		return typeSSHRSA
	}
	return typeSSHEd25519
}

// add appends a resolved identity and its metadata to the set.
func (s *IdentitySet) add(id age.Identity, path string, kind identityType, probe prober) {
	s.identities = append(s.identities, id)
	s.candidates = append(s.candidates, candidate{identity: id, path: path, kind: kind, probe: probe})
}

// logSkip records a discovery skip under verbose mode only; skips are otherwise
// silent.
func (s *IdentitySet) logSkip(ks KeySource, path, reason string) {
	if ks.Verbose {
		slog.Debug("skipping ssh candidate", "path", path, "reason", reason)
	}
}

// triedList renders the resolved candidates as "path [type]" entries for the
// no-match error.
func (s *IdentitySet) triedList() string {
	parts := make([]string, 0, len(s.candidates))
	for _, c := range s.candidates {
		parts = append(parts, fmt.Sprintf("%s [%s]", c.path, c.kind))
	}
	return strings.Join(parts, ", ")
}

// wrapDecryptError turns age's no-match error into dotsmith guidance naming the
// file and the identities tried; other errors are wrapped verbatim.
func (s *IdentitySet) wrapDecryptError(path string, err error) error {
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		target := "the input"
		if path != "" {
			target = path
		}
		return fmt.Errorf(
			"decrypt %s: no configured identity matched any recipient — tried %s; "+
				"this file may not be encrypted to any key on this machine, re-encrypt including this machine's recipient: %w",
			target, s.triedList(), err,
		)
	}
	if path != "" {
		return fmt.Errorf("decrypt file %s: %w", path, err)
	}
	return fmt.Errorf("decrypt: %w", err)
}

// emptySetError guides a user who has no candidate identities at all.
func emptySetError() error {
	return errors.New(
		"no decryption identity available — set age.identity_file, add paths to age.identities, " +
			"or enable age.ssh_discovery to scan ~/.ssh",
	)
}

// defaultSSHDir returns the ~/.ssh directory path.
func defaultSSHDir() (string, error) {
	home, err := userHomeDirFunc()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".ssh"), nil
}

// defaultListDir lists directory entries at dir.
func defaultListDir(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	return entries, nil
}

// isDeniedSSHName reports whether name is a known-non-key file that must never
// be treated as a candidate key.
func isDeniedSSHName(name string) bool {
	switch name {
	case "config", "authorized_keys":
		return true
	}
	if strings.HasPrefix(name, "known_hosts") {
		return true
	}
	if strings.HasSuffix(name, ".pub") {
		return true
	}
	return false
}
