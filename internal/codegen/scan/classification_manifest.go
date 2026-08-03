package scan

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// ClassificationEntry is one checked-in classification decision for exactly
// one candidate occurrence.
//
// The key is (Owner, Pattern, ContentWindow, Section, Ordinal) — content, not
// a bare regex-matched prefix and not raw whole-file encounter order:
//
//   - ContentWindow is the enclosing source line around the match (see
//     Candidate.ContentWindow), not the short matched prefix (e.g. "Skill(/")
//     Candidate.Snippet still reports for display/diagnostics. Two
//     occurrences of the same pattern almost always sit on different lines,
//     so ContentWindow alone usually makes the key unique without any
//     ordinal at all: keying by the bare prefix made the key
//     indistinguishable between two differently-classified occurrences,
//     so reordering them in the source silently swapped which physical
//     location received which classification, with zero manifest diff and
//     zero scan error.
//   - Section additionally scopes Ordinal to the nearest preceding heading
//     (the same Section every Candidate/Location already reports), so
//     Ordinal only disambiguates a genuinely byte-identical ContentWindow
//     within one section, not across the whole file — a section-level
//     reorder (moving a whole subsection, or inserting a new one before an
//     existing group of identical-content duplicates) cannot silently
//     reassign meaning the way whole-file ordinal could.
//   - Ordinal is a last resort: it disambiguates two truly identical lines
//     (same owner, pattern, ContentWindow, and Section) — a swap between
//     those specific occurrences is classification-harmless by construction
//     (a maintainer reviewing byte-identical content under one heading has
//     no way to tell them apart either).
type ClassificationEntry struct {
	Owner          string
	Pattern        PatternID
	ContentWindow  string
	Section        string
	Ordinal        int
	Classification Classification
	Notes          string
}

type classificationKey struct {
	owner         string
	pattern       PatternID
	contentWindow string
	section       string
	ordinal       int
}

func candidateClassificationKey(owner string, pattern PatternID, contentWindow, section string, ordinal int) classificationKey {
	return classificationKey{owner: owner, pattern: pattern, contentWindow: contentWindow, section: section, ordinal: ordinal}
}

// ClassificationManifest is the immutable, validated, checked-in set of
// every explicit candidate classification decision. A candidate with no
// matching entry is not defaulted to any Classification — see
// Inventory/Classify — it is reported unclassified.
type ClassificationManifest struct {
	entries map[classificationKey]ClassificationEntry
}

// NewClassificationManifest validates and constructs a ClassificationManifest.
func NewClassificationManifest(entries []ClassificationEntry) (ClassificationManifest, error) {
	seen := make(map[classificationKey]ClassificationEntry, len(entries))
	for index, entry := range entries {
		if strings.TrimSpace(entry.Owner) == "" {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest entry %d has an empty owner", index),
				"every classification entry must name the owner it was reviewed in",
				"scan.NewClassificationManifest", "classification manifest construction",
				"the entry cannot be matched against any scanned candidate",
				"supply a non-empty owner path",
				nil,
			)
		}
		if !entry.Pattern.IsValid() {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest entry for owner %q has an unknown pattern %q", entry.Owner, entry.Pattern),
				"pattern is the closed, code-owned registry (see PatternIDs)",
				"scan.NewClassificationManifest", "classification manifest construction",
				"the entry cannot be matched against any scanned candidate",
				"use one of PatternIDs()",
				nil,
			)
		}
		if entry.ContentWindow == "" {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest entry for owner %q pattern %q has an empty content window", entry.Owner, entry.Pattern),
				"the content window (the enclosing source line) is the reviewed content this entry classifies",
				"scan.NewClassificationManifest", "classification manifest construction",
				"the entry cannot be matched against any scanned candidate",
				"supply the exact non-empty enclosing line reported by Candidate.ContentWindow",
				nil,
			)
		}
		if strings.TrimSpace(entry.Section) == "" {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest entry for owner %q pattern %q has an empty section", entry.Owner, entry.Pattern),
				"section scopes ordinal disambiguation and must match the candidate's reported Location.Section()",
				"scan.NewClassificationManifest", "classification manifest construction",
				"the entry cannot be matched against any scanned candidate",
				"supply the non-empty section Candidate.Location().Section() reports (\"body\" before the first heading)",
				nil,
			)
		}
		if entry.Ordinal < 0 {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest entry for owner %q pattern %q has a negative ordinal %d", entry.Owner, entry.Pattern, entry.Ordinal),
				"ordinal is a zero-based occurrence index",
				"scan.NewClassificationManifest", "classification manifest construction",
				"the entry cannot be matched against any scanned candidate",
				"use ordinal 0 for the first occurrence of this exact owner/pattern/content-window/section tuple",
				nil,
			)
		}
		if !entry.Classification.IsValid() {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest entry for owner %q pattern %q has an unknown classification %q", entry.Owner, entry.Pattern, entry.Classification),
				"classification is the closed, exhaustive sum (see Classifications)",
				"scan.NewClassificationManifest", "classification manifest construction",
				"the entry cannot classify any candidate",
				"use one of Classifications()",
				nil,
			)
		}
		key := candidateClassificationKey(entry.Owner, entry.Pattern, entry.ContentWindow, entry.Section, entry.Ordinal)
		if _, duplicate := seen[key]; duplicate {
			return ClassificationManifest{}, diagnostic(
				fmt.Sprintf("classification manifest duplicates owner %q pattern %q section %q ordinal %d", entry.Owner, entry.Pattern, entry.Section, entry.Ordinal),
				"every exact candidate occurrence has exactly one classification",
				"scan.NewClassificationManifest", "classification manifest construction",
				"matching could not tell which classification applies",
				"remove the duplicate entry or give it a distinct ordinal",
				nil,
			)
		}
		seen[key] = entry
	}
	return ClassificationManifest{entries: seen}, nil
}

