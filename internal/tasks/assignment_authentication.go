package tasks

// assignment_authentication.go owns ONE question: may this command act under
// the assignment it claims? A command is allowed to write only when the journal
// itself proves three things about the assignment it names: the start row, the
// material fact that describes it, and the command evidence that created it.
//
// There is no Pasture-owned table of started episodes behind this answer any
// more. Everything here is read from the journal through its public API, so a
// gate and a command see the same facts and neither can disagree with the other.
//
// THE ORDER OF THE THREE CHECKS is the order of trust. The start row says WHO
// holds the task. The material fact says WHICH slot they hold it in. The
// command evidence says WHY the holder was created and by which operation. A
// row that fails any one of them is refused, and the refusal happens before the
// command writes anything.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dayvidpham/provenance"
)

// maxParentProofPages bounds the command proof read. It is the ONLY bound on
// the write path: a command authenticates at most this many pages of material
// and at most this many pages of command evidence, each page holding at most
// maxParentProofPageRows rows. Every predecessor inside the authenticated start
// page is checked, however many there are; no separate per-predecessor budget
// exists and none is needed, because the page size already bounds the work.
const (
	maxParentProofPages    = 16
	maxParentProofPageRows = 64
)

// AssignmentAuthenticationError is a refusal before any write, never a policy
// denial. It is raised when a command could not authenticate the assignment it
// acts under.
type AssignmentAuthenticationError struct {
	Step  string
	Cause error
}

// Error names what could not be authenticated, why the check exists, where it
// ran, when it ran relative to the write, what the caller lost, and what to do
// about it.
func (e *AssignmentAuthenticationError) Error() string {
	return fmt.Sprintf(
		"Pasture could not authenticate the assignment this command acts under (step %s): %v. "+
			"Why: the command checks the assignment's start, material and command evidence in the journal before it writes, and this check failed. "+
			"Where: internal/tasks/assignment_authentication.go, authenticateRecoveryRows. "+
			"When: before the command wrote anything. "+
			"Impact: the command was refused and nothing was written; this is not a policy denial. "+
			"Fix: check that the assignment id, task and actor on the command are the ones the parent assignment was started with, then retry; "+
			"if the check fails again on unchanged input, run Journal.VerifyIntegrity on the store.",
		e.Step, e.Cause)
}

func (e *AssignmentAuthenticationError) Unwrap() error { return e.Cause }

func authenticationFault(step string, err error) error {
	return &AssignmentAuthenticationError{Step: step, Cause: err}
}

// normalizeRecoveryJSON invokes the public pure preparation API only. No Apply,
// tracker, journal, SQL or receipt is involved; field/mutation size limits and
// duplicate-key rejection are exactly those of the stored Provenance payload.
func normalizeRecoveryJSON(data []byte) ([]byte, error) {
	prepared, err := provenance.Canonicalize(provenance.OperationInput{Effects: []provenance.Effect{{
		Sort: provenance.EffectEvidence, EvidenceKind: assignmentCommandEvidenceKind, ContentDigest: []byte{1}, Payload: data,
	}}})
	if err != nil {
		return nil, err
	}
	effects := prepared.NormalizedEffects()
	if len(effects) != 1 {
		return nil, fmt.Errorf("pure normalization did not return one evidence effect")
	}
	return effects[0].Payload, nil
}

func decodeRecoveryJSON(data []byte, value any) error {
	normalized, err := normalizeRecoveryJSON(data)
	if err != nil {
		return err
	}
	if !bytes.Equal(normalized, data) {
		return fmt.Errorf("stored recovery JSON is not Provenance-normalized")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON after recovery payload")
	}
	encoded, err := canonicalJSON(value)
	if err != nil {
		return err
	}
	typedNormalized, err := normalizeRecoveryJSON(encoded)
	if err != nil {
		return err
	}
	if !bytes.Equal(typedNormalized, data) {
		return fmt.Errorf("recovery payload omits required fields or uses unsupported null/number forms")
	}
	return nil
}

type recoveryMember struct {
	Assignment provenance.AssignmentID
	Task       provenance.TaskID
	Actor      provenance.ActorID
	Producer   provenance.OperationID
}

