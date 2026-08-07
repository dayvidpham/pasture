package model

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// LinkRecord is a committed, read-side occurrence lineage edge: a durable
// predecessor link between two occurrences that carry the same native identity
// (Kind, Value) on one harness. It is classified as an ImmutableSnapshot in the
// lifecycle guard map (the classification entry is pre-landed by the write-gate
// slice, per the Stage-3 IP-4 integration point) because a link is committed
// once and never mutated: re-materialization is idempotent against the set of
// already-committed links, not an in-place update.
//
// Chains are reconstructed PER HOST (Harness): asymmetric chain depth across
// harnesses is expected and accepted, because only enabled events on each
// harness produce occurrences. LinkRecord carries only correlation data owned
// by the native harness (Kind, Value); it is not a Pasture actor, authority,
// decision, or review-evidence identity.
//
// The field set is the IP-3 export contract consumed by the context-disclosure
// slice (its chain summaries read committed LinkRecords). Its shape is fixed at
// L1 and must not change without coordinating the consuming slice.
type LinkRecord struct {
	// LinkID is the journal identity of this committed link.
	LinkID LifecycleLinkID
	// Harness is the enabled host family whose occurrence chain this edge
	// belongs to. Chains are per host, so an edge never crosses harnesses.
	Harness ir.HarnessID
	// Kind is the native identity kind shared by the two linked occurrences
	// (session, turn, request, tool-call, agent, message).
	Kind runtime.NativeIdentityKind
	// Value is the native identity value shared by the two linked occurrences.
	Value string
	// From is the immediately preceding occurrence carrying (Kind, Value).
	From OccurrenceID
	// To is the succeeding occurrence carrying (Kind, Value); this edge records
	// that From directly precedes To in the per-host identity chain.
	To OccurrenceID
}

// JournalID returns the committed link's journal identity, satisfying the
// journal-record convention used across the lifecycle model.
func (r LinkRecord) JournalID() provenance.JournalID { return r.LinkID.JournalID() }

// IsValid reports whether the link record is well-formed: a journaled link over
// an enabled harness and a declared native identity kind, with a non-empty
// value and two distinct occurrence endpoints. A self-edge (From == To) is
// invalid because a link records that one occurrence precedes another.
func (r LinkRecord) IsValid() bool {
	return r.LinkID.JournalID() != 0 &&
		r.Harness.IsValid() &&
		r.Kind.IsValid() &&
		r.Value != "" &&
		r.From.JournalID() != 0 &&
		r.To.JournalID() != 0 &&
		r.From.JournalID() != r.To.JournalID()
}
