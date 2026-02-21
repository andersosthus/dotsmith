package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/andersosthus/dotsmith/internal/identity"
)

// makeDir creates a directory relative to base, creating parents as needed.
func makeDir(t *testing.T, base string, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{base}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	return dir
}

// writeFile writes content to a file, creating parent dirs.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}

// stubDotfiles creates a minimal dotfiles structure for testing.
func stubDotfiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	makeDir(t, root, "base")
	return root
}

var baseOnly = identity.Identity{}

func TestDiscover_BaseOnly(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")

	writeFile(t, base, ".subfile-010.bashrc", "# base 010")
	writeFile(t, base, ".subfile-020.bashrc", "# base 020")
	writeFile(t, base, ".vimrc", "\" base vimrc")

	ctx := context.Background()
	entries, err := Discover(ctx, root, baseOnly)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if _, ok := entries[".bashrc"]; !ok {
		t.Error("expected .bashrc entry")
	}
	if _, ok := entries[".vimrc"]; !ok {
		t.Error("expected .vimrc entry")
	}

	bashrc := entries[".bashrc"]
	if len(bashrc.Subfiles) != 2 {
		t.Errorf("len(Subfiles) = %d, want 2", len(bashrc.Subfiles))
	}
	if bashrc.Subfiles[0].Number != "010" {
		t.Errorf("first subfile number = %q, want %q", bashrc.Subfiles[0].Number, "010")
	}
	if bashrc.Subfiles[1].Number != "020" {
		t.Errorf("second subfile number = %q, want %q", bashrc.Subfiles[1].Number, "020")
	}

	vimrc := entries[".vimrc"]
	if !vimrc.IsRegular {
		t.Error("expected .vimrc to be a regular file")
	}
}

func TestDiscover_OSOverrideAdds(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	osDir := makeDir(t, root, "os", "linux")

	writeFile(t, base, ".subfile-010.bashrc", "# base 010")
	writeFile(t, osDir, ".subfile-015.bashrc", "# linux 015")

	ctx := context.Background()
	id := identity.Identity{OS: "linux"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	bashrc := entries[".bashrc"]
	if len(bashrc.Subfiles) != 2 {
		t.Fatalf("len(Subfiles) = %d, want 2", len(bashrc.Subfiles))
	}
	if bashrc.Subfiles[0].Number != "010" || bashrc.Subfiles[1].Number != "015" {
		t.Errorf("subfiles = %v, want [010, 015]", bashrc.Subfiles)
	}
}

func TestDiscover_HostnameReplaces(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "workstation")

	writeFile(t, base, ".subfile-020.bashrc", "# base 020")
	writeFile(t, hostDir, ".subfile-020.bashrc", "# workstation 020 replacement")

	ctx := context.Background()
	id := identity.Identity{Hostname: "workstation"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	bashrc := entries[".bashrc"]
	if len(bashrc.Subfiles) != 1 {
		t.Fatalf("len(Subfiles) = %d, want 1", len(bashrc.Subfiles))
	}
	if bashrc.Subfiles[0].Layer != "hostname/workstation" {
		t.Errorf("Layer = %q, want %q", bashrc.Subfiles[0].Layer, "hostname/workstation")
	}
}

func TestDiscover_IgnoreMarkerSubfile(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "workstation")

	writeFile(t, base, ".subfile-010.bashrc", "# base 010")
	writeFile(t, base, ".subfile-030.bashrc", "# base 030")
	writeFile(t, hostDir, ".subfile-030.bashrc.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "workstation"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	bashrc := entries[".bashrc"]
	if len(bashrc.Subfiles) != 1 {
		t.Fatalf("len(Subfiles) = %d, want 1 (030 should be ignored)", len(bashrc.Subfiles))
	}
	if bashrc.Subfiles[0].Number != "010" {
		t.Errorf("remaining subfile number = %q, want %q", bashrc.Subfiles[0].Number, "010")
	}
}

func TestDiscover_IgnoreMarkerRegular(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "workstation")

	writeFile(t, base, ".vimrc", "\" base vimrc")
	writeFile(t, hostDir, ".vimrc.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "workstation"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if _, ok := entries[".vimrc"]; ok {
		t.Error("expected .vimrc to be removed by ignore marker")
	}
}