// commandRecoveryBinding validates closed nested command types without running
// service replay, mutable eligibility, or receipt reconstruction. Repository
// arrays remain in caller order; only membership validation uses maps.
// A nil operation reconstructs closed producer bytes for digest authentication.
// The row-authentication caller supplies its public operation to additionally
// bind deterministic replacement/member identities. Neither stage reads a store.
func commandRecoveryBinding(record *assignmentCommandRecord, operation *provenance.OperationID) (AssignmentRole, []recoveryMember, error) {
	var typed any
	var parent provenance.TaskID
	var assignment provenance.AssignmentID
	var expectedParent, childRole AssignmentRole
	var handle, suffix string
	var subject ReviewSubjectRef
	var kind SubjectKind
	var validation error
	validRepositories := func(items []RepositoryCandidate) error {
		if len(items) == 0 {
			return fmt.Errorf("integration command has no repositories")
		}
		repos := map[RepositoryID]bool{}
		candidates := map[ImplementationCandidateID]bool{}
		for _, item := range items {
			if err := validateRepositoryID(item.Repository); err != nil {
				return err
			}
			if err := validateGitOID(item.Commit); err != nil {
				return err
			}
			if _, err := provenance.ParseTaskID(string(item.Candidate)); err != nil {
				return err
			}
			if repos[item.Repository] || candidates[item.Candidate] {
				return fmt.Errorf("duplicate integration repository or candidate")
			}
			repos[item.Repository] = true
			candidates[item.Candidate] = true
		}
		return nil
	}
	switch record.Mutation {
	case MutationCreateSlice:
		p := struct {
			Plan       provenance.TaskID       `json:"plan"`
			Assignment provenance.AssignmentID `json:"assignment"`
		}{}
		if err := decodeRecoveryJSON(record.Payload, &p); err != nil {
			return 0, nil, err
		}
		typed = p
		parent = p.Plan
		assignment = p.Assignment
		expectedParent = RoleGoverningSupervisor
		childRole = RoleOwnerResponsibility
		handle = "slice"
		suffix = "-slice-owner"
	case MutationSetSliceCandidate:
		p := struct {
			Slice      provenance.TaskID       `json:"slice"`
			Repository RepositoryID            `json:"repository"`
			Commit     provenance.GitOID       `json:"commit"`
			Assignment provenance.AssignmentID `json:"assignment"`
		}{}
		if err := decodeRecoveryJSON(record.Payload, &p); err != nil {
			return 0, nil, err
		}
		validation = validateRepositoryID(p.Repository)
		if validation == nil {
			validation = validateGitOID(p.Commit)
		}
		typed = p
		parent = p.Slice
		assignment = p.Assignment
		expectedParent = RoleOwnerResponsibility
		childRole = RoleOwnerResponsibility
		handle = "slice-candidate"
		suffix = "-candidate-owner"
	case MutationReworkSlice:
		p := struct {
			Slice            provenance.TaskID         `json:"slice"`
			Candidate        ImplementationCandidateID `json:"candidate"`
			Replacement      ImplementationCandidateID `json:"replacement"`
			Assignment       provenance.AssignmentID   `json:"assignment"`
			ReplacementValue SliceCandidateReplacement `json:"replacement_value"`
			Rework           ReworkSubmission          `json:"rework"`
		}{}
		if err := decodeRecoveryJSON(record.Payload, &p); err != nil {
			return 0, nil, err
		}
		handle = "slice-candidate-replacement"
		suffix = "-candidate-owner"
		if operation != nil && string(p.Replacement) != deterministicTask(*operation, handle).String() {
			return 0, nil, fmt.Errorf("wrong replacement identity")
		}
		if _, err := provenance.ParseTaskID(string(p.Candidate)); err != nil {
			return 0, nil, err
		}
		validation = validateRepositoryID(p.ReplacementValue.Repository)
		if validation == nil {
			validation = validateGitOID(p.ReplacementValue.Commit)
		}
		if validation == nil {
			validation = validateReworkSubmission(p.Rework)
		}
		typed = p
		parent = p.Slice
		assignment = p.Assignment
		expectedParent = RoleOwnerResponsibility
		childRole = RoleOwnerResponsibility
	case MutationCreateIntegrationCandidate:
		p := struct {
			Plan         provenance.TaskID       `json:"plan"`
			Repositories []RepositoryCandidate   `json:"repositories"`
			Assignment   provenance.AssignmentID `json:"assignment"`
		}{}
		if err := decodeRecoveryJSON(record.Payload, &p); err != nil {
			return 0, nil, err
		}
		validation = validRepositories(p.Repositories)
		typed = p
		parent = p.Plan
		assignment = p.Assignment
		expectedParent = RoleGoverningSupervisor
		childRole = RoleGoverningSupervisor
		handle = "integration-candidate"
		suffix = "-candidate-owner"
	case MutationReworkIntegrationCandidate:
		p := struct {
			Candidate        IntegrationCandidateSetID       `json:"candidate"`
			Replacement      IntegrationCandidateSetID       `json:"replacement"`
			Assignment       provenance.AssignmentID         `json:"assignment"`
			ReplacementValue IntegrationCandidateReplacement `json:"replacement_value"`
			Rework           ReworkSubmission                `json:"rework"`
		}{}
		if err := decodeRecoveryJSON(record.Payload, &p); err != nil {
			return 0, nil, err
		}
		handle = "integration-candidate-replacement"
		suffix = "-candidate-owner"
		if operation != nil && string(p.Replacement) != deterministicTask(*operation, handle).String() {
			return 0, nil, fmt.Errorf("wrong replacement identity")
		}
		var err error
		_, err = provenance.ParseTaskID(string(p.Candidate))
		if err != nil {
			return 0, nil, err
		}
		// New integration replacements are plan-governed. The old candidate is
		// a separately validated payload identity, not the allocation parent.
		parent = record.Task
		validation = validRepositories(p.ReplacementValue.Repositories)
		if validation == nil {
			validation = validateReworkSubmission(p.Rework)
		}
		typed = p
		assignment = p.Assignment
		expectedParent = RoleGoverningSupervisor
		childRole = RoleGoverningSupervisor
	case MutationStartReview:
		p := struct {
			Subject ReviewSubjectRef `json:"subject"`
			Kind    SubjectKind      `json:"kind"`
		}{}
		if err := decodeRecoveryJSON(record.Payload, &p); err != nil {
			return 0, nil, err
		}
		typed = p
		parent = record.Task
		assignment = record.Assignment
		expectedParent = RoleGoverningSupervisor
		childRole = RoleAxisReviewer
		subject = p.Subject
		kind = p.Kind
		if err := subject.validate(); err != nil {
			return 0, nil, err
		}
		if !kind.valid() {
			return 0, nil, fmt.Errorf("invalid review command kind")
		}
		if (subject.Kind == ReviewSubjectDocumentRevision && kind != SubjectPlan) || (subject.Kind == ReviewSubjectImplementationCandidate && kind != SubjectImplementation) {
			return 0, nil, fmt.Errorf("review subject and kind disagree")
		}
	default:
		return 0, nil, fmt.Errorf("unsupported composed assignment mutation %d", record.Mutation)
	}
	if validation != nil {
		return 0, nil, validation
	}
	if parent != record.Task || assignment != record.Assignment || record.Role != expectedParent {
		return 0, nil, fmt.Errorf("command parent task, assignment or role mismatch")
	}
	if _, err := epochTaskID(record.Epoch); err != nil {
		return 0, nil, err
	}
	request, err := assignmentRequestCommand(record.Mutation, record.Epoch, typed)
	if err != nil {
		return 0, nil, err
	}
	var requestEnvelope struct {
		Mutation EpochMutationKind `json:"mutation"`
		Epoch    EpochRootID       `json:"epoch"`
		Payload  json.RawMessage   `json:"payload"`
	}
	if err := decodeRecoveryJSON(record.Request, &requestEnvelope); err != nil {
		return 0, nil, err
	}
	normalizedRequest, err := normalizeRecoveryJSON(request)
	if err != nil {
		return 0, nil, err
	}
	if !bytes.Equal(normalizedRequest, record.Request) {
		return 0, nil, fmt.Errorf("nested command request does not match typed payload")
	}
	record.Payload, err = canonicalJSON(typed)
	if err != nil {
		return 0, nil, err
	}
	record.Request = request
	if operation == nil {
		return childRole, nil, nil
	}
	if record.Mutation != MutationStartReview {
		return childRole, []recoveryMember{
			{
				Assignment: provenance.AssignmentID(string(*operation) + suffix),
				Task:       deterministicTask(*operation, handle),
				Actor:      record.Occupant,
				Producer:   *operation,
			},
		}, nil
	}
	subjectTask, err := provenance.ParseTaskID(subject.SnapshotID)
	if err != nil {
		return 0, nil, err
	}
	plan, err := PlanReviewRound(subjectTask, subject, kind)
	if err != nil {
		return 0, nil, err
	}
	var members []recoveryMember
	for _, task := range plan.Tasks {
		members = append(
			members,
			recoveryMember{
				Assignment: provenance.AssignmentID(string(*operation) + "-" + task.Handle),
				Task:       deterministicTask(*operation, task.Handle),
				Actor:      record.Occupant,
				Producer:   *operation,
			},
		)
	}
	return childRole, members, nil
}

