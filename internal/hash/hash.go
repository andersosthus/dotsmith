// Package hash provides the single content-hashing helper used across dotsmith.
//
// Every content hash in dotsmith — the compile-idempotency check, the linker's
// stale detection, and the manifest/state hashes — exists only for change
// detection, never as an integrity or security boundary. Routing every call
// site through this one helper makes a mismatched-algorithm bug structurally
// impossible: compile and link can never disagree about whether a file changed.
//
// The digest is XXH3-128 (non-cryptographic). See ADR 0012 for the rationale.
package hash

import (
	"encoding/hex"

	"github.com/zeebo/xxh3"
)

// Sum returns the hex-encoded XXH3-128 digest of content.
//
// The result is a 32-character lowercase hex string. The hash is for change
// detection only and must not be relied on as a security or integrity
// guarantee.
func Sum(content []byte) string {
	digest := xxh3.Hash128(content).Bytes()
	return hex.EncodeToString(digest[:])
}