func TestDiscover_IgnoreNonExistentWarns(t *testing.T) {
	root := stubDotfiles(t)
	hostDir := makeDir(t, root, "hostname", "workstation")

	// Ignore marker for a file that doesn't exist — should warn but not error.
	writeFile(t, hostDir, ".nonexistent.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "workstation"}
	_, err := Discover(ctx, root, id)
	if err != nil {
		t.Errorf("Discover should not error on non-existent ignore target, got: %v", err)
	}
}

func TestDiscover_IgnoreNonExistentSubfileWarns(t *testing.T) {
	root := stubDotfiles(t)
	hostDir := makeDir(t, root, "hostname", "workstation")

	// Ignore marker for a subfile entry that doesn't exist.
	writeFile(t, hostDir, ".subfile-999.bashrc.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "workstation"}
	_, err := Discover(ctx, root, id)
	if err != nil {
		t.Errorf("expected no error for missing subfile ignore, got: %v", err)
	}
}

func TestDiscover_IgnoreNonExistentSubfileWithTarget(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "workstation")

	writeFile(t, base, ".subfile-010.bashrc", "# base 010")
	// Ignore marker for a subfile number that doesn't exist in .bashrc.
	writeFile(t, hostDir, ".subfile-999.bashrc.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "workstation"}
	_, err := Discover(ctx, root, id)
	if err != nil {
		t.Errorf("expected no error for non-existent subfile number, got: %v", err)
	}
}

func TestDiscover_MultiLayerStack(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	osDir := makeDir(t, root, "os", "linux")
	hostDir := makeDir(t, root, "hostname", "box")
	userDir := makeDir(t, root, "username", "alice")
	userhostDir := makeDir(t, root, "userhost", "alice@box")

	writeFile(t, base, ".subfile-010.bashrc", "# base 010")
	writeFile(t, osDir, ".subfile-015.bashrc", "# linux 015")
	writeFile(t, hostDir, ".subfile-020.bashrc", "# box 020")
	writeFile(t, userDir, ".subfile-020.bashrc", "# alice 020 (replaces box)")
	writeFile(t, userhostDir, ".subfile-025.bashrc", "# alice@box 025")

	ctx := context.Background()
	id := identity.Identity{OS: "linux", Hostname: "box", Username: "alice"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	bashrc := entries[".bashrc"]
	if len(bashrc.Subfiles) != 4 {
		t.Fatalf("len(Subfiles) = %d, want 4", len(bashrc.Subfiles))
	}
	// After natural sort: 010, 015, 020, 025.
	expected := []string{"010", "015", "020", "025"}
	for i, sf := range bashrc.Subfiles {
		if sf.Number != expected[i] {
			t.Errorf("Subfiles[%d].Number = %q, want %q", i, sf.Number, expected[i])
		}
	}
	// The 020 should come from alice (username layer replaces box's 020).
	sf020 := bashrc.Subfiles[2]
	if sf020.Layer != "username/alice" {
		t.Errorf("020 layer = %q, want %q", sf020.Layer, "username/alice")
	}
}

func TestDiscover_RegularFileReplacement(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	userDir := makeDir(t, root, "username", "anders")

	writeFile(t, base, ".vimrc", "\" base vimrc")
	writeFile(t, userDir, ".vimrc", "\" anders vimrc")

	ctx := context.Background()
	id := identity.Identity{Username: "anders"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	vimrc := entries[".vimrc"]
	if vimrc.Subfiles[0].Layer != "username/anders" {
		t.Errorf("Layer = %q, want %q", vimrc.Subfiles[0].Layer, "username/anders")
	}
}

func TestDiscover_EncryptedSubfile(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")

	writeFile(t, base, ".subfile-040.bashrc.age", "fake encrypted content")

	ctx := context.Background()
	entries, err := Discover(ctx, root, baseOnly)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	bashrc := entries[".bashrc"]
	if len(bashrc.Subfiles) != 1 {
		t.Fatalf("len(Subfiles) = %d, want 1", len(bashrc.Subfiles))
	}
	if !bashrc.Subfiles[0].Encrypted {
		t.Error("expected subfile to be marked as encrypted")
	}
}

func TestDiscover_MissingLayerDir(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	writeFile(t, base, ".subfile-010.bashrc", "# base")

	ctx := context.Background()
	// Hostname/username layers don't exist — should be silently skipped.
	id := identity.Identity{OS: "linux", Hostname: "nosuchhost", Username: "nosuchuser"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one entry from base")
	}
}

func TestDiscover_NestedPath(t *testing.T) {
	root := stubDotfiles(t)
	base := makeDir(t, root, "base", ".config", "git")

	writeFile(t, base, "config", "[core]\n\tautocrlf = false\n")

	ctx := context.Background()
	entries, err := Discover(ctx, root, baseOnly)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	key := filepath.Join(".config", "git", "config")
	if _, ok := entries[key]; !ok {
		t.Errorf("expected entry for %q, got keys: %v", key, keys(entries))
	}
}

// keys returns the keys of a map for error reporting.
func keys(m map[string]*FileEntry) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}

func TestDiscover_SubfileWithExtensionTarget(t *testing.T) {
	// The naming convention is <stem>.subfile-NNN.<ext>.
	// For a target named config.fish, use config.subfile-NNN.fish.
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")

	writeFile(t, base, "config.subfile-001.fish", "# 001\n")
	writeFile(t, base, "config.subfile-050.fish", "# 050\n")

	ctx := context.Background()
	entries, err := Discover(ctx, root, baseOnly)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if _, ok := entries["config.fish"]; !ok {
		t.Errorf("expected entry for %q, got keys: %v", "config.fish", keys(entries))
	}
}

func TestDiscover_NestedSubfileWithExtensionTarget(t *testing.T) {
	root := stubDotfiles(t)
	base := makeDir(t, root, "base", ".config", "fish")

	writeFile(t, base, "config.subfile-001.fish", "# 001\n")
	writeFile(t, base, "config.subfile-050.fish", "# 050\n")

	ctx := context.Background()
	entries, err := Discover(ctx, root, baseOnly)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	key := filepath.Join(".config", "fish", "config.fish")
	if _, ok := entries[key]; !ok {
		t.Errorf("expected entry for %q, got keys: %v", key, keys(entries))
	}
}

func TestDiscover_IgnoreSubfileWithExtensionTarget(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "box")

	writeFile(t, base, "config.subfile-001.fish", "# 001\n")
	writeFile(t, base, "config.subfile-050.fish", "# 050\n")
	writeFile(t, hostDir, "config.subfile-050.fish.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "box"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	e := entries["config.fish"]
	if e == nil {
		t.Fatalf("expected entry for config.fish")
	}
	if len(e.Subfiles) != 1 {
		t.Fatalf("len(Subfiles) = %d, want 1 (050 should be ignored)", len(e.Subfiles))
	}
	if e.Subfiles[0].Number != "001" {
		t.Errorf("remaining subfile = %q, want %q", e.Subfiles[0].Number, "001")
	}
}

func TestDiscover_RegularEncryptedFile(t *testing.T) {
	// A regular file named my.yml.age should be discovered with target "my.yml",
	// not "my.yml.age".
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")

	writeFile(t, base, "my.yml.age", "fake encrypted content")

	ctx := context.Background()
	entries, err := Discover(ctx, root, baseOnly)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	e, ok := entries["my.yml"]
	if !ok {
		t.Fatalf("expected entry for %q, got keys: %v", "my.yml", keys(entries))
	}
	if !e.Subfiles[0].Encrypted {
		t.Error("expected Encrypted = true")
	}
	if _, ok := entries["my.yml.age"]; ok {
		t.Error(".age extension must not appear in the target key")
	}
}

func TestDiscover_RegularEncryptedFileIgnorePlain(t *testing.T) {
	// A my.yml.ignore marker should remove the entry keyed as my.yml,
	// even though the source file on disk is my.yml.age.
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "box")

	writeFile(t, base, "my.yml.age", "fake encrypted content")
	writeFile(t, hostDir, "my.yml.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "box"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if _, ok := entries["my.yml"]; ok {
		t.Error("expected my.yml to be removed by my.yml.ignore marker")
	}
}

func TestDiscover_RegularEncryptedFileIgnoreWithAge(t *testing.T) {
	// A my.yml.age.ignore marker should also remove the entry keyed as my.yml.
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	hostDir := makeDir(t, root, "hostname", "box")

	writeFile(t, base, "my.yml.age", "fake encrypted content")
	writeFile(t, hostDir, "my.yml.age.ignore", "")

	ctx := context.Background()
	id := identity.Identity{Hostname: "box"}
	entries, err := Discover(ctx, root, id)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if _, ok := entries["my.yml"]; ok {
		t.Error("expected my.yml to be removed by my.yml.age.ignore marker")
	}
}

func TestApplyIgnoreToSubfile_NonSubfileBase(t *testing.T) {
	entries := make(map[string]*FileEntry)
	// targetBase is not a subfile name — ParseSubfileName returns nil, early return.
	applyIgnoreToSubfile(entries, ".vimrc", ".vimrc", ".vimrc", "base")
	if len(entries) != 0 {
		t.Error("expected no entries after no-op ignore")
	}
}

func TestDiscover_RelPathError(t *testing.T) {
	root := stubDotfiles(t)
	base := filepath.Join(root, "base")
	writeFile(t, base, ".subfile-010.bashrc", "# base")

	orig := filepathRelFunc
	t.Cleanup(func() { filepathRelFunc = orig })
	filepathRelFunc = func(_, _ string) (string, error) {
		return "", fmt.Errorf("forced error")
	}

	ctx := context.Background()
	_, err := Discover(ctx, root, baseOnly)
	if err == nil {
		t.Fatal("expected error from filepathRelFunc, got nil")
	}
}

func TestSortSubfiles_OutOfOrder(t *testing.T) {
	sfs := []SubfileDesc{
		{Number: "020"},
		{Number: "010"},
	}
	sortSubfiles(sfs)
	if sfs[0].Number != "010" || sfs[1].Number != "020" {
		t.Errorf("sortSubfiles = [%s, %s], want [010, 020]", sfs[0].Number, sfs[1].Number)
	}
}
