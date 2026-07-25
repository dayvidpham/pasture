// Package tasks — the production decision PolicySet (#49).
//
// policy_set.go constructs the explicit production PolicySet: the concrete decision
// descriptors registered on top of the base catalog machinery, plus the typed draft
// helpers live commit handlers use to lower a policy decision to one
// canonical DecisionDraft. There is NO init-time registry and NO process-global set: the
// PolicySet is constructed explicitly by NewProductionPolicySet and passed by value.
//
// Delivered-surface note. The issue sketches the constructor as an unexported
// newProductionPolicySet. It is exported here as NewProductionPolicySet because the
// completed package's declared consumer (#40's version-bounded lowerings) imports this
// package and lowers these descriptors, so the constructor and the descriptor accessors
// must be reachable across the package boundary. The descriptor STRUCT FIELDS remain
// unexported (only accessor methods are public), so a caller still cannot fabricate a
// descriptor — it must come from this constructor.

package tasks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Canonical codec + kind identities for the #49 decision descriptors.
const (
	// policyCanonicalJSONCodec is the canonical, deterministic JSON codec every #49
	// decision payload uses. Its bytes are HTML-unescaped, newline-trimmed, stable-field-
	// order JSON (canonicalJSON), so a payload's encoding is a pure function of its value.
	policyCanonicalJSONCodec DecisionCodecID = "pasture.canonical-json/v1"

	// DecisionPlanUATAccepted / ...ChangesRequested / ...DeferredByAFK are the three Plan-UAT
	// decision kinds; DecisionImplementationUAT is the single Implementation-UAT kind.
	DecisionPlanUATAccepted         DecisionKindID = "pasture.plan-uat.accepted/v1"
	DecisionPlanUATChangesRequested DecisionKindID = "pasture.plan-uat.changes-requested/v1"
	DecisionPlanUATDeferredByAFK    DecisionKindID = "pasture.plan-uat.deferred-by-afk/v1"
	DecisionImplementationUAT       DecisionKindID = "pasture.implementation-uat/v1"
	DecisionPlanRatified            DecisionKindID = "pasture.plan.ratified/v1"
	DecisionLanded                  DecisionKindID = "pasture.epoch.landed/v1"
)

// PolicySet is the explicit production set of #49 decision descriptors plus the immutable
// #43 catalog that registers them. It is constructed once by NewProductionPolicySet and
// passed by value; its descriptor fields are unexported so only this package can mint a
// draft, but each is reachable through an exported accessor for the #40 lowering consumer.
type PolicySet struct {
	Catalog           DecisionCatalog
	modeChanged       DecisionDescriptor[InteractionModeChanged]
	planAccepted      DecisionDescriptor[PlanAccepted]
	planChanges       DecisionDescriptor[PlanChangesRequested]
	planDeferred      DecisionDescriptor[PlanDeferredByAFK]
	implementationUAT DecisionDescriptor[implementationUATRecord]
	planRatified      DecisionDescriptor[PlanRatified]
	landed            DecisionDescriptor[EpochLanded]
}

// PlanRatified is the immutable decision payload binding ratification to its exact
// proposal, accepted review round, and accepted Plan UAT decision.
type PlanRatified struct {
	Proposal    string                `json:"proposal"`
	ReviewRound ReviewRoundID         `json:"reviewRound"`
	PlanUAT     DecisionLedgerEntryID `json:"planUat"`
}

func validatePlanRatified(v PlanRatified) error {
	if v.Proposal == "" || v.ReviewRound == "" || v.PlanUAT == "" {
		return decisionErr("PlanRatified", "ratification is missing its proposal, review round, or Plan UAT evidence", "ratification must bind all three exact inputs", "supply the proposal, accepted review round, and accepted Plan UAT decision")
	}
	return nil
}

// EpochLanded is the immutable decision payload binding landing to the exact candidate
// and accepted Implementation UAT decision.
type EpochLanded struct {
	Candidate         IntegrationCandidateSetID `json:"candidate"`
	ImplementationUAT DecisionLedgerEntryID     `json:"implementationUat"`
}

func validateEpochLanded(v EpochLanded) error {
	if v.Candidate == "" || v.ImplementationUAT == "" {
		return decisionErr("EpochLanded", "landing is missing its candidate or Implementation UAT evidence", "landing must bind both exact inputs", "supply the candidate and accepted Implementation UAT decision")
	}
	return nil
}

// implementationUATRecord is the private canonical value stored under the existing
// implementation-UAT decision kind. Keeping outcome beside payload in one encoding makes
// the authoritative verdict durable without duplicating it inside ImplUATPayload.
type implementationUATRecord struct {
	Outcome ImplementationUATVerdict `json:"outcome"`
	Payload ImplUATPayload           `json:"payload"`
}

