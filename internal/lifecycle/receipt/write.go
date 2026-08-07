package receipt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

const (
	definitionCommand         = "pasture.lifecycle.definition.append/v1"
	definitionSlot            = provenance.ResultSlotID("definition")
	definitionKind            = provenance.EvidenceKind("pasture.lifecycle.definition.v1")
	definitionStateSlot       = provenance.ResultSlotID("definition-state")
	definitionStateKind       = provenance.EvidenceKind("pasture.lifecycle.definition-state.v1")
	definitionOperationPrefix = "pasture.lifecycle.definition."
)

// GatedWrite is a constructor-owned non-delivery lifecycle write: one provenance
// operation carrying a closed, ordered effect set with a DETERMINISTIC,
// content-derived operation identity and command digest. Determinism is
// load-bearing: two concurrent first deliveries build byte-identical operations,
// so the journal's replay arbiter admits exactly one and short-circuits the
// rest benignly (F11) rather than raising a conflict. Nothing in a GatedWrite is
// wall-clock derived (the operation's audit RecordedAt, set at Commit, is NOT
// part of the compared replay identity).
type GatedWrite struct {
	class         gate.WriteClass
	command       string
	operationID   provenance.OperationID
	commandDigest []byte
	effects       []provenance.Effect
	constructed   bool
}

// Class returns the write class this gated write commits as.
func (w GatedWrite) Class() gate.WriteClass { return w.class }

// OperationID returns the deterministic content-derived operation identity.
func (w GatedWrite) OperationID() provenance.OperationID { return w.operationID }

// NewDefinitionActivation builds the definition-activation gated write for a
// metamodel coordinate and its canonical body. It produces two ordered effects —
// the definition snapshot (the canonical body evidence) and the activation
// state fact — and a deterministic operation identity derived from the content
// identity, so a concurrent duplicate collapses to one activation.
func NewDefinitionActivation(manifest model.LifecycleMetamodelManifest, body []byte) (GatedWrite, error) {
	if !manifest.IsValid() {
		return GatedWrite{}, structured(pasterrors.CategoryValidation,
			"The definition-activation write has an invalid metamodel coordinate.",
			"A journaled definition must be addressed by a nonzero id, version, and content identity.",
			"Constructing a definition-activation gated write (internal/lifecycle/receipt/write.go in receipt.NewDefinitionActivation).",
			"No gated write was constructed.",
			"Pass metamodel.Active() as the coordinate.", nil)
	}
	bodyDigest := sha256.Sum256(body)
	if model.ContentIdentity(bodyDigest) != manifest.Content {
		return GatedWrite{}, structured(pasterrors.CategoryValidation,
			"The definition-activation body does not match the metamodel coordinate content identity.",
			"The coordinate's content identity is the sha256 of the canonical body, so a mismatch means the body and coordinate disagree.",
			"Constructing a definition-activation gated write (internal/lifecycle/receipt/write.go in receipt.NewDefinitionActivation).",
			"No gated write was constructed.",
			"Pass metamodel.Body() for the same coordinate returned by metamodel.Active().", nil)
	}

	snapshotPayload := append([]byte(nil), body...)
	snapshotDigest := sha256.Sum256(snapshotPayload)
	statePayload, err := canonicalDefinitionStatePayload(manifest)
	if err != nil {
		return GatedWrite{}, err
	}
	stateDigest := sha256.Sum256(statePayload)

	effects := []provenance.Effect{
		{Sort: provenance.EffectEvidence, ResultSlot: definitionSlot, EvidenceKind: definitionKind, ContentDigest: snapshotDigest[:], Payload: append(json.RawMessage(nil), snapshotPayload...)},
		{Sort: provenance.EffectEvidence, ResultSlot: definitionStateSlot, EvidenceKind: definitionStateKind, ContentDigest: stateDigest[:], Payload: append(json.RawMessage(nil), statePayload...)},
	}
	operationID := provenance.OperationID(definitionOperationPrefix + hex.EncodeToString(manifest.Content[:16]))
	command := sha256.Sum256(append([]byte(definitionCommand+"\x00"), manifest.Content[:]...))
	return GatedWrite{
		class:         gate.WriteDefinitionActivation,
		command:       definitionCommand,
		operationID:   operationID,
		commandDigest: command[:],
		effects:       effects,
		constructed:   true,
	}, nil
}

// definitionStatePayload is the canonical, wall-clock-free activation state fact.
// Its bytes are content-derived so the definition-activation operation stays
// byte-identical across concurrent first deliveries.
type definitionStatePayload struct {
	Definition string `json:"definition"`
	Event      string `json:"event"`
}

func canonicalDefinitionStatePayload(manifest model.LifecycleMetamodelManifest) ([]byte, error) {
	return encodeCanonical(definitionStatePayload{
		Definition: hex.EncodeToString(manifest.Content[:]),
		Event:      "activated",
	})
}

// CommitReceipt is the result of committing a GatedWrite. ShortCircuited is true
// when the operation was already committed (benign-already-activated): the
// journal returned the existing result without folding effects again.
type CommitReceipt struct {
	Operation       provenance.OperationID
	ShortCircuited  bool
	Definition      provenance.JournalID
	DefinitionState provenance.JournalID
}

