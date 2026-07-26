package tasks

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

const (
	implementationReviewAuthorityEvidenceKind  provenance.EvidenceKind = "pasture.implementation-review.authority.v1"
	candidateManifestEvidenceKind              provenance.EvidenceKind = "pasture.integration.candidate-manifest.v1"
	candidatePublicationSetEvidenceKind        provenance.EvidenceKind = "pasture.integration.publication-set.v1"
	implementationUATReviewBindingEvidenceKind provenance.EvidenceKind = "pasture.implementation-uat.review-binding.v1"
)

// reviewAuthorityState is the closed lifecycle of the current implementation
// review authority for one integration candidate.
type reviewAuthorityState uint8

const (
	reviewAuthorityStateInvalid reviewAuthorityState = iota
	reviewStarted
	reviewFinalizedRevising
	reviewFinalizedClean
	reviewInvalidated
)

func (s reviewAuthorityState) valid() bool {
	return s >= reviewStarted && s <= reviewInvalidated
}

// reviewAxisAuthority binds one canonical review axis to its immutable event and
// verdict. The array containing these values is always in the authority's
// private canonical order.
type reviewAxisAuthority struct {
	Axis    ReviewAxis
	Event   provenance.JournalID
	Verdict Verdict
}

// implementationReviewAuthority is the typed current-review fact consumed by
// later user gates. It deliberately contains no material-event payload; events
// remain audit references while this fact is the eligibility authority.
type implementationReviewAuthority struct {
	Epoch     EpochRootID
	Candidate IntegrationCandidateSetID
	Round     ReviewRoundID
	State     reviewAuthorityState
	Axes      [3]reviewAxisAuthority
	Operation provenance.OperationID
}

// candidateMember is one immutable repository/candidate binding in a candidate
// set. Repository and Candidate are each unique within one manifest.
type candidateMember struct {
	Repository RepositoryID
	Candidate  ImplementationCandidateID
	Commit     provenance.GitOID
}

// integrationCandidateManifest is the immutable membership fact for an
// integration candidate. A replacement receives a new candidate identity and a
// new manifest rather than mutating this value.
type integrationCandidateManifest struct {
	Epoch     EpochRootID
	Candidate IntegrationCandidateSetID
	Members   []candidateMember
	Operation provenance.OperationID
}

// repositoryPublication is one exact remote verification in a complete
// publication snapshot.
type repositoryPublication struct {
	Repository            RepositoryID
	Candidate             ImplementationCandidateID
	Ref                   GitRef
	Commit                provenance.GitOID
	VerificationOperation provenance.OperationID
}

// candidatePublicationSet is a complete current snapshot. It is replaced as a
// whole for every publication operation; it is not an append-only per-repository
// authority.
type candidatePublicationSet struct {
	Epoch        EpochRootID
	Candidate    IntegrationCandidateSetID
	Publications []repositoryPublication
	Operation    provenance.OperationID
}

// implementationUATReviewBinding records the exact review fact consulted by one
// Implementation UAT decision. The current review selector remains the
// transaction-local gate; this immutable binding preserves the exact fact that
// was accepted by the UAT operation.
type implementationUATReviewBinding struct {
	Epoch       EpochRootID
	Candidate   IntegrationCandidateSetID
	ReviewRound ReviewRoundID
	ReviewFact  provenance.JournalID
	Operation   provenance.OperationID
}

// subjectStateSource is the closed source discriminator for candidate-state
// evidence. Human states carry a decision identity; assignment states carry
// only their producing operation.
type subjectStateSource uint8

const (
	subjectStateSourceInvalid subjectStateSource = iota
	subjectStateSourceHumanDecision
	subjectStateSourceAssignmentOperation
)

// subjectState is the private lifecycle projection shared by human and
// assignment-controlled aggregates. The candidate-current and reworked states
// are assignment-produced; all decision states are human-produced.
type subjectState string

const (
	subjectStateInvalid                subjectState = ""
	subjectStatePlanAccepted           subjectState = "plan-accepted"
	subjectStatePlanChangesRequested   subjectState = "plan-changes-requested"
	subjectStatePlanDeferred           subjectState = "plan-deferred"
	subjectStatePlanRatified           subjectState = "plan-ratified"
	subjectStateCandidateCurrent       subjectState = "candidate-current"
	subjectStateImplementationAccepted subjectState = "implementation-accepted"
	subjectStateImplementationChanges  subjectState = "implementation-changes-requested"
	subjectStateImplementationLanded   subjectState = "implementation-landed"
	subjectStateReworked               subjectState = "reworked"
)

// subjectStateEvidence keeps the historical human fields stable for persisted
// facts while Source makes the authority class explicit. Assignment-produced
// states leave Decision and DecisionKind empty by construction.
type subjectStateEvidence struct {
	Epoch   string       `json:"epoch"`
	Subject string       `json:"subject"`
	State   subjectState `json:"state"`
	// Source is a typed in-memory discriminator. The compact wire contract uses
	// the closed presence of Decision/DecisionKind: human states carry both,
	// assignment states carry neither. This preserves the existing evidence
	// family payload shape while preventing assignment states from fabricating a
	// decision identity.
	Source       subjectStateSource     `json:"-"`
	Decision     DecisionLedgerEntryID  `json:"decision,omitempty"`
	DecisionKind DecisionKindID         `json:"decisionKind,omitempty"`
	Operation    provenance.OperationID `json:"operation"`
}

