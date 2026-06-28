// Package linker creates, updates, and removes symlinks from the compiled
// output directory to the target directory.
package linker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andersosthus/dotsmith/internal/hash"
	"github.com/andersosthus/dotsmith/internal/state"
)

// LinkConfig holds parameters for link operations.
type LinkConfig struct {
	// CompileDir is the directory containing compiled dotfiles.
	CompileDir string
	// TargetDir is the directory in which symlinks are created (typically ~).
	TargetDir string
	// DryRun suppresses all filesystem writes when true.
	DryRun bool
}

// FileRef references a compiled file by relative path and content hash.
type FileRef struct {
	// RelPath is the relative path within CompileDir (and TargetDir).
	RelPath string
	// ContentHash is the hex-encoded SHA-256 hash of the compiled content.
	ContentHash string
}

// LinkResult reports what changed during a Link call.
type LinkResult struct {
	// Created is the number of newly created symlinks.
	Created int
	// Updated is the number of symlinks whose state hash was refreshed.
	Updated int
	// Unchanged is the number of symlinks already up to date.
	Unchanged int
	// Removed is the number of orphaned symlinks and compiled files removed.
	Removed int
}

// StatusKind classifies the current state of a managed symlink.
type StatusKind string

const (
	// StatusMissing means the symlink does not exist.
	StatusMissing StatusKind = "missing"
	// StatusCorrect means the symlink is present and the content is up to date.
	StatusCorrect StatusKind = "correct"
	// StatusStale means the symlink is present but the compiled content changed.
	StatusStale StatusKind = "stale"
	// StatusConflict means the path exists but is not our symlink.
	StatusConflict StatusKind = "conflict"
)

// StatusEntry reports the status of a single managed path.
type StatusEntry struct {
	// RelPath is the relative path of the managed file.
	RelPath string
	// Kind is the current status classification.
	Kind StatusKind
}

// linkChange classifies the outcome of linking a single file.
type linkChange int

const (
	linkUnchanged linkChange = iota
	linkCreated
	linkUpdated
)

// Injectable OS functions for testing error paths.
var (
	osMkdirAllFunc = os.MkdirAll
	osSymlinkFunc  = os.Symlink
	osRemoveFunc   = os.Remove
	osReadlinkFunc = os.Readlink
	osLstatFunc    = os.Lstat
)

// Link creates or refreshes symlinks from compiled files to TargetDir.
// Conflicts (target exists but is not a symlink to the source) return an error.
// Orphans (state entries with no corresponding file in files) are removed.
func Link(ctx context.Context, cfg LinkConfig, files []FileRef) (*LinkResult, error) {
	s, err := state.Load(ctx, cfg.CompileDir)
	if err != nil {
		return nil, fmt.Errorf("link: load state: %w", err)
	}

	currentFiles := make(map[string]struct{}, len(files))
	for _, f := range files {
		currentFiles[f.RelPath] = struct{}{}
	}

	result := &LinkResult{}
	for _, f := range files {
		if err = linkFile(cfg, f, s, result); err != nil {
			return nil, err
		}
	}

	if err = removeOrphans(cfg, s, currentFiles, result); err != nil {
		return nil, err
	}

	if !cfg.DryRun {
		if err = state.Save(ctx, s, cfg.CompileDir); err != nil {
			return nil, fmt.Errorf("link: save state: %w", err)
		}
	}
	return result, nil
}

// safeJoin joins base and rel and confirms the result stays within base. It is
// the belt-and-suspenders guard for every deletion sink: state.Load already
// rejects non-local Source/Target paths, but any future producer of state
// entries that bypasses Load must not be able to escape base and delete
// arbitrary files. rel must be a local path (no leading "/" or ".." escape).
func safeJoin(base, rel string) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf(
			"refusing non-local path %q under %s — entry escapes its directory",
			rel, base,
		)
	}
	return filepath.Join(base, rel), nil
}

