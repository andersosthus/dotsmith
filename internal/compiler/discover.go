package compiler

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/andersosthus/dotsmith/internal/identity"
)

// filepathRelFunc is injectable for testing the error path of filepath.Rel.
var filepathRelFunc = filepath.Rel

// FileEntry represents a single target file and all its resolved fragments.
type FileEntry struct {
	// Target is the relative path of the compiled output file (e.g. ".bashrc").
	Target string
	// Subfiles is the ordered list of subfile descriptors after override
	// resolution, sorted by natural order.
	Subfiles []SubfileDesc
	// IsRegular is true when the file is not split into subfiles.
	IsRegular bool
}

// SubfileDesc describes a single subfile fragment after override resolution.
type SubfileDesc struct {
	// Number is the digit string used for natural sorting (e.g. "020").
	Number string
	// SourcePath is the absolute path to the source file.
	SourcePath string
	// Encrypted is true if the source file is age-encrypted.
	Encrypted bool
	// Layer is the override layer from which this fragment originates
	// (e.g. "base", "hostname/workstation").
	Layer string
	// SourceName is the basename of the source file (used in comment headers).
	SourceName string
}

// Discover walks the dotfiles repository layers for the given identity and
// returns a map of target relative paths to resolved FileEntry values.
func Discover(ctx context.Context, dotfilesDir string, id identity.Identity) (map[string]*FileEntry, error) {
	entries := make(map[string]*FileEntry)
	layers := id.Layers()

	for _, layer := range layers {
		layerDir := filepath.Join(dotfilesDir, string(layer.Layer), layer.Key)
		layerLabel := string(layer.Layer) + "/" + layer.Key
		if layer.Layer == identity.LayerBase {
			layerDir = filepath.Join(dotfilesDir, "base")
			layerLabel = "base"
		}

		if err := applyLayer(ctx, entries, layerDir, layerLabel); err != nil {
			return nil, fmt.Errorf("discover: apply layer %s: %w", layerLabel, err)
		}
	}

	// Sort subfiles within each entry by natural order.
	for _, e := range entries {
		sortSubfiles(e.Subfiles)
	}

	return entries, nil
}

// applyLayer walks layerDir and applies its files to the entries map.
func applyLayer(ctx context.Context, entries map[string]*FileEntry, layerDir, layerLabel string) error {
	if _, err := os.Stat(layerDir); os.IsNotExist(err) {
		return nil // layer directory not present — silently skip
	}

	err := filepath.WalkDir(layerDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}

		rel, relErr := filepathRelFunc(layerDir, path)
		if relErr != nil {
			return fmt.Errorf("relative path of %s from %s: %w", path, layerDir, relErr)
		}

		return applyFile(ctx, entries, path, rel, layerLabel)
	})
	return err //nolint:wrapcheck // walk callback pre-wraps all errors with context
}

// applyFile processes a single discovered file, updating entries accordingly.
func applyFile(_ context.Context, entries map[string]*FileEntry, absPath, rel, layerLabel string) error {
	base := filepath.Base(rel)
	dir := filepath.Dir(rel)

	// Handle .ignore markers.
	if strings.HasSuffix(base, ".ignore") {
		targetBase := strings.TrimSuffix(base, ".ignore")
		targetRel := filepath.Join(dir, targetBase)
		// Normalize dir for root-level files.
		if dir == "." {
			targetRel = targetBase
		}

		// Check if this .ignore targets a subfile or a regular file.
		info := ParseSubfileName(targetBase)
		if info != nil {
			applyIgnoreToSubfile(entries, info.Target, targetBase, targetRel, layerLabel)
		} else {
			applyIgnoreToRegular(entries, targetRel, layerLabel)
		}
		return nil
	}

	// Check if this is a subfile.
	info := ParseSubfileName(base)
	if info != nil {
		target := filepath.Join(dir, info.Target)
		if dir == "." {
			target = info.Target
		}
		return applySubfile(entries, absPath, rel, target, info, layerLabel)
	}

	// Regular file: replaces any existing entry for this relative path.
	target := rel
	e := &FileEntry{
		Target:    target,
		IsRegular: true,
		Subfiles: []SubfileDesc{{
			Number:     "",
			SourcePath: absPath,
			Encrypted:  strings.HasSuffix(base, ".age"),
			Layer:      layerLabel,
			SourceName: base,
		}},
	}
	entries[target] = e
	return nil
}

// applySubfile adds or replaces a subfile fragment in the target's FileEntry.
func applySubfile(
	entries map[string]*FileEntry,
	absPath, rel, target string,
	info *SubfileInfo,
	layerLabel string,
) error {
	e, ok := entries[target]
	if !ok {
		e = &FileEntry{Target: target}
		entries[target] = e
	}

	// Check for same-number replacement or new addition.
	for i, sf := range e.Subfiles {
		if sf.Number == info.Number {
			// Replace existing subfile with same number.
			e.Subfiles[i] = SubfileDesc{
				Number:     info.Number,
				SourcePath: absPath,
				Encrypted:  info.Encrypted,
				Layer:      layerLabel,
				SourceName: filepath.Base(rel),
			}
			return nil
		}
	}

	// New subfile number: add it.
	e.Subfiles = append(e.Subfiles, SubfileDesc{
		Number:     info.Number,
		SourcePath: absPath,
		Encrypted:  info.Encrypted,
		Layer:      layerLabel,
		SourceName: filepath.Base(rel),
	})
	return nil
}

// applyIgnoreToSubfile removes a specific subfile number from a target.
func applyIgnoreToSubfile(
	entries map[string]*FileEntry,
	subfileTarget, targetBase, targetRel, layerLabel string,
) {
	// Find which target this subfile belongs to.
	info := ParseSubfileName(targetBase)
	if info == nil {
		return
	}
	e, ok := entries[subfileTarget]
	if !ok {
		slog.Warn("ignore marker targets non-existent file",
			"target", targetRel, "layer", layerLabel)
		return
	}
	filtered := e.Subfiles[:0]
	for _, sf := range e.Subfiles {
		if sf.Number != info.Number {
			filtered = append(filtered, sf)
		}
	}
	if len(filtered) == len(e.Subfiles) {
		slog.Warn("ignore marker targets non-existent subfile",
			"target", targetRel, "layer", layerLabel)
	}
	e.Subfiles = filtered
}

// applyIgnoreToRegular removes a regular file entry entirely.
func applyIgnoreToRegular(entries map[string]*FileEntry, targetRel, layerLabel string) {
	if _, ok := entries[targetRel]; !ok {
		slog.Warn("ignore marker targets non-existent file",
			"target", targetRel, "layer", layerLabel)
		return
	}
	delete(entries, targetRel)
}

// sortSubfiles sorts subfiles in place by natural order of their Number fields.
func sortSubfiles(sfs []SubfileDesc) {
	n := len(sfs)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && NaturalLess(sfs[j].Number, sfs[j-1].Number); j-- {
			sfs[j], sfs[j-1] = sfs[j-1], sfs[j]
		}
	}
}