func (s *subjectStateEvidence) UnmarshalJSON(payload []byte) error {
	type wire struct {
		Epoch        string                 `json:"epoch"`
		Subject      string                 `json:"subject"`
		State        subjectState           `json:"state"`
		Decision     DecisionLedgerEntryID  `json:"decision,omitempty"`
		DecisionKind DecisionKindID         `json:"decisionKind,omitempty"`
		Operation    provenance.OperationID `json:"operation"`
	}
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("subject state payload has trailing JSON")
		}
		return err
	}
	s.Epoch, s.Subject, s.State = value.Epoch, value.Subject, value.State
	s.Decision, s.DecisionKind, s.Operation = value.Decision, value.DecisionKind, value.Operation
	s.Source = subjectStateSourceAssignmentOperation
	if value.Decision != "" || value.DecisionKind != "" {
		s.Source = subjectStateSourceHumanDecision
	}
	return nil
}

type subjectStateSnapshot struct {
	subject      provenance.TaskID
	state        subjectState
	decision     DecisionLedgerEntryID
	decisionKind DecisionKindID
	operation    provenance.OperationID
	source       subjectStateSource
	journalID    provenance.JournalID
	family       provenance.EvidenceKind
}

func (s subjectStateSnapshot) conditionCurrent() provenance.Condition {
	return currentEvidenceCondition(s.subject, s.family, s.journalID)
}

func (s subjectStateSnapshot) conditionExact() provenance.Condition {
	return evidenceCondition(s.subject, s.family, s.journalID, provenance.ConditionExactFact)
}

func (s subjectStateSnapshot) terminal() bool {
	return s.state == subjectStatePlanRatified || s.state == subjectStateImplementationLanded
}

func currentEvidenceCondition(subject provenance.TaskID, kind provenance.EvidenceKind, asserted provenance.JournalID) provenance.Condition {
	return evidenceCondition(subject, kind, asserted, provenance.ConditionCurrentFact)
}

func evidenceCondition(subject provenance.TaskID, kind provenance.EvidenceKind, asserted provenance.JournalID, conditionKind provenance.ConditionKind) provenance.Condition {
	return provenance.Condition{Kind: conditionKind, Selector: provenance.FactSelector{Kind: provenance.FactEvidence, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, EvidenceKind: kind}, AssertedJournalID: asserted}
}

func reviewAuthoritySelector(subject provenance.TaskID) provenance.FactSelector {
	return evidenceSelector(subject, implementationReviewAuthorityEvidenceKind)
}

func candidateManifestSelector(subject provenance.TaskID) provenance.FactSelector {
	return evidenceSelector(subject, candidateManifestEvidenceKind)
}

func candidatePublicationSetSelector(subject provenance.TaskID) provenance.FactSelector {
	return evidenceSelector(subject, candidatePublicationSetEvidenceKind)
}

func candidateStateSelector(subject provenance.TaskID) provenance.FactSelector {
	return evidenceSelector(subject, candidateEvidenceKind)
}

func implementationUATReviewBindingSelector(subject provenance.TaskID) provenance.FactSelector {
	return evidenceSelector(subject, implementationUATReviewBindingEvidenceKind)
}

func evidenceSelector(subject provenance.TaskID, kind provenance.EvidenceKind) provenance.FactSelector {
	return provenance.FactSelector{
		Kind:         provenance.FactEvidence,
		Filter:       provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}},
		EvidenceKind: kind,
	}
}

func reviewAuthorityCurrentCondition(subject provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: reviewAuthoritySelector(subject), AssertedJournalID: asserted}
}

func candidateManifestExactCondition(subject provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionExactFact, Selector: candidateManifestSelector(subject), AssertedJournalID: asserted}
}

func candidatePublicationSetCurrentCondition(subject provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: candidatePublicationSetSelector(subject), AssertedJournalID: asserted}
}

func candidateStateCurrentCondition(subject provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: candidateStateSelector(subject), AssertedJournalID: asserted}
}

func implementationUATReviewBindingExactCondition(subject provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionExactFact, Selector: implementationUATReviewBindingSelector(subject), AssertedJournalID: asserted}
}

func newReviewStartedAuthority(epoch EpochRootID, candidate IntegrationCandidateSetID, round ReviewRoundID, operation provenance.OperationID) (implementationReviewAuthority, error) {
	return newImplementationReviewAuthority(implementationReviewAuthority{Epoch: epoch, Candidate: candidate, Round: round, State: reviewStarted, Operation: operation})
}