// removeOrphans removes symlinks, compiled files, and state entries for paths
// present in state but absent from currentFiles. The Source/Target paths are
// re-validated as local before each os.Remove so a state entry that bypassed
// state.Load's containment check cannot delete files outside TargetDir/CompileDir.
func removeOrphans(
	cfg LinkConfig,
	s *state.State,
	currentFiles map[string]struct{},
	r *LinkResult,
) error {
	for relPath, entry := range s.Symlinks {
		if _, ok := currentFiles[relPath]; ok {
			continue
		}
		if cfg.DryRun {
			r.Removed++
			continue
		}
		if err := removeOrphanEntry(cfg, entry); err != nil {
			return err
		}
		delete(s.Symlinks, relPath)
		r.Removed++
	}
	return nil
}

// removeOrphanEntry deletes the symlink and compiled source file for a single
// orphaned state entry. Both paths are re-validated as local before os.Remove.
func removeOrphanEntry(cfg LinkConfig, entry state.SymlinkEntry) error {
	targetPath, err := safeJoin(cfg.TargetDir, entry.Target)
	if err != nil {
		return fmt.Errorf("remove orphan symlink: %w", err)
	}
	sourcePath, err := safeJoin(cfg.CompileDir, entry.Source)
	if err != nil {
		return fmt.Errorf("remove orphan compiled: %w", err)
	}
	if err := osRemoveFunc(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove orphan symlink %s: %w", targetPath, err)
	}
	removeEmptyParents(filepath.Dir(targetPath), cfg.TargetDir)
	if err := osRemoveFunc(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove orphan compiled %s: %w", sourcePath, err)
	}
	return nil
}

// linkFile processes a single FileRef within a Link call.
func linkFile(cfg LinkConfig, f FileRef, s *state.State, r *LinkResult) error {
	sourcePath := filepath.Join(cfg.CompileDir, f.RelPath)
	targetPath := filepath.Join(cfg.TargetDir, f.RelPath)
	changed, err := linkOne(cfg, f, sourcePath, targetPath, s)
	if err != nil {
		return fmt.Errorf("link %s: %w", f.RelPath, err)
	}
	switch changed {
	case linkCreated:
		r.Created++
	case linkUpdated:
		r.Updated++
	default:
		r.Unchanged++
	}
	return nil
}

// linkOne determines the action for a single target path and applies it.
func linkOne(cfg LinkConfig, f FileRef, sourcePath, targetPath string, s *state.State) (linkChange, error) {
	fi, statErr := osLstatFunc(targetPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return linkUnchanged, fmt.Errorf("stat %s: %w", targetPath, statErr)
	}
	if errors.Is(statErr, os.ErrNotExist) {
		return linkNew(cfg, f, sourcePath, targetPath, s)
	}
	return linkExisting(cfg, f, sourcePath, targetPath, s, fi)
}

// linkNew creates a new symlink for a target that does not yet exist.
func linkNew(cfg LinkConfig, f FileRef, sourcePath, targetPath string, s *state.State) (linkChange, error) {
	if !cfg.DryRun {
		if err := osMkdirAllFunc(filepath.Dir(targetPath), 0o755); err != nil {
			return linkUnchanged, fmt.Errorf("create dir: %w", err)
		}
		if err := osSymlinkFunc(sourcePath, targetPath); err != nil {
			return linkUnchanged, fmt.Errorf("symlink: %w", err)
		}
		s.Symlinks[f.RelPath] = state.SymlinkEntry{
			Source: f.RelPath, Target: f.RelPath, ContentHash: f.ContentHash,
		}
	}
	return linkCreated, nil
}

// linkExisting handles a target path that already exists.
func linkExisting(
	cfg LinkConfig,
	f FileRef,
	sourcePath, targetPath string,
	s *state.State,
	fi os.FileInfo,
) (linkChange, error) {
	if fi.Mode()&os.ModeSymlink == 0 {
		return linkUnchanged, fmt.Errorf("conflict: %s exists and is not a symlink", targetPath)
	}
	existing, err := osReadlinkFunc(targetPath)
	if err != nil {
		return linkUnchanged, fmt.Errorf("readlink %s: %w", targetPath, err)
	}
	if existing != sourcePath {
		return linkUnchanged, fmt.Errorf(
			"conflict: %s points to %s, expected %s", targetPath, existing, sourcePath,
		)
	}

	// Symlink is correct. Return early if hash still matches.
	entry, ok := s.Symlinks[f.RelPath]
	if ok && entry.ContentHash == f.ContentHash {
		return linkUnchanged, nil
	}

	// Hash changed (stale) or entry absent — refresh state.
	if !cfg.DryRun {
		s.Symlinks[f.RelPath] = state.SymlinkEntry{
			Source: f.RelPath, Target: f.RelPath, ContentHash: f.ContentHash,
		}
	}
	return linkUpdated, nil
}

