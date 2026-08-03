package scan

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// ScanCandidates reads and parses every active (non-dead, per owners) owner
// named in discovered, in the given (already deterministic) order, and
// returns every candidate found across all of them. discovered must already
// have been reconciled against owners (see ReconcileOwners) — ScanCandidates
// still defensively errors if it encounters a discovered path owners does
// not know about, rather than silently skipping it.
func ScanCandidates(baseDir string, discovered []string, owners OwnerManifest) ([]Candidate, error) {
	var all []Candidate
	for _, relPath := range discovered {
		entry, ok := owners.Lookup(relPath)
		if !ok {
			return nil, diagnostic(
				fmt.Sprintf("discovered owner %q has no manifest entry", relPath),
				"ScanCandidates must not guess a disposition for an unreconciled owner",
				"scan.ScanCandidates:"+relPath, "candidate scanning",
				"the scan cannot decide whether to parse this owner for candidates",
				"call ReconcileOwners before ScanCandidates so every drift is caught first",
				nil,
			)
		}
		if entry.Disposition == OwnerDead {
			continue
		}
		content, err := os.ReadFile(filepath.Join(baseDir, relPath))
		if err != nil {
			return nil, diagnostic(
				fmt.Sprintf("could not read active owner %q", relPath),
				"every active owner named by Discover must be readable to be parsed for candidates",
				"scan.ScanCandidates:"+relPath, "candidate scanning",
				"the scan cannot produce a complete inventory",
				"resolve the filesystem error and re-run the scan",
				err,
			)
		}
		found, err := scanFileCandidates(relPath, relPath, content)
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	return all, nil
}

// ScanWithManifests runs the complete pasture#47 pipeline against baseDir
// using caller-supplied manifests: independent root discovery, owner
// reconciliation, before/after byte-for-byte read-only tree hashing,
// candidate scanning of every active owner, classification, and
// classification-manifest reconciliation (RequireNoOrphanedClassifications):
// a checked-in classification entry that matches no real candidate fails the
// scan here, exactly as an owner-manifest drift already fails it in
// ReconcileOwners above. Tests use this directly with small synthetic
// manifests and roots to isolate one behavior; ScanCanonical is the
// production entrypoint using this repository's real, checked-in manifests.
func ScanWithManifests(baseDir string, roots []string, owners OwnerManifest, classifications ClassificationManifest) (Inventory, error) {
	before, err := HashTree(baseDir, roots)
	if err != nil {
		return Inventory{}, err
	}

	discovered, err := Discover(baseDir, roots)
	if err != nil {
		return Inventory{}, err
	}

	if err := ReconcileOwners(discovered, owners); err != nil {
		return Inventory{}, err
	}

	candidates, err := ScanCandidates(baseDir, discovered, owners)
	if err != nil {
		return Inventory{}, err
	}

	after, err := HashTree(baseDir, roots)
	if err != nil {
		return Inventory{}, err
	}
	if before != after {
		return Inventory{}, diagnostic(
			"the canonical tree changed while scanning",
			"pasture#47 requires the scanner to be strictly read-only; before/after hashes must match byte-for-byte",
			"scan.ScanWithManifests", "read-only proof",
			"the produced inventory cannot be trusted and must be discarded",
			"find and remove whatever modified a canonical-root file during the scan, then re-run",
			nil,
		)
	}

	inventory := Classify(candidates, classifications)
	if err := RequireNoOrphanedClassifications(inventory); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}

//go:embed manifest/owners.json
var embeddedOwnerManifest []byte

//go:embed manifest/classifications.json
var embeddedClassificationManifest []byte

// ScanCanonical runs ScanWithManifests against this repository's real,
// checked-in owner and classification manifests (internal/codegen/scan/
// manifest/*.json) over CanonicalRoots. This is the production entrypoint
// #46, #43, #40, and #42 (and any future CLI) call; baseDir is the pasture
// module root (see ModuleRoot).
func ScanCanonical(baseDir string) (Inventory, error) {
	owners, err := DecodeOwnerManifest(embeddedOwnerManifest)
	if err != nil {
		return Inventory{}, err
	}
	classifications, err := DecodeClassificationManifest(embeddedClassificationManifest)
	if err != nil {
		return Inventory{}, err
	}
	return ScanWithManifests(baseDir, CanonicalRoots(), owners, classifications)
}

// ModuleRoot walks upward from the current working directory until it finds
// go.mod, returning that directory. It mirrors tools/codegen/main.go's
// unexported moduleRoot helper so any caller — test or future CLI — can
// locate the real repository root ScanCanonical needs as baseDir without
// re-deriving this walk.
func ModuleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", diagnostic(
			"could not read the current working directory",
			"module-root discovery walks upward from the current working directory",
			"scan.ModuleRoot", "module root discovery",
			"the canonical scan cannot locate its source roots",
			"ensure the process has a valid working directory",
			err,
		)
	}
	dir := wd
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", diagnostic(
				fmt.Sprintf("could not find go.mod walking up from %q", wd),
				"ScanCanonical needs the module root to resolve skills/ and agents/ under it",
				"scan.ModuleRoot", "module root discovery",
				"the canonical scan cannot locate its source roots",
				"run from inside the pasture module, or call ScanWithManifests with an explicit baseDir",
				nil,
			)
		}
		dir = parent
	}
}