func newFinalizedReviewAuthority(epoch EpochRootID, candidate IntegrationCandidateSetID, round ReviewRoundID, axes [3]reviewAxisAuthority, operation provenance.OperationID) (implementationReviewAuthority, error) {
	state := reviewFinalizedClean
	for _, axis := range axes {
		if axis.Verdict == VerdictRevise {
			state = reviewFinalizedRevising
			break
		}
	}
	return newImplementationReviewAuthority(implementationReviewAuthority{Epoch: epoch, Candidate: candidate, Round: round, State: state, Axes: axes, Operation: operation})
}

func newInvalidatedReviewAuthority(epoch EpochRootID, candidate IntegrationCandidateSetID, priorRound ReviewRoundID, operation provenance.OperationID) (implementationReviewAuthority, error) {
	return newImplementationReviewAuthority(implementationReviewAuthority{Epoch: epoch, Candidate: candidate, Round: priorRound, State: reviewInvalidated, Operation: operation})
}

func newImplementationReviewAuthority(value implementationReviewAuthority) (implementationReviewAuthority, error) {
	if err := validateImplementationReviewAuthority(value); err != nil {
		return implementationReviewAuthority{}, err
	}
	return value, nil
}

func validateImplementationReviewAuthority(value implementationReviewAuthority) error {
	if err := validateEpoch(value.Epoch); err != nil {
		return authorityErr("implementationReviewAuthority", "the epoch identity is malformed", "review authority must be scoped to one valid epoch", "supply an epoch task identity")
	}
	if err := validateTaskWrapper(string(value.Candidate), "candidate"); err != nil {
		return err
	}
	if err := validateText(string(value.Round), "review round"); err != nil {
		return err
	}
	if err := validateOperationID(value.Operation); err != nil {
		return authorityErr("implementationReviewAuthority", "the producing operation identity is malformed", "authority replay requires one stable operation identity", "supply a non-empty operation identity without control characters")
	}
	if !value.State.valid() {
		return authorityErr("implementationReviewAuthority", "the review authority state is unknown", "review authority is a closed lifecycle", "use reviewStarted, reviewFinalizedRevising, reviewFinalizedClean, or reviewInvalidated")
	}
	if value.State == reviewStarted || value.State == reviewInvalidated {
		if !zeroReviewAxes(value.Axes) {
			return authorityErr("implementationReviewAuthority", "a started or invalidated review carries axis authority", "those states have no accepted axis facts", "clear all review axis entries")
		}
		return nil
	}
	if err := validateReviewAxes(value.Axes, value.State); err != nil {
		return err
	}
	return nil
}

func validateReviewAxes(axes [3]reviewAxisAuthority, state reviewAuthorityState) error {
	hasRevise := false
	seenEvents := make(map[provenance.JournalID]struct{}, len(axes))
	canonicalAxes := canonicalReviewAxes()
	for i, axis := range axes {
		if axis.Axis != canonicalAxes[i] {
			return authorityErr("reviewAxisAuthority", fmt.Sprintf("axis %d is %s", i, axis.Axis), "review authority uses correctness, test-quality, elegance order", "supply correctness, test-quality, elegance order")
		}
		if axis.Event <= 0 {
			return authorityErr("reviewAxisAuthority", fmt.Sprintf("axis %d has invalid event %d", i, axis.Event), "a finalized authority must bind every immutable review event with a positive journal identity", "supply three distinct positive review event IDs")
		}
		if _, found := seenEvents[axis.Event]; found {
			return authorityErr("reviewAxisAuthority", fmt.Sprintf("axis %d repeats event %d", i, axis.Event), "each review axis must bind a distinct event", "supply one distinct event per axis")
		}
		seenEvents[axis.Event] = struct{}{}
		if !axis.Verdict.valid() {
			return authorityErr("reviewAxisAuthority", fmt.Sprintf("axis %d has an unknown verdict", i), "review verdicts are a closed accept/revise enum", "supply VerdictAccept or VerdictRevise")
		}
		hasRevise = hasRevise || axis.Verdict == VerdictRevise
	}
	if state == reviewFinalizedClean && hasRevise {
		return authorityErr("reviewAxisAuthority", "a clean finalized authority contains a revise verdict", "clean authority requires acceptance on every axis", "use reviewFinalizedRevising or replace the axis verdicts")
	}
	if state == reviewFinalizedRevising && !hasRevise {
		return authorityErr("reviewAxisAuthority", "a revising finalized authority contains no revise verdict", "revising authority requires actionable revision", "use reviewFinalizedClean or include a revise axis")
	}
	return nil
}

// canonicalReviewAxes returns a fresh value so authority validation cannot be
// changed by mutation of the exported ReviewAxes API value.
func canonicalReviewAxes() [3]ReviewAxis {
	return [3]ReviewAxis{AxisCorrectness, AxisTestQuality, AxisElegance}
}

func zeroReviewAxes(axes [3]reviewAxisAuthority) bool {
	return axes == [3]reviewAxisAuthority{}
}

