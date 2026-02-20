// Package state reads and writes the .dotsmith.state JSON file that tracks
// managed symlinks and content hashes.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const stateFile = ".dotsmith.state"

// SymlinkEntry records a managed symlink and the content hash of its source
// file at the time it was linked.
type SymlinkEntry struct {
	// Source is the path to the compiled file (in the compile directory).
	Source string `json:"source"`
	// Target is the path to the symlink in the target directory.
	Target string `json:"target"`
	// ContentHash is the hex-encoded SHA-256 hash of Source at link time.
	ContentHash string `json:"content_hash"`
}

// State represents the full contents of the state file.
type State struct {
	// Symlinks maps target paths to their SymlinkEntry.
	Symlinks map[string]SymlinkEntry `json:"symlinks"`
}

// New returns an empty State ready for use.
func New() *State {
	return &State{Symlinks: make(map[string]SymlinkEntry)}
}

// Load reads the state file from compileDir. If the file does not exist, an
// empty State is returned. If the file exists but is corrupt, an error is
// returned.
func Load(_ context.Context, compileDir string) (*State, error) {
	path := filepath.Join(compileDir, stateFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return New(), nil
		}
		return nil, fmt.Errorf("load state from %s: %w", path, err)
	}

	var s State
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("load state from %s: parse JSON: %w", path, err)
	}
	if s.Symlinks == nil {
		s.Symlinks = make(map[string]SymlinkEntry)
	}
	return &s, nil
}

// jsonMarshalIndentFunc is injectable for testing.
var jsonMarshalIndentFunc = func(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// Save writes s to the state file in compileDir with 0600 permissions.
func Save(_ context.Context, s *State, compileDir string) error {
	path := filepath.Join(compileDir, stateFile)
	data, err := jsonMarshalIndentFunc(s, "", "  ")
	if err != nil {
		return fmt.Errorf("save state to %s: marshal JSON: %w", path, err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save state to %s: %w", path, err)
	}
	return nil
}