// Status reports the state of all managed symlinks recorded in the state file.
func Status(ctx context.Context, cfg LinkConfig) ([]StatusEntry, error) {
	s, err := state.Load(ctx, cfg.CompileDir)
	if err != nil {
		return nil, fmt.Errorf("status: load state: %w", err)
	}

	entries := make([]StatusEntry, 0, len(s.Symlinks))
	for relPath, entry := range s.Symlinks {
		sourcePath := filepath.Join(cfg.CompileDir, entry.Source)
		targetPath := filepath.Join(cfg.TargetDir, entry.Target)
		kind := statusOne(sourcePath, targetPath, entry.ContentHash)
		entries = append(entries, StatusEntry{RelPath: relPath, Kind: kind})
	}
	return entries, nil
}

// statusOne classifies a single symlink against its expected state.
func statusOne(sourcePath, targetPath, stateHash string) StatusKind {
	fi, err := osLstatFunc(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StatusMissing
		}
		return StatusConflict
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return StatusConflict
	}
	existing, err := osReadlinkFunc(targetPath)
	if err != nil {
		return StatusConflict
	}
	if existing != sourcePath {
		return StatusConflict
	}

	current, err := os.ReadFile(sourcePath)
	if err != nil {
		return StatusStale
	}
	if hashBytes(current) == stateHash {
		return StatusCorrect
	}
	return StatusStale
}

// Clean removes all managed symlinks, empty parent directories, and compiled
// source files. In dry-run mode no changes are made.
func Clean(ctx context.Context, cfg LinkConfig) error {
	s, err := state.Load(ctx, cfg.CompileDir)
	if err != nil {
		return fmt.Errorf("clean: load state: %w", err)
	}

	if !cfg.DryRun {
		if err = cleanSymlinks(cfg, s); err != nil {
			return err
		}
		s = state.New()
		if err = state.Save(ctx, s, cfg.CompileDir); err != nil {
			return fmt.Errorf("clean: save state: %w", err)
		}
	}
	return nil
}

// cleanSymlinks removes each managed symlink and its compiled source file. The
// Source/Target paths are re-validated as local before each os.Remove so a state
// entry that bypassed state.Load's containment check cannot delete files outside
// TargetDir/CompileDir.
func cleanSymlinks(cfg LinkConfig, s *state.State) error {
	for _, entry := range s.Symlinks {
		targetPath, err := safeJoin(cfg.TargetDir, entry.Target)
		if err != nil {
			return fmt.Errorf("clean: remove symlink: %w", err)
		}
		sourcePath, err := safeJoin(cfg.CompileDir, entry.Source)
		if err != nil {
			return fmt.Errorf("clean: remove compiled: %w", err)
		}

		if err := osRemoveFunc(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean: remove symlink %s: %w", targetPath, err)
		}
		removeEmptyParents(filepath.Dir(targetPath), cfg.TargetDir)
		if err := osRemoveFunc(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean: remove compiled %s: %w", sourcePath, err)
		}
	}
	return nil
}

// removeEmptyParents removes dir and its ancestors up to (but not including)
// stopAt, stopping at the first non-empty directory. Both dir and stopAt are
// cleaned before comparison so the stop point is honoured regardless of
// trailing slashes or other non-canonical formatting in the caller's paths.
func removeEmptyParents(dir, stopAt string) {
	dir = filepath.Clean(dir)
	stopAt = filepath.Clean(stopAt)
	for dir != stopAt && dir != filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// hashBytes returns the content digest of data via the shared hash helper.
func hashBytes(data []byte) string {
	return hash.Sum(data)
}
