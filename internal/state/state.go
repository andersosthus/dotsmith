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

// FileName is the reserved basename of the dotsmith state file, stored inside
// the compile directory. It must never be produced as a managed dotfile, or it
// would clobber dotsmith's own bookkeeping.
const FileName = ".dotsmith.state"

// stateFile is the legacy internal alias for FileName.
const stateFile = FileName

// SymlinkEntry records a managed symlink and the content hash of its source
// file at the time it was linked.
type SymlinkEntry struct {
	// Source is the path to the compiled file (in the compile directory).
	Source string `json:"source"`
	// Target is the path to the symlink in the target directory.
	Target string `json:"target"`
	// ContentHash is the hex-encoded content hash of Source at link time.
	ContentHash string `json:"content_hash"`
}

// CompiledEntry records a single file the previous compile produced. It is the
// manifest unit that lets compile prune only files it created itself.
//
// The content hash is stored even though pruning only needs the key set: it
// enables a future compile to detect a locally-edited compiled file. A linked
// file's hash is therefore stored twice — here (what was compiled) and in its
// SymlinkEntry (what was linked) — answering different questions; the two are
// intentionally not deduped.
//
// ContentHash and SourceSignature answer the two reuse gates (see ADR 0015):
// ContentHash asks "is the artifact still the one I wrote?" and SourceSignature
// asks "have the inputs changed?". They are intentionally distinct.
type CompiledEntry struct {
	// ContentHash is the hex-encoded content hash of the compiled file at the
	// time it was produced.
	ContentHash string `json:"content_hash"`
	// SourceSignature is the ordered digest of the content hash of each
	// contributing subfile, computed without decrypting any encrypted source
	// (see ADR 0015). It is omitted from the JSON when empty so that state files
	// written by older binaries — which have no signature — round-trip cleanly;
	// a loaded entry with an absent signature is treated as the empty string.
	SourceSignature string `json:"source_signature,omitempty"`
}

// State represents the full contents of the state file.
type State struct {
	// Symlinks maps target paths to their SymlinkEntry.
	Symlinks map[string]SymlinkEntry `json:"symlinks"`
	// Compiled is the compile manifest: it maps each compiled file's path
	// (relative to the compile directory) to its CompiledEntry. It records
	// exactly the files the previous compile produced so that the next compile
	// can prune the ones whose source has since disappeared.
	Compiled map[string]CompiledEntry `json:"compiled"`
}

// New returns an empty State ready for use, with both Symlinks and Compiled
// zeroed.
func New() *State {
	return &State{
		Symlinks: make(map[string]SymlinkEntry),
		Compiled: make(map[string]CompiledEntry),
	}
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
	if err = validateLocalPaths(path, &s); err != nil {
		return nil, err
	}
	if s.Symlinks == nil {
		s.Symlinks = make(map[string]SymlinkEntry)
	}
	if s.Compiled == nil {
		s.Compiled = make(map[string]CompiledEntry)
	}
	return &s, nil
}

// validateLocalPaths rejects any state entry whose paths escape their directory
// (e.g. "../"). A state file is only trusted to reference files within the
// compile/target directories; a non-local path would let a crafted state file
// delete arbitrary files elsewhere. path is used only for error context.
func validateLocalPaths(path string, s *State) error {
	for k, e := range s.Symlinks {
		if !filepath.IsLocal(e.Target) || !filepath.IsLocal(e.Source) {
			return fmt.Errorf(
				"load state from %s: entry %q has non-local path (source=%q target=%q) — "+
					"refusing a state file that escapes its directory",
				path, k, e.Source, e.Target,
			)
		}
	}
	// Manifest keys are joined under the compile directory at prune time; reject
	// any that escape it so a crafted state file cannot make compile delete
	// arbitrary files outside the compile directory.
	for k := range s.Compiled {
		if !filepath.IsLocal(k) {
			return fmt.Errorf(
				"load state from %s: compiled entry %q has non-local path — "+
					"refusing a state file that escapes its directory",
				path, k,
			)
		}
	}
	return nil
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
