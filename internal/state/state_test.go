package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.Symlinks == nil {
		t.Error("New().Symlinks should not be nil")
	}
	if len(s.Symlinks) != 0 {
		t.Errorf("New().Symlinks should be empty, got %d entries", len(s.Symlinks))
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	original := New()
	original.Symlinks["/home/user/.bashrc"] = SymlinkEntry{
		Source:      "/home/user/.dotcompiled/.bashrc",
		Target:      "/home/user/.bashrc",
		ContentHash: "abc123",
	}
	original.Symlinks["/home/user/.vimrc"] = SymlinkEntry{
		Source:      "/home/user/.dotcompiled/.vimrc",
		Target:      "/home/user/.vimrc",
		ContentHash: "def456",
	}

	if err := Save(ctx, original, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Symlinks) != len(original.Symlinks) {
		t.Fatalf("len(Symlinks) = %d, want %d", len(loaded.Symlinks), len(original.Symlinks))
	}

	for k, v := range original.Symlinks {
		got, ok := loaded.Symlinks[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if got != v {
			t.Errorf("Symlinks[%q] = %v, want %v", k, got, v)
		}
	}
}

func TestLoad_MissingFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load of missing file should succeed, got: %v", err)
	}
	if s == nil {
		t.Fatal("Load returned nil state")
	}
	if len(s.Symlinks) != 0 {
		t.Errorf("expected empty symlinks, got %d entries", len(s.Symlinks))
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	path := filepath.Join(dir, stateFile)
	if err := os.WriteFile(path, []byte("not valid json {{{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(ctx, dir)
	if err == nil {
		t.Fatal("expected error for corrupt JSON, got nil")
	}
}

func TestLoad_NullSymlinks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// JSON with null symlinks field.
	path := filepath.Join(dir, stateFile)
	if err := os.WriteFile(path, []byte(`{"symlinks":null}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Symlinks == nil {
		t.Error("Symlinks should be initialised to empty map, not nil")
	}
}

func TestSave_Permissions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if err := Save(ctx, New(), dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, stateFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestSave_EmptyState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if err := Save(ctx, New(), dir); err != nil {
		t.Fatalf("Save empty state: %v", err)
	}

	loaded, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Symlinks) != 0 {
		t.Errorf("expected 0 symlinks, got %d", len(loaded.Symlinks))
	}
}

func TestSave_ValidJSON(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s := New()
	s.Symlinks["key"] = SymlinkEntry{Source: "src", Target: "tgt", ContentHash: "hash"}

	if err := Save(ctx, s, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, stateFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]json.RawMessage
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

func TestSave_MarshalError(t *testing.T) {
	orig := jsonMarshalIndentFunc
	t.Cleanup(func() { jsonMarshalIndentFunc = orig })
	jsonMarshalIndentFunc = func(_ any, _, _ string) ([]byte, error) {
		return nil, errors.New("injected marshal error")
	}

	ctx := context.Background()
	dir := t.TempDir()
	err := Save(ctx, New(), dir)
	if err == nil {
		t.Fatal("expected error from marshal failure, got nil")
	}
}

func TestSave_WriteError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Make the directory read-only so WriteFile fails.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := Save(ctx, New(), dir)
	if err == nil {
		t.Fatal("expected error from write failure, got nil")
	}
}

func TestLoad_ReadError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Create the state file then make the directory unreadable.
	path := filepath.Join(dir, stateFile)
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	_, err := Load(ctx, dir)
	if err == nil {
		t.Fatal("expected error reading unreadable file, got nil")
	}
}
