package compiler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andersosthus/dotsmith/internal/comment"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/hash"
	"github.com/andersosthus/dotsmith/internal/identity"
	"github.com/andersosthus/dotsmith/internal/safepath"
	"github.com/andersosthus/dotsmith/internal/state"
)

// CompileConfig holds the inputs for a compile operation.
type CompileConfig struct {
	// DotfilesDir is the root of the dotfiles repository.
	DotfilesDir string
	// Identity is the resolved identity for override layer selection.
	Identity identity.Identity
	// Identities is the resolved candidate identity set for decrypting
	// age-encrypted files. It is built once per run and shared across all files.
	Identities encrypt.IdentitySet
	// DryRun, when true, makes Compile probe each age-encrypted source for the
	// identity that would decrypt it instead of decrypting it. No passphrase is
	// requested and no key is unlocked; the probe outcomes are returned in
	// CompileResult.DryRunReports. Encrypted file content is left empty.
	DryRun bool
}

// CompiledFile represents a single file in the compiled output.
type CompiledFile struct {
	// RelPath is the path relative to the compile directory.
	RelPath string
	// Content is the assembled file content.
	Content []byte
	// ContentHash is the hex content hash of Content.
	ContentHash string
	// FromEncrypted is true if any source subfile was age-encrypted.
	FromEncrypted bool
}

// DryRunReport records, for one age-encrypted source file, which identity would
// decrypt it on this machine — gathered without unlocking or prompting.
type DryRunReport struct {
	// SourcePath is the .age source file that was probed.
	SourcePath string
	// Matched is true when a candidate identity would decrypt the file.
	Matched bool
	// IdentityPath is the source file of the matching identity (empty on no match).
	IdentityPath string
	// IdentityKind is the type label of the matching identity (empty on no match).
	IdentityKind string
}

// CompileResult holds all compiled files from a single compile run.
type CompileResult struct {
	Files []CompiledFile
	// DryRunReports holds one entry per age-encrypted source probed during a
	// dry-run compile, in discovery order. It is empty for a normal compile.
	DryRunReports []DryRunReport
}

// WriteConfig holds parameters for writing compiled output to disk.
type WriteConfig struct {
	// CompileDir is the directory to write compiled files into.
	CompileDir string
	// DryRun suppresses all writes when true.
	DryRun bool
}

// WriteStats reports what changed during a WriteCompiled call.
type WriteStats struct {
	// Written is the number of files actually written to disk.
	Written int
	// Unchanged is the number of files whose content was identical.
	Unchanged int
	// Pruned holds the relative paths of compiled files removed because their
	// source no longer exists (present in the previous manifest, absent from the
	// current result). Under DryRun these are the paths that would be pruned.
	Pruned []string
	// Dangling holds the subset of Pruned that still had a state.Symlinks entry
	// and will therefore leave a dangling symlink in the target directory until
	// link runs. It is computed by a read-only peek at the symlink state and
	// never mutates it.
	Dangling []string
}

// Compile discovers and assembles all dotfiles for the given configuration.
func Compile(ctx context.Context, cfg CompileConfig) (*CompileResult, error) {
	discovered, err := Discover(ctx, cfg.DotfilesDir, cfg.Identity)
	if err != nil {
		return nil, fmt.Errorf("compile: discover: %w", err)
	}

	result := &CompileResult{}
	for _, entry := range discovered {
		cf, reports, compileErr := compileEntry(ctx, entry, cfg)
		if compileErr != nil {
			return nil, fmt.Errorf("compile %s: %w", entry.Target, compileErr)
		}
		result.Files = append(result.Files, *cf)
		result.DryRunReports = append(result.DryRunReports, reports...)
	}
	return result, nil
}

// compileEntry assembles the content of a single FileEntry, returning any dry-run
// probe reports for age-encrypted sources it touched.
func compileEntry(ctx context.Context, entry *FileEntry, cfg CompileConfig) (*CompiledFile, []DryRunReport, error) {
	if entry.IsRegular {
		return compileRegular(ctx, entry, cfg)
	}
	return compileSubfiles(ctx, entry, cfg)
}

// compileRegular copies a regular (non-subfile) file as-is.
func compileRegular(ctx context.Context, entry *FileEntry, cfg CompileConfig) (*CompiledFile, []DryRunReport, error) {
	if len(entry.Subfiles) == 0 {
		return nil, nil, fmt.Errorf("regular file entry has no source")
	}
	sf := entry.Subfiles[0]
	content, report, err := sourceContent(ctx, sf, cfg)
	if err != nil {
		return nil, nil, err
	}
	cf := &CompiledFile{
		RelPath:       entry.Target,
		Content:       content,
		ContentHash:   hashContent(content),
		FromEncrypted: sf.Encrypted,
	}
	return cf, reportsOf(report), nil
}

