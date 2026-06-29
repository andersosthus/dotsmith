// Package linker creates, updates, and removes symlinks from the compiled
// output directory to the target directory.
package linker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/andersosthus/dotsmith/internal/hash"
	"github.com/andersosthus/dotsmith/internal/safepath"
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
	// Disowned lists relative paths whose target was not a dotsmith-managed
	// symlink (e.g. replaced by a real file of the user's). The user's file is
	// left untouched while dotsmith forgets the path: its compiled artifact is
	// removed and its state entry dropped. The CLI warns about these paths.
	Disowned []string
	// Blockers lists the per-file reasons a dry-run would have prevented linking,
	// collected instead of aborting on the first one. It is always empty for a
	// real (non-dry-run) run, which still fails fast on the first conflict.
	// Blockers are sorted by RelPath for deterministic output.
	Blockers []Blocker
}

// BlockerKind classifies why a single file could not be linked.
type BlockerKind string

const (
	// BlockerConflict means the target is occupied by something that is not our
	// managed symlink: a real file, a symlink pointing elsewhere, or a symlinked
	// parent directory. This mirrors the StatusConflict classification.
	BlockerConflict BlockerKind = "conflict"
)

// Blocker is a single reason one file could not be linked during a dry-run.
type Blocker struct {
	// RelPath is the relative path of the file that could not be linked.
	RelPath string
	// Kind classifies the blocker (currently only conflict).
	Kind BlockerKind
	// Detail is the human-readable leaf message describing the problem. It is
	// byte-identical to the message a real run would surface for the same file.
	Detail string
}

// blockerError is the typed error returned by the conflict leaf paths. It is the
// single classification site: its Error() reproduces today's exact leaf message
// so a real run is byte-identical, while Unwrap() preserves any wrapped OS error
// so errors.Is checks still hold. In dry-run, linkFile extracts it via errors.As
// to build a Blocker and continue; otherwise the error propagates and the real
// run aborts as before.
type blockerError struct {
	kind BlockerKind
	msg  string
	err  error
}

// Error returns the leaf message verbatim.
func (e *blockerError) Error() string { return e.msg }

// Unwrap returns the wrapped OS error, if any.
func (e *blockerError) Unwrap() error { return e.err }

// CleanResult reports what happened during a Clean call.
type CleanResult struct {
	// Disowned lists relative paths whose target was not a dotsmith-managed
	// symlink and so was left untouched. The compiled artifact is still removed
	// and the state entry dropped. The CLI warns about these paths.
	Disowned []string
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

	// Sort blockers by relative path so dry-run output and tests are
	// deterministic regardless of the order files were processed.
	sort.Slice(result.Blockers, func(i, j int) bool {
		return result.Blockers[i].RelPath < result.Blockers[j].RelPath
	})
	return result, nil
}

// removeOrphans removes symlinks, compiled files, and state entries for paths
// present in state but absent from currentFiles. The Source/Target paths are
// re-validated as local before each os.Remove so a state entry that bypassed
// state.Load's containment check cannot delete files outside TargetDir/CompileDir.
//
// Before deleting a target it verifies the target really is a dotsmith-managed
// symlink (lstat + is-symlink + readlink resolves to the expected compiled
// source). When it is not — e.g. the user replaced it with a real file — the
// path is disowned: the user's file is left untouched, but the compiled artifact
// is removed and the state entry dropped so dotsmith stops tracking it.
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
		disowned, err := removeOrphanEntry(cfg, entry)
		if err != nil {
			return err
		}
		delete(s.Symlinks, relPath)
		if disowned {
			r.Disowned = append(r.Disowned, relPath)
		} else {
			r.Removed++
		}
	}
	return nil
}

// targetKind classifies a target path at a deletion sink.
type targetKind int

const (
	// targetManaged means the target is a dotsmith-managed symlink resolving to
	// the expected compiled source and is safe to delete.
	targetManaged targetKind = iota
	// targetAbsent means the target does not exist; nothing to delete or disown.
	targetAbsent
	// targetForeign means the target exists but is not our symlink (a real file,
	// or a symlink pointing elsewhere). The user's file must be left untouched
	// and the path disowned.
	targetForeign
)

// removeOrphanEntry deletes the symlink and compiled source file for a single
// orphaned state entry. Both paths are re-validated as local before os.Remove.
//
// It returns disowned=true when the target exists but is not a dotsmith-managed
// symlink and so was left in place; in that case only the compiled artifact is
// removed.
func removeOrphanEntry(cfg LinkConfig, entry state.SymlinkEntry) (bool, error) {
	targetPath, err := safepath.Join(cfg.TargetDir, entry.Target)
	if err != nil {
		return false, fmt.Errorf("remove orphan symlink: %w", err)
	}
	sourcePath, err := safepath.Join(cfg.CompileDir, entry.Source)
	if err != nil {
		return false, fmt.Errorf("remove orphan compiled: %w", err)
	}

	kind, err := classifyTarget(targetPath, sourcePath)
	if err != nil {
		return false, fmt.Errorf("remove orphan symlink %s: %w", targetPath, err)
	}
	if kind == targetManaged {
		if err := osRemoveFunc(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("remove orphan symlink %s: %w", targetPath, err)
		}
		safepath.RemoveEmptyParents(filepath.Dir(targetPath), cfg.TargetDir)
	}
	if err := osRemoveFunc(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove orphan compiled %s: %w", sourcePath, err)
	}
	return kind == targetForeign, nil
}