func decodeRecoveryCommand(row provenance.EvidenceRow) (assignmentCommandRecord, error) {
	var record assignmentCommandRecord
	if err := decodeRecoveryJSON(row.Payload, &record); err != nil {
		return record, err
	}
	if _, _, err := commandRecoveryBinding(&record, nil); err != nil {
		return record, err
	}
	producerBytes, err := canonicalJSON(record)
	if err != nil {
		return record, err
	}
	digest := sha256.Sum256(producerBytes)
	if !bytes.Equal(digest[:], row.ContentDigest) {
		return record, fmt.Errorf("command evidence content digest mismatch")
	}
	normalized, err := normalizeRecoveryJSON(producerBytes)
	if err != nil {
		return record, err
	}
	if !bytes.Equal(normalized, row.Payload) {
		return record, fmt.Errorf("normalized producer command differs from stored evidence")
	}
	if row.TaskID == nil || *row.TaskID != record.Task || record.Task == (provenance.TaskID{}) || record.Assignment == "" || record.Authority <= 0 || record.Occupant == (provenance.ActorID{}) || row.EffectiveActorID != record.Occupant {
		return record, fmt.Errorf("command evidence has inconsistent task, authority, assignment or actor")
	}
	// Return the stored closed record so later binding decodes normalized nested
	// values again; producer-order RawMessages are not mistaken for stored bytes.
	if err := decodeRecoveryJSON(row.Payload, &record); err != nil {
		return record, err
	}
	return record, nil
}

