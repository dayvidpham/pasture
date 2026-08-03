package scan

import (
	"fmt"
	"sort"
	"strings"
)

// ClassifiedCandidate pairs one scanned Candidate with its manifest
// classification decision. Classified is false exactly when no
// ClassificationManifest entry matched — pasture#47's "no fallback assigns
// meaning": Classification is the zero value in that case and must not be
// read as ClassificationOrchestration or any other member. Ordinal is the
// zero-based occurrence index Classify assigned this candidate within its
// (owner, pattern, ContentWindow, section) scope — the same value looked up
// against (and, once classified, matched to) a ClassificationEntry.
type ClassifiedCandidate struct {
	Candidate      Candidate
	Classification Classification
	Classified     bool
	Notes          string
	Ordinal        int
}

// occurrenceKey identifies one exact candidate occurrence's classification
// scope, without the ordinal: Classify computes the ordinal itself by
// counting prior candidates sharing the same owner/pattern/content-window/
// section tuple, in encounter order. Scoping by ContentWindow (the enclosing
// source line, not the short matched Snippet) and by Section (rather than
// whole-file) is what makes ordinal assignment robust to an unrelated
// documentation edit reordering occurrences elsewhere in the file — see
// ClassificationEntry's doc comment.
type occurrenceKey struct {
	owner         string
	pattern       PatternID
	contentWindow string
	section       string
}

// Classify assigns a classification-manifest decision to every candidate.
// candidates must already be in deterministic scan order (see
// scanFileCandidates/ScanCandidates): Classify computes each candidate's
// classification-manifest ordinal from its position among prior candidates
// sharing the same owner/pattern/content-window/section tuple, so ordinal
// assignment is itself deterministic and reproducible across runs and
// worktrees, and preserves that same order in the returned Inventory.
//
// The returned Inventory also retains manifest and per-key match/occurrence
// bookkeeping so OrphanedClassifications/RequireNoOrphanedClassifications
// can later report every checked-in entry that matched no real candidate,
// without recomputing (and risking drifting from) this same ordinal
// assignment a second time.
func Classify(candidates []Candidate, manifest ClassificationManifest) Inventory {
	counts := make(map[occurrenceKey]int, len(candidates))
	matched := make(map[classificationKey]bool, len(candidates))
	out := make([]ClassifiedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		occ := occurrenceKey{
			owner:         candidate.Location().Owner(),
			pattern:       candidate.Pattern(),
			contentWindow: candidate.ContentWindow(),
			section:       candidate.Location().Section(),
		}
		ordinal := counts[occ]
		counts[occ] = ordinal + 1

		key := candidateClassificationKey(occ.owner, occ.pattern, occ.contentWindow, occ.section, ordinal)
		entry, ok := manifest.entries[key]
		classified := ClassifiedCandidate{Candidate: candidate, Ordinal: ordinal}
		if ok {
			classified.Classification = entry.Classification
			classified.Classified = true
			classified.Notes = entry.Notes
			matched[key] = true
		}
		out = append(out, classified)
	}
	return Inventory{candidates: out, manifest: manifest, matchedKeys: matched, occurrenceCounts: counts}
}

// Inventory is the classified candidate set #46, #43, and #40 consume
// before freezing their own closed sets, and #42 consumes through
// RequireZeroUnclassified before enabling strict rejection.
type Inventory struct {
	candidates       []ClassifiedCandidate
	manifest         ClassificationManifest
	matchedKeys      map[classificationKey]bool
	occurrenceCounts map[occurrenceKey]int
}

// Candidates returns a defensive copy of every classified candidate, in
// deterministic scan order.
func (inv Inventory) Candidates() []ClassifiedCandidate {
	return append([]ClassifiedCandidate(nil), inv.candidates...)
}

// Len returns the total candidate count, classified and unclassified.
func (inv Inventory) Len() int { return len(inv.candidates) }

// Unclassified returns every candidate with no matching classification-
// manifest entry, in deterministic scan order.
func (inv Inventory) Unclassified() []ClassifiedCandidate {
	var out []ClassifiedCandidate
	for _, candidate := range inv.candidates {
		if !candidate.Classified {
			out = append(out, candidate)
		}
	}
	return out
}

// UnclassifiedCount returns the number of candidates with no matching
// classification-manifest entry.
func (inv Inventory) UnclassifiedCount() int { return len(inv.Unclassified()) }

// CountByClassification returns the number of candidates explicitly
// classified as c.
func (inv Inventory) CountByClassification(c Classification) int {
	count := 0
	for _, candidate := range inv.candidates {
		if candidate.Classified && candidate.Classification == c {
			count++
		}
	}
	return count
}

