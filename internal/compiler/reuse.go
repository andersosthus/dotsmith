package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andersosthus/dotsmith/internal/state"
)

// reuseDecision is the outcome of the two-gate reuse check for a single target
// (see ADR 0015). When Reuse is true the target's already-compiled output may be
// served as-is, skipping decryption; ContentHash then carries the prior compiled
// file's hash so the linker and a refreshed manifest keep referring to it.
type reuseDecision struct {
	// Reuse is true only when both gates passed.
	Reuse bool
	// ContentHash is the recorded hash of the existing compiled output, valid
	// only when Reuse is true.
	ContentHash string
}

// osReadFileFunc is the on-disk read sink for gate 2, injectable so tests can
// exercise the read-error path independently of a real filesystem.
var osReadFileFunc = os.ReadFile

// reuseGate evaluates the two reuse gates for a single target (see ADR 0015):
//
//   - Gate 1 — source signature: the freshly computed signature must equal the
//     one the previous compile recorded for this target. A zero/absent prior
//     signature (a first run, or a state file from a pre-reuse binary) never
//     matches, forcing a recompile.
//   - Gate 2 — output integrity: the compiled file must still exist on disk and
//     hash to the prior manifest's recorded ContentHash, proving a valid
//     artifact exists to reuse.
//
// Both gates must pass for reuse. On any mismatch, a missing prior entry, a
// missing compiled file, or a read error, it returns no reuse — compile then
// falls back to full decryption. It never decrypts and never writes.
func reuseGate(
	prior map[string]state.CompiledEntry,
	compileDir, target, freshSignature string,
) reuseDecision {
	entry, ok := prior[target]
	if !ok {
		return reuseDecision{} // no prior record — recompile
	}
	// Gate 1: the inputs must be unchanged.
	if entry.SourceSignature == "" || entry.SourceSignature != freshSignature {
		return reuseDecision{}
	}
	// Gate 2: a valid compiled artifact must still be on disk.
	existing, err := osReadFileFunc(filepath.Join(compileDir, target))
	if err != nil {
		return reuseDecision{} // missing or unreadable output — recompile
	}
	if hashContent(existing) != entry.ContentHash {
		return reuseDecision{} // output altered on disk — recompile
	}
	return reuseDecision{Reuse: true, ContentHash: entry.ContentHash}
}

// loadReuseState loads the prior compile state from compileDir for the reuse
// decision. It returns an empty (non-nil) state when no state file exists yet,
// so the first compile after upgrade simply recompiles every target.
func loadReuseState(ctx context.Context, compileDir string) (*state.State, error) {
	if compileDir == "" {
		// No compile directory configured (e.g. render, or a caller that does
		// not write output): reuse is impossible, so behave as a first run.
		return state.New(), nil
	}
	s, err := state.Load(ctx, compileDir)
	if err != nil {
		return nil, fmt.Errorf("load prior state: %w", err)
	}
	return s, nil
}
