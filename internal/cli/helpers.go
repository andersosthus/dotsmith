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
	// SourcePath is a repo-controlled .age path whose basename may contain
	// terminal control sequences; render repo-controlled paths with %q so
	// escapes, CRs, and newlines cannot manipulate or spoof the terminal.
	for _, r := range reports {
		if r.Matched {
			_, _ = fmt.Fprintf(w, "would decrypt %q -> %q [%s]\n", r.SourcePath, r.IdentityPath, r.IdentityKind)
			continue
		}
		_, _ = fmt.Fprintf(w,
			"would NOT decrypt %q -> no identity on this machine matches any recipient\n",
			r.SourcePath)
	}
}

// printDryRunReuse annotates, under dry-run, which compiled targets a real
// compile would reuse versus recompile (user story 12). It prints one line per
// target plus a summary count, so the user understands what a real run would do
// without changing anything. It is a no-op when there are no files. This is
// distinct from the key-health probe (printDryRunReports): the probe runs
// unconditionally for every encrypted source regardless of reuse (ADR 0004),
// whereas this annotation reports the reuse decision the probe does not affect.
func printDryRunReuse(w io.Writer, files []compiler.CompiledFile) {
	if len(files) == 0 {
		return
	}
	reuse := 0
	for _, f := range files {
		// RelPath is a repo-controlled target path; render with %q so any
		// terminal control sequences in it cannot manipulate or spoof the
		// terminal (same reasoning as printDryRunReports).
		if f.WouldReuse {
			reuse++
			_, _ = fmt.Fprintf(w, "would reuse %q\n", f.RelPath)
			continue
		}
		_, _ = fmt.Fprintf(w, "would recompile %q\n", f.RelPath)
	}
	_, _ = fmt.Fprintf(w, "dry-run: %d would reuse, %d would recompile\n",
		reuse, len(files)-reuse)
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

// warnDisowned warns that one or more managed paths were left untouched because
// they are no longer dotsmith-managed symlinks (e.g. the user replaced a symlink
// with a real file of their own). dotsmith has stopped tracking those paths:
// their compiled artifact was removed and their state entry dropped, so the
// warning does not recur. The path list is capped to avoid flooding the
// terminal. It is a no-op when nothing was disowned.
func warnDisowned(w io.Writer, disowned []string) {
	if len(disowned) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w,
		"warning: %d path(s) left untouched because they are no longer dotsmith-managed symlinks; "+
			"dotsmith has stopped tracking them:\n",
		len(disowned))
	shown := disowned
	if len(shown) > danglingWarnCap {
		shown = shown[:danglingWarnCap]
	}
	for _, p := range shown {
		_, _ = fmt.Fprintf(w, "  %s\n", p)
	}
	if len(disowned) > danglingWarnCap {
		_, _ = fmt.Fprintf(w, "  ... and %d more\n", len(disowned)-danglingWarnCap)
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
