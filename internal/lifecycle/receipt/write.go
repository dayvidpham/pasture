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
// codebook coordinate and its canonical body. It produces two ordered effects —
// the definition snapshot (the canonical body evidence) and the activation
// state fact — and a deterministic operation identity derived from the content
// identity, so a concurrent duplicate collapses to one activation.
func NewDefinitionActivation(book model.CodebookCoordinate, body []byte) (GatedWrite, error) {
	if !book.IsValid() {
		return GatedWrite{}, structured(pasterrors.CategoryValidation,
			"The definition-activation write has an invalid codebook coordinate.",
			"A journaled definition must be addressed by a nonzero id, version, and content identity.",
			"Constructing a definition-activation gated write (internal/lifecycle/receipt/write.go in receipt.NewDefinitionActivation).",
			"No gated write was constructed.",
			"Pass codebook.Active() as the coordinate.", nil)
	}
	bodyDigest := sha256.Sum256(body)
	if model.ContentIdentity(bodyDigest) != book.Content {
		return GatedWrite{}, structured(pasterrors.CategoryValidation,
			"The definition-activation body does not match the codebook coordinate content identity.",
			"The coordinate's content identity is the sha256 of the canonical body, so a mismatch means the body and coordinate disagree.",
			"Constructing a definition-activation gated write (internal/lifecycle/receipt/write.go in receipt.NewDefinitionActivation).",
			"No gated write was constructed.",
			"Pass codebook.Body() for the same coordinate returned by codebook.Active().", nil)
	}

	snapshotPayload := append([]byte(nil), body...)
	snapshotDigest := sha256.Sum256(snapshotPayload)
	statePayload, err := canonicalDefinitionStatePayload(book)
	if err != nil {
		return GatedWrite{}, err
	}
	stateDigest := sha256.Sum256(statePayload)

	effects := []provenance.Effect{
		{Sort: provenance.EffectEvidence, ResultSlot: definitionSlot, EvidenceKind: definitionKind, ContentDigest: snapshotDigest[:], Payload: append(json.RawMessage(nil), snapshotPayload...)},
		{Sort: provenance.EffectEvidence, ResultSlot: definitionStateSlot, EvidenceKind: definitionStateKind, ContentDigest: stateDigest[:], Payload: append(json.RawMessage(nil), statePayload...)},
	}
	operationID := provenance.OperationID(definitionOperationPrefix + hex.EncodeToString(book.Content[:16]))
	command := sha256.Sum256(append([]byte(definitionCommand+"\x00"), book.Content[:]...))
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

func canonicalDefinitionStatePayload(book model.CodebookCoordinate) ([]byte, error) {
	return encodeCanonical(definitionStatePayload{
		Definition: hex.EncodeToString(book.Content[:]),
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

// Commit is the non-delivery commit surface of the normative write gate. It
// authorizes the presented warrant against the write's class (typed *gate.Refusal
// before any I/O), then applies the operation through the bounded context
// journal. Because the operation identity and effects are deterministic, a
// concurrent duplicate is admitted as CommittedExact/ShortCircuited with a nil
// error — the benign-already-activated outcome; a genuine identity reuse with
// differing content surfaces as an ErrOperationConflict.
func (s Service) Commit(ctx context.Context, warrant gate.Warrant, write GatedWrite) (CommitReceipt, error) {
	if refusal := gate.Authorize(warrant, write.class); refusal != nil {
		return CommitReceipt{}, refusal
	}
	if !write.constructed {
		return CommitReceipt{}, invalidCommit("The gated lifecycle write is not constructed.", "Only a constructor-built GatedWrite carries a deterministic operation identity and validated effects.", "Build the write through receipt.NewDefinitionActivation before committing.")
	}
	if s.Identity == nil || s.Clock == nil || s.Appender.Journal == nil {
		return CommitReceipt{}, invalidCommit("The lifecycle receipt service is incompletely wired for gated writes.", "Identity resolution, a clock, and the provenance journal are all required to commit a gated write.", "Construct the service through tasks.NewLifecycleReceiptService.")
	}
	contextJournal, ok := s.Appender.Journal.(provenance.ContextJournal)
	if !ok {
		return CommitReceipt{}, invalidCommit("The lifecycle journal cannot enforce the commit deadline.", "The configured provenance journal does not implement ContextJournal.ApplyContext.", "Use the pinned provenance journal implementation.")
	}
	deadline := s.Appender.Deadline
	if deadline <= 0 {
		return CommitReceipt{}, invalidCommit("The gated lifecycle write has no commit deadline.", "Timeouts must come from one validated injected profile.", "Construct the receipt service with a validated timeout profile.")
	}

	identity, err := s.Identity.ResolveLifecycleIdentity(ctx)
	if err != nil {
		return CommitReceipt{}, err
	}
	authority := identity.Authority
	input := provenance.OperationInput{
		OperationID:        write.operationID,
		ActorID:            identity.Actor,
		AuthorityJournalID: &authority,
		CommandDigest:      append([]byte(nil), write.commandDigest...),
		RecordedAt:         s.Clock.Now().UTC().UnixNano(),
		Effects:            append([]provenance.Effect(nil), write.effects...),
	}
	canonical, err := provenance.Canonicalize(input)
	if err != nil {
		return CommitReceipt{}, structured(pasterrors.CategoryValidation, "The gated lifecycle write could not cross the canonical journal boundary.", "The validated effects did not produce one canonical operation.", "Committing a gated lifecycle write (internal/lifecycle/receipt/write.go in receipt.Service.Commit).", "No operation was committed.", "Construct effects through receipt gated-write constructors and retry.", err)
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
		return CommitReceipt{}, structured(pasterrors.CategoryStorage, "The gated lifecycle write could not be committed.", "The provenance journal rejected the definition-activation operation.", "Committing a gated lifecycle write (internal/lifecycle/receipt/write.go in receipt.Service.Commit).", "No definition activation was recorded.", "Inspect the error; a persisted operation-identity conflict means a different operation reused the deterministic identity and must be repaired before retrying.", err)
	}

	receipt := CommitReceipt{Operation: write.operationID, ShortCircuited: result.ShortCircuited}
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