func normalizeCandidateMembers(members []candidateMember) ([]candidateMember, error) {
	if len(members) == 0 {
		return nil, authorityErr("candidateManifest", "the candidate manifest has no members", "an integration candidate must identify at least one repository commit", "supply one valid member per candidate repository")
	}
	copyMembers := append([]candidateMember(nil), members...)
	for i, member := range copyMembers {
		if err := validateRepositoryID(member.Repository); err != nil {
			return nil, fmt.Errorf("candidate manifest member %d: %w", i, err)
		}
		if err := validateTaskWrapper(string(member.Candidate), "candidate member"); err != nil {
			return nil, fmt.Errorf("candidate manifest member %d: %w", i, err)
		}
		if err := validateGitOID(member.Commit); err != nil {
			return nil, fmt.Errorf("candidate manifest member %d: %w", i, err)
		}
	}
	sort.Slice(copyMembers, func(i, j int) bool {
		if copyMembers[i].Repository != copyMembers[j].Repository {
			return copyMembers[i].Repository < copyMembers[j].Repository
		}
		return copyMembers[i].Candidate < copyMembers[j].Candidate
	})
	for i := 1; i < len(copyMembers); i++ {
		if copyMembers[i-1].Repository == copyMembers[i].Repository || copyMembers[i-1].Candidate == copyMembers[i].Candidate {
			return nil, authorityErr("candidateManifest", fmt.Sprintf("members %d and %d duplicate a repository or candidate", i-1, i), "one complete manifest has one candidate commit per repository", "remove the duplicate member")
		}
	}
	return copyMembers, nil
}

func newIntegrationCandidateManifest(epoch EpochRootID, candidate IntegrationCandidateSetID, members []candidateMember, operation provenance.OperationID) (integrationCandidateManifest, error) {
	if err := validateEpoch(epoch); err != nil {
		return integrationCandidateManifest{}, err
	}
	if err := validateTaskWrapper(string(candidate), "candidate set"); err != nil {
		return integrationCandidateManifest{}, err
	}
	if err := validateOperationID(operation); err != nil {
		return integrationCandidateManifest{}, authorityErr("candidateManifest", "the manifest operation identity is malformed", "immutable membership must identify its producer", "supply a stable operation identity")
	}
	normalized, err := normalizeCandidateMembers(members)
	if err != nil {
		return integrationCandidateManifest{}, err
	}
	return integrationCandidateManifest{Epoch: epoch, Candidate: candidate, Members: normalized, Operation: operation}, nil
}

func normalizePublications(publications []repositoryPublication) ([]repositoryPublication, error) {
	if len(publications) == 0 {
		return nil, authorityErr("candidatePublicationSet", "the publication set has no verified members", "a current publication snapshot must contain the verification being recorded", "supply at least one repository publication")
	}
	copyPublications := append([]repositoryPublication(nil), publications...)
	for i, publication := range copyPublications {
		if err := validateRepositoryID(publication.Repository); err != nil {
			return nil, fmt.Errorf("publication %d: %w", i, err)
		}
		if err := validateTaskWrapper(string(publication.Candidate), "published candidate"); err != nil {
			return nil, fmt.Errorf("publication %d: %w", i, err)
		}
		if err := validateGitRef(publication.Ref); err != nil {
			return nil, fmt.Errorf("publication %d: %w", i, err)
		}
		if err := validateGitOID(publication.Commit); err != nil {
			return nil, fmt.Errorf("publication %d: %w", i, err)
		}
		if err := validateOperationID(publication.VerificationOperation); err != nil {
			return nil, authorityErr("candidatePublicationSet", fmt.Sprintf("publication %d has a malformed verification operation", i), "each verification must be replayable and attributable", "supply a stable operation identity")
		}
	}
	sort.Slice(copyPublications, func(i, j int) bool {
		if copyPublications[i].Repository != copyPublications[j].Repository {
			return copyPublications[i].Repository < copyPublications[j].Repository
		}
		return copyPublications[i].Candidate < copyPublications[j].Candidate
	})
	for i := 1; i < len(copyPublications); i++ {
		if copyPublications[i-1].Repository == copyPublications[i].Repository || copyPublications[i-1].Candidate == copyPublications[i].Candidate {
			return nil, authorityErr("candidatePublicationSet", fmt.Sprintf("publications %d and %d duplicate a repository or candidate", i-1, i), "a publication set is one complete verification per candidate repository", "remove the duplicate publication")
		}
	}
	return copyPublications, nil
}

func newCandidatePublicationSet(epoch EpochRootID, candidate IntegrationCandidateSetID, publications []repositoryPublication, operation provenance.OperationID) (candidatePublicationSet, error) {
	if err := validateEpoch(epoch); err != nil {
		return candidatePublicationSet{}, err
	}
	if err := validateTaskWrapper(string(candidate), "candidate set"); err != nil {
		return candidatePublicationSet{}, err
	}
	if err := validateOperationID(operation); err != nil {
		return candidatePublicationSet{}, authorityErr("candidatePublicationSet", "the publication-set operation identity is malformed", "a current publication snapshot must identify its producer", "supply a stable operation identity")
	}
	normalized, err := normalizePublications(publications)
	if err != nil {
		return candidatePublicationSet{}, err
	}
	return candidatePublicationSet{Epoch: epoch, Candidate: candidate, Publications: normalized, Operation: operation}, nil
}