func (m ClassificationManifest) lookup(owner string, pattern PatternID, contentWindow, section string, ordinal int) (ClassificationEntry, bool) {
	entry, ok := m.entries[candidateClassificationKey(owner, pattern, contentWindow, section, ordinal)]
	return entry, ok
}

// Len returns the number of manifested classification entries.
func (m ClassificationManifest) Len() int { return len(m.entries) }

type classificationManifestWire struct {
	Entries []classificationEntryWire `json:"entries"`
}

type classificationEntryWire struct {
	Owner          string `json:"owner"`
	Pattern        string `json:"pattern"`
	ContentWindow  string `json:"content_window"`
	Section        string `json:"section"`
	Ordinal        int    `json:"ordinal"`
	Classification string `json:"classification"`
	Notes          string `json:"notes,omitempty"`
}

// DecodeClassificationManifest strictly decodes and validates a checked-in
// classification manifest document. It first runs the whole document through
// ir.StrictJSONWithPresence (duplicate-member rejection, unknown-field
// rejection, top-level "entries" presence, trailing-content rejection), then
// independently re-checks per-entry field presence: Ordinal's JSON zero
// value (0) is a legitimate first-occurrence value, so an omitted "ordinal"
// must be rejected rather than silently decoded as 0 — the same reasoning
// ir.StrictJSONWithPresence's own doc comment gives for its top-level
// requiredFields, applied one level deeper (ir.StrictJSONWithPresence itself
// only checks top-level document fields).
func DecodeClassificationManifest(data []byte) (ClassificationManifest, error) {
	var wire classificationManifestWire
	if err := ir.StrictJSONWithPresence(data, []string{"entries"}, &wire); err != nil {
		return ClassificationManifest{}, classificationManifestDecodeError(err)
	}

	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return ClassificationManifest{}, classificationManifestDecodeError(err)
	}
	var entryPresence []map[string]json.RawMessage
	if err := json.Unmarshal(topLevel["entries"], &entryPresence); err != nil {
		return ClassificationManifest{}, classificationManifestDecodeError(err)
	}
	requiredEntryFields := []string{"owner", "pattern", "content_window", "section", "ordinal", "classification"}
	for index, fields := range entryPresence {
		for _, field := range requiredEntryFields {
			if _, present := fields[field]; !present {
				return ClassificationManifest{}, classificationManifestDecodeError(
					fmt.Errorf("classification entry %d omits required field %q", index, field),
				)
			}
		}
	}

	entries := make([]ClassificationEntry, 0, len(wire.Entries))
	for _, entry := range wire.Entries {
		entries = append(entries, ClassificationEntry{
			Owner:          entry.Owner,
			Pattern:        PatternID(entry.Pattern),
			ContentWindow:  entry.ContentWindow,
			Section:        entry.Section,
			Ordinal:        entry.Ordinal,
			Classification: Classification(entry.Classification),
			Notes:          entry.Notes,
		})
	}
	return NewClassificationManifest(entries)
}

func classificationManifestDecodeError(cause error) error {
	why := "the checked-in classification manifest must be exact strict JSON: no duplicate members, no unknown fields, every entry's owner/pattern/content_window/section/ordinal/classification field explicitly present, and no trailing content"
	if ir.IsDuplicateJSONMember(cause) {
		why = "the checked-in classification manifest repeats one JSON object member, which encoding/json would otherwise silently resolve by \"last member wins\""
	}
	return diagnostic(
		"classification manifest JSON could not be decoded",
		why,
		"scan.DecodeClassificationManifest", "classification manifest decoding",
		"the scan cannot classify any candidate without a valid classification manifest",
		"correct the manifest JSON and re-run the scan",
		cause,
	)
}
