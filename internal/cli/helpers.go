package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/config"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/linker"
	"github.com/andersosthus/dotsmith/internal/state"
)

// Injectable for testing.
var (
	filepathRelHelpersFunc = filepath.Rel
	resolveIdentitiesFunc  = encrypt.Resolve
)

// keySourceFor builds the encrypt.KeySource from the resolved config.
func keySourceFor(cfg config.Config) encrypt.KeySource {
	return encrypt.KeySource{
		IdentityFile:         cfg.AgeIdentity,
		IdentityFileExplicit: cfg.AgeIdentityExplicit,
		Identities:           cfg.AgeIdentities,
		SSHDiscovery:         cfg.AgeSSHDiscovery,
		Verbose:              cfg.Verbose,
	}
}

// resolveIdentitySet builds the candidate identity set once for a run, wiring
// the terminal prompter for any passphrase-protected SSH key.
func resolveIdentitySet(ctx context.Context, cfg config.Config) (encrypt.IdentitySet, error) {
	set, err := resolveIdentitiesFunc(ctx, keySourceFor(cfg), encrypt.TerminalPrompter{})
	if err != nil {
		return encrypt.IdentitySet{}, fmt.Errorf("resolve identities: %w", err)
	}
	return set, nil
}

// printDryRunReports writes one line per probed age-encrypted source describing
// which identity would decrypt it on this machine — or that none would — without
// having unlocked any key. It is a no-op when there are no reports.
func printDryRunReports(w io.Writer, reports []compiler.DryRunReport) {
	for _, r := range reports {
		if r.Matched {
			_, _ = fmt.Fprintf(w, "would decrypt %s -> %s [%s]\n", r.SourcePath, r.IdentityPath, r.IdentityKind)
			continue
		}
		_, _ = fmt.Fprintf(w,
			"would NOT decrypt %s -> no identity on this machine matches any recipient\n",
			r.SourcePath)
	}
}

// compiledFileRefs walks compileDir and returns a FileRef for every compiled
// file (skipping the state file and any hidden metadata).
func compiledFileRefs(compileDir string) ([]linker.FileRef, error) {
	var refs []linker.FileRef
	err := filepath.WalkDir(compileDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepathRelHelpersFunc(compileDir, path)
		if relErr != nil {
			return relErr
		}
		// Skip the state file.
		if rel == state.FileName {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		sum := sha256.Sum256(data)
		refs = append(refs, linker.FileRef{
			RelPath:     rel,
			ContentHash: hex.EncodeToString(sum[:]),
		})
		return nil
	})
	return refs, err //nolint:wrapcheck // WalkDir callback pre-wraps all errors with context
}