func validateImplementationUATRecord(record implementationUATRecord) error {
	return validateImplUATPayload(record.Outcome, record.Payload)
}

// NewProductionPolicySet constructs the explicit production PolicySet. It builds the
// registered concrete descriptors and freezes them into one immutable catalog; a construction failure
// (an invalid descriptor or a catalog conflict) is returned rather than panicked.
func NewProductionPolicySet() (PolicySet, error) {
	modeChanged, err := newJSONDescriptor(
		DecisionInteractionModeChanged, "interaction-mode.changed{from,to}", validateInteractionModeChanged)
	if err != nil {
		return PolicySet{}, err
	}
	planAccepted, err := newJSONDescriptor(
		DecisionPlanUATAccepted, "plan-uat.accepted{snapshot,interactions,feedback}", validatePlanAccepted)
	if err != nil {
		return PolicySet{}, err
	}
	planChanges, err := newJSONDescriptor(
		DecisionPlanUATChangesRequested, "plan-uat.changes-requested{snapshot,interactions,feedback}", validatePlanChangesRequested)
	if err != nil {
		return PolicySet{}, err
	}
	planDeferred, err := newJSONDescriptor(
		DecisionPlanUATDeferredByAFK, "plan-uat.deferred-by-afk{snapshot,interactions,feedback,heldQuestions,modeEntry}", validatePlanDeferredByAFK)
	if err != nil {
		return PolicySet{}, err
	}
	implementationUAT, err := newJSONDescriptor(
		DecisionImplementationUAT, "implementation-uat{outcome,payload{interactions,feedback,heldAnswers,planFeedback,ledgerDecisions}}", validateImplementationUATRecord)
	if err != nil {
		return PolicySet{}, err
	}
	planRatified, err := newJSONDescriptor(DecisionPlanRatified, "plan.ratified{proposal,reviewRound,planUat}", validatePlanRatified)
	if err != nil {
		return PolicySet{}, err
	}
	landed, err := newJSONDescriptor(DecisionLanded, "epoch.landed{candidate,implementationUat}", validateEpochLanded)
	if err != nil {
		return PolicySet{}, err
	}
	catalog, err := NewDecisionCatalog(
		BindDecision(modeChanged),
		BindDecision(planAccepted),
		BindDecision(planChanges),
		BindDecision(planDeferred),
		BindDecision(implementationUAT),
		BindDecision(planRatified),
		BindDecision(landed),
	)
	if err != nil {
		return PolicySet{}, err
	}
	return PolicySet{
		Catalog:           catalog,
		modeChanged:       modeChanged,
		planAccepted:      planAccepted,
		planChanges:       planChanges,
		planDeferred:      planDeferred,
		implementationUAT: implementationUAT,
		planRatified:      planRatified,
		landed:            landed,
	}, nil
}

// ModeChangedDescriptor / Plan*Descriptor / ImplementationUATDescriptor expose the
// registered descriptors for the #40 lowering consumer. They return the descriptor by
// value; because the underlying struct fields are unexported, the returned descriptor can
// only decode/draft through this package's registered catalog.
func (s PolicySet) ModeChangedDescriptor() DecisionDescriptor[InteractionModeChanged] {
	return s.modeChanged
}
func (s PolicySet) PlanAcceptedDescriptor() DecisionDescriptor[PlanAccepted] { return s.planAccepted }
func (s PolicySet) PlanChangesRequestedDescriptor() DecisionDescriptor[PlanChangesRequested] {
	return s.planChanges
}
func (s PolicySet) PlanDeferredByAFKDescriptor() DecisionDescriptor[PlanDeferredByAFK] {
	return s.planDeferred
}
func (s PolicySet) ImplementationUATDescriptor() DecisionDescriptor[implementationUATRecord] {
	return s.implementationUAT
}

// DraftModeChange validates and canonically encodes a mode change into a catalog-registered
// draft. It is the sole typed construction point for a mode-change decision.
func (s PolicySet) DraftModeChange(c InteractionModeChanged) (DecisionDraft, error) {
	return s.modeChanged.Draft(c)
}