// assignmentRecoveryProof is what one authenticated page established: the
// episodes it proved, and the review members its commands declared. Rows are in
// journal order, so a transfer successor always follows the predecessor it names.
type assignmentRecoveryProof struct {
	Rows    []startedEpisode
	Members []recoveryMember
}

type recoveryMaterialKey struct {
	Task       provenance.TaskID
	Assignment provenance.AssignmentID
}
type recoveryMaterial struct {
	Row     provenance.TaskEventRow
	Payload assignmentStartPayload
	Actor   provenance.ActorID
	Role    AssignmentRole
}
type recoveryEvidenceKey struct {
	Operation provenance.OperationID
	Kind      provenance.EvidenceKind
}

func decodeRecoveryMaterials(rows []provenance.TaskEventRow, snapshot provenance.JournalID) (map[recoveryMaterialKey]recoveryMaterial, error) {
	result := map[recoveryMaterialKey]recoveryMaterial{}
	for _, row := range rows {
		if _, err := normalizeRecoveryJSON(row.Payload); err != nil {
			return nil, err
		}
		payload, err := decodeAssignmentStart(row.Payload)
		if err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(row.Payload, &fields); err != nil {
			return nil, err
		}
		if _, present := fields["authorityJournalId"]; present && payload.AuthorityJournalID <= 0 {
			return nil, fmt.Errorf("present material authority must be positive")
		}
		if payload.Assignment == "" || payload.AuthorityJournalID < 0 || row.ActorID == (provenance.ActorID{}) || row.ProducedByOperationJournalID == nil || *row.ProducedByOperationJournalID <= 0 || *row.ProducedByOperationJournalID > snapshot {
			return nil, fmt.Errorf("assignment material %d has invalid identity or producer", row.JournalID)
		}
		actor, err := provenance.ParseActorID(payload.Occupant)
		if err != nil || actor == (provenance.ActorID{}) {
			return nil, fmt.Errorf("assignment material %d has invalid occupant", row.JournalID)
		}
		var role AssignmentRole
		for _, candidate := range []AssignmentRole{RoleOwnerResponsibility, RoleGoverningSupervisor, RoleAxisReviewer} {
			if candidate.String() == payload.Role {
				role = candidate
			}
		}
		if !role.valid() {
			return nil, fmt.Errorf("assignment material %d has unknown role", row.JournalID)
		}
		key := recoveryMaterialKey{row.TaskID, provenance.AssignmentID(payload.Assignment)}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate assignment material for %s/%s", row.TaskID, payload.Assignment)
		}
		result[key] = recoveryMaterial{row, payload, actor, role}
	}
	return result, nil
}

