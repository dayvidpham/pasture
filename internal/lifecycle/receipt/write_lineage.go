package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/lineage"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

const (
	lineageCommand         = "pasture.lifecycle.link.append/v1"
	lineageLinkKind        = provenance.EvidenceKind("pasture.lifecycle.link.v1")
	lineageOperationPrefix = "pasture.lifecycle.link."
)

// linkPayload is the canonical, wall-clock-free wire form of one committed
// occurrence lineage edge. Kind is encoded as its native-identity ordinal (the
// same encoding interpreted evidence uses for identity kinds), and the endpoints
// are the two occurrences' journal identities. The bytes are content-derived, so
// the whole lineage operation is byte-identical across two derivations that
// discover the same edge set — the journal's replay arbiter then collapses a
// concurrent duplicate to one committed operation.
type linkPayload struct {
	Harness string                     `json:"harness"`
	Kind    runtime.NativeIdentityKind `json:"kind"`
	Value   string                     `json:"value"`
	From    int64                      `json:"from"`
	To      int64                      `json:"to"`
}

// lineageRefusal builds an actionable validation error for a malformed or
// out-of-bounds lineage-links write, filling the shared where/impact text so
// each call site states only its specific what/why/fix. It mirrors the receipt
// package's invalid() helper and is C-actionable-errors compliant.
func lineageRefusal(what, why, fix string) error {
	return structured(pasterrors.CategoryValidation, what, why,
		"Constructing a lineage-links gated write (internal/lifecycle/receipt/write_lineage.go in receipt.NewLineageLinks).",
		"No lineage-links write was constructed and no warrant can be committed.",
		fix, nil)
}

// NewLineageLinks builds the lineage-links gated write for a set of derived
// predecessor edges. It is the write-class analogue of NewDefinitionActivation:
// it produces one operation carrying N ordered link effects (slot "link-<i>",
// kind pasture.lifecycle.link.v1) with a DETERMINISTIC, content-derived
// operation identity.
//
// It refuses, with an actionable error, an empty set, a set larger than
// gate.MaxLinksPerOperation (the operator must "narrow the scope with
// --binding" — pagination is a deferred follow-up, so an over-cap derivation is
// refused rather than split), a set spanning more than one harness (chains are
// per host and one lineage operation is legalized for exactly one harness), or a
// malformed edge. Idempotence is content-addressed over (harness, kind, value,
// from, to): a second materialization over already-committed links derives an
// empty set and never reaches this constructor, and two racing identical
// derivations build a byte-identical operation that collapses to one commit.
func NewLineageLinks(links []lineage.LinkFact) (GatedWrite, error) {
	if len(links) == 0 {
		return GatedWrite{}, lineageRefusal(
			"a lineage-links write carries no edges",
			"a lineage operation commits between one and the bounded maximum of predecessor edges, so an empty derivation is nothing to write",
			"materialize lineage only when the derivation reports missing edges",
		)
	}
	if len(links) > gate.MaxLinksPerOperation {
		return GatedWrite{}, lineageRefusal(
			fmt.Sprintf("a lineage-links write has %d edges, above the per-operation cap of %d: narrow the scope with --binding", len(links), gate.MaxLinksPerOperation),
			"a lineage operation commits at most the bounded maximum of predecessor edges so one write cannot grow unbounded; splitting into pages is a deferred follow-up",
			"narrow the scope with --binding so the derivation yields at most the per-operation cap of edges",
		)
	}

	ordered := append([]lineage.LinkFact(nil), links...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].ContentID(), ordered[j].ContentID()
		return bytes.Compare(a[:], b[:]) < 0
	})

	harness := ordered[0].Harness
	seen := make(map[model.ContentIdentity]struct{}, len(ordered))
	effects := make([]provenance.Effect, 0, len(ordered))
	digestAccumulator := sha256.New()
	for index, fact := range ordered {
		if fact.Harness != harness {
			return GatedWrite{}, lineageRefusal(
				fmt.Sprintf("a lineage-links write spans harnesses %q and %q", harness, fact.Harness),
				"occurrence chains are reconstructed per host, so one lineage operation is legalized for exactly one harness",
				"narrow the scope with --binding to a single harness's identity so the derivation yields one host's chain",
			)
		}
		if err := validateLinkFact(fact); err != nil {
			return GatedWrite{}, err
		}
		content := fact.ContentID()
		if _, duplicate := seen[content]; duplicate {
			continue
		}
		seen[content] = struct{}{}

		payload, encErr := canonicalLinkPayload(fact)
		if encErr != nil {
			return GatedWrite{}, lineageRefusal(
				"a lineage-links edge could not be canonically encoded",
				"the validated edge failed JSON encoding, which indicates an internal contract defect",
				"report the incompatible edge shape; it cannot be committed until the encoder is corrected",
			)
		}
		payloadDigest := sha256.Sum256(payload)
		effects = append(effects, provenance.Effect{
			Sort:          provenance.EffectEvidence,
			ResultSlot:    provenance.ResultSlotID(fmt.Sprintf("link-%d", index)),
			EvidenceKind:  lineageLinkKind,
			ContentDigest: payloadDigest[:],
			Payload:       append(json.RawMessage(nil), payload...),
		})
		digestAccumulator.Write(content[:])
	}

	setDigest := digestAccumulator.Sum(nil)
	operationID := provenance.OperationID(lineageOperationPrefix + hex.EncodeToString(setDigest[:16]))
	command := sha256.Sum256(append([]byte(lineageCommand+"\x00"), setDigest...))
	return GatedWrite{
		class:         gate.WriteLineageLinks,
		command:       lineageCommand,
		operationID:   operationID,
		commandDigest: command[:],
		effects:       effects,
		constructed:   true,
	}, nil
}