// gatedCommitSurface carries the per-write-class specifics the shared gated
// commit pipeline (Service.commitGated) cannot derive from the GatedWrite
// itself: the class to authorize and match against, whether a fresh
// operation-identity source is required, and the actionable refusal text unique
// to each commit surface. It exists so Commit, CommitLineage, and
// CommitDisclosure share ONE authorize-first, canonicalize, re-digest, apply
// pipeline while keeping every per-class message byte-identical to the
// standalone form each surface had before the pipeline was consolidated.
type gatedCommitSurface struct {
	// expected is the write class this surface authorizes against (gate.Authorize
	// runs FIRST, before any I/O) and matches the constructed write against. The
	// definition surface passes write.class so an unconstructed write is refused
	// EXACTLY as before (a zero class is an unenumerated expected class that
	// gate.Authorize rejects with a typed *gate.Refusal); the lineage and
	// disclosure surfaces pass their fixed class, so a constructed write of the
	// wrong class is refused as not-constructed.
	expected gate.WriteClass
	// requireOperations is true only for the disclosure surface, whose write
	// carries no deterministic operation identity and must mint a fresh one from
	// s.Operations; the deterministic surfaces do not require it.
	requireOperations bool
	notConstructed    gatedInvalidText
	incompleteWiring  gatedInvalidText
	canonicalBoundary gatedStructuredText
	applyRejected     gatedStructuredText
}

// gatedInvalidText is the what/why/fix of a CategoryValidation refusal raised
// through invalidCommit (which supplies the shared where/impact).
type gatedInvalidText struct{ what, why, fix string }

// gatedStructuredText is the full what/why/where/impact/fix of a structured
// refusal raised at the canonicalize or apply boundary of a gated commit.
type gatedStructuredText struct{ what, why, where, impact, fix string }

// commitGated is the shared, per-class-agnostic durable commit pipeline behind
// Commit, CommitLineage, and CommitDisclosure. It enforces the normative gate
// discipline IDENTICALLY for every class: gate.Authorize runs FIRST (a zero or
// class-mismatched warrant is refused with a typed *gate.Refusal BEFORE any
// I/O), then the constructed/class, wiring, journal-capability, and deadline
// preconditions are checked, identity is resolved, the operation identity is
// determined (the deterministic content-derived write.operationID, or — when the
// write left it empty, the disclosure policy — a fresh one minted from
// s.Operations), the operation is canonicalized, its evidence effects are
// re-digested over their normalized payloads, and the operation is applied under
// the bounded commit deadline. It returns the journal result and the committed
// operation identity so each typed wrapper maps its own result slots.
func (s Service) commitGated(ctx context.Context, warrant gate.Warrant, write GatedWrite, surface gatedCommitSurface) (provenance.CommittedResult, provenance.OperationID, error) {
	if refusal := gate.Authorize(warrant, surface.expected); refusal != nil {
		return provenance.CommittedResult{}, "", refusal
	}
	if !write.constructed || write.class != surface.expected {
		return provenance.CommittedResult{}, "", invalidCommit(surface.notConstructed.what, surface.notConstructed.why, surface.notConstructed.fix)
	}
	if s.Identity == nil || s.Clock == nil || s.Appender.Journal == nil || (surface.requireOperations && s.Operations == nil) {
		return provenance.CommittedResult{}, "", invalidCommit(surface.incompleteWiring.what, surface.incompleteWiring.why, surface.incompleteWiring.fix)
	}
	contextJournal, ok := s.Appender.Journal.(provenance.ContextJournal)
	if !ok {
		return provenance.CommittedResult{}, "", invalidCommit("The lifecycle journal cannot enforce the commit deadline.", "The configured provenance journal does not implement ContextJournal.ApplyContext.", "Use the pinned provenance journal implementation.")
	}
	deadline := s.Appender.Deadline
	if deadline <= 0 {
		return provenance.CommittedResult{}, "", invalidCommit("The gated lifecycle write has no commit deadline.", "Timeouts must come from one validated injected profile.", "Construct the receipt service with a validated timeout profile.")
	}

	identity, err := s.Identity.ResolveLifecycleIdentity(ctx)
	if err != nil {
		return provenance.CommittedResult{}, "", err
	}
	operationID := write.operationID
	if operationID == "" {
		fresh, err := s.Operations.NewOperationID()
		if err != nil {
			return provenance.CommittedResult{}, "", structured(pasterrors.CategoryStorage,
				"A fresh disclosure operation identity could not be created.",
				"Each context disclosure commits one operation with a distinct fresh identity.",
				"Committing a disclosure gated write (internal/lifecycle/receipt/write_disclosure.go in receipt.Service.CommitDisclosure).",
				"No disclosure was recorded; nothing was printed.",
				"Restore the injected operation identity source and retry the disclosure.", err)
		}
		operationID = provenance.OperationID(fresh)
	}
	authority := identity.Authority
	input := provenance.OperationInput{
		OperationID:        operationID,
		ActorID:            identity.Actor,
		AuthorityJournalID: &authority,
		CommandDigest:      append([]byte(nil), write.commandDigest...),
		RecordedAt:         s.Clock.Now().UTC().UnixNano(),
		Effects:            append([]provenance.Effect(nil), write.effects...),
	}
	canonical, err := provenance.Canonicalize(input)
	if err != nil {
		return provenance.CommittedResult{}, "", structured(pasterrors.CategoryValidation,
			surface.canonicalBoundary.what, surface.canonicalBoundary.why, surface.canonicalBoundary.where,
			surface.canonicalBoundary.impact, surface.canonicalBoundary.fix, err)
	}
	input.Effects = canonical.NormalizedEffects()
	for index := range input.Effects {
		if input.Effects[index].Sort == provenance.EffectEvidence {
			sum := sha256.Sum256(input.Effects[index].Payload)
			input.Effects[index].ContentDigest = append([]byte(nil), sum[:]...)
		}
	}

	bounded, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	result, err := contextJournal.ApplyContext(bounded, input)
	if err != nil {
		return provenance.CommittedResult{}, "", structured(pasterrors.CategoryStorage,
			surface.applyRejected.what, surface.applyRejected.why, surface.applyRejected.where,
			surface.applyRejected.impact, surface.applyRejected.fix, err)
	}
	return result, operationID, nil
}