// classifyTarget inspects targetPath and classifies it for the deletion sinks.
// It is the shared verify-then-disown predicate applied at both linker deletion
// sinks (orphan removal during Link, and cleanSymlinks during Clean).
//
// It mirrors the linkExisting guard: lstat the path, confirm it is a symlink,
// then readlink and compare the link's target string to expectedSource. The
// comparison is on the stored target string, so it still holds after the
// compiled source has been pruned from disk.
//
//   - A non-existent target is targetAbsent (already gone — a clean removal, not
//     a conflict to warn about).
//   - A regular file, or a symlink pointing elsewhere, is targetForeign and must
//     be left untouched (disowned).
//   - A symlink resolving to expectedSource is targetManaged and safe to delete.
//
// An unexpected lstat/readlink error (other than not-exist) is returned to the
// caller rather than swallowed, so a transient failure never causes a silent
// disown.
func classifyTarget(targetPath, expectedSource string) (targetKind, error) {
	fi, err := osLstatFunc(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return targetAbsent, nil
		}
		return targetAbsent, fmt.Errorf("lstat %s: %w", targetPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return targetForeign, nil
	}
	existing, err := osReadlinkFunc(targetPath)
	if err != nil {
		return targetAbsent, fmt.Errorf("readlink %s: %w", targetPath, err)
	}
	if existing != expectedSource {
		return targetForeign, nil
	}
	return targetManaged, nil
}

