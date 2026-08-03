package scan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// OwnerEntry is one checked-in disposition for a canonical owner path.
type OwnerEntry struct {
	// Path is the owner's path relative to the scan base directory,
	// slash-separated (e.g. "skills/worker/SKILL.md").
	Path string
	// Disposition is the owner's closed active/dead disposition.
	Disposition OwnerDisposition
	// Reason is required (non-empty) when Disposition is OwnerDead and
	// otherwise ignored — pasture#47 requires "explicit dead-owner
	// dispositions", not a silent skip.
	Reason string
}

// OwnerManifest is the immutable, validated, checked-in set of every
// canonical owner's disposition. It is reconciliation input only — see
// ReconcileOwners — never the discovery source: Discover walks the
// filesystem independently, and OwnerManifest is compared against what
// Discover actually found.
type OwnerManifest struct {
	entries map[string]OwnerEntry
}

// NewOwnerManifest validates and constructs an OwnerManifest from entries.
func NewOwnerManifest(entries []OwnerEntry) (OwnerManifest, error) {
	seen := make(map[string]OwnerEntry, len(entries))
	for index, entry := range entries {
		if strings.TrimSpace(entry.Path) == "" {
			return OwnerManifest{}, diagnostic(
				fmt.Sprintf("owner manifest entry %d has an empty path", index),
				"every owner-manifest entry must name a real relative Markdown path",
				"scan.NewOwnerManifest", "owner manifest construction",
				"the manifest cannot be reconciled against discovery",
				"supply a non-empty relative path for every entry",
				nil,
			)
		}
		if !entry.Disposition.IsValid() {
			return OwnerManifest{}, diagnostic(
				fmt.Sprintf("owner manifest entry %q has an unknown disposition %q", entry.Path, entry.Disposition),
				"disposition is a closed active/dead sum (see OwnerDispositions)",
				"scan.NewOwnerManifest", "owner manifest construction",
				"the manifest cannot be reconciled against discovery",
				"use scan.OwnerActive or scan.OwnerDead",
				nil,
			)
		}
		if entry.Disposition == OwnerDead && strings.TrimSpace(entry.Reason) == "" {
			return OwnerManifest{}, diagnostic(
				fmt.Sprintf("dead owner %q has no reason", entry.Path),
				"pasture#47 requires explicit dead-owner dispositions, not a silent skip",
				"scan.NewOwnerManifest", "owner manifest construction",
				"a reviewer cannot audit why this owner is excluded from candidate scanning",
				"supply a non-empty Reason for every dead owner",
				nil,
			)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return OwnerManifest{}, diagnostic(
				fmt.Sprintf("owner manifest duplicates path %q", entry.Path),
				"every owner has exactly one disposition",
				"scan.NewOwnerManifest", "owner manifest construction",
				"reconciliation could not tell which disposition applies",
				"remove the duplicate owner-manifest entry",
				nil,
			)
		}
		seen[entry.Path] = entry
	}
	return OwnerManifest{entries: seen}, nil
}

// Lookup returns the manifest entry for path, if present.
func (m OwnerManifest) Lookup(path string) (OwnerEntry, bool) {
	entry, ok := m.entries[path]
	return entry, ok
}

// Paths returns every manifested owner path, sorted.
func (m OwnerManifest) Paths() []string {
	paths := make([]string, 0, len(m.entries))
	for path := range m.entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// Len returns the number of manifested owners.
func (m OwnerManifest) Len() int { return len(m.entries) }

// ReconcileError aggregates every owner-manifest drift problem found by
// ReconcileOwners into one actionable error, so a caller sees every problem
// in one report instead of fixing them one failed run at a time.
type ReconcileError struct{ Problems []string }

func (e *ReconcileError) Error() string {
	return fmt.Sprintf(
		"what: %d owner-manifest reconciliation problem(s) found: %s; "+
			"why: independent canonical-root discovery must exactly match the checked-in owner manifest; "+
			"where: scan.ReconcileOwners; phase: owner reconciliation; "+
			"impact: the scan cannot trust which owners are active, dead, or drifted; "+
			"fix: add a manifest entry for every unlisted active file, remove every stale entry, and give every owner an explicit disposition",
		len(e.Problems), strings.Join(e.Problems, "; "),
	)
}

// ReconcileOwners compares discovered (Discover's independent result)
// against manifest and returns a *ReconcileError naming every problem:
//
//   - an unlisted active file: discovered but absent from the manifest;
//   - a stale manifest entry: manifested but no longer discovered; and
//   - (structurally impossible once NewOwnerManifest has validated manifest)
//     a missing/invalid disposition.
//
// A nil error means every discovered path has exactly one manifested
// disposition and every manifested path was actually discovered.
func ReconcileOwners(discovered []string, manifest OwnerManifest) error {
	discoveredSet := make(map[string]bool, len(discovered))
	for _, path := range discovered {
		discoveredSet[path] = true
	}

	var problems []string
	for _, path := range discovered {
		if _, ok := manifest.Lookup(path); !ok {
			problems = append(problems, fmt.Sprintf("unlisted active file %q has no owner-manifest entry", path))
		}
	}
	for _, path := range manifest.Paths() {
		if !discoveredSet[path] {
			problems = append(problems, fmt.Sprintf("stale owner-manifest entry %q was not discovered under any canonical root", path))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return &ReconcileError{Problems: problems}
}

// ownerManifestWire is the checked-in JSON shape for OwnerManifest.
type ownerManifestWire struct {
	Owners []ownerEntryWire `json:"owners"`
}

type ownerEntryWire struct {
	Path        string `json:"path"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason,omitempty"`
}

// DecodeOwnerManifest strictly decodes and validates a checked-in owner
// manifest document (see ir.StrictJSONWithPresence and NewOwnerManifest).
func DecodeOwnerManifest(data []byte) (OwnerManifest, error) {
	var wire ownerManifestWire
	if err := ir.StrictJSONWithPresence(data, []string{"owners"}, &wire); err != nil {
		return OwnerManifest{}, ownerManifestDecodeError(err)
	}
	entries := make([]OwnerEntry, 0, len(wire.Owners))
	for _, owner := range wire.Owners {
		entries = append(entries, OwnerEntry{
			Path:        owner.Path,
			Disposition: OwnerDisposition(owner.Disposition),
			Reason:      owner.Reason,
		})
	}
	return NewOwnerManifest(entries)
}

func ownerManifestDecodeError(cause error) error {
	why := "the checked-in owner manifest must be exact strict JSON: no duplicate members, no unknown fields, the required \"owners\" field present, and no trailing content"
	if ir.IsDuplicateJSONMember(cause) {
		why = "the checked-in owner manifest repeats one JSON object member, which encoding/json would otherwise silently resolve by \"last member wins\""
	}
	return diagnostic(
		"owner manifest JSON could not be decoded",
		why,
		"scan.DecodeOwnerManifest", "owner manifest decoding",
		"the scan cannot reconcile discovery without a valid owner manifest",
		"correct the manifest JSON and re-run the scan",
		cause,
	)
}
