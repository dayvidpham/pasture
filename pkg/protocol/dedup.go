package protocol

import "github.com/google/uuid"

// dedupNamespace is the fixed UUID namespace for forensic-row deduplication
// keys. It is PINNED: changing it changes every derived key and breaks
// exactly-once across already-recorded rows. Both forensic tiers (audit events
// and provenance activities) derive their keys from this single namespace.
var dedupNamespace = uuid.MustParse("8b1d6e2a-3c47-5f90-a1b2-c3d4e5f60718")

// DedupKey derives the deterministic deduplication key for a single forensic
// emission. It is a name-based UUID (version 5) over the name
//
//	"<epochID>/<phase>/<kind>/<stepSeq>"
//
// with field order and the "/" separator fixed. The same inputs always yield
// the same key, and distinct epochs always yield distinct keys (the epoch is
// hashed into the name), so a crash-replay collapses onto the same row while
// two different epochs at the same (phase, kind, stepSeq) stay distinct.
//
// Both forensic tiers call this one function: the audit tier passes its
// event_type as kind and stores the result in the audit_events dedup_key
// column; the activities tier passes its activity_kind as kind and uses the
// result as the activity's primary-key id. They differ only in storage, never
// in derivation.
//
// Invariant: at most one forensic emission of a given kind per step. Two
// emissions of the same kind within one durable step would derive the same key
// and the second would be dropped by the ON CONFLICT clause; callers that need
// multiple same-kind emissions in one step must disambiguate the stepSeq.
func DedupKey(epochID, phase, kind, stepSeq string) string {
	name := epochID + "/" + phase + "/" + kind + "/" + stepSeq
	return uuid.NewSHA1(dedupNamespace, []byte(name)).String()
}