func validateLinkFact(fact lineage.LinkFact) error {
	switch {
	case !fact.Harness.IsValid():
		return lineageRefusal(
			"a lineage-links edge names no enabled harness",
			"every committed link belongs to one enabled host's occurrence chain",
			"derive lineage only from committed occurrences whose runtime contract names an enabled harness",
		)
	case !fact.Kind.IsValid():
		return lineageRefusal(
			"a lineage-links edge has an undeclared native identity kind",
			"a link threads a declared native correlation kind (session, tool-call, ...)",
			"derive lineage only from interpreted identities with a declared native identity kind",
		)
	case fact.Value == "" || len(fact.Value) > 512:
		return lineageRefusal(
			"a lineage-links edge has an empty or over-long identity value",
			"a native identity value is 1 through 512 bytes, matching the interpreted-evidence bound",
			"derive lineage only from interpreted identities with a 1..512-byte value",
		)
	case fact.From.JournalID() == 0 || fact.To.JournalID() == 0 || fact.From.JournalID() == fact.To.JournalID():
		return lineageRefusal(
			"a lineage-links edge has invalid or self-referential endpoints",
			"a link records that one committed occurrence directly precedes another, so both endpoints are distinct journaled occurrences",
			"derive lineage from committed occurrences with distinct journal identities",
		)
	}
	return nil
}

func canonicalLinkPayload(fact lineage.LinkFact) ([]byte, error) {
	return encodeCanonical(linkPayload{
		Harness: string(fact.Harness),
		Kind:    fact.Kind,
		Value:   fact.Value,
		From:    int64(fact.From.JournalID()),
		To:      int64(fact.To.JournalID()),
	})
}

// LineageCommitReceipt is the result of committing a lineage-links GatedWrite.
// Committed is the number of link rows the operation produced; ShortCircuited is
// true when a byte-identical operation had already been committed (a concurrent
// duplicate collapsed benignly to one commit), in which case no new rows were
// folded.
type LineageCommitReceipt struct {
	Operation      provenance.OperationID
	ShortCircuited bool
	Committed      int
}

