// Package lineage derives read-side occurrence lineage edges from committed
// Level-2 lifecycle evidence.
//
// Lineage is READ-SIDE and PROVISIONAL (Stage-3 M5, ratified). The hook
// delivery path is never touched: nothing here runs at delivery, adds a read to
// the delivery hot path, or emits a delivery-time effect. Edges are materialized
// only by the explicit `pasture hook lifecycle lineage` command, which reads
// bounded committed records, derives the MISSING predecessor edges with the pure
// DeriveLinks function below, and legalizes a single bounded lineage-links write
// through the normative gate. Delivery-time linking was explicitly rejected;
// this package's derivation is loss-free precisely because retention is
// store-all, so a re-derivation always sees the full history.
//
// The core (DeriveLinks) is a pure function: it takes committed inputs and
// returns the edges that are not yet committed. It performs no I/O, holds no
// clock or store, and is deterministic — re-running it over the same inputs
// (once the derived edges are themselves committed) yields nothing. That
// property is what makes materialization idempotent without any cursor state:
// idempotence is content-addressed over (harness, kind, value, from, to), not
// tracked with a high-water mark.
package lineage

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// LinkFact is a derived, not-yet-committed predecessor edge between two
// occurrences that carry the same native identity (Kind, Value) on one harness.
// It is the pure-derivation analogue of a committed model.LinkRecord: it carries
// every field that content-addresses a link (harness, kind, value, from, to)
// but not the journal LinkID, which is assigned only when the edge is committed.
//
// A LinkFact is emitted by DeriveLinks in journal order (From strictly precedes
// To). The write path converts each fact into one durable
// pasture.lifecycle.link.v1 effect; the read path reconstructs the same edge as
// a model.LinkRecord.
type LinkFact struct {
	// Harness is the enabled host family this edge belongs to. Chains are per
	// host, so a fact never links occurrences from two different harnesses.
	Harness ir.HarnessID
	// Kind is the native identity kind shared by the two linked occurrences.
	Kind runtime.NativeIdentityKind
	// Value is the native identity value shared by the two linked occurrences.
	Value string
	// From is the immediately preceding occurrence carrying (Kind, Value).
	From model.OccurrenceID
	// To is the succeeding occurrence carrying (Kind, Value).
	To model.OccurrenceID
}

// ContentID is the content address of the edge over (harness, kind, value,
// from, to). Two facts with the same address are the same link: this is the
// idempotency key that lets materialization diff derived edges against the
// already-committed set without any cursor. The encoding is length-prefixed so
// distinct field boundaries can never collide (e.g. a value ending where the
// next field begins).
func (f LinkFact) ContentID() model.ContentIdentity {
	h := sha256.New()
	writeField := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	var kindByte [1]byte
	kindByte[0] = byte(f.Kind)
	var from, to [8]byte
	binary.BigEndian.PutUint64(from[:], uint64(f.From.JournalID()))
	binary.BigEndian.PutUint64(to[:], uint64(f.To.JournalID()))

	writeField([]byte(f.Harness))
	writeField(kindByte[:])
	writeField([]byte(f.Value))
	writeField(from[:])
	writeField(to[:])

	var out model.ContentIdentity
	copy(out[:], h.Sum(nil))
	return out
}

// LinkRecordContentID content-addresses an already-committed link with the same
// scheme DeriveLinks uses for derived facts, so a committed record and the fact
// that would re-derive it share one address. This is the join key for the
// derivation-vs-committed diff.
func LinkRecordContentID(r model.LinkRecord) model.ContentIdentity {
	return LinkFact{
		Harness: r.Harness,
		Kind:    r.Kind,
		Value:   r.Value,
		From:    r.From,
		To:      r.To,
	}.ContentID()
}

// identityKey is the per-host correlation key an occurrence chain is threaded
// over. Two occurrences with the same identityKey belong to the same chain.
type identityKey struct {
	harness ir.HarnessID
	kind    runtime.NativeIdentityKind
	value   string
}

// DeriveLinks returns the MISSING predecessor edges for the given committed
// lifecycle records — the edges that are not already present in committed.
//
// It is PURE and deterministic: records are processed in ascending occurrence
// journal order (a defensive copy is sorted, so caller order does not matter),
// and for each (harness, kind, value) chain the immediately preceding
// occurrence is linked to the current one. An edge is emitted only if its
// content address (harness, kind, value, from, to) is neither already committed
// nor already emitted in this call, so the result is duplicate-free.
//
// Idempotence: once the returned facts are committed, a second DeriveLinks over
// the same records (now with those links in committed) returns an empty slice —
// the read-side command materializes once and is a no-op thereafter. This
// derivation NEVER runs at delivery; it is only invoked by the explicit lineage
// command over committed evidence.
//
// It returns an actionable error only for structurally corrupt committed input
// (an occurrence whose runtime contract does not name an enabled harness), which
// indicates a damaged journal rather than a caller mistake.
func DeriveLinks(records []model.LifecycleRecord, committed []model.LinkRecord) ([]LinkFact, error) {
	committedByContent := make(map[model.ContentIdentity]struct{}, len(committed))
	for _, record := range committed {
		committedByContent[LinkRecordContentID(record)] = struct{}{}
	}

	ordered := append([]model.LifecycleRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Occurrence.JournalID() < ordered[j].Occurrence.JournalID()
	})

	predecessor := make(map[identityKey]model.OccurrenceID)
	emitted := make(map[model.ContentIdentity]struct{})
	var facts []LinkFact

	for _, record := range ordered {
		occurrence := record.Occurrence
		to := occurrence.OccurrenceID
		if to.JournalID() == 0 {
			// An unjournaled occurrence cannot anchor a chain edge; committed
			// records always carry a journal identity, so skip defensively
			// rather than mint a zero endpoint.
			continue
		}
		harness := occurrence.RuntimeContract.Harness()
		if !harness.IsValid() {
			return nil, fmt.Errorf(
				"derive lifecycle lineage: committed occurrence %d names runtime contract %q whose harness is not an enabled host; the lifecycle journal is corrupt at this occurrence and lineage cannot be reconstructed until it is repaired (run pasture migrate/rebuild and verify the occurrence's contract)",
				to.JournalID(), occurrence.RuntimeContract.String(),
			)
		}
		for _, interpreted := range record.Interpreted() {
			for _, identity := range interpreted.Identities() {
				if !identity.Kind.IsValid() || identity.Value == "" {
					continue
				}
				key := identityKey{harness: harness, kind: identity.Kind, value: identity.Value}
				prior, seen := predecessor[key]
				predecessor[key] = to
				if !seen {
					continue
				}
				if prior.JournalID() == to.JournalID() {
					// Same identity appearing twice on one occurrence never
					// forms a self-edge.
					continue
				}
				fact := LinkFact{
					Harness: harness,
					Kind:    identity.Kind,
					Value:   identity.Value,
					From:    prior,
					To:      to,
				}
				content := fact.ContentID()
				if _, done := committedByContent[content]; done {
					continue
				}
				if _, done := emitted[content]; done {
					continue
				}
				emitted[content] = struct{}{}
				facts = append(facts, fact)
			}
		}
	}
	return facts, nil
}