// definitionCommitSurface is the per-class specialization the definition-
// activation write presents to commitGated. It authorizes against write.class
// (preserving the pre-consolidation behavior where an unconstructed definition
// write is refused with a typed *gate.Refusal for an unenumerated class) and
// carries the definition surface's actionable refusal text verbatim.
func definitionCommitSurface(class gate.WriteClass) gatedCommitSurface {
	return gatedCommitSurface{
		expected: class,
		notConstructed: gatedInvalidText{
			"The gated lifecycle write is not constructed.",
			"Only a constructor-built GatedWrite carries a deterministic operation identity and validated effects.",
			"Build the write through receipt.NewDefinitionActivation before committing.",
		},
		incompleteWiring: gatedInvalidText{
			"The lifecycle receipt service is incompletely wired for gated writes.",
			"Identity resolution, a clock, and the provenance journal are all required to commit a gated write.",
			"Construct the service through tasks.NewLifecycleReceiptService.",
		},
		canonicalBoundary: gatedStructuredText{
			"The gated lifecycle write could not cross the canonical journal boundary.",
			"The validated effects did not produce one canonical operation.",
			"Committing a gated lifecycle write (internal/lifecycle/receipt/write.go in receipt.Service.Commit).",
			"No operation was committed.",
			"Construct effects through receipt gated-write constructors and retry.",
		},
		applyRejected: gatedStructuredText{
			"The gated lifecycle write could not be committed.",
			"The provenance journal rejected the definition-activation operation.",
			"Committing a gated lifecycle write (internal/lifecycle/receipt/write.go in receipt.Service.Commit).",
			"No definition activation was recorded.",
			"Inspect the error; a persisted operation-identity conflict means a different operation reused the deterministic identity and must be repaired before retrying.",
		},
	}
}

// Commit is the non-delivery commit surface of the normative write gate for the
// definition-activation write class. It is a thin typed wrapper over the shared
// commitGated pipeline: it authorizes the presented warrant against the write's
// class (typed *gate.Refusal before any I/O), commits the deterministic
// operation through the bounded context journal, and resolves the definition and
// definition-state result slots. Because the operation identity and effects are
// deterministic, a concurrent duplicate is admitted as CommittedExact/
// ShortCircuited with a nil error — the benign-already-activated outcome; a
// genuine identity reuse with differing content surfaces as an
// ErrOperationConflict.
func (s Service) Commit(ctx context.Context, warrant gate.Warrant, write GatedWrite) (CommitReceipt, error) {
	result, operationID, err := s.commitGated(ctx, warrant, write, definitionCommitSurface(write.class))
	if err != nil {
		return CommitReceipt{}, err
	}
	receipt := CommitReceipt{Operation: operationID, ShortCircuited: result.ShortCircuited}
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case definitionSlot:
			receipt.Definition = slot.ProducedJournalID
		case definitionStateSlot:
			receipt.DefinitionState = slot.ProducedJournalID
		}
	}
	if receipt.Definition == 0 {
		return CommitReceipt{}, structured(pasterrors.CategoryStorage, "The gated lifecycle write committed without its definition identity.", "The journal result did not contain the mandatory definition result slot.", "Committing a gated lifecycle write (internal/lifecycle/receipt/write.go in receipt.Service.Commit).", "The caller cannot resolve the journaled definition.", "Use a compatible provenance journal and inspect the committed operation.", fmt.Errorf("missing result slot %q", definitionSlot))
	}
	return receipt, nil
}

func invalidCommit(what, why, fix string) error {
	return structured(pasterrors.CategoryValidation, what, why, "Committing a gated lifecycle write (internal/lifecycle/receipt/write.go in receipt.Service.Commit).", "No operation was committed.", fix, nil)
}
