package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJoin verifies Join accepts local paths and refuses non-local ones.
func TestJoin(t *testing.T) {
	base := t.TempDir()
	tests := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{name: "simple local", rel: ".bashrc", wantErr: false},
		{name: "nested local", rel: filepath.Join("a", "b", "c"), wantErr: false},
		{name: "current dir", rel: ".", wantErr: false},
		{name: "parent escape", rel: filepath.Join("..", "evil"), wantErr: true},
		{name: "deep parent escape", rel: filepath.Join("a", "..", "..", "evil"), wantErr: true},
		{name: "bare parent", rel: "..", wantErr: true},
		{name: "absolute path", rel: "/etc/passwd", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Join(base, tt.rel)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Join(%q, %q) = %q, want error", base, tt.rel, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Join(%q, %q) unexpected error: %v", base, tt.rel, err)
			}
			if got != filepath.Join(base, tt.rel) {
				t.Errorf("Join = %q, want %q", got, filepath.Join(base, tt.rel))
			}
		})
	}
}

// TestJoin_ErrorNamesPath verifies the rejection error names the offending path,
// matching the actionable-error convention.
func TestJoin_ErrorNamesPath(t *testing.T) {
	_, err := Join("/base", "../evil")
	if err == nil {
		t.Fatal("expected error for non-local path, got nil")
	}
	if !contains(err.Error(), "../evil") {
		t.Errorf("error %q does not name the offending path", err.Error())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestRemoveEmptyParents verifies the climb-up stops at stopAt regardless of
// non-canonical formatting (e.g. a trailing slash) on either path.
func TestRemoveEmptyParents(t *testing.T) {
	tests := []struct {
		name string
		// stopAtSuffix is appended to the cleaned stop directory to simulate
		// non-canonical caller formatting (e.g. a trailing slash).
		stopAtSuffix string
		// dirSuffix is appended to the cleaned start directory.
		dirSuffix string
	}{
		{name: "canonical stopAt", stopAtSuffix: "", dirSuffix: ""},
		{name: "stopAt trailing slash", stopAtSuffix: "/", dirSuffix: ""},
		{name: "dir trailing slash", stopAtSuffix: "", dirSuffix: "/"},
		{name: "both trailing slash", stopAtSuffix: "/", dirSuffix: "/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// Layout: <root>/a/b/c — all empty below the stop point.
			deep := filepath.Join(root, "a", "b", "c")
			if err := os.MkdirAll(deep, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}

			RemoveEmptyParents(deep+tc.dirSuffix, root+tc.stopAtSuffix)

			// The empty descendants of root must be gone.
			if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
				t.Errorf("expected %s removed, stat err = %v", filepath.Join(root, "a"), err)
			}
			// stopAt itself (root) must survive.
			if _, err := os.Stat(root); err != nil {
				t.Errorf("expected stopAt %s to survive, stat err = %v", root, err)
			}
		})
	}
}

// TestRemoveEmptyParents_StopsAtNonEmpty verifies the climb halts at the first
// non-empty directory and leaves it (and the stop point) intact.
func TestRemoveEmptyParents_StopsAtNonEmpty(t *testing.T) {
	root := t.TempDir()
	mid := filepath.Join(root, "a")
	deep := filepath.Join(mid, "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Sibling file makes mid non-empty.
	if err := os.WriteFile(filepath.Join(mid, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	RemoveEmptyParents(deep, root)

	if _, err := os.Stat(deep); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, stat err = %v", deep, err)
	}
	if _, err := os.Stat(mid); err != nil {
		t.Errorf("expected non-empty %s to survive, stat err = %v", mid, err)
	}
}

// TestRemoveEmptyParents_DirIsStopAt verifies that when dir already equals
// stopAt nothing is removed.
func TestRemoveEmptyParents_DirIsStopAt(t *testing.T) {
	root := t.TempDir()

	RemoveEmptyParents(root, root)

	if _, err := os.Stat(root); err != nil {
		t.Errorf("expected stopAt %s to survive, stat err = %v", root, err)
	}
}

// TestRemoveEmptyParents_StopsAtSymlinkedParent verifies the climb refuses to
// remove a symlinked-directory ancestor: os.Remove would unlink the symlink
// itself (not its non-empty target), silently deleting a user's non-managed
// symlink such as ~/.config -> a cloud-synced location.
func TestRemoveEmptyParents_StopsAtSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	// real is the directory the user's symlink points at; it holds precious data.
	real := filepath.Join(root, "cloud")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("MkdirAll real: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "precious.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// target is the dotsmith TargetDir (the stop point).
	target := filepath.Join(root, "home")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	// link is the user's non-managed symlinked parent dir under target.
	link := filepath.Join(target, ".config")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// A managed leaf was planted inside the real dir (through the symlink). Climb
	// from the leaf's parent — which resolves to the symlink itself.
	RemoveEmptyParents(link, target)

	// The user's symlink must survive untouched.
	if fi, err := os.Lstat(link); err != nil {
		t.Fatalf("expected symlink %s to survive, lstat err = %v", link, err)
	} else if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected %s to still be a symlink", link)
	}
	// The data behind it must remain.
	if _, err := os.Stat(filepath.Join(real, "precious.txt")); err != nil {
		t.Errorf("expected data behind symlink to survive, stat err = %v", err)
	}
}

// TestRemoveEmptyParents_StopsOnLstatError verifies the climb halts silently
// when an ancestor cannot be Lstat'd (e.g. already removed by a concurrent
// process), leaving the stop point intact.
func TestRemoveEmptyParents_StopsOnLstatError(t *testing.T) {
	root := t.TempDir()
	// dir does not exist, so Lstat fails on the very first iteration.
	missing := filepath.Join(root, "gone", "deeper")

	RemoveEmptyParents(missing, root)

	if _, err := os.Stat(root); err != nil {
		t.Errorf("expected stopAt %s to survive, stat err = %v", root, err)
	}
}

// TestRemoveEmptyParents_StopsOnRemoveError verifies the climb terminates when
// stopAt is never an ancestor, relying on os.Remove failing at the first
// non-empty directory rather than looping up to the filesystem root.
func TestRemoveEmptyParents_StopsOnRemoveError(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A sibling file in root keeps root non-empty, so the climb fails on
	// os.Remove(root) and never reaches the filesystem root.
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// stopAt is a path that is never an ancestor of deep.
	RemoveEmptyParents(deep, filepath.Join(root, "never"))

	if _, err := os.Stat(deep); !os.IsNotExist(err) {
		t.Errorf("expected empty %s removed, stat err = %v", deep, err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("expected non-empty %s to survive, stat err = %v", root, err)
	}
}