// linkFile processes a single FileRef within a Link call.
//
// In dry-run mode a per-file blockerError is collected as a Blocker and the run
// continues, so a dry-run surfaces every blocker in one pass. In a real run any
// error (blocker or not) is returned and the call aborts on the first one,
// preserving today's fail-fast behaviour and byte-identical messages.
func linkFile(cfg LinkConfig, f FileRef, s *state.State, r *LinkResult) error {
	sourcePath := filepath.Join(cfg.CompileDir, f.RelPath)
	targetPath := filepath.Join(cfg.TargetDir, f.RelPath)
	changed, err := linkOne(cfg, f, sourcePath, targetPath, s)
	if err != nil {
		var be *blockerError
		if cfg.DryRun && errors.As(err, &be) {
			r.Blockers = append(r.Blockers, Blocker{
				RelPath: f.RelPath,
				Kind:    be.kind,
				Detail:  be.Error(),
			})
			return nil
		}
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
	if err := guardSymlinkedParents(cfg.TargetDir, targetPath); err != nil {
		return linkUnchanged, err
	}
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

// guardSymlinkedParents refuses to create a managed link whose target sits below
// a symlinked directory component between targetDir and the link's parent.
//
// os.MkdirAll silently follows a symlinked ancestor, planting the managed link
// inside the directory the user's symlink points to (e.g. ~/.config pointing at a
// cloud-synced location). That couples dotsmith's deletion sinks to a path the
// user owns: subsequent orphan removal or clean would climb back through the
// symlink. Treating a symlinked-dir ancestor as a conflict — mirroring
// linkExisting's "exists and is not a symlink" guard — keeps managed links out of
// user-owned symlinked directories in the first place. The leaf (targetPath) is
// not inspected here: it is known absent (linkNew is the not-exist branch) and a
// symlinked leaf is handled by linkExisting.
func guardSymlinkedParents(targetDir, targetPath string) error {
	targetDir = filepath.Clean(targetDir)
	dir := filepath.Dir(targetPath)
	for dir != targetDir && dir != filepath.Dir(dir) {
		fi, err := osLstatFunc(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				dir = filepath.Dir(dir)
				continue
			}
			return fmt.Errorf("stat parent %s: %w", dir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return &blockerError{
				kind: BlockerConflict,
				msg: fmt.Sprintf(
					"conflict: parent %s is a symlink — refusing to plant a managed link inside it; "+
						"replace the symlink with a real directory or move the target out from under it",
					dir,
				),
			}
		}
		dir = filepath.Dir(dir)
	}
	return nil
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
		return linkUnchanged, &blockerError{
			kind: BlockerConflict,
			msg:  fmt.Sprintf("conflict: %s exists and is not a symlink", targetPath),
		}
	}
	existing, err := osReadlinkFunc(targetPath)
	if err != nil {
		return linkUnchanged, fmt.Errorf("readlink %s: %w", targetPath, err)
	}
	if existing != sourcePath {
		return linkUnchanged, &blockerError{
			kind: BlockerConflict,
			msg:  fmt.Sprintf("conflict: %s points to %s, expected %s", targetPath, existing, sourcePath),
		}
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
// source files, then zeroes both state fields (Symlinks and Compiled). In
// dry-run mode no changes are made.
//
// It removes every compiled file recorded in the manifest (state.Compiled), not
// only those that had a symlink entry, so a file that was compiled but never
// linked is also torn down and the compile directory is left holding no compiled
// files (only the state file).
//
// A target that is no longer a dotsmith-managed symlink (lstat + is-symlink +
// readlink resolves to the expected source) is left untouched; its compiled
// artifact is still removed and it is reported in CleanResult.Disowned so the
// CLI can warn that a substituted file was left in place.
func Clean(ctx context.Context, cfg LinkConfig) (*CleanResult, error) {
	s, err := state.Load(ctx, cfg.CompileDir)
	if err != nil {
		return nil, fmt.Errorf("clean: load state: %w", err)
	}

	result := &CleanResult{}
	if !cfg.DryRun {
		removed, err := cleanSymlinks(cfg, s, result)
		if err != nil {
			return nil, err
		}
		if err = cleanCompiled(cfg, s, removed); err != nil {
			return nil, err
		}
		s = state.New()
		if err = state.Save(ctx, s, cfg.CompileDir); err != nil {
			return nil, fmt.Errorf("clean: save state: %w", err)
		}
	}
	return result, nil
}

// cleanSymlinks removes each managed symlink and its compiled source file. The
// Source/Target paths are re-validated as local before each os.Remove so a state
// entry that bypassed state.Load's containment check cannot delete files outside
// TargetDir/CompileDir.
//
// Each target is verified as a dotsmith-managed symlink before removal. A target
// that is not (e.g. replaced by a real file of the user's) is left untouched and
// recorded in result.Disowned; its compiled artifact is still removed.
//
// It returns the set of compiled Source paths it removed so the manifest sweep
// (cleanCompiled) can skip them and only delete compiled files that were never
// linked.
func cleanSymlinks(cfg LinkConfig, s *state.State, result *CleanResult) (map[string]struct{}, error) {
	removed := make(map[string]struct{}, len(s.Symlinks))
	for relPath, entry := range s.Symlinks {
		disowned, err := cleanSymlinkEntry(cfg, entry)
		if err != nil {
			return nil, err
		}
		removed[entry.Source] = struct{}{}
		if disowned {
			result.Disowned = append(result.Disowned, relPath)
		}
	}
	return removed, nil
}

// cleanCompiled removes every compiled file recorded in the manifest
// (s.Compiled) that cleanSymlinks did not already remove. This is what lets
// clean empty the compile directory of files that were compiled but never
// linked — cleanSymlinks only reaches files with a corresponding symlink entry.
//
// Each manifest key is re-validated as local under CompileDir before os.Remove,
// so a state file that bypassed state.Load's containment check cannot delete
// files outside the compile directory. A missing file is not an error (it may
// have already been pruned). After removal, now-empty parent directories within
// the compile directory are cleaned up.
func cleanCompiled(cfg LinkConfig, s *state.State, alreadyRemoved map[string]struct{}) error {
	for relPath := range s.Compiled {
		if _, done := alreadyRemoved[relPath]; done {
			continue
		}
		sourcePath, err := safepath.Join(cfg.CompileDir, relPath)
		if err != nil {
			return fmt.Errorf("clean: remove compiled: %w", err)
		}
		if err := osRemoveFunc(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean: remove compiled %s: %w", sourcePath, err)
		}
		safepath.RemoveEmptyParents(filepath.Dir(sourcePath), cfg.CompileDir)
	}
	return nil
}

// cleanSymlinkEntry removes the symlink and compiled source for a single managed
// entry during Clean. Both paths are re-validated as local before os.Remove.
//
// It returns disowned=true when the target exists but is not a dotsmith-managed
// symlink and so was left in place; the compiled artifact is removed regardless.
func cleanSymlinkEntry(cfg LinkConfig, entry state.SymlinkEntry) (bool, error) {
	targetPath, err := safepath.Join(cfg.TargetDir, entry.Target)
	if err != nil {
		return false, fmt.Errorf("clean: remove symlink: %w", err)
	}
	sourcePath, err := safepath.Join(cfg.CompileDir, entry.Source)
	if err != nil {
		return false, fmt.Errorf("clean: remove compiled: %w", err)
	}

	kind, err := classifyTarget(targetPath, sourcePath)
	if err != nil {
		return false, fmt.Errorf("clean: remove symlink %s: %w", targetPath, err)
	}
	if kind == targetManaged {
		if err := osRemoveFunc(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("clean: remove symlink %s: %w", targetPath, err)
		}
		safepath.RemoveEmptyParents(filepath.Dir(targetPath), cfg.TargetDir)
	}
	if err := osRemoveFunc(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("clean: remove compiled %s: %w", sourcePath, err)
	}
	return kind == targetForeign, nil
}

// hashBytes returns the content digest of data via the shared hash helper.
func hashBytes(data []byte) string {
	return hash.Sum(data)
}
