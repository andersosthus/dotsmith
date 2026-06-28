// Package safepath holds the containment invariant shared by every dotsmith
// deletion sink.
//
// Both compile (pruning stale compiled files via its manifest) and link
// (removing orphaned symlinks and compiled sources, and tearing everything down
// on clean) delete files derived from relative paths recorded in state. Those
// relative paths must never escape the directory they are joined under, or a
// corrupt or hand-edited state file could cause dotsmith to remove files
// elsewhere on the machine. Keeping the join guard and the bounded empty-parent
// cleanup in one package means the invariant lives in exactly one place and
// cannot drift between compile and link.
//
// See ADR 0008 (re-assert the local-path invariant at the linker's deletion
// sinks) and ADR 0011 (verify a managed symlink before deleting it) for the
// lineage.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
)

// Join joins base and rel and confirms the result stays within base.
//
// It is the belt-and-suspenders guard for every deletion sink: state.Load
// already rejects non-local Source/Target paths, but any future producer of
// state entries that bypasses Load must not be able to escape base and delete
// arbitrary files. rel must be a local path (no leading "/" and no ".." escape);
// a non-local rel returns an error naming the offending path and joins nothing.
func Join(base, rel string) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf(
			"refusing non-local path %q under %s — entry escapes its directory",
			rel, base,
		)
	}
	return filepath.Join(base, rel), nil
}

// RemoveEmptyParents removes dir and its ancestors up to (but not including)
// stopAt, stopping at the first non-empty directory.
//
// Both dir and stopAt are cleaned before comparison so the stop point is
// honoured regardless of trailing slashes or other non-canonical formatting in
// the caller's paths. The climb also halts at the filesystem root as a final
// backstop. Removal errors (including a non-empty directory) end the climb
// silently — leaving a directory in place is never a failure.
//
// Each ancestor is Lstat'd before removal and the climb stops at the first
// symlink: os.Remove issues unlink(2), which acts on the symlink itself rather
// than the directory it points to, so removing a symlinked-directory ancestor
// would silently unlink a user's non-managed symlink (e.g. ~/.config pointing at
// a cloud-synced location) regardless of whether its target is empty. A
// symlinked directory is never something dotsmith created via MkdirAll, so it is
// always left in place. See ADR 0011's verify-then-disown invariant.
func RemoveEmptyParents(dir, stopAt string) {
	dir = filepath.Clean(dir)
	stopAt = filepath.Clean(stopAt)
	for dir != stopAt && dir != filepath.Dir(dir) {
		fi, err := os.Lstat(dir)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