// validatePublicationSetAgainstManifest proves completeness at the producer
// boundary: every manifest member has exactly one matching publication and no
// publication names a repository/candidate outside the immutable manifest.
func validatePublicationSetAgainstManifest(manifest integrationCandidateManifest, publicationSet candidatePublicationSet) error {
	if manifest.Epoch != publicationSet.Epoch || manifest.Candidate != publicationSet.Candidate {
		return authorityErr("publication completeness", "the publication set is scoped to a different epoch or candidate", "publication evidence must verify the immutable candidate manifest it belongs to", "use the manifest's epoch and candidate identities")
	}
	if len(manifest.Members) != len(publicationSet.Publications) {
		return authorityErr("publication completeness", "the publication set is incomplete", "landing requires one exact publication for every candidate-manifest member", "publish every manifest member before landing")
	}
	for i, member := range manifest.Members {
		publication := publicationSet.Publications[i]
		if publication.Repository != member.Repository || publication.Candidate != member.Candidate || publication.Commit != member.Commit {
			return authorityErr("publication completeness", fmt.Sprintf("publication %d does not match its manifest member", i), "repository, candidate, and commit bindings are immutable across publication", "verify the exact manifest commit at the published ref")
		}
	}
	return nil
}

func newImplementationUATReviewBinding(epoch EpochRootID, candidate IntegrationCandidateSetID, round ReviewRoundID, reviewFact provenance.JournalID, operation provenance.OperationID) (implementationUATReviewBinding, error) {
	if err := validateEpoch(epoch); err != nil {
		return implementationUATReviewBinding{}, err
	}
	if err := validateTaskWrapper(string(candidate), "candidate set"); err != nil {
		return implementationUATReviewBinding{}, err
	}
	if err := validateText(string(round), "review round"); err != nil {
		return implementationUATReviewBinding{}, err
	}
	if reviewFact <= 0 {
		return implementationUATReviewBinding{}, authorityErr("implementationUATReviewBinding", fmt.Sprintf("the review fact %d is not positive", reviewFact), "Implementation UAT must bind one exact finalized review authority fact with a positive journal identity", "supply the committed positive review evidence journal id")
	}
	if err := validateOperationID(operation); err != nil {
		return implementationUATReviewBinding{}, authorityErr("implementationUATReviewBinding", "the UAT operation identity is malformed", "review binding must be replayable", "supply a stable operation identity")
	}
	return implementationUATReviewBinding{Epoch: epoch, Candidate: candidate, ReviewRound: round, ReviewFact: reviewFact, Operation: operation}, nil
}

func newHumanSubjectStateEvidence(epoch, subject provenance.TaskID, state subjectState, decision DecisionLedgerEntryID, kind DecisionKindID, operation provenance.OperationID) (subjectStateEvidence, error) {
	value := subjectStateEvidence{Epoch: epoch.String(), Subject: subject.String(), State: state, Source: subjectStateSourceHumanDecision, Decision: decision, DecisionKind: kind, Operation: operation}
	if err := validateSubjectStateEvidence(value, epoch, subject, "", operation); err != nil {
		return subjectStateEvidence{}, err
	}
	return value, nil
}

func newAssignmentSubjectStateEvidence(epoch, subject provenance.TaskID, state subjectState, operation provenance.OperationID) (subjectStateEvidence, error) {
	value := subjectStateEvidence{Epoch: epoch.String(), Subject: subject.String(), State: state, Source: subjectStateSourceAssignmentOperation, Operation: operation}
	if err := validateSubjectStateEvidence(value, epoch, subject, candidateEvidenceKind, operation); err != nil {
		return subjectStateEvidence{}, err
	}
	return value, nil
}

