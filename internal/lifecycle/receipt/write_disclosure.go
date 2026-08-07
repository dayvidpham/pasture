package receipt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

const (
	disclosureCommand     = "pasture.lifecycle.disclosure.append/v1"
	disclosurePlanSlot    = provenance.ResultSlotID("disclosure-plan")
	disclosurePlanKind    = provenance.EvidenceKind("pasture.lifecycle.disclosure.plan.v1")
	disclosureAttemptSlot = provenance.ResultSlotID("disclosure-attempt")
	disclosureAttemptKind = provenance.EvidenceKind("pasture.lifecycle.disclosure.attempt.v1")
	disclosureResultSlot  = provenance.ResultSlotID("disclosure-result")
	disclosureResultKind  = provenance.EvidenceKind("pasture.lifecycle.disclosure.result.v1")
)

// disclosurePlanPayload is the canonical wire form of a disclosure plan fact:
// the scope fingerprint (hex), the projection content digest (hex), and the
// static policy note. It records WHAT was disclosed without retaining the
// released bytes.
type disclosurePlanPayload struct {
	Scope      string `json:"scope"`
	Projection string `json:"projection"`
	Policy     string `json:"policy"`
}

// disclosureAttemptPayload is the canonical wire form of a disclosure attempt
// fact: the recorded-at instant in UTC unix nanoseconds. Unlike the
// definition-activation state fact, disclosure is NOT content-deduplicated (one
// operation per invocation, fresh random operation identity), so a wall-clock
// instant is carried durably here.
type disclosureAttemptPayload struct {
	RecordedAt int64 `json:"recorded_at"`
}

// disclosureResultPayload is the canonical wire form of a disclosure result
// fact: the disposition spelling. At M5 the only honest disposition is
// "released".
type disclosureResultPayload struct {
	Disposition string `json:"disposition"`
}

// disclosureRefusal builds an actionable validation error for a malformed
// disclosure write, filling the shared where/impact text so each call site
// states only its specific what/why/fix. It mirrors the receipt package's other
// gated-write constructors and is C-actionable-errors compliant.
func disclosureRefusal(what, why, fix string) error {
	return structured(pasterrors.CategoryValidation, what, why,
		"Constructing a disclosure gated write (internal/lifecycle/receipt/write_disclosure.go in receipt.NewDisclosure).",
		"No disclosure write was constructed and no warrant can be committed.",
		fix, nil)
}

// NewDisclosure builds the context-disclosure gated write: ONE operation
// carrying three ordered effects — the plan (slot "disclosure-plan"), attempt
// (slot "disclosure-attempt"), and result (slot "disclosure-result") facts —
// which the disclosure command commits together, BEFORE it prints the projection
// (Stage-3 M5 UAT resolution 6: one gated operation, commit before print).
//
// Unlike NewDefinitionActivation and NewLineageLinks, a disclosure write carries
// NO deterministic operation identity: disclosure is one operation per `context`
// invocation with a fresh random identity (the ratified durable-evidence story),
// because Pasture cannot observe consumption and there is nothing to
// deduplicate. The command digest binds the three canonical payloads so the
// committed command is attributable to exactly this disclosure.
func NewDisclosure(plan model.DisclosurePlanFact, attempt model.DisclosureAttemptFact, result model.DisclosureResultFact) (GatedWrite, error) {
	if !plan.IsValid() {
		return GatedWrite{}, disclosureRefusal(
			"a disclosure write has an invalid plan fact",
			"a disclosure plan records a nonzero scope fingerprint, a nonzero projection content digest, and a static policy note",
			"build the plan from context.Project's ScopeFingerprint and Digest with the M5 disclosure policy note")
	}
	if !attempt.IsValid() {
		return GatedWrite{}, disclosureRefusal(
			"a disclosure write has an invalid attempt fact",
			"a disclosure attempt records the instant the disclosure was made",
			"build the attempt fact with the injected clock's current instant")
	}
	if !result.IsValid() {
		return GatedWrite{}, disclosureRefusal(
			"a disclosure write has an invalid result disposition",
			"a disclosure result records an enumerated disposition; at M5 the only honest disposition is DisclosureReleased",
			"set the result disposition to model.DisclosureReleased")
	}

	planPayload, err := encodeCanonical(disclosurePlanPayload{
		Scope:      hex.EncodeToString(plan.Scope[:]),
		Projection: hex.EncodeToString(plan.Projection[:]),
		Policy:     plan.Policy,
	})
	if err != nil {
		return GatedWrite{}, disclosureRefusal(
			"a disclosure plan fact could not be canonically encoded",
			"the validated plan failed JSON encoding, which indicates an internal contract defect",
			"report the incompatible plan shape; it cannot be committed until the encoder is corrected")
	}
	attemptPayload, err := encodeCanonical(disclosureAttemptPayload{RecordedAt: attempt.RecordedAt.UTC().UnixNano()})
	if err != nil {
		return GatedWrite{}, disclosureRefusal(
			"a disclosure attempt fact could not be canonically encoded",
			"the validated attempt failed JSON encoding, which indicates an internal contract defect",
			"report the incompatible attempt shape; it cannot be committed until the encoder is corrected")
	}
	resultPayload, err := encodeCanonical(disclosureResultPayload{Disposition: result.Disposition.String()})
	if err != nil {
		return GatedWrite{}, disclosureRefusal(
			"a disclosure result fact could not be canonically encoded",
			"the validated result failed JSON encoding, which indicates an internal contract defect",
			"report the incompatible result shape; it cannot be committed until the encoder is corrected")
	}

	planDigest := sha256.Sum256(planPayload)
	attemptDigest := sha256.Sum256(attemptPayload)
	resultDigest := sha256.Sum256(resultPayload)
	effects := []provenance.Effect{
		{Sort: provenance.EffectEvidence, ResultSlot: disclosurePlanSlot, EvidenceKind: disclosurePlanKind, ContentDigest: planDigest[:], Payload: append(json.RawMessage(nil), planPayload...)},
		{Sort: provenance.EffectEvidence, ResultSlot: disclosureAttemptSlot, EvidenceKind: disclosureAttemptKind, ContentDigest: attemptDigest[:], Payload: append(json.RawMessage(nil), attemptPayload...)},
		{Sort: provenance.EffectEvidence, ResultSlot: disclosureResultSlot, EvidenceKind: disclosureResultKind, ContentDigest: resultDigest[:], Payload: append(json.RawMessage(nil), resultPayload...)},
	}

	commandAccumulator := sha256.New()
	commandAccumulator.Write([]byte(disclosureCommand + "\x00"))
	commandAccumulator.Write(planDigest[:])
	commandAccumulator.Write(attemptDigest[:])
	commandAccumulator.Write(resultDigest[:])
	command := commandAccumulator.Sum(nil)

	return GatedWrite{
		class:         gate.WriteDisclosure,
		command:       disclosureCommand,
		commandDigest: command,
		effects:       effects,
		constructed:   true,
	}, nil
}