// RequireZeroUnclassified is the strict-gate check pasture#42's migration
// gate consumes: a nonzero unclassified candidate count is a hard
// prerequisite failure that must block strict-mode activation. It reports
// every unclassified candidate's owner/file/section/range/ordinal and
// pattern so a maintainer can classify each one without re-running the
// scanner to find them.
func RequireZeroUnclassified(inv Inventory) error {
	unclassified := inv.Unclassified()
	if len(unclassified) == 0 {
		return nil
	}
	const maxListed = 10
	listed := unclassified
	truncated := false
	if len(listed) > maxListed {
		listed = listed[:maxListed]
		truncated = true
	}
	what := fmt.Sprintf("%d candidate(s) are unclassified", len(unclassified))
	for _, candidate := range listed {
		location := candidate.Candidate.Location()
		rng := location.Range()
		what += fmt.Sprintf("; %s:%s [%d..%d] (%s, pattern %q, ordinal %d)",
			location.File(), location.Section(), rng.Start, rng.Stop, candidate.Candidate.ASTNode(), candidate.Candidate.Pattern(), candidate.Ordinal)
	}
	if truncated {
		what += fmt.Sprintf("; (%d more not listed)", len(unclassified)-maxListed)
	}
	return diagnostic(
		what,
		"pasture#47 requires every candidate to be explicitly classified or visibly unclassified before pasture#42's strict rejection gate may activate",
		"scan.RequireZeroUnclassified", "strict-gate migration check",
		"pasture#42 cannot enable strict rejection and no target output may be produced while any candidate is unclassified",
		"add a classification-manifest entry for every listed candidate (see ClassificationManifest), then re-run the scan",
		nil,
	)
}

// OrphanedClassifications returns every checked-in ClassificationManifest
// entry that matched no real candidate during Classify — content that was
// reviewed and classified but no longer corresponds to anything the current
// scan actually found (the source occurrence was edited or removed, the
// owner/pattern was renamed, or the content-window/section/ordinal was
// mistyped). This is the classification-manifest counterpart to
// ReconcileOwners' stale-entry detection.
func (inv Inventory) OrphanedClassifications() []ClassificationEntry {
	var out []ClassificationEntry
	for key, entry := range inv.manifest.entries {
		if !inv.matchedKeys[key] {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		if out[i].Pattern != out[j].Pattern {
			return out[i].Pattern < out[j].Pattern
		}
		if out[i].Section != out[j].Section {
			return out[i].Section < out[j].Section
		}
		if out[i].ContentWindow != out[j].ContentWindow {
			return out[i].ContentWindow < out[j].ContentWindow
		}
		return out[i].Ordinal < out[j].Ordinal
	})
	return out
}

// RequireNoOrphanedClassifications aggregates every orphaned classification-
// manifest entry into one actionable error (mirroring ReconcileError's
// everything-in-one-report design), naming each entry's owner/pattern/
// section/ordinal and, where cheap, a closest-miss reason: how many real
// occurrences of that entry's (owner, pattern, content-window, section)
// scope the current scan actually found, so a maintainer can immediately see
// whether the entry's ordinal simply ran past the real occurrence count.
// ScanWithManifests calls this between Classify and its own return, so the
// production pipeline fails on classification-manifest drift exactly as it
// already fails on owner-manifest drift (see ReconcileOwners).
func RequireNoOrphanedClassifications(inv Inventory) error {
	orphaned := inv.OrphanedClassifications()
	if len(orphaned) == 0 {
		return nil
	}
	var problems []string
	for _, entry := range orphaned {
		occ := occurrenceKey{owner: entry.Owner, pattern: entry.Pattern, contentWindow: entry.ContentWindow, section: entry.Section}
		actual := inv.occurrenceCounts[occ]
		problems = append(problems, fmt.Sprintf(
			"owner %q pattern %q section %q ordinal %d has no matching candidate (this scan found %d occurrence(s) of that exact owner/pattern/content-window/section)",
			entry.Owner, entry.Pattern, entry.Section, entry.Ordinal, actual,
		))
	}
	return diagnostic(
		fmt.Sprintf("%d classification-manifest entry(ies) matched no candidate: %s", len(orphaned), strings.Join(problems, "; ")),
		"the checked-in classification manifest must be an accurate record of what was actually reviewed; an unmatched entry means the source occurrence it once classified was edited, removed, renamed, or mistyped",
		"scan.RequireNoOrphanedClassifications", "classification manifest reconciliation",
		"the manifest can drift arbitrarily far from reality with no scan failure, silently misrepresenting what a maintainer reviewed",
		"remove the stale entry, or correct its owner/pattern/content_window/section/ordinal to match a real candidate this scan found",
		nil,
	)
}