func validateSubjectStateEvidence(value subjectStateEvidence, epoch, subject provenance.TaskID, family provenance.EvidenceKind, producingOperation provenance.OperationID) error {
	for _, scalar := range []struct {
		value string
		label string
	}{
		{value.Epoch, "subject-state epoch"},
		{value.Subject, "subject-state subject"},
		{string(value.State), "subject-state value"},
		{string(value.Decision), "subject-state decision"},
		{string(value.DecisionKind), "subject-state decision kind"},
	} {
		if err := validateAuthorityUTF8(scalar.value, scalar.label); err != nil {
			return err
		}
	}
	if epoch == (provenance.TaskID{}) || subject == (provenance.TaskID{}) || value.Epoch != epoch.String() || value.Subject != subject.String() {
		return authorityErr("subjectStateEvidence", "the epoch or subject identity is inconsistent", "candidate state must point to the exact task-scoped subject", "construct the state from the epoch and subject task IDs")
	}
	if _, err := provenance.TaskContext(epoch); err != nil {
		return authorityErr("subjectStateEvidence", "the epoch identity is malformed", "candidate state must be scoped to a valid Provenance task", "supply a valid epoch task identity")
	}
	if _, err := provenance.TaskContext(subject); err != nil {
		return authorityErr("subjectStateEvidence", "the subject identity is malformed", "candidate state must be scoped to a valid Provenance task", "supply a valid subject task identity")
	}
	if err := validateOperationID(value.Operation); err != nil || value.Operation != producingOperation {
		return authorityErr("subjectStateEvidence", "the producing operation identity is inconsistent", "state evidence must identify the operation that emitted the fact", "use the operation identity from the enclosing Apply")
	}
	if !subjectStateBelongsToFamily(value.State, family) && family != "" {
		return authorityErr("subjectStateEvidence", "the state does not belong to its evidence family", "plan and candidate lifecycles use fixed evidence families", "construct the state with its matching family")
	}
	switch value.Source {
	case subjectStateSourceHumanDecision:
		kind, ok := subjectStateDecisionKind(value.State)
		if !ok || value.Decision == "" || value.DecisionKind != kind || value.Decision != decisionIDForOperation(value.Operation) {
			return authorityErr("subjectStateEvidence", "human state lacks its exact decision binding", "human lifecycle state must point to the immutable decision that produced it", "supply the matching decision id and decision kind")
		}
	case subjectStateSourceAssignmentOperation:
		if value.Decision != "" || value.DecisionKind != "" || (value.State != subjectStateCandidateCurrent && value.State != subjectStateReworked) || family != candidateEvidenceKind {
			return authorityErr("subjectStateEvidence", "assignment state carries an impossible decision binding", "assignment-produced candidate state has operation authority only", "use candidate-current or reworked with empty decision fields")
		}
	default:
		return authorityErr("subjectStateEvidence", "the state source discriminator is unknown", "candidate state evidence is a closed source union", "construct it with a human-decision or assignment-operation constructor")
	}
	return nil
}

func newReviewAuthorityEvidenceEffect(subject provenance.TaskID, value implementationReviewAuthority, resultSlot provenance.ResultSlotID) (provenance.Effect, error) {
	canonical, err := newImplementationReviewAuthority(value)
	if err != nil {
		return provenance.Effect{}, err
	}
	if err := validateEvidenceSubject(subject, string(canonical.Candidate), "review authority"); err != nil {
		return provenance.Effect{}, err
	}
	return newTypedEvidenceEffect(subject, implementationReviewAuthorityEvidenceKind, canonical, resultSlot, "implementation review authority")
}

func newCandidateManifestEvidenceEffect(subject provenance.TaskID, value integrationCandidateManifest, resultSlot provenance.ResultSlotID) (provenance.Effect, error) {
	canonical, err := newIntegrationCandidateManifest(value.Epoch, value.Candidate, value.Members, value.Operation)
	if err != nil {
		return provenance.Effect{}, err
	}
	if err := validateEvidenceSubject(subject, string(canonical.Candidate), "candidate manifest"); err != nil {
		return provenance.Effect{}, err
	}
	return newTypedEvidenceEffect(subject, candidateManifestEvidenceKind, canonical, resultSlot, "candidate manifest")
}

func newCandidatePublicationSetEvidenceEffect(subject provenance.TaskID, value candidatePublicationSet, resultSlot provenance.ResultSlotID) (provenance.Effect, error) {
	canonical, err := newCandidatePublicationSet(value.Epoch, value.Candidate, value.Publications, value.Operation)
	if err != nil {
		return provenance.Effect{}, err
	}
	if err := validateEvidenceSubject(subject, string(canonical.Candidate), "candidate publication set"); err != nil {
		return provenance.Effect{}, err
	}
	return newTypedEvidenceEffect(subject, candidatePublicationSetEvidenceKind, canonical, resultSlot, "candidate publication set")
}

func newImplementationUATReviewBindingEvidenceEffect(subject provenance.TaskID, value implementationUATReviewBinding, resultSlot provenance.ResultSlotID) (provenance.Effect, error) {
	canonical, err := newImplementationUATReviewBinding(value.Epoch, value.Candidate, value.ReviewRound, value.ReviewFact, value.Operation)
	if err != nil {
		return provenance.Effect{}, err
	}
	if err := validateEvidenceSubject(subject, string(canonical.Candidate), "Implementation UAT review binding"); err != nil {
		return provenance.Effect{}, err
	}
	return newTypedEvidenceEffect(subject, implementationUATReviewBindingEvidenceKind, canonical, resultSlot, "Implementation UAT review binding")
}

func newSubjectStateEvidenceEffect(subject provenance.TaskID, value subjectStateEvidence, resultSlot provenance.ResultSlotID) (provenance.Effect, error) {
	epoch, err := parseAuthorityTaskID(value.Epoch, "subject-state epoch")
	if err != nil {
		return provenance.Effect{}, err
	}
	if err := validateSubjectStateEvidence(value, epoch, subject, familyForSubjectState(value.State), value.Operation); err != nil {
		return provenance.Effect{}, err
	}
	return newTypedEvidenceEffect(subject, familyForSubjectState(value.State), value, resultSlot, "candidate state")
}