func validateRecoveryEvidence(rows []provenance.EvidenceRow, snapshot provenance.JournalID) (map[recoveryEvidenceKey]provenance.EvidenceRow, error) {
	result := map[recoveryEvidenceKey]provenance.EvidenceRow{}
	for _, row := range rows {
		if row.ProducingOperationID == "" || row.ProducingOperationJournalID <= 0 || row.ProducingOperationJournalID > snapshot || row.EffectiveActorID == (provenance.ActorID{}) || row.TaskID == nil {
			return nil, fmt.Errorf("invalid recovery evidence identity or producer")
		}
		key := recoveryEvidenceKey{row.ProducingOperationID, row.EvidenceKind}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate evidence for %s/%s", row.ProducingOperationID, row.EvidenceKind)
		}
		switch row.EvidenceKind {
		case assignmentCommandEvidenceKind:
			if _, err := decodeRecoveryCommand(row); err != nil {
				return nil, err
			}
		case reviewRoundAuthorityEvidenceKind:
			var value reviewRoundAuthority
			if err := decodeRecoveryJSON(row.Payload, &value); err != nil {
				return nil, err
			}
			if err := validateReviewRoundAuthority(value, *row.TaskID); err != nil {
				return nil, err
			}
			producerBytes, err := canonicalJSON(value)
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(producerBytes)
			if !bytes.Equal(digest[:], row.ContentDigest) {
				return nil, fmt.Errorf("review evidence digest mismatch")
			}
		default:
			return nil, fmt.Errorf("unexpected recovery evidence kind")
		}
		result[key] = row
	}
	return result, nil
}