// DraftPlanUAT lowers a PlanUATDecision to the matching typed payload and drafts it. It
// enforces the cross-field rules the verdict implies: a deferral must be AFK-anchored with
// a stable held question and no FIX-NOW feedback (EvaluatePlanDeferral); an acceptance
// carries no FIX-NOW feedback. The Mode cursor is consumed only for the deferred branch.
func (s PolicySet) DraftPlanUAT(d PlanUATDecision) (DecisionDraft, error) {
	if !d.ReportedVerdict.valid() {
		return DecisionDraft{}, uatErr("PlanUATDecision.ReportedVerdict", fmt.Sprintf("the reported verdict %q is not a known plan-UAT verdict", d.ReportedVerdict),
			"a plan UAT is accepted, changes_requested, or deferred_by_afk", "report accepted, changes_requested, or deferred_by_afk")
	}
	switch d.ReportedVerdict {
	case PlanUATAccepted:
		if len(d.HeldQuestions) != 0 {
			return DecisionDraft{}, uatErr("PlanUATDecision.HeldQuestions", "accepted Plan UAT carries held questions",
				"held questions are meaningful only when the Plan UAT is deferred by AFK",
				"remove held questions or report deferred_by_afk")
		}
		return s.planAccepted.Draft(PlanAccepted{
			Snapshot:     d.Snapshot,
			Interactions: d.Interactions,
			Feedback:     d.Feedback,
		})
	case PlanUATChangesRequested:
		if len(d.HeldQuestions) != 0 {
			return DecisionDraft{}, uatErr("PlanUATDecision.HeldQuestions", "changes-requested Plan UAT carries held questions",
				"held questions are meaningful only when the Plan UAT is deferred by AFK",
				"remove held questions or report deferred_by_afk")
		}
		return s.planChanges.Draft(PlanChangesRequested{
			Snapshot:     d.Snapshot,
			Interactions: d.Interactions,
			Feedback:     d.Feedback,
		})
	case PlanUATDeferredByAFK:
		if err := EvaluatePlanDeferral(PlanDeferralInput{
			Mode:          d.Mode,
			HeldQuestions: d.HeldQuestions,
			Feedback:      d.Feedback,
			Snapshot:      d.Snapshot,
		}); err != nil {
			return DecisionDraft{}, err
		}
		return s.planDeferred.Draft(PlanDeferredByAFK{
			Snapshot:      d.Snapshot,
			Interactions:  d.Interactions,
			Feedback:      d.Feedback,
			HeldQuestions: d.HeldQuestions,
			ModeEntry:     *d.Mode.Entry,
		})
	default:
		return DecisionDraft{}, uatErr("PlanUATDecision.ReportedVerdict", fmt.Sprintf("unhandled plan-UAT verdict %q", d.ReportedVerdict),
			"every plan-UAT verdict must lower to a registered decision", "report a known plan-UAT verdict")
	}
}

// DraftImplementationUAT validates the authoritative verdict with its structured payload,
// then drafts the payload through the production descriptor.
func (s PolicySet) DraftImplementationUAT(verdict ImplementationUATVerdict, p ImplUATPayload) (DecisionDraft, error) {
	return s.implementationUAT.Draft(implementationUATRecord{Outcome: verdict, Payload: p})
}

func (s PolicySet) DraftPlanRatified(v PlanRatified) (DecisionDraft, error) {
	return s.planRatified.Draft(v)
}

func (s PolicySet) DraftLanded(v EpochLanded) (DecisionDraft, error) {
	return s.landed.Draft(v)
}

// newJSONDescriptor builds a #49 decision descriptor over a payload type T using the
// canonical-JSON codec, a schema digest derived deterministically from the kind and a
// field-shape descriptor, and the supplied typed validate. Encoding is canonicalJSON;
// decoding is a strict JSON unmarshal — the #43 machinery re-encodes and requires
// byte-identical output, so any non-canonical stored payload is rejected on read.
func newJSONDescriptor[T any](kind DecisionKindID, shape string, validate func(T) error) (DecisionDescriptor[T], error) {
	schema := policySchemaDigest(kind, shape)
	encode := func(v T) (CanonicalDecisionPayload, error) {
		b, err := canonicalJSON(v)
		if err != nil {
			return nil, fmt.Errorf("canonical encode decision %q: %w", kind, err)
		}
		return CanonicalDecisionPayload(b), nil
	}
	decode := func(p CanonicalDecisionPayload) (T, error) {
		var v T
		if err := json.Unmarshal(p, &v); err != nil {
			return v, fmt.Errorf("decode decision %q: %w", kind, err)
		}
		return v, nil
	}
	return NewDecisionDescriptor[T](kind, policyCanonicalJSONCodec, schema, validate, encode, decode)
}

// policySchemaDigest derives a stable, non-zero schema digest from a decision kind and a
// field-shape descriptor. Changing the payload shape (and thus the descriptor string)
// changes the digest, which the golden schema test pins — so an accidental encoding change
// is caught, exactly as the #43 base contract requires.
func policySchemaDigest(kind DecisionKindID, shape string) DecisionSchemaDigest {
	h := sha256.New()
	h.Write([]byte("pasture.decision-schema/v1"))
	h.Write([]byte{0})
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(shape))
	var d DecisionSchemaDigest
	copy(d[:], h.Sum(nil))
	return d
}