func newTypedEvidenceEffect(subject provenance.TaskID, kind provenance.EvidenceKind, value any, resultSlot provenance.ResultSlotID, label string) (provenance.Effect, error) {
	if subject == (provenance.TaskID{}) {
		return provenance.Effect{}, authorityErr("newTypedEvidenceEffect", "the evidence subject is zero", label+" evidence must be task-scoped", "supply the exact subject task")
	}
	if _, err := provenance.TaskContext(subject); err != nil {
		return provenance.Effect{}, authorityErr("newTypedEvidenceEffect", "the evidence subject is malformed", label+" evidence requires a valid Provenance task identity", "supply a valid namespace--uuid task identity")
	}
	if resultSlot == "" {
		return provenance.Effect{}, authorityErr("newTypedEvidenceEffect", "the evidence result slot is empty", label+" evidence must expose its committed fact binding", "supply a non-empty result slot")
	}
	payload, err := canonicalJSON(value)
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("encode %s evidence payload: %w", label, err)
	}
	digest := sha256.Sum256(payload)
	return provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: resultSlot, TaskID: subject, EvidenceKind: kind, ContentDigest: digest[:], Payload: payload}, nil
}

func decodeImplementationReviewAuthority(payload []byte) (implementationReviewAuthority, error) {
	var value implementationReviewAuthority
	if err := decodeAuthorityJSON(payload, &value); err != nil {
		return implementationReviewAuthority{}, fmt.Errorf("decode implementation review authority: %w", err)
	}
	if err := validateImplementationReviewAuthority(value); err != nil {
		return implementationReviewAuthority{}, err
	}
	return value, nil
}

func decodeCandidateManifest(payload []byte) (integrationCandidateManifest, error) {
	var value integrationCandidateManifest
	if err := decodeAuthorityJSON(payload, &value); err != nil {
		return integrationCandidateManifest{}, fmt.Errorf("decode candidate manifest: %w", err)
	}
	normalized, err := newIntegrationCandidateManifest(value.Epoch, value.Candidate, value.Members, value.Operation)
	if err != nil {
		return integrationCandidateManifest{}, err
	}
	if !equalMembers(value.Members, normalized.Members) {
		return integrationCandidateManifest{}, authorityErr("decodeCandidateManifest", "the manifest members are not in canonical order", "immutable evidence must have one deterministic encoding", "re-encode members sorted by repository then candidate")
	}
	return normalized, nil
}

func decodeCandidatePublicationSet(payload []byte) (candidatePublicationSet, error) {
	var value candidatePublicationSet
	if err := decodeAuthorityJSON(payload, &value); err != nil {
		return candidatePublicationSet{}, fmt.Errorf("decode candidate publication set: %w", err)
	}
	normalized, err := newCandidatePublicationSet(value.Epoch, value.Candidate, value.Publications, value.Operation)
	if err != nil {
		return candidatePublicationSet{}, err
	}
	if !equalPublications(value.Publications, normalized.Publications) {
		return candidatePublicationSet{}, authorityErr("decodeCandidatePublicationSet", "the publication set is not in canonical order", "current snapshots need deterministic payloads for exact replay", "re-encode publications sorted by repository then candidate")
	}
	return normalized, nil
}

func decodeImplementationUATReviewBinding(payload []byte) (implementationUATReviewBinding, error) {
	var value implementationUATReviewBinding
	if err := decodeAuthorityJSON(payload, &value); err != nil {
		return implementationUATReviewBinding{}, fmt.Errorf("decode Implementation UAT review binding: %w", err)
	}
	return newImplementationUATReviewBinding(value.Epoch, value.Candidate, value.ReviewRound, value.ReviewFact, value.Operation)
}

func decodeSubjectStateEvidence(payload []byte, epoch, subject provenance.TaskID, family provenance.EvidenceKind, producingOperation provenance.OperationID) (subjectStateEvidence, error) {
	var value subjectStateEvidence
	if err := decodeAuthorityJSON(payload, &value); err != nil {
		return subjectStateEvidence{}, fmt.Errorf("decode subject state evidence: %w", err)
	}
	if err := validateSubjectStateEvidence(value, epoch, subject, family, producingOperation); err != nil {
		return subjectStateEvidence{}, err
	}
	return value, nil
}

func decodeAuthorityJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("malformed JSON payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("malformed JSON payload: trailing value")
		}
		return fmt.Errorf("malformed JSON payload: trailing data: %w", err)
	}
	return nil
}

func familyForSubjectState(state subjectState) provenance.EvidenceKind {
	if state == subjectStatePlanAccepted || state == subjectStatePlanChangesRequested || state == subjectStatePlanDeferred || state == subjectStatePlanRatified {
		return planSubjectEvidenceKind
	}
	return candidateEvidenceKind
}