func authenticateRecoveryRows(ctx context.Context, journal provenance.Journal, page provenance.AssignmentStartPage, materials []provenance.TaskEventRow, evidence []provenance.EvidenceRow, predecessors map[provenance.AssignmentID]startedEpisode, authenticatedThrough provenance.JournalID) (assignmentRecoveryProof, error) {
	var proof assignmentRecoveryProof
	material, err := decodeRecoveryMaterials(materials, page.SnapshotMaxJournalID)
	if err != nil {
		return proof, authenticationFault("material decoding", err)
	}
	evidences, err := validateRecoveryEvidence(evidence, page.SnapshotMaxJournalID)
	if err != nil {
		return proof, authenticationFault("evidence decoding", err)
	}
	for _, row := range page.Rows {
		if err := ctx.Err(); err != nil {
			return proof, authenticationFault("proof cancellation", err)
		}
		if row.AuthorityJournalID <= 0 || row.AuthorityJournalID > page.SnapshotMaxJournalID || row.ProducingOperationJournalID <= 0 || row.ProducingOperationJournalID > page.SnapshotMaxJournalID || row.AssignmentID == "" || row.TaskID == (provenance.TaskID{}) || row.Occupant == (provenance.ActorID{}) || row.SlotID != provenance.SlotOwnerResponsibility {
			return proof, authenticationFault("assignment row", fmt.Errorf("unusable assignment start"))
		}
		m, present := material[recoveryMaterialKey{row.TaskID, row.AssignmentID}]
		if present && (m.Actor != row.Occupant || (m.Payload.AuthorityJournalID != 0 && provenance.JournalID(m.Payload.AuthorityJournalID) != row.AuthorityJournalID)) {
			return proof, authenticationFault("material binding", fmt.Errorf("occupant or authority mismatch for %s", row.AssignmentID))
		}
		var role AssignmentRole
		switch {
		case row.PredecessorAssignmentID != nil:
			// A transfer successor inherits the task from the episode it names.
			// EVERY predecessor in the authenticated page is checked; the page
			// size is the only bound on how many that is.
			previous, ok := predecessors[*row.PredecessorAssignmentID]
			if row.ParentAssignmentID != nil || !ok || previous.Task != row.TaskID || previous.Role != RoleOwnerResponsibility || previous.Authority > authenticatedThrough || (present && m.Role != RoleOwnerResponsibility) {
				return proof, authenticationFault("transfer predecessor", fmt.Errorf("no authenticated owner predecessor for %s", row.AssignmentID))
			}
			governs, err := journal.AuthorityGovernsTaskAt(previous.Authority, row.TaskID, row.ProducingOperationJournalID)
			if err != nil || !governs {
				return proof, authenticationFault(
					"transfer predecessor governance",
					fmt.Errorf("predecessor does not govern at producer boundary: %v", err),
				)
			}
			role = RoleOwnerResponsibility
		case present && *m.Row.ProducedByOperationJournalID == row.ProducingOperationJournalID:
			role = m.Role // The producer actor need not be the assigned occupant.
		default:
			supplement := provenance.GovernedAllocationSupplementOperationID(row.ProducingOperationID)
			e, ok := evidences[recoveryEvidenceKey{supplement, assignmentCommandEvidenceKind}]
			if !ok {
				return proof, authenticationFault("composed producer bridge", fmt.Errorf("missing exact command evidence for %s", row.AssignmentID))
			}
			record, err := decodeRecoveryCommand(e)
			if err != nil {
				return proof, authenticationFault("command decoding", err)
			}
			if row.ParentAssignmentID == nil || *row.ParentAssignmentID != record.Assignment || record.Occupant != row.Occupant || record.Authority > row.ProducingOperationJournalID || (present && (m.Row.ActorID != record.Occupant || *m.Row.ProducedByOperationJournalID != e.ProducingOperationJournalID)) {
				return proof, authenticationFault("composed command binding", fmt.Errorf("parent, actor, authority or material producer mismatch"))
			}
			var members []recoveryMember
			role, members, err = commandRecoveryBinding(&record, &row.ProducingOperationID)
			if err != nil {
				return proof, authenticationFault("composed mutation binding", err)
			}
			matched := false
			for _, member := range members {
				if member.Assignment == row.AssignmentID && member.Task == row.TaskID && member.Actor == row.Occupant {
					matched = true
				}
			}
			if !matched || (present && m.Role != role) {
				return proof, authenticationFault("composed child binding", fmt.Errorf("assignment is not a declared child in its declared role"))
			}
			if record.Mutation != MutationStartReview {
				if !present {
					return proof, authenticationFault("ordinary composed material", fmt.Errorf("missing material for non-review assignment"))
				}
			} else {
				review, ok := evidences[recoveryEvidenceKey{supplement, reviewRoundAuthorityEvidenceKind}]
				if !ok || review.ProducingOperationJournalID != e.ProducingOperationJournalID || review.EffectiveActorID != record.Occupant {
					return proof, authenticationFault("review producer bridge", fmt.Errorf("missing or mismatched started review evidence"))
				}
				var value reviewRoundAuthority
				_ = json.Unmarshal(review.Payload, &value)
				var payload struct {
					Subject ReviewSubjectRef `json:"subject"`
					Kind    SubjectKind      `json:"kind"`
				}
				_ = json.Unmarshal(record.Payload, &payload)
				if value.State != reviewRoundStarted || value.Operation != row.ProducingOperationID || value.Epoch != record.Epoch || value.Subject.String() != payload.Subject.SnapshotID || value.Kind != payload.Kind || string(value.Round) != deterministicTask(row.ProducingOperationID, "review-round").String() {
					return proof, authenticationFault("review shape", fmt.Errorf("review evidence does not bind the command and deterministic round"))
				}
				for _, axis := range value.Graph {
					if axis.Task != deterministicTask(row.ProducingOperationID, "axis-"+axis.Axis.String()) {
						return proof, authenticationFault("review graph", fmt.Errorf("axis task is not deterministic"))
					}
					if value.Kind == SubjectImplementation {
						for _, group := range axis.Groups {
							if group.Task != deterministicTask(row.ProducingOperationID, "axis-"+axis.Axis.String()+".group-"+group.Severity.String()) {
								return proof, authenticationFault("review graph", fmt.Errorf("severity task is not deterministic"))
							}
						}
					}
				}
				proof.Members = append(proof.Members, members...)
			}
		}
		proof.Rows = append(
			proof.Rows,
			startedEpisode{
				Assignment: row.AssignmentID,
				Task:       row.TaskID,
				Actor:      row.Occupant,
				Role:       role,
				Authority:  row.AuthorityJournalID,
			},
		)
		predecessors[row.AssignmentID] = proof.Rows[len(proof.Rows)-1]
	}
	return proof, nil
}