// compileSubfiles assembles the content of a subfile target.
func compileSubfiles(ctx context.Context, entry *FileEntry, cfg CompileConfig) (*CompiledFile, []DryRunReport, error) {
	// Validate: check for duplicate subfile numbers (shouldn't happen after
	// Discover, but guard defensively).
	if err := validateNoDuplicates(entry); err != nil {
		return nil, nil, err
	}

	// Determine comment style from the target file extension.
	ext := strings.TrimPrefix(filepath.Ext(entry.Target), ".")
	style := comment.ForExtension(ext)

	var buf bytes.Buffer
	fromEncrypted := false
	var reports []DryRunReport

	for _, sf := range entry.Subfiles {
		content, report, err := sourceContent(ctx, sf, cfg)
		if err != nil {
			return nil, nil, err
		}
		if report != nil {
			reports = append(reports, *report)
		}
		if sf.Encrypted {
			fromEncrypted = true
		}

		if style != nil {
			header := comment.Header(style, sf.SourceName, sf.Layer)
			buf.WriteString(header)
		}
		buf.Write(content)
	}

	assembled := buf.Bytes()
	cf := &CompiledFile{
		RelPath:       entry.Target,
		Content:       assembled,
		ContentHash:   hashContent(assembled),
		FromEncrypted: fromEncrypted,
	}
	return cf, reports, nil
}

// sourceContent returns the content of a single subfile. For an unencrypted
// source it reads the file. For an encrypted source it decrypts normally, except
// under DryRun where it instead probes (without unlocking or prompting) which
// identity would decrypt the file, returning empty content and a DryRunReport.
func sourceContent(ctx context.Context, sf SubfileDesc, cfg CompileConfig) ([]byte, *DryRunReport, error) {
	if !sf.Encrypted {
		content, err := os.ReadFile(sf.SourcePath)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", sf.SourcePath, err)
		}
		return content, nil, nil
	}

	if cfg.DryRun {
		res, err := cfg.Identities.DryRunProbeFile(ctx, sf.SourcePath)
		if err != nil {
			return nil, nil, fmt.Errorf("dry-run decrypt %s: %w", sf.SourcePath, err)
		}
		return nil, &DryRunReport{
			SourcePath:   sf.SourcePath,
			Matched:      res.Matched,
			IdentityPath: res.Path,
			IdentityKind: res.Kind,
		}, nil
	}

	content, err := encrypt.DecryptFile(ctx, sf.SourcePath, cfg.Identities)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt %s: %w", sf.SourcePath, err)
	}
	return content, nil, nil
}

// reportsOf wraps an optional single report into a slice for the caller.
func reportsOf(r *DryRunReport) []DryRunReport {
	if r == nil {
		return nil
	}
	return []DryRunReport{*r}
}

// validateNoDuplicates returns an error if any subfile number appears twice.
func validateNoDuplicates(entry *FileEntry) error {
	seen := make(map[string]bool)
	for _, sf := range entry.Subfiles {
		if seen[sf.Number] {
			return fmt.Errorf(
				"compile %s: duplicate subfile number %s — rename one to resolve",
				entry.Target, sf.Number,
			)
		}
		seen[sf.Number] = true
	}
	return nil
}

// WriteCompiled makes the compile directory reflect result idempotently.
//
// It owns the compile directory: it prunes any compiled file present in the
// previous manifest (state.Compiled) but absent from result, then writes the
// current files (skipping those whose content is unchanged), then — on full
// success only — saves the new manifest. Pruning is scoped strictly to the
// compile directory and cleans now-empty parent directories within it.
//
// The returned WriteStats lists the pruned relative paths and the subset that
// still have a state.Symlinks entry (and will therefore dangle until link
// runs). The symlink state is read only; it is never mutated.
//
// Under DryRun nothing is written, no file is pruned, and no state is saved, but
// the prune and dangling sets are still computed and reported.
func WriteCompiled(ctx context.Context, result *CompileResult, cfg WriteConfig) (WriteStats, error) {
	s, err := state.Load(ctx, cfg.CompileDir)
	if err != nil {
		return WriteStats{}, fmt.Errorf("write compiled: load state: %w", err)
	}

	current := currentManifest(result)
	pruned := pruneSet(s.Compiled, current)
	dangling := danglingPaths(pruned, s.Symlinks)

	stats := WriteStats{Pruned: pruned, Dangling: dangling}
	if cfg.DryRun {
		return stats, nil
	}

	if err = ensureCompileDir(cfg.CompileDir); err != nil {
		return WriteStats{}, err
	}

	if err = prune(cfg.CompileDir, pruned); err != nil {
		return WriteStats{}, err
	}

	for _, cf := range result.Files {
		changed, writeErr := writeCompiledFile(ctx, cf, cfg)
		if writeErr != nil {
			return WriteStats{}, writeErr
		}
		if changed {
			stats.Written++
		} else {
			stats.Unchanged++
		}
	}

	// Save the manifest only after every file is on disk, so it never claims a
	// file that was not written.
	s.Compiled = current
	if err = stateSaveFunc(ctx, s, cfg.CompileDir); err != nil {
		return WriteStats{}, fmt.Errorf("write compiled: save state: %w", err)
	}
	return stats, nil
}

