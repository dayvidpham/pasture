package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

type Delivery struct {
	Contract ir.RuntimeContractID
	Event    model.ContractEventKind
	Envelope model.OccurrenceEnvelopeRef
	Bindings []model.NativeBinding
	Capture  model.CaptureDisposition
	Body     []byte
}

type Receipt struct{ OccurrenceID model.OccurrenceID }

func (r Receipt) JournalID() provenance.JournalID { return r.OccurrenceID.JournalID() }

type Service struct {
	Blobs      BlobStore
	Appender   JournalAppender
	Identity   IdentityResolver
	Clock      Clock
	Operations OperationIDSource
}

type occurrencePayload struct {
	Contract ir.RuntimeContractID        `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Bindings []model.NativeBinding       `json:"bindings"`
	Capture  model.CaptureDisposition    `json:"capture"`
	Body     string                      `json:"body_digest"`
}

// Receive is the sole durable lifecycle write for a host delivery. It is a
// commit surface of the normative write gate: it requires a delivery-receipt
// gate.Warrant and refuses a zero or class-mismatched warrant with a typed
// *gate.Refusal BEFORE any I/O, so an ungated write neither compiles (the
// parameter is required) nor reaches the store. The gate is origin-blind: the
// warrant certifies only the write class, never which host or delivery produced
// it.
func (s Service) Receive(ctx context.Context, warrant gate.Warrant, delivery Delivery, extra ...provenance.Effect) (Receipt, error) {
	if refusal := gate.Authorize(warrant, gate.WriteDeliveryReceipt); refusal != nil {
		return Receipt{}, refusal
	}
	if err := validateDelivery(delivery); err != nil {
		return Receipt{}, err
	}
	if err := validateLifecycleExtras(extra); err != nil {
		return Receipt{}, err
	}
	if s.Blobs == nil || s.Identity == nil || s.Clock == nil || s.Operations == nil {
		return Receipt{}, structured(pasterrors.CategoryValidation, "The lifecycle receipt service is incompletely wired.", "Blob storage, identity resolution, clock, and operation identity are all required to produce an attributable durable receipt.", "Receiving a lifecycle delivery (internal/lifecycle/receipt/service.go in receipt.Service.Receive).", "Nothing was recorded.", "Construct the service through the unified production opener with every dependency supplied.", nil)
	}
	body := append([]byte(nil), delivery.Body...)
	ref := digest.FromBytes(body)

	// Crash safety depends on this order: an orphan blob is reclaimable, while a
	// journal row that names an absent blob is corruption.
	if err := s.Blobs.Put(ctx, ref, body); err != nil {
		return Receipt{}, err
	}
	identity, err := s.Identity.ResolveLifecycleIdentity(ctx)
	if err != nil {
		return Receipt{}, err
	}
	operation, err := s.Operations.NewOperationID()
	if err != nil {
		return Receipt{}, structured(pasterrors.CategoryStorage, "A fresh lifecycle operation identity could not be created.", "Every host delivery must have a distinct operation identity, including byte-identical deliveries.", "Receiving a lifecycle delivery (internal/lifecycle/receipt/service.go in receipt.Service.Receive).", "The payload blob may remain as a reclaimable orphan; no occurrence was committed.", "Restore the injected operation identity source and retry the delivery.", err)
	}
	receivedAt := s.Clock.Now().UTC()
	payload, err := json.Marshal(occurrencePayload{Contract: delivery.Contract, Event: delivery.Event, Envelope: delivery.Envelope, Bindings: append([]model.NativeBinding(nil), delivery.Bindings...), Capture: delivery.Capture, Body: ref.String()})
	if err != nil {
		return Receipt{}, structured(pasterrors.CategoryStorage, "The lifecycle occurrence envelope could not be encoded.", "The validated typed delivery failed JSON encoding, which indicates an internal contract defect.", "Receiving a lifecycle delivery (internal/lifecycle/receipt/service.go in receipt.Service.Receive).", "The payload blob may remain as a reclaimable orphan; no occurrence was committed.", "Report the incompatible delivery shape and retry only after correcting it.", err)
	}
	command := sha256.Sum256(append([]byte(receiptCommand+"\x00"), payload...))
	payloadDigest := sha256.Sum256(payload)
	authority := identity.Authority
	effects := make([]provenance.Effect, 0, 1+len(extra))
	effects = append(effects, provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: receiptSlot, EvidenceKind: receiptKind, ContentDigest: payloadDigest[:], Payload: payload})
	effects = append(effects, extra...)
	input := provenance.OperationInput{
		OperationID:        provenance.OperationID(operation),
		ActorID:            identity.Actor,
		AuthorityJournalID: &authority,
		CommandDigest:      command[:],
		RecordedAt:         receivedAt.UnixNano(),
		Effects:            effects,
	}
	canonical, err := provenance.Canonicalize(input)
	if err != nil {
		return Receipt{}, structured(pasterrors.CategoryValidation, "Lifecycle evidence could not cross the canonical journal boundary.", "The validated effects did not produce one canonical operation.", "Preparing lifecycle evidence (internal/lifecycle/receipt/service.go in receipt.Service.Receive).", "No occurrence was committed; the blob may be reclaimable.", "Construct effects through receipt records and retry.", err)
	}
	input.Effects = canonical.NormalizedEffects()
	for index := range input.Effects {
		if input.Effects[index].Sort == provenance.EffectEvidence {
			if input.Effects[index].EvidenceKind == consultationKind && index > 0 {
				payload, bindErr := rebindConsultationPayload(input.Effects[index].Payload, input.Effects[index-1].Payload)
				if bindErr != nil {
					return Receipt{}, bindErr
				}
				input.Effects[index].Payload = payload
			}
			sum := sha256.Sum256(input.Effects[index].Payload)
			input.Effects[index].ContentDigest = append([]byte(nil), sum[:]...)
		}
	}
	if err := validateLifecycleExtras(input.Effects[1:]); err != nil {
		return Receipt{}, err
	}
	id, err := s.Appender.Append(ctx, input)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{OccurrenceID: id}, nil
}

func validateLifecycleExtras(extra []provenance.Effect) error {
	if len(extra) > 2 {
		return invalid("The lifecycle delivery contains too many interpreted effects.", "Only one interpreted record and its immediately following consultation may accompany an occurrence.", "Pass the canonical ordered interpreted/consultation pair once.")
	}
	if len(extra) == 0 {
		return nil
	}
	interpreted := extra[0]
	if interpreted.Sort != provenance.EffectEvidence || interpreted.ResultSlot != interpretedSlot || interpreted.EvidenceKind != interpretedKind || !effectDigestValid(interpreted) {
		return invalid("The lifecycle delivery contains a forged interpreted effect.", "Its slot, kind, or content digest is not canonical interpreted evidence.", "Use receipt.Record.Effect without modifying it.")
	}
	if err := validateInterpretedPayload(interpreted.Payload); err != nil {
		return invalid("The lifecycle delivery contains malformed interpreted evidence.", err.Error(), "Use receipt.Record.Effect without modifying or reconstructing its payload.")
	}
	if len(extra) == 1 {
		return nil
	}
	consultation := extra[1]
	if consultation.Sort != provenance.EffectEvidence || consultation.ResultSlot != consultationSlot || consultation.EvidenceKind != consultationKind || !effectDigestValid(consultation) {
		return invalid("The lifecycle delivery contains a forged consultation effect.", "Its slot, kind, or content digest is not canonical consultation evidence.", "Use receipt.ConsultationRecord.Effect without modifying it.")
	}
	if err := validateConsultationPayload(consultation.Payload, interpreted.Payload); err != nil {
		return invalid("The lifecycle consultation does not reference its immediately preceding interpreted effect.", "The operation-local slot and exact interpreted payload digest must match as one ordered pair.", "Construct both records together and preserve interpreted-then-consultation order.")
	}
	return nil
}

func effectDigestValid(effect provenance.Effect) bool {
	sum := sha256.Sum256(effect.Payload)
	return len(effect.ContentDigest) == sha256.Size && bytes.Equal(effect.ContentDigest, sum[:])
}

func validateDelivery(d Delivery) error {
	switch {
	case !d.Contract.IsValid():
		return invalid("The lifecycle delivery has no runtime contract.", "A receipt must preserve the exact host contract that produced the delivery.", "Supply the generated runtime contract coordinate.")
	case d.Event == 0:
		return invalid("The lifecycle delivery has no event kind.", "A receipt with an unknown event cannot be interpreted or queried safely.", "Supply the generated typed event kind.")
	case d.Capture == 0:
		return invalid("The lifecycle delivery has no capture disposition.", "Every delivery records whether its bytes were valid, malformed, truncated, or otherwise classified.", "Classify the delivery at the ingress boundary before receipt storage.")
	case len(d.Body) > model.MaxNativePayloadBytes:
		return invalid(fmt.Sprintf("The lifecycle delivery body is %d bytes, above the %d-byte limit.", len(d.Body), model.MaxNativePayloadBytes), "Payload storage is statically bounded so one hook cannot monopolize the shared database.", "Reject or truncate the delivery at ingress and record the over-limit capture disposition without this body.")
	case len(d.Bindings) > 16:
		return invalid("The lifecycle delivery has more than 16 native bindings.", "Native correlation is bounded to prevent an untrusted host payload from creating unbounded receipt metadata.", "Keep at most the 16 contract-defined bindings and record the delivery as over-limit.")
	}
	for _, binding := range d.Bindings {
		if err := model.ValidateNativeBinding(binding); err != nil {
			return invalid("The lifecycle delivery contains an invalid native binding.", err.Error(), "Use a declared kind and normalized 1..512-byte UTF-8 native name and value without controls or padding.")
		}
	}
	return nil
}

func invalid(what, why, fix string) error {
	return structured(pasterrors.CategoryValidation, what, why, "Validating a lifecycle delivery (internal/lifecycle/receipt/service.go in receipt.validateDelivery).", "Nothing was recorded.", fix, nil)
}