// authenticateAssignmentStarts is the write path's only authentication driver.
// It pages the material the named starts describe, pages the command evidence
// of the starts that need it, and authenticates the page.
//
// starts must be in journal order and belong to ONE task. The material read
// begins at the earliest authority in the page: every material fact describing
// a start is written by the operation that produced that start, so it is always
// above that start's own authority, and reading from the earliest authority
// finds all of them without reading anything older.
//
// snapshot is the journal ceiling for the read. Zero means "pin on the first
// page", which is what a caller that has not read a pinned maximum wants; a
// positive value pins every page there.
func authenticateAssignmentStarts(
	ctx context.Context,
	journal provenance.Journal,
	starts []provenance.AssignmentStartRow,
	snapshot provenance.JournalID,
) (assignmentRecoveryProof, error) {
	var proof assignmentRecoveryProof
	refuse := func(problem string) error {
		return assignmentErr("exactCandidateParentAuthority", problem,
			"a command parent must match an exact public assignment start and authenticated material, not a neighboring journal row",
			"supply the exact active assignment; repair inconsistent history or request an explicitly reviewed larger command proof budget")
	}
	if len(starts) == 0 {
		return proof, refuse("the authenticated start page is empty")
	}
	task := starts[0].TaskID
	after := starts[0].AuthorityJournalID
	for _, start := range starts {
		if start.TaskID != task {
			return proof, refuse("the authenticated start page spans more than one task")
		}
		if start.AuthorityJournalID < after {
			after = start.AuthorityJournalID
		}
	}
	if err := ctx.Err(); err != nil {
		return proof, err
	}
	var material []provenance.TaskEventRow
	through := snapshot
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber == maxParentProofPages {
			return proof, refuse("parent material history exceeds the bounded command proof budget")
		}
		if err := ctx.Err(); err != nil {
			return proof, err
		}
		page, err := journal.QueryTaskEvents(provenance.JournalQueryV1{
			OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{task},
			EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: maxParentProofPageRows,
			AfterJournalID: after, SnapshotMaxJournalID: through,
		})
		if err != nil {
			return proof, err
		}
		through = page.SnapshotMaxJournalID
		material = append(material, page.Events...)
		if page.Next == nil {
			break
		}
		if page.Next.AfterJournalID <= after {
			return proof, refuse("the parent material cursor made no progress")
		}
		after = page.Next.AfterJournalID
	}
	decoded, err := decodeRecoveryMaterials(material, through)
	if err != nil {
		return proof, err
	}
	var operations []provenance.OperationID
	seen := map[provenance.OperationID]bool{}
	for _, start := range starts {
		fact, present := decoded[recoveryMaterialKey{start.TaskID, start.AssignmentID}]
		if start.PredecessorAssignmentID != nil || (present && *fact.Row.ProducedByOperationJournalID == start.ProducingOperationJournalID) {
			continue
		}
		operation := provenance.GovernedAllocationSupplementOperationID(start.ProducingOperationID)
		if !seen[operation] {
			seen[operation] = true
			operations = append(operations, operation)
		}
	}
	if len(operations) > provenance.MaxFactFilterValues {
		return proof, refuse("parent supplement population exceeds the public filter bound")
	}
	var evidence []provenance.EvidenceRow
	if len(operations) > 0 {
		query := provenance.EvidenceQuery{
			Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: operations},
			Kinds:  []provenance.EvidenceKind{assignmentCommandEvidenceKind, reviewRoundAuthorityEvidenceKind},
			Page:   provenance.FactPageRequest{Limit: maxParentProofPageRows, SnapshotMaxJournalID: through},
		}
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber == maxParentProofPages {
				return proof, refuse("parent evidence exceeds the bounded command proof budget")
			}
			if err := ctx.Err(); err != nil {
				return proof, err
			}
			page, err := journal.Facts().QueryEvidence(query)
			if err != nil {
				return proof, err
			}
			evidence = append(evidence, page.Rows...)
			if page.Next == nil {
				break
			}
			if page.Next.AfterJournalID <= query.Page.AfterJournalID {
				return proof, refuse("the parent evidence cursor made no progress")
			}
			query.Page.AfterJournalID = page.Next.AfterJournalID
		}
	}
	return authenticateRecoveryRows(ctx, journal, provenance.AssignmentStartPage{
		Rows: starts, SnapshotPinned: true, SnapshotMaxJournalID: through,
	}, material, evidence, map[provenance.AssignmentID]startedEpisode{}, through)
}
