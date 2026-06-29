// Package encrypt wraps filippo.io/age for transparent decryption of
// age-encrypted dotfiles during compilation.
package encrypt

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// isArmored reports whether age ciphertext is in the ASCII-armored encoding,
// decided purely from its leading bytes. It is a strict byte-prefix match
// against the armor envelope marker (armor.Header): the leading bytes must
// equal the marker exactly, with no leading-whitespace tolerance (age's default
// armored output starts the marker at byte 0).
//
// Detection is positive-detect-armor, default-to-binary: only an exact marker
// match is treated as armored, and everything else — true binary files and
// corrupt/non-age files alike — is treated as binary. This is deliberate. If a
// broken file were instead defaulted to the armor path, age would reject it
// with a misleading "invalid armor" error; defaulting to binary lets age
// surface its own accurate binary-header error for such files. A slice shorter
// than the marker (a short read or Peek-EOF on a tiny file) cannot match the
// prefix and is therefore classified as binary, so age reports any truncation.
func isArmored(leading []byte) bool {
	return strings.HasPrefix(string(leading), armor.Header)
}

// decryptReader peeks the leading bytes of src and returns an io.Reader ready to
// hand straight to age.Decrypt: an armor reader when the input is armored, and
// the buffered reader itself (unwrapped) for binary input. It is the shared
// encoding-detection seam, intended as the single place the armored/binary
// decision is made so call sites cannot drift. All three decrypt paths — the
// streaming Decrypt, DecryptFile, and the dry-run stanza-capture probe — route
// through it.
//
// Peeking — rather than trying one decoder and rewinding on failure — keeps the
// streaming io.Reader contract intact and is deterministic. A short Peek (fewer
// bytes than the marker, e.g. on EOF) is not an error here: isArmored treats the
// short slice as binary and age then reports any truncation itself.
func decryptReader(src io.Reader) io.Reader {
	br := bufio.NewReader(src)
	leading, _ := br.Peek(len(armor.Header))
	if isArmored(leading) {
		return armor.NewReader(br)
	}
	return br
}

// KeySource describes the inputs from which a candidate identity set is
// resolved. The native age identity file is kept for back-compat; SSH discovery
// and extra identity paths broaden the set so files encrypted to a machine's SSH
// key can be decrypted without per-machine configuration.
type KeySource struct {
	// IdentityFile is the path to a native age identity file (age-keygen). It is
	// always consulted when non-empty.
	IdentityFile string
	// IdentityFileExplicit records whether IdentityFile was set by the user
	// (config or flag) rather than left at its default. A missing default file is
	// tolerated; a missing explicit file is a hard error.
	IdentityFileExplicit bool
	// Identities holds extra identity paths (native age or SSH), each
	// format-auto-detected. Every listed path is treated as explicit: a missing
	// one is a hard error.
	Identities []string
	// SSHDiscovery enables scanning ~/.ssh/ for usable SSH private keys.
	SSHDiscovery bool
	// Verbose enables per-skip logging during discovery.
	Verbose bool
}

// Decrypt reads age-encrypted data from src and returns the plaintext, using the
// already-resolved candidate identity set. Resolve the set once per run and pass
// it to every Decrypt/DecryptFile call so passphrase-protected keys are unlocked
// at most once.
func Decrypt(_ context.Context, src io.Reader, set IdentitySet) ([]byte, error) {
	if len(set.identities) == 0 {
		return nil, emptySetError()
	}

	set.setDecryptSource("") // unnamed stream: a passphrase prompt names only the key

	r, err := age.Decrypt(decryptReader(src), set.identities...)
	if err != nil {
		return nil, set.wrapDecryptError("", err)
	}

	out, err := ioReadAllFunc(r)
	if err != nil {
		return nil, fmt.Errorf("decrypt: read plaintext: %w", err)
	}
	return out, nil
}

// DecryptFile reads and decrypts the age-encrypted file at path with the
// resolved candidate identity set, returning the plaintext bytes.
func DecryptFile(ctx context.Context, path string, set IdentitySet) ([]byte, error) {
	if len(set.identities) == 0 {
		return nil, emptySetError()
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("decrypt file %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	set.setDecryptSource(path) // name this file in any SSH-key passphrase prompt

	r, err := age.Decrypt(decryptReader(f), set.identities...)
	if err != nil {
		return nil, set.wrapDecryptError(path, err)
	}

	out, err := ioReadAllFunc(r)
	if err != nil {
		return nil, fmt.Errorf("decrypt file %s: read plaintext: %w", path, err)
	}
	return out, nil
}

// Injectable functions for testing error paths.
var (
	userHomeDirFunc = os.UserHomeDir
	ioReadAllFunc   = io.ReadAll
)

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