// DisclosureCommitReceipt is the result of committing a disclosure GatedWrite.
// Plan, Attempt, and Result are the journal identities of the three committed
// facts.
type DisclosureCommitReceipt struct {
	Operation provenance.OperationID
	Plan      provenance.JournalID
	Attempt   provenance.JournalID
	Result    provenance.JournalID
}

// CommitDisclosure is the disclosure commit surface of the normative write gate.
//
// It exists alongside Service.Commit and Service.CommitLineage because each
// commit surface resolves its own result slots (definition, link-<i>, and here
// disclosure-plan/attempt/result). All three enforce the SAME normative
// discipline: gate.Authorize is called FIRST, so a zero or class-mismatched
// warrant is refused with a typed *gate.Refusal before any I/O, and an
// unwarranted disclosure write cannot reach the store.
//
// Unlike the deterministic definition and lineage writes, a disclosure operation
// takes a FRESH RANDOM operation identity (one per invocation), because Pasture
// cannot observe consumption and there is nothing to deduplicate. The commit
// completes before the disclosure command prints, so the durable trail is intact
// even if the later stdout write fails.
func (s Service) CommitDisclosure(ctx context.Context, warrant gate.Warrant, write GatedWrite) (DisclosureCommitReceipt, error) {
	result, operationID, err := s.commitGated(ctx, warrant, write, gatedCommitSurface{
		expected:          gate.WriteDisclosure,
		requireOperations: true,
		notConstructed: gatedInvalidText{
			"The gated lifecycle write is not a constructed disclosure write.",
			"CommitDisclosure commits only a GatedWrite built by receipt.NewDisclosure.",
			"Build the write through receipt.NewDisclosure before committing.",
		},
		incompleteWiring: gatedInvalidText{
			"The lifecycle receipt service is incompletely wired for gated writes.",
			"Identity resolution, a clock, an operation identity source, and the provenance journal are all required to commit a disclosure write.",
			"Construct the service through tasks.NewLifecycleReceiptService.",
		},
		canonicalBoundary: gatedStructuredText{
			"The disclosure write could not cross the canonical journal boundary.",
			"The validated disclosure effects did not produce one canonical operation.",
			"Committing a disclosure gated write (internal/lifecycle/receipt/write_disclosure.go in receipt.Service.CommitDisclosure).",
			"No disclosure operation was committed.",
			"Construct effects through receipt.NewDisclosure and retry.",
		},
		applyRejected: gatedStructuredText{
			"The disclosure write could not be committed.",
			"The provenance journal rejected the disclosure operation.",
			"Committing a disclosure gated write (internal/lifecycle/receipt/write_disclosure.go in receipt.Service.CommitDisclosure).",
			"No disclosure was recorded; nothing was printed.",
			"Inspect the error and retry the disclosure.",
		},
	})
	if err != nil {
		return DisclosureCommitReceipt{}, err
	}

	receipt := DisclosureCommitReceipt{Operation: operationID}
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case disclosurePlanSlot:
			receipt.Plan = slot.ProducedJournalID
		case disclosureAttemptSlot:
			receipt.Attempt = slot.ProducedJournalID
		case disclosureResultSlot:
			receipt.Result = slot.ProducedJournalID
		}
	}
	if receipt.Plan == 0 || receipt.Attempt == 0 || receipt.Result == 0 {
		return DisclosureCommitReceipt{}, structured(pasterrors.CategoryStorage,
			"The disclosure write committed without all three fact identities.",
			"A committed disclosure operation must expose the plan, attempt, and result result slots.",
			"Committing a disclosure gated write (internal/lifecycle/receipt/write_disclosure.go in receipt.Service.CommitDisclosure).",
			"The caller cannot resolve the journaled disclosure facts.",
			"Use a compatible provenance journal and inspect the committed operation.", nil)
	}
	return receipt, nil
}
