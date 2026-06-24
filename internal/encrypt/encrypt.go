// Package encrypt wraps filippo.io/age for transparent decryption of
// age-encrypted dotfiles during compilation.
package encrypt

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

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

	armorReader := armor.NewReader(src)
	r, err := age.Decrypt(armorReader, set.identities...)
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

	armorReader := armor.NewReader(f)
	r, err := age.Decrypt(armorReader, set.identities...)
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