// CommitLineage is the lineage-links commit surface of the normative write gate.
//
// It exists alongside Service.Commit because Service.Commit is specialized to
// the definition-activation write (it resolves and requires the definition
// result slot); a lineage operation produces "link-<i>" slots instead. Both
// surfaces enforce the SAME normative discipline: gate.Authorize is called
// FIRST, so a zero or class-mismatched warrant is refused with a typed
// *gate.Refusal before any I/O, and an unwarranted lineage write cannot reach
// the store. The operation identity and effects are deterministic, so a
// concurrent duplicate is admitted as CommittedExact/ShortCircuited with a nil
// error rather than a conflict.
func (s Service) CommitLineage(ctx context.Context, warrant gate.Warrant, write GatedWrite) (LineageCommitReceipt, error) {
	result, operationID, err := s.commitGated(ctx, warrant, write, gatedCommitSurface{
		expected: gate.WriteLineageLinks,
		notConstructed: gatedInvalidText{
			"The gated lifecycle write is not a constructed lineage-links write.",
			"CommitLineage commits only a GatedWrite built by receipt.NewLineageLinks.",
			"Build the write through receipt.NewLineageLinks before committing.",
		},
		incompleteWiring: gatedInvalidText{
			"The lifecycle receipt service is incompletely wired for gated writes.",
			"Identity resolution, a clock, and the provenance journal are all required to commit a gated write.",
			"Construct the service through tasks.NewLifecycleReceiptService.",
		},
		canonicalBoundary: gatedStructuredText{
			"The lineage-links write could not cross the canonical journal boundary.",
			"The validated link effects did not produce one canonical operation.",
			"Committing a lineage-links gated write (internal/lifecycle/receipt/write_lineage.go in receipt.Service.CommitLineage).",
			"No lineage operation was committed.",
			"Construct effects through receipt.NewLineageLinks and retry.",
		},
		applyRejected: gatedStructuredText{
			"The lineage-links write could not be committed.",
			"The provenance journal rejected the lineage-links operation.",
			"Committing a lineage-links gated write (internal/lifecycle/receipt/write_lineage.go in receipt.Service.CommitLineage).",
			"No occurrence links were recorded.",
			"Inspect the error; a persisted operation-identity conflict means a different operation reused the deterministic identity and must be repaired before retrying.",
		},
	})
	if err != nil {
		return LineageCommitReceipt{}, err
	}
	committed := 0
	for _, slot := range result.ResultSlots {
		if slot.ProducedJournalID > 0 {
			committed++
		}
	}
	return LineageCommitReceipt{Operation: operationID, ShortCircuited: result.ShortCircuited, Committed: committed}, nil
}

// DecodeLink strictly decodes canonical committed link evidence into a
// model.LinkRecord, assigning the committed journal identity as the LinkID. It
// applies the same canonical-JSON discipline as DecodeInterpreted — duplicate
// members are rejected, unknown fields are disallowed, trailing bytes are
// rejected, and the payload must equal its canonical re-encoding — so a
// tampered or noncanonical link row is refused rather than silently accepted.
func DecodeLink(linkID model.LifecycleLinkID, payload []byte) (model.LinkRecord, error) {
	const what = "decode committed lifecycle link evidence"
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return model.LinkRecord{}, fmt.Errorf("%s: %w", what, err)
	}
	var wire linkPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return model.LinkRecord{}, fmt.Errorf("%s: %w", what, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return model.LinkRecord{}, fmt.Errorf("%s: trailing JSON value", what)
	}
	record := model.LinkRecord{
		LinkID:  linkID,
		Harness: ir.HarnessID(wire.Harness),
		Kind:    wire.Kind,
		Value:   wire.Value,
		From:    model.OccurrenceID(wire.From),
		To:      model.OccurrenceID(wire.To),
	}
	if !record.IsValid() {
		return model.LinkRecord{}, fmt.Errorf("%s: link fields are not a well-formed per-host predecessor edge", what)
	}
	if len(wire.Value) > 512 {
		return model.LinkRecord{}, fmt.Errorf("%s: identity value exceeds the 512-byte bound", what)
	}
	canonical, err := canonicalLinkPayload(lineage.LinkFact{
		Harness: record.Harness,
		Kind:    record.Kind,
		Value:   record.Value,
		From:    record.From,
		To:      record.To,
	})
	// The committed row is the journal's NORMALIZED payload, which may differ
	// from the raw canonical encode. Accept either form — the raw canonical
	// bytes or the journal normalization of them — exactly as DecodeInterpreted
	// does, so a faithfully committed link round-trips while a tampered one does
	// not.
	normalized := canonical
	if mutation, normErr := provenance.Canonicalize(provenance.OperationInput{Effects: []provenance.Effect{{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    provenance.ResultSlotID("link"),
		EvidenceKind:  lineageLinkKind,
		ContentDigest: make([]byte, sha256.Size),
		Payload:       canonical,
	}}}); normErr == nil {
		normalized = mutation.NormalizedEffects()[0].Payload
	}
	if err != nil || (!bytes.Equal(canonical, payload) && !bytes.Equal(normalized, payload)) {
		return model.LinkRecord{}, fmt.Errorf("%s: payload is not canonical compact JSON", what)
	}
	return record, nil
}