func validateEpoch(epoch EpochRootID) error {
	return validateTaskWrapper(string(epoch), "epoch")
}

func validateEvidenceSubject(subject provenance.TaskID, candidate, label string) error {
	if subject == (provenance.TaskID{}) {
		return authorityErr("evidence subject", fmt.Sprintf("the %s subject is zero", label), "authority evidence must be scoped to the candidate task", "supply the candidate set task as the evidence subject")
	}
	parsed, err := parseAuthorityTaskID(candidate, label+" candidate")
	if err != nil || parsed != subject {
		return authorityErr("evidence subject", fmt.Sprintf("the %s candidate does not match its task subject", label), "a typed authority fact cannot bind one candidate identity to another task", "use the exact candidate task identity as both values")
	}
	return nil
}

func validateTaskWrapper(raw, label string) error {
	_, err := parseAuthorityTaskID(raw, label)
	return err
}

func validateRepositoryID(repository RepositoryID) error {
	return validateText(string(repository), "repository")
}

func validateGitRef(ref GitRef) error {
	if err := validateText(string(ref), "git ref"); err != nil {
		return err
	}
	value := string(ref)
	if value == "@" || !strings.Contains(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, "~^:?*[\\") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "//") {
		return authorityErr("git ref", fmt.Sprintf("the ref %q is malformed", ref), "publication evidence must identify a valid non-ambiguous Git ref", "supply a Git ref accepted by git-check-ref-format")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return authorityErr("git ref", fmt.Sprintf("the ref %q is malformed", ref), "publication evidence must identify a valid non-ambiguous Git ref", "supply a Git ref accepted by git-check-ref-format")
		}
	}
	return nil
}

func validateGitOID(oid provenance.GitOID) error {
	if err := validateAuthorityUTF8(string(oid), "git object identity"); err != nil {
		return err
	}
	if _, err := provenance.GitContext(oid); err != nil {
		return authorityErr("git object identity", fmt.Sprintf("the Git object id %q is malformed", oid), "candidate and publication facts require canonical SHA-1 or SHA-256 object identities", "supply a lowercase 40- or 64-hex Git object id")
	}
	return nil
}

func validateText(value, label string) error {
	if err := validateAuthorityUTF8(value, label); err != nil {
		return err
	}
	if value == "" || strings.TrimSpace(value) != value {
		return authorityErr("typed scalar", fmt.Sprintf("the %s is empty or padded", label), "authority payloads require a canonical non-empty scalar", fmt.Sprintf("supply a trimmed %s", label))
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return authorityErr("typed scalar", fmt.Sprintf("the %s contains whitespace or control data", label), "authority payloads must have one deterministic scalar representation", fmt.Sprintf("remove whitespace/control characters from the %s", label))
		}
	}
	return nil
}

// validateAuthorityUTF8 rejects lossy JSON inputs before any parser or rune
// traversal can observe replacement runes instead of the original bytes.
func validateAuthorityUTF8(value, label string) error {
	if !utf8.ValidString(value) {
		return authorityErr("typed scalar", fmt.Sprintf("the %s contains invalid UTF-8", label), "authority text must be valid UTF-8 before it can be parsed or encoded as canonical JSON", fmt.Sprintf("supply valid UTF-8 text for the %s", label))
	}
	return nil
}

func validateOperationID(operation provenance.OperationID) error {
	if err := validateAuthorityUTF8(string(operation), "operation identity"); err != nil {
		return err
	}
	return provenance.ValidateOperationID(operation)
}

// parseAuthorityTaskID preserves the source parser failure while adding the
// authority boundary's actionable validation context.
func parseAuthorityTaskID(raw, label string) (provenance.TaskID, error) {
	if err := validateAuthorityUTF8(raw, label); err != nil {
		return provenance.TaskID{}, err
	}
	id, err := provenance.ParseTaskID(raw)
	if err != nil {
		return provenance.TaskID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture rejected an epoch authority contract: the %s identity %q is malformed.", label, raw),
			Why:      "Authority facts are scoped to existing Provenance task identities.",
			Where:    "Epoch authority contract (internal/tasks/epoch_authority.go, parseAuthorityTaskID).",
			Impact:   "The authority fact was not constructed; no journal effect was produced.",
			Fix:      fmt.Sprintf("Supply %s as namespace--uuid.", label),
			Cause:    err,
		}
	}
	return id, nil
}

func equalMembers(left, right []candidateMember) bool {
	return len(left) == len(right) && func() bool {
		for i := range left {
			if left[i] != right[i] {
				return false
			}
		}
		return true
	}()
}

func equalPublications(left, right []repositoryPublication) bool {
	return len(left) == len(right) && func() bool {
		for i := range left {
			if left[i] != right[i] {
				return false
			}
		}
		return true
	}()
}

func authorityErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected an epoch authority contract: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("Epoch authority contract (internal/tasks/epoch_authority.go, %s).", where),
		Impact:   "The authority fact was not constructed; no journal effect was produced.",
		Fix:      fix + ".",
	}
}
