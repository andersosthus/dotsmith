package compiler

import (
	"context"
	"fmt"
	"os"

	"github.com/andersosthus/dotsmith/internal/hash"
)

// SourceSignature computes the ordered source signature of a target's resolved
// subfiles (see ADR 0015). The signature is the digest of the concatenation of
// each subfile's content hash, taken in the subfiles' resolved (natural-sorted)
// order, so it moves whenever:
//
//   - a subfile's body content changes,
//   - a subfile is added or removed,
//   - the subfiles are reordered.
//
// The hash of each subfile is taken over the raw bytes on disk: ciphertext for
// an .age source, plaintext for a plain source. No source is ever decrypted —
// computing the signature requires no identity and leaks nothing the ciphertext
// does not already. Provenance inputs (source name, layer) feed only the comment
// header and are deliberately excluded, so a rename or layer move with identical
// bytes does not move the signature.
//
// The order is captured by hashing the per-subfile hashes joined with a newline
// separator (which cannot appear inside a fixed-width hex digest), so that
// reordering two subfiles with swappable content still changes the result.
func SourceSignature(ctx context.Context, subfiles []SubfileDesc) (string, error) {
	var buf []byte
	for i := range subfiles {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("source signature: %w", err)
		}
		sf := subfiles[i]
		content, err := os.ReadFile(sf.SourcePath)
		if err != nil {
			return "", fmt.Errorf("source signature: read %s: %w", sf.SourcePath, err)
		}
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, hash.Sum(content)...)
	}
	return hash.Sum(buf), nil
}
