package compiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andersosthus/dotsmith/internal/comment"
	"github.com/andersosthus/dotsmith/internal/encrypt"
	"github.com/andersosthus/dotsmith/internal/identity"
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
	// ContentHash is the hex SHA-256 of Content.
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

// WriteCompiled writes compiled files to compileDir idempotently.
// Files whose content has not changed are not rewritten.
func WriteCompiled(ctx context.Context, result *CompileResult, cfg WriteConfig) (WriteStats, error) {
	if !cfg.DryRun {
		if err := os.MkdirAll(cfg.CompileDir, 0o700); err != nil {
			return WriteStats{}, fmt.Errorf("create compile dir %s: %w", cfg.CompileDir, err)
		}
	}

	var stats WriteStats
	for _, cf := range result.Files {
		changed, err := writeCompiledFile(ctx, cf, cfg)
		if err != nil {
			return stats, err
		}
		if changed {
			stats.Written++
		} else {
			stats.Unchanged++
		}
	}
	return stats, nil
}

// writeCompiledFile writes a single compiled file. Returns true if the file was
// written (new or changed content), false if content was already up to date.
func writeCompiledFile(_ context.Context, cf CompiledFile, cfg WriteConfig) (bool, error) {
	destPath := filepath.Join(cfg.CompileDir, cf.RelPath)

	if cfg.DryRun {
		return true, nil // treat as "would write" in dry-run
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return false, fmt.Errorf("create dir %s: %w", destDir, err)
	}

	// Check existing content to avoid unnecessary writes.
	existing, readErr := os.ReadFile(destPath)
	if readErr == nil && hashContent(existing) == cf.ContentHash {
		return false, nil // unchanged
	}

	perm := os.FileMode(0o644)
	if cf.FromEncrypted {
		perm = 0o600
	}
	if err := os.WriteFile(destPath, cf.Content, perm); err != nil {
		return false, fmt.Errorf("write %s: %w", destPath, err)
	}
	return true, nil
}

// hashContent returns the hex-encoded SHA-256 hash of content.
func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