// stateSaveFunc is the manifest-saving sink, injectable so tests can exercise
// the save-failure path that the always-writable compile dir otherwise hides.
var stateSaveFunc = state.Save

// ensureCompileDir creates the compile directory and enforces its 0700 mode.
func ensureCompileDir(compileDir string) error {
	if err := os.MkdirAll(compileDir, 0o700); err != nil {
		return fmt.Errorf("create compile dir %s: %w", compileDir, err)
	}
	// MkdirAll does not tighten a pre-existing directory; chmod explicitly so a
	// loose compile dir (e.g. 0755 from an older run) is brought to 0700.
	if err := os.Chmod(compileDir, 0o700); err != nil {
		return fmt.Errorf("chmod compile dir %s: %w", compileDir, err)
	}
	return nil
}

// currentManifest builds the manifest map for the current compile result.
func currentManifest(result *CompileResult) map[string]state.CompiledEntry {
	m := make(map[string]state.CompiledEntry, len(result.Files))
	for _, cf := range result.Files {
		m[cf.RelPath] = state.CompiledEntry{ContentHash: cf.ContentHash}
	}
	return m
}

// pruneSet returns the relative paths present in the previous manifest but
// absent from the current compile result. The result is sorted for stable,
// deterministic reporting (so two dry-runs report identically).
func pruneSet(previous, current map[string]state.CompiledEntry) []string {
	var pruned []string
	for relPath := range previous {
		if _, ok := current[relPath]; !ok {
			pruned = append(pruned, relPath)
		}
	}
	sort.Strings(pruned)
	return pruned
}

// danglingPaths returns the subset of pruned paths that still have a symlink
// entry and will therefore leave a dangling symlink until link runs. The
// symlinks map is read only and never mutated. The result preserves the sorted
// order of pruned.
func danglingPaths(pruned []string, symlinks map[string]state.SymlinkEntry) []string {
	var dangling []string
	for _, relPath := range pruned {
		if _, ok := symlinks[relPath]; ok {
			dangling = append(dangling, relPath)
		}
	}
	return dangling
}

// prune removes each pruned compiled file from compileDir and cleans the
// now-empty parent directories within it. Each relative path is joined under the
// compile directory with a containment check so a state entry that bypassed
// state.Load cannot escape and delete files elsewhere.
func prune(compileDir string, pruned []string) error {
	for _, relPath := range pruned {
		path, err := safepath.Join(compileDir, relPath)
		if err != nil {
			return fmt.Errorf("prune compiled: %w", err)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prune compiled %s: %w", path, err)
		}
		safepath.RemoveEmptyParents(filepath.Dir(path), compileDir)
	}
	return nil
}

// writeCompiledFile writes a single compiled file. Returns true if the file was
// written (new or changed content), false if content was already up to date.
//
// It is only called on the non-dry-run path: WriteCompiled returns before
// writing anything under DryRun.
func writeCompiledFile(_ context.Context, cf CompiledFile, cfg WriteConfig) (bool, error) {
	destPath := filepath.Join(cfg.CompileDir, cf.RelPath)

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return false, fmt.Errorf("create dir %s: %w", destDir, err)
	}

	perm := compiledFileMode(cf)

	// Check existing content to avoid unnecessary writes.
	existing, readErr := os.ReadFile(destPath)
	if readErr == nil && hashContent(existing) == cf.ContentHash {
		// Content is unchanged, but a pre-existing file may carry a stale,
		// looser mode (os.WriteFile only applies perm on creation). Repair the
		// mode for encrypted-derived files so the 0600 guarantee always holds.
		if cf.FromEncrypted {
			if err := os.Chmod(destPath, perm); err != nil {
				return false, fmt.Errorf("chmod %s: %w", destPath, err)
			}
		}
		return false, nil // unchanged
	}

	if err := os.WriteFile(destPath, cf.Content, perm); err != nil {
		return false, fmt.Errorf("write %s: %w", destPath, err)
	}
	// os.WriteFile leaves the mode untouched when the file already exists, so
	// chmod explicitly to enforce the intended mode on the changed path too.
	if err := os.Chmod(destPath, perm); err != nil {
		return false, fmt.Errorf("chmod %s: %w", destPath, err)
	}
	return true, nil
}

// compiledFileMode returns the file mode a compiled file should carry: 0600 for
// files derived from an encrypted source, 0644 otherwise.
func compiledFileMode(cf CompiledFile) os.FileMode {
	if cf.FromEncrypted {
		return 0o600
	}
	return 0o644
}

// hashContent returns the content digest of content via the shared hash helper.
func hashContent(content []byte) string {
	return hash.Sum(content)
}
