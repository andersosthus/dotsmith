package cli

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/andersosthus/dotsmith/internal/compiler"
	"github.com/andersosthus/dotsmith/internal/config"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/hash"
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

// danglingWarnCap is the maximum number of dangling paths listed individually
// in the warning; beyond it the remainder is summarised as a count.
const danglingWarnCap = 10

// printDanglingWarning warns that pruning compiled files left symlinks pointing
// at nothing and advises running link to clean them up. The path list is capped
// so a large prune does not flood the terminal. It is a no-op when nothing
// dangles. The apply command deliberately does not call this: it runs link
// immediately, so the dangle never surfaces.
func printDanglingWarning(w io.Writer, dangling []string) {
	if len(dangling) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w,
		"warning: %d symlink(s) now dangle because their compiled source was pruned:\n",
		len(dangling))
	shown := dangling
	if len(shown) > danglingWarnCap {
		shown = shown[:danglingWarnCap]
	}
	for _, p := range shown {
		_, _ = fmt.Fprintf(w, "  %s\n", p)
	}
	if len(dangling) > danglingWarnCap {
		_, _ = fmt.Fprintf(w, "  ... and %d more\n", len(dangling)-danglingWarnCap)
	}
	_, _ = fmt.Fprintln(w, "run 'dotsmith link' to remove the dangling symlinks")
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
		// Skip the state file. Compared case-insensitively so a case variant
		// (e.g. ".DOTSMITH.STATE") that folds onto the real state file on
		// case-insensitive filesystems is not mistaken for a compiled file.
		if strings.EqualFold(rel, state.FileName) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		refs = append(refs, linker.FileRef{
			RelPath:     rel,
			ContentHash: hash.Sum(data),
		})
		return nil
	})
	return refs, err //nolint:wrapcheck // WalkDir callback pre-wraps all errors with context
}
