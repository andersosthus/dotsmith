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
	if s.Compiled == nil {
		t.Error("New().Compiled should not be nil")
	}
	if len(s.Compiled) != 0 {
		t.Errorf("New().Compiled should be empty, got %d entries", len(s.Compiled))
	}
}

func TestSaveLoad_CompiledRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	original := New()
	original.Symlinks[".bashrc"] = SymlinkEntry{Source: ".bashrc", Target: ".bashrc", ContentHash: "h1"}
	original.Compiled[".bashrc"] = CompiledEntry{ContentHash: "h1"}
	original.Compiled[".config/nvim/init.vim"] = CompiledEntry{ContentHash: "h2"}

	if err := Save(ctx, original, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Compiled) != len(original.Compiled) {
		t.Fatalf("len(Compiled) = %d, want %d", len(loaded.Compiled), len(original.Compiled))
	}
	for k, v := range original.Compiled {
		got, ok := loaded.Compiled[k]
		if !ok || got != v {
			t.Errorf("Compiled[%q] = %v (ok=%v), want %v", k, got, ok, v)
		}
	}
}

func TestLoad_NullCompiled(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, stateFile)
	// Legacy state with no compiled field — upgrade case.
	if err := os.WriteFile(path, []byte(`{"symlinks":{}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Compiled == nil {
		t.Error("Compiled should be initialised to an empty map, not nil")
	}
}

func TestLoad_NonLocalCompiledRejected(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		json string
	}{
		{
			name: "compiled key escapes with ..",
			json: `{"symlinks":{},"compiled":{"../etc/passwd":{"content_hash":"d"}}}`,
		},
		{
			name: "compiled key is absolute",
			json: `{"symlinks":{},"compiled":{"/etc/passwd":{"content_hash":"d"}}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(tc.json), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := Load(ctx, dir); err == nil {
				t.Fatal("expected error for non-local compiled path, got nil")
			}
		})
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Source/Target are repo-relative paths (as written by the linker).
	original := New()
	original.Symlinks[".bashrc"] = SymlinkEntry{
		Source:      ".bashrc",
		Target:      ".bashrc",
		ContentHash: "abc123",
	}
	original.Symlinks[".config/nvim/init.vim"] = SymlinkEntry{
		Source:      ".config/nvim/init.vim",
		Target:      ".config/nvim/init.vim",
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

func TestLoad_NonLocalPathsRejected(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		json string
	}{
		{
			name: "target escapes with ..",
			json: `{"symlinks":{"e":{"source":"x","target":"../.ssh/authorized_keys","content_hash":"d"}}}`,
		},
		{
			name: "source escapes with ..",
			json: `{"symlinks":{"e":{"source":"../etc/passwd","target":"x","content_hash":"d"}}}`,
		},
		{
			name: "target is absolute",
			json: `{"symlinks":{"e":{"source":"x","target":"/etc/passwd","content_hash":"d"}}}`,
		},
		{
			name: "source is absolute",
			json: `{"symlinks":{"e":{"source":"/abs/path","target":"x","content_hash":"d"}}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(tc.json), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := Load(ctx, dir); err == nil {
				t.Fatal("expected error for non-local path, got nil")
			}
		})
	}
}

func TestLoad_LocalPathsAccepted(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	jsonData := `{"symlinks":{".bashrc":{"source":".bashrc","target":".bashrc","content_hash":"d"}}}`
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(jsonData), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load of local-path state should succeed, got: %v", err)
	}
	if len(s.Symlinks) != 1 {
		t.Errorf("expected 1 entry, got %d", len(s.Symlinks))
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
