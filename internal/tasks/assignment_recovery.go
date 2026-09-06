package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

func decodeRecoveryJSON(data []byte, value any) error {
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
	if !bytes.Equal(encoded, data) {
		return fmt.Errorf("recovery payload is not the producer's canonical encoding")
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
func commandRecoveryBinding(record assignmentCommandRecord, operation provenance.OperationID) (AssignmentRole, []recoveryMember, error) {
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
		if string(p.Replacement) != deterministicTask(operation, handle).String() {
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
		if string(p.Replacement) != deterministicTask(operation, handle).String() {
			return 0, nil, fmt.Errorf("wrong replacement identity")
		}
		var err error
		parent, err = provenance.ParseTaskID(string(p.Candidate))
		if err != nil {
			return 0, nil, err
		}
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
	if !bytes.Equal(request, record.Request) {
		return 0, nil, fmt.Errorf("nested command request does not match typed payload")
	}
	if record.Mutation != MutationStartReview {
		return childRole, []recoveryMember{{Assignment: provenance.AssignmentID(string(operation) + suffix), Task: deterministicTask(operation, handle), Actor: record.Occupant, Producer: operation}}, nil
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
		members = append(members, recoveryMember{Assignment: provenance.AssignmentID(string(operation) + "-" + task.Handle), Task: deterministicTask(operation, task.Handle), Actor: record.Occupant, Producer: operation})
	}
	return childRole, members, nil
}

func decodeRecoveryCommand(row provenance.EvidenceRow) (assignmentCommandRecord, error) {
	var record assignmentCommandRecord
	if err := decodeRecoveryJSON(row.Payload, &record); err != nil {
		return record, err
	}
	digest := sha256.Sum256(row.Payload)
	if !bytes.Equal(digest[:], row.ContentDigest) {
		return record, fmt.Errorf("command evidence content digest mismatch")
	}
	if row.TaskID == nil || *row.TaskID != record.Task || record.Task == (provenance.TaskID{}) || record.Assignment == "" || record.Authority <= 0 || record.Occupant == (provenance.ActorID{}) || row.EffectiveActorID != record.Occupant {
		return record, fmt.Errorf("command evidence has inconsistent task, authority, assignment or actor")
	}
	return record, nil
}

type assignmentRecoveryProof struct {
	Rows                 []startedEpisode
	Members              []recoveryMember
	HistoricalPredicates int
}

func applyRecoveryPrefixTx(ctx context.Context, tx *sql.Tx, state assignmentRecoveryState, proof assignmentRecoveryProof, operator bool) (bool, error) {
	seen := map[provenance.AssignmentID]startedEpisode{}
	for _, row := range proof.Rows {
		if err := row.validate("applyRecoveryPrefixTx"); err != nil {
			return true, err
		}
		if old, ok := seen[row.Assignment]; ok && old != row {
			return true, fmt.Errorf("contradictory assignment in consumed prefix")
		}
		seen[row.Assignment] = row
		existing, found, err := readStartedEpisodeTx(ctx, tx, state.Generation, row.Assignment)
		if err != nil {
			return true, err
		}
		if found && existing != row {
			return true, fmt.Errorf("consumed prefix conflicts with existing assignment %q", row.Assignment)
		}
	}
	// Validate all member identities before any mutation, including member-cache
	// conflicts on resumed pages. Count unique member identities, not increments.
	for _, member := range proof.Members {
		if !operator {
			row, ok := seen[member.Assignment]
			if !ok || row.Task != member.Task || row.Actor != member.Actor || row.Role != RoleAxisReviewer {
				return false, fmt.Errorf("bounded gate page leaves pending review members")
			}
			continue
		}
		var task, actor string
		err := tx.QueryRowContext(ctx, `SELECT task_id,actor_id FROM pasture_assignment_recovery_member
		 WHERE generation=? AND snapshot_jid=? AND producer=? AND assignment_id=?`, state.Generation, state.Snapshot, string(member.Producer), string(member.Assignment)).Scan(&task, &actor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil && (task != member.Task.String() || actor != member.Actor.String()) {
			return true, fmt.Errorf("review member cache conflicts with authenticated member")
		}
	}
	for _, row := range proof.Rows {
		_, err := tx.ExecContext(ctx, `INSERT INTO pasture_actor_assignment(assignment_id,actor_id,task_id,role,authority_journal_id,generation)
		 VALUES(?,?,?,?,?,?) ON CONFLICT(assignment_id) DO NOTHING`, string(row.Assignment), row.Actor.String(), row.Task.String(), row.Role.String(), row.Authority, state.Generation)
		if err != nil {
			return false, err
		}
	}
	if operator {
		for _, member := range proof.Members {
			_, err := tx.ExecContext(ctx, `INSERT INTO pasture_assignment_recovery_member(generation,snapshot_jid,producer,assignment_id,task_id,actor_id,seen)
			 VALUES(?,?,?,?,?,?,0) ON CONFLICT(generation,snapshot_jid,producer,assignment_id) DO NOTHING`, state.Generation, state.Snapshot, string(member.Producer), string(member.Assignment), member.Task.String(), member.Actor.String())
			if err != nil {
				return false, err
			}
		}
		for _, row := range proof.Rows {
			_, err := tx.ExecContext(ctx, `UPDATE pasture_assignment_recovery_member SET seen=1
			 WHERE generation=? AND snapshot_jid=? AND assignment_id=? AND task_id=? AND actor_id=?`, state.Generation, state.Snapshot, string(row.Assignment), row.Task.String(), row.Actor.String())
			if err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

func publishAssignmentRecoveryPage(ctx context.Context, db *sql.DB, scan assignmentRecoveryScan, proof assignmentRecoveryProof) (assignmentRecoveryState, error) {
	state := scan.State
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return state, recoveryFault("prefix transaction", err)
	}
	defer tx.Rollback()
	current, err := readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return state, recoveryFault("prefix state read", err)
	}
	if current != state || state.Status == assignmentCoverageDirty || state.Snapshot != scan.Page.SnapshotMaxJournalID {
		return state, recoveryFault("prefix compare-and-swap", fmt.Errorf("captured state or snapshot changed"))
	}
	conflict, err := applyRecoveryPrefixTx(ctx, tx, state, proof, true)
	if conflict {
		return state, commitAssignmentDirtyTx(ctx, tx, state, err.Error())
	}
	if err != nil {
		return state, recoveryFault("prefix application", err)
	}
	after := scan.Page.SnapshotMaxJournalID
	complete := scan.Page.Next == nil
	if !complete {
		after = scan.Page.Next.AfterJournalID
	} else {
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pasture_assignment_recovery_member WHERE generation=? AND snapshot_jid=? AND seen=0`, state.Generation, state.Snapshot).Scan(&pending); err != nil {
			return state, recoveryFault("terminal member check", err)
		}
		if pending != 0 {
			return state, recoveryFault("terminal member check", fmt.Errorf("%d authenticated review members were not seen", pending))
		}
		var bad int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT producer,COUNT(*) AS n FROM pasture_assignment_recovery_member WHERE generation=? AND snapshot_jid=? GROUP BY producer HAVING n NOT IN (4,13))`, state.Generation, state.Snapshot).Scan(&bad); err != nil {
			return state, recoveryFault("terminal batch count", err)
		}
		if bad != 0 {
			return state, recoveryFault("terminal batch count", fmt.Errorf("review batch lacks exact four or thirteen members"))
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE pasture_assignment_recovery_scan SET after_jid=?,complete=?
	 WHERE generation=? AND snapshot_jid=? AND kind='assignment' AND identity='all' AND after_jid=? AND complete=0`, after, complete, state.Generation, state.Snapshot, state.After)
	if err != nil {
		return state, recoveryFault("assignment cursor update", err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return state, recoveryFault("assignment cursor guard", fmt.Errorf("updated %d scan rows: %v", n, err))
	}
	if complete {
		err = updateRecoveryStateTx(ctx, tx, state, `coverage_status='valid',completed_through_jid=?,in_progress_snapshot_jid=0,in_progress_after_jid=0,recovery_complete=1,certified_destructive_revision=destructive_revision,state_revision=state_revision+1`, state.Snapshot)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO pasture_actor_assignment_watermark(singleton_id,last_indexed_jid) VALUES(0,?) ON CONFLICT(singleton_id) DO UPDATE SET last_indexed_jid=excluded.last_indexed_jid`, state.Snapshot)
		}
	} else {
		err = updateRecoveryStateTx(ctx, tx, state, `in_progress_after_jid=?,state_revision=state_revision+1`, after)
	}
	if err != nil {
		return state, recoveryFault("prefix state publication", err)
	}
	next, err := readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return state, recoveryFault("prefix state readback", err)
	}
	if err := tx.Commit(); err != nil {
		return state, recoveryFault("prefix commit", err)
	}
	return next, nil
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
		payload, err := decodeAssignmentStart(row.Payload)
		if err != nil {
			return nil, err
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
			digest := sha256.Sum256(row.Payload)
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

func authenticateRecoveryRows(ctx context.Context, journal provenance.Journal, page provenance.AssignmentStartPage, materials []provenance.TaskEventRow, evidence []provenance.EvidenceRow, predecessors map[provenance.AssignmentID]startedEpisode, certifiedThrough provenance.JournalID) (assignmentRecoveryProof, error) {
	var proof assignmentRecoveryProof
	material, err := decodeRecoveryMaterials(materials, page.SnapshotMaxJournalID)
	if err != nil {
		return proof, recoveryFault("material decoding", err)
	}
	evidences, err := validateRecoveryEvidence(evidence, page.SnapshotMaxJournalID)
	if err != nil {
		return proof, recoveryFault("evidence decoding", err)
	}
	governance := 0
	for _, row := range page.Rows {
		if err := ctx.Err(); err != nil {
			return proof, recoveryFault("proof cancellation", err)
		}
		if row.AuthorityJournalID <= 0 || row.AuthorityJournalID > page.SnapshotMaxJournalID || row.ProducingOperationJournalID <= 0 || row.ProducingOperationJournalID > page.SnapshotMaxJournalID || row.AssignmentID == "" || row.TaskID == (provenance.TaskID{}) || row.Occupant == (provenance.ActorID{}) || row.SlotID != provenance.SlotOwnerResponsibility {
			return proof, recoveryFault("assignment row", fmt.Errorf("unusable assignment start"))
		}
		m, present := material[recoveryMaterialKey{row.TaskID, row.AssignmentID}]
		if present && (m.Actor != row.Occupant || (m.Payload.AuthorityJournalID != 0 && provenance.JournalID(m.Payload.AuthorityJournalID) != row.AuthorityJournalID)) {
			return proof, recoveryFault("material binding", fmt.Errorf("occupant or authority mismatch for %s", row.AssignmentID))
		}
		var role AssignmentRole
		switch {
		case row.PredecessorAssignmentID != nil:
			previous, ok := predecessors[*row.PredecessorAssignmentID]
			if row.ParentAssignmentID != nil || !ok || previous.Task != row.TaskID || previous.Role != RoleOwnerResponsibility || previous.Authority > certifiedThrough || (present && m.Role != RoleOwnerResponsibility) {
				return proof, recoveryFault("transfer predecessor", fmt.Errorf("no certified owner predecessor for %s", row.AssignmentID))
			}
			governance++
			if governance > 64 {
				return proof, recoveryFault("transfer governance budget", fmt.Errorf("historical predicate budget exhausted"))
			}
			proof.HistoricalPredicates = governance
			governs, err := journal.AuthorityGovernsTaskAt(previous.Authority, row.TaskID, row.ProducingOperationJournalID)
			if err != nil || !governs {
				return proof, recoveryFault("historical transfer predecessor", fmt.Errorf("predecessor does not govern at producer boundary: %v", err))
			}
			role = RoleOwnerResponsibility
		case present && *m.Row.ProducedByOperationJournalID == row.ProducingOperationJournalID:
			role = m.Role // The producer actor need not be the assigned occupant.
		default:
			supplement := provenance.GovernedAllocationSupplementOperationID(row.ProducingOperationID)
			e, ok := evidences[recoveryEvidenceKey{supplement, assignmentCommandEvidenceKind}]
			if !ok {
				return proof, recoveryFault("composed producer bridge", fmt.Errorf("missing exact command evidence for %s", row.AssignmentID))
			}
			record, err := decodeRecoveryCommand(e)
			if err != nil {
				return proof, recoveryFault("command decoding", err)
			}
			if row.ParentAssignmentID == nil || *row.ParentAssignmentID != record.Assignment || record.Occupant != row.Occupant || record.Authority > row.ProducingOperationJournalID || (present && (m.Row.ActorID != record.Occupant || *m.Row.ProducedByOperationJournalID != e.ProducingOperationJournalID)) {
				return proof, recoveryFault("composed command binding", fmt.Errorf("parent, actor, authority or material producer mismatch"))
			}
			var members []recoveryMember
			role, members, err = commandRecoveryBinding(record, row.ProducingOperationID)
			if err != nil {
				return proof, recoveryFault("composed mutation binding", err)
			}
			matched := false
			for _, member := range members {
				if member.Assignment == row.AssignmentID && member.Task == row.TaskID && member.Actor == row.Occupant {
					matched = true
				}
			}
			if !matched || (present && m.Role != role) {
				return proof, recoveryFault("composed child binding", fmt.Errorf("assignment is not a declared child in its declared role"))
			}
			if record.Mutation != MutationStartReview {
				if !present {
					return proof, recoveryFault("ordinary composed material", fmt.Errorf("missing material for non-review assignment"))
				}
			} else {
				review, ok := evidences[recoveryEvidenceKey{supplement, reviewRoundAuthorityEvidenceKind}]
				if !ok || review.ProducingOperationJournalID != e.ProducingOperationJournalID || review.EffectiveActorID != record.Occupant {
					return proof, recoveryFault("review producer bridge", fmt.Errorf("missing or mismatched started review evidence"))
				}
				var value reviewRoundAuthority
				_ = json.Unmarshal(review.Payload, &value)
				var payload struct {
					Subject ReviewSubjectRef `json:"subject"`
					Kind    SubjectKind      `json:"kind"`
				}
				_ = json.Unmarshal(record.Payload, &payload)
				if value.State != reviewRoundStarted || value.Operation != row.ProducingOperationID || value.Epoch != record.Epoch || value.Subject.String() != payload.Subject.SnapshotID || value.Kind != payload.Kind || string(value.Round) != deterministicTask(row.ProducingOperationID, "review-round").String() {
					return proof, recoveryFault("review shape", fmt.Errorf("review evidence does not bind the command and deterministic round"))
				}
				for _, axis := range value.Graph {
					if axis.Task != deterministicTask(row.ProducingOperationID, "axis-"+axis.Axis.String()) {
						return proof, recoveryFault("review graph", fmt.Errorf("axis task is not deterministic"))
					}
					if value.Kind == SubjectImplementation {
						for _, group := range axis.Groups {
							if group.Task != deterministicTask(row.ProducingOperationID, "axis-"+axis.Axis.String()+".group-"+group.Severity.String()) {
								return proof, recoveryFault("review graph", fmt.Errorf("severity task is not deterministic"))
							}
						}
					}
				}
				proof.Members = append(proof.Members, members...)
			}
		}
		proof.Rows = append(proof.Rows, startedEpisode{Assignment: row.AssignmentID, Task: row.TaskID, Actor: row.Occupant, Role: role, Authority: row.AuthorityJournalID})
		predecessors[row.AssignmentID] = proof.Rows[len(proof.Rows)-1]
	}
	return proof, nil
}

// AssignmentIndexRebuildOptions belongs to the operator, not hook configuration.
// PageSize may reduce the normal bound for paced maintenance; it cannot widen it.
type AssignmentIndexRebuildOptions struct {
	Reset              bool
	ExpectedGeneration *int64
	PageSize           int
}

func recoveryPredecessors(ctx context.Context, db *sql.DB, state assignmentRecoveryState, rows []provenance.AssignmentStartRow) (map[provenance.AssignmentID]startedEpisode, error) {
	result := map[provenance.AssignmentID]startedEpisode{}
	for _, row := range rows {
		if row.PredecessorAssignmentID == nil {
			continue
		}
		previous, found, err := readStartedEpisodeTx(ctx, db, state.Generation, *row.PredecessorAssignmentID)
		if err != nil {
			return nil, err
		}
		if found {
			result[previous.Assignment] = previous
		}
	}
	return result, nil
}

// RebuildAssignmentIndex is the operator production path. It never holds a
// Pasture transaction across a journal query or the whole-store integrity audit.
func RebuildAssignmentIndex(ctx context.Context, tracker protocol.TaskTracker, options AssignmentIndexRebuildOptions) error {
	t, ok := tracker.(*trackerImpl)
	if !ok || t == nil {
		return recoveryFault("operator store", fmt.Errorf("rebuild requires the unified task tracker"))
	}
	limit := options.PageSize
	if limit == 0 {
		limit = gateauthority.CatchUpPageSize
	}
	if limit < 1 || limit > gateauthority.CatchUpPageSize {
		return recoveryFault("operator page bound", fmt.Errorf("page size must be between 1 and %d", gateauthority.CatchUpPageSize))
	}
	state, err := readAssignmentRecoveryState(ctx, t.auditDB)
	if err != nil {
		return recoveryFault("operator state", err)
	}
	if options.ExpectedGeneration != nil && state.Generation != *options.ExpectedGeneration {
		return recoveryFault("operator expected generation", fmt.Errorf("generation changed from %d to %d", *options.ExpectedGeneration, state.Generation))
	}
	if options.Reset {
		if _, err := resetAssignmentRecovery(ctx, t.auditDB, state); err != nil {
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return recoveryFault("operator cancellation", err)
		}
		scan, err := beginAssignmentRecoveryScan(ctx, t.auditDB, t.Journal(), limit)
		if err != nil {
			return err
		}
		tasks := map[provenance.TaskID]bool{}
		operations := map[provenance.OperationID]bool{}
		for _, row := range scan.Page.Rows {
			tasks[row.TaskID] = true
			if row.PredecessorAssignmentID == nil {
				operations[provenance.GovernedAllocationSupplementOperationID(row.ProducingOperationID)] = true
			}
		}
		var materials []provenance.TaskEventRow
		var evidence []provenance.EvidenceRow
		for task := range tasks {
			rows, err := drainRecoveryMaterial(ctx, t.auditDB, t.Journal(), &scan.State, task)
			if err != nil {
				return recoveryFault("operator material drain", err)
			}
			materials = append(materials, rows...)
		}
		for operation := range operations {
			rows, err := drainRecoveryEvidence(ctx, t.auditDB, t.Journal(), &scan.State, operation)
			if err != nil {
				return recoveryFault("operator evidence drain", err)
			}
			evidence = append(evidence, rows...)
		}
		predecessors, err := recoveryPredecessors(ctx, t.auditDB, scan.State, scan.Page.Rows)
		if err != nil {
			return recoveryFault("operator predecessor rows", err)
		}
		// Operator prefix rows are authenticated but not yet globally certified.
		// The terminal audit and state CAS below precede any completion certificate.
		proof, err := authenticateRecoveryRows(ctx, t.Journal(), scan.Page, materials, evidence, predecessors, scan.Page.SnapshotMaxJournalID)
		if err != nil {
			return err
		}
		if err := authenticateOperatorTransfers(ctx, t, &scan.State, scan.Page.Rows, materials); err != nil {
			return err
		}
		if scan.Page.Next == nil {
			if err := ctx.Err(); err != nil {
				return recoveryFault("pre-audit cancellation", err)
			}
			if err := t.Journal().VerifyIntegrity(); err != nil {
				return recoveryFault("operator terminal audit", err)
			}
			if err := ctx.Err(); err != nil {
				return recoveryFault("post-audit cancellation", err)
			}
		}
		if _, err := publishAssignmentRecoveryPage(ctx, t.auditDB, scan, proof); err != nil {
			return err
		}
		if scan.Page.Next == nil {
			return nil
		}
	}
}

func authenticateOperatorTransfers(ctx context.Context, t *trackerImpl, state *assignmentRecoveryState, starts []provenance.AssignmentStartRow, materials []provenance.TaskEventRow) error {
	decoded, err := decodeRecoveryMaterials(materials, state.Snapshot)
	if err != nil {
		return recoveryFault("operator transfer material", err)
	}
	for _, row := range starts {
		if row.PredecessorAssignmentID == nil {
			continue
		}
		material, present := decoded[recoveryMaterialKey{row.TaskID, row.AssignmentID}]
		if !present {
			continue
		}
		identity := string(row.ProducingOperationID)
		after, complete, err := openRecoveryAux(ctx, t.auditDB, state, "transfer", identity)
		if err != nil {
			return recoveryFault("transfer scan", err)
		}
		if complete {
			cached, err := loadRecoveryCache(ctx, t.auditDB, *state, "transfer", identity)
			if err != nil {
				return recoveryFault("transfer cache", err)
			}
			if len(cached) != 1 {
				return recoveryFault("transfer cache", fmt.Errorf("transfer authentication missing"))
			}
			var anchor provenance.JournalID
			if err := json.Unmarshal(cached[0], &anchor); err != nil {
				return recoveryFault("transfer cache decode", err)
			}
			if anchor != *material.Row.ProducedByOperationJournalID || anchor > state.Snapshot {
				return recoveryFault("transfer cached anchor", fmt.Errorf("material producer differs from authenticated anchor"))
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return recoveryFault("transfer lookup cancellation", err)
		}
		committed, err := t.Journal().LookupCommitted(transferMaterialFactOperationID(row.ProducingOperationID))
		if err != nil {
			return recoveryFault("operator transfer lookup", err)
		}
		if err := ctx.Err(); err != nil {
			return recoveryFault("transfer lookup cancellation", err)
		}
		if committed.Kind != provenance.CommittedExact || committed.AnchorJournalID <= 0 || committed.AnchorJournalID > state.Snapshot || committed.AnchorJournalID != *material.Row.ProducedByOperationJournalID {
			return recoveryFault("operator transfer anchor", fmt.Errorf("transfer material anchor is unavailable or wrong at this snapshot"))
		}
		value, err := json.Marshal(committed.AnchorJournalID)
		if err != nil {
			return recoveryFault("transfer cache encoding", err)
		}
		if err := commitRecoveryAux(ctx, t.auditDB, state, "transfer", identity, after, state.Snapshot, true, []recoveryCacheRow{{committed.AnchorJournalID, value}}); err != nil {
			return recoveryFault("transfer cache publication", err)
		}
	}
	return nil
}

type recoveryCacheRow struct {
	Journal provenance.JournalID
	Value   []byte
}

func openRecoveryAux(ctx context.Context, db *sql.DB, state *assignmentRecoveryState, kind, identity string) (provenance.JournalID, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	current, err := readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return 0, false, err
	}
	if current != *state || state.Status == assignmentCoverageDirty {
		return 0, false, fmt.Errorf("auxiliary state changed")
	}
	var after provenance.JournalID
	var complete bool
	err = tx.QueryRowContext(ctx, `SELECT after_jid,complete FROM pasture_assignment_recovery_scan WHERE generation=? AND snapshot_jid=? AND kind=? AND identity=?`, state.Generation, state.Snapshot, kind, identity).Scan(&after, &complete)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO pasture_assignment_recovery_scan(generation,snapshot_jid,kind,identity,after_jid,complete) VALUES(?,?,?,?,0,0)`, state.Generation, state.Snapshot, kind, identity)
		if err == nil {
			err = updateRecoveryStateTx(ctx, tx, *state, `state_revision=state_revision+1`)
		}
		if err == nil {
			current, err = readAssignmentRecoveryState(ctx, tx)
		}
	}
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	*state = current
	return after, complete, nil
}

func commitRecoveryAux(ctx context.Context, db *sql.DB, state *assignmentRecoveryState, kind, identity string, from, after provenance.JournalID, complete bool, rows []recoveryCacheRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return err
	}
	if current != *state || state.Status == assignmentCoverageDirty {
		return fmt.Errorf("auxiliary generation or revision changed")
	}
	for _, row := range rows {
		var prior []byte
		err := tx.QueryRowContext(ctx, `SELECT value FROM pasture_assignment_recovery_cache WHERE generation=? AND snapshot_jid=? AND kind=? AND identity=? AND journal_id=?`, state.Generation, state.Snapshot, kind, identity, row.Journal).Scan(&prior)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && !bytes.Equal(prior, row.Value) {
			return fmt.Errorf("conflicting auxiliary cache row")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pasture_assignment_recovery_cache(generation,snapshot_jid,kind,identity,journal_id,value) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, state.Generation, state.Snapshot, kind, identity, row.Journal, row.Value)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE pasture_assignment_recovery_scan SET after_jid=?,complete=? WHERE generation=? AND snapshot_jid=? AND kind=? AND identity=? AND after_jid=? AND complete=0`, after, complete, state.Generation, state.Snapshot, kind, identity, from)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("auxiliary cursor changed: count=%d err=%v", n, err)
	}
	if err := updateRecoveryStateTx(ctx, tx, *state, `state_revision=state_revision+1`); err != nil {
		return err
	}
	current, err = readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	*state = current
	return nil
}

func loadRecoveryCache(ctx context.Context, db *sql.DB, state assignmentRecoveryState, kind, identity string) ([][]byte, error) {
	rows, err := db.QueryContext(ctx, `SELECT value FROM pasture_assignment_recovery_cache WHERE generation=? AND snapshot_jid=? AND kind=? AND identity=? ORDER BY journal_id`, state.Generation, state.Snapshot, kind, identity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result [][]byte
	for rows.Next() {
		var value []byte
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func drainRecoveryMaterial(ctx context.Context, db *sql.DB, journal provenance.Journal, state *assignmentRecoveryState, task provenance.TaskID) ([]provenance.TaskEventRow, error) {
	identity := task.String()
	after, complete, err := openRecoveryAux(ctx, db, state, "material", identity)
	if err != nil {
		return nil, err
	}
	for !complete {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := journal.QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{task}, EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: 64, SnapshotMaxJournalID: state.Snapshot, AfterJournalID: after})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := decodeRecoveryMaterials(page.Events, state.Snapshot); err != nil {
			return nil, err
		}
		var rows []recoveryCacheRow
		for _, row := range page.Events {
			value, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			rows = append(rows, recoveryCacheRow{row.JournalID, value})
		}
		complete = page.Next == nil
		next := state.Snapshot
		if !complete {
			next = page.Next.AfterJournalID
		}
		if err := commitRecoveryAux(ctx, db, state, "material", identity, after, next, complete, rows); err != nil {
			return nil, err
		}
		after = next
	}
	values, err := loadRecoveryCache(ctx, db, *state, "material", identity)
	if err != nil {
		return nil, err
	}
	var result []provenance.TaskEventRow
	for _, value := range values {
		var row provenance.TaskEventRow
		if err := json.Unmarshal(value, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if _, err := decodeRecoveryMaterials(result, state.Snapshot); err != nil {
		return nil, err
	}
	return result, nil
}

func drainRecoveryEvidence(ctx context.Context, db *sql.DB, journal provenance.Journal, state *assignmentRecoveryState, operation provenance.OperationID) ([]provenance.EvidenceRow, error) {
	identity := string(operation)
	after, complete, err := openRecoveryAux(ctx, db, state, "evidence", identity)
	if err != nil {
		return nil, err
	}
	for !complete {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := journal.Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: []provenance.OperationID{operation}}, Kinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind, reviewRoundAuthorityEvidenceKind}, Page: provenance.FactPageRequest{Limit: 64, SnapshotMaxJournalID: state.Snapshot, AfterJournalID: after}})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := validateRecoveryEvidence(page.Rows, state.Snapshot); err != nil {
			return nil, err
		}
		var rows []recoveryCacheRow
		for _, row := range page.Rows {
			value, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			rows = append(rows, recoveryCacheRow{row.JournalID, value})
		}
		complete = page.Next == nil
		next := state.Snapshot
		if !complete {
			next = page.Next.AfterJournalID
		}
		if err := commitRecoveryAux(ctx, db, state, "evidence", identity, after, next, complete, rows); err != nil {
			return nil, err
		}
		after = next
	}
	values, err := loadRecoveryCache(ctx, db, *state, "evidence", identity)
	if err != nil {
		return nil, err
	}
	var result []provenance.EvidenceRow
	for _, value := range values {
		var row provenance.EvidenceRow
		if err := json.Unmarshal(value, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if _, err := validateRecoveryEvidence(result, state.Snapshot); err != nil {
		return nil, err
	}
	return result, nil
}

type assignmentCoverage string

const (
	assignmentCoverageValid      assignmentCoverage = "valid"
	assignmentCoverageDirty      assignmentCoverage = "dirty"
	assignmentCoverageRebuilding assignmentCoverage = "rebuilding"
)

// assignmentRecoveryState is a captured compare-and-swap token, not a lease.
// Journal calls run after the transaction that captured it has closed.
type assignmentRecoveryState struct {
	Generation           int64
	Revision             int64
	Destructive          int64
	CertifiedDestructive int64
	Status               assignmentCoverage
	Snapshot             provenance.JournalID
	After                provenance.JournalID
	Through              provenance.JournalID
	Complete             bool
}

type recoveryRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readAssignmentRecoveryState(ctx context.Context, q recoveryRowQuerier) (assignmentRecoveryState, error) {
	var state assignmentRecoveryState
	err := q.QueryRowContext(ctx, `SELECT generation,state_revision,destructive_revision,
		certified_destructive_revision,coverage_status,in_progress_snapshot_jid,
		in_progress_after_jid,completed_through_jid,recovery_complete
		FROM pasture_actor_assignment_state WHERE singleton_id=0`).Scan(
		&state.Generation, &state.Revision, &state.Destructive, &state.CertifiedDestructive,
		&state.Status, &state.Snapshot, &state.After, &state.Through, &state.Complete)
	return state, err
}

func recoveryFault(step string, err error) error {
	return &IndexStaleError{Cause: fmt.Errorf("assignment recovery %s failed: %w; no completeness certificate may be used from this attempt; run pasture gate rebuild-index, adding --reset when the generation is dirty", step, err)}
}

func recoveryGuard(state assignmentRecoveryState) []any {
	return []any{state.Generation, state.Revision, state.Destructive, state.Status}
}

const recoveryGuardSQL = ` WHERE singleton_id=0 AND generation=? AND state_revision=? AND destructive_revision=? AND coverage_status=?`

func updateRecoveryStateTx(ctx context.Context, tx *sql.Tx, state assignmentRecoveryState, clause string, values ...any) error {
	args := append(values, recoveryGuard(state)...)
	result, err := tx.ExecContext(ctx, `UPDATE pasture_actor_assignment_state SET `+clause+recoveryGuardSQL, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("generation, revision, destructive revision or coverage status changed")
	}
	return nil
}

// commitAssignmentDirtyTx is an error outcome that MUST commit. No index row is
// changed before this path is selected. Returning through a rollback-only helper
// here would erase the invalidation and permit a later caller to trust the index.
func commitAssignmentDirtyTx(ctx context.Context, tx *sql.Tx, state assignmentRecoveryState, reason string) error {
	if err := updateRecoveryStateTx(ctx, tx, state, `coverage_status='dirty',destructive_revision=destructive_revision+1,state_revision=state_revision+1`); err != nil {
		return recoveryFault("dirty marker not persisted", err)
	}
	if err := tx.Commit(); err != nil {
		return recoveryFault("dirty marker commit failed; marker not persisted", err)
	}
	return recoveryFault("committed dirty generation", fmt.Errorf("%s; reset is required", reason))
}

func readStartedEpisodeTx(ctx context.Context, tx recoveryRowQuerier, generation int64, assignment provenance.AssignmentID) (startedEpisode, bool, error) {
	var row startedEpisode
	var actor, task, role string
	err := tx.QueryRowContext(ctx, `SELECT assignment_id,actor_id,task_id,role,authority_journal_id
	 FROM pasture_actor_assignment WHERE generation=? AND assignment_id=?`, generation, string(assignment)).Scan(&row.Assignment, &actor, &task, &role, &row.Authority)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	row.Actor, err = provenance.ParseActorID(actor)
	if err != nil {
		return row, false, err
	}
	row.Task, err = provenance.ParseTaskID(task)
	if err != nil {
		return row, false, err
	}
	for _, candidate := range []AssignmentRole{RoleOwnerResponsibility, RoleGoverningSupervisor, RoleAxisReviewer} {
		if candidate.String() == role {
			row.Role = candidate
		}
	}
	if err := row.validate("readStartedEpisodeTx"); err != nil {
		return row, false, err
	}
	return row, true, nil
}

// ensureAssignmentRecoverySchema changes Pasture tables only. Keeping all DDL
// in one serialized transaction also makes concurrent first opens safe.
func ensureAssignmentRecoverySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return recoveryFault("schema transaction", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pasture_actor_assignment_state (
		singleton_id INTEGER PRIMARY KEY CHECK(singleton_id=0),
		generation INTEGER NOT NULL, state_revision INTEGER NOT NULL,
		destructive_revision INTEGER NOT NULL, certified_destructive_revision INTEGER NOT NULL,
		coverage_status TEXT NOT NULL CHECK(coverage_status IN ('valid','dirty','rebuilding')),
		in_progress_snapshot_jid INTEGER NOT NULL, in_progress_after_jid INTEGER NOT NULL,
		completed_through_jid INTEGER NOT NULL, recovery_complete INTEGER NOT NULL CHECK(recovery_complete IN (0,1)))`)
	if err != nil {
		return recoveryFault("state schema", err)
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('pasture_actor_assignment') WHERE name='generation'`).Scan(&exists); err != nil {
		return recoveryFault("index column inventory", err)
	}
	if exists == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE pasture_actor_assignment ADD COLUMN generation INTEGER NOT NULL DEFAULT 0`); err != nil {
			return recoveryFault("index generation column", err)
		}
	}
	// Reset changes the current generation BEFORE retiring old rows. No durable
	// bypass flag exists: a deletion in rebuilding is just as destructive as a
	// deletion in valid. INSERT and exact no-op UPDATE do not invalidate coverage.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS pasture_assignment_recovery_scan (
		 generation INTEGER NOT NULL,snapshot_jid INTEGER NOT NULL,kind TEXT NOT NULL,identity TEXT NOT NULL,
		 after_jid INTEGER NOT NULL,complete INTEGER NOT NULL CHECK(complete IN (0,1)),
		 PRIMARY KEY(generation,snapshot_jid,kind,identity))`,
		`CREATE TABLE IF NOT EXISTS pasture_assignment_recovery_cache (
		 generation INTEGER NOT NULL,snapshot_jid INTEGER NOT NULL,kind TEXT NOT NULL,identity TEXT NOT NULL,
		 journal_id INTEGER NOT NULL,value BLOB NOT NULL,
		 PRIMARY KEY(generation,snapshot_jid,kind,identity,journal_id))`,
		`CREATE TABLE IF NOT EXISTS pasture_assignment_recovery_member (
		 generation INTEGER NOT NULL,snapshot_jid INTEGER NOT NULL,producer TEXT NOT NULL,
		 assignment_id TEXT NOT NULL,task_id TEXT NOT NULL,actor_id TEXT NOT NULL,seen INTEGER NOT NULL CHECK(seen IN (0,1)),
		 PRIMARY KEY(generation,snapshot_jid,producer,assignment_id))`,
		`CREATE TRIGGER IF NOT EXISTS pasture_assignment_deleted AFTER DELETE ON pasture_actor_assignment
		BEGIN UPDATE pasture_actor_assignment_state SET coverage_status='dirty',
		 destructive_revision=destructive_revision+1,state_revision=state_revision+1
		 WHERE singleton_id=0 AND generation=OLD.generation; END`,
		`CREATE TRIGGER IF NOT EXISTS pasture_assignment_changed AFTER UPDATE ON pasture_actor_assignment
		WHEN OLD.assignment_id IS NOT NEW.assignment_id OR OLD.actor_id IS NOT NEW.actor_id
		 OR OLD.task_id IS NOT NEW.task_id OR OLD.role IS NOT NEW.role
		 OR OLD.authority_journal_id IS NOT NEW.authority_journal_id OR OLD.generation IS NOT NEW.generation
		BEGIN UPDATE pasture_actor_assignment_state SET coverage_status='dirty',
		 destructive_revision=destructive_revision+1,state_revision=state_revision+1
		 WHERE singleton_id=0 AND generation=OLD.generation; END`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return recoveryFault("invalidation trigger", err)
		}
	}
	return tx.Commit()
}

// resetAssignmentRecovery is the only exit from dirty. Generation changes first,
// so retirement of old rows never needs a persistent trigger-exemption flag.
func resetAssignmentRecovery(ctx context.Context, db *sql.DB, expected assignmentRecoveryState) (assignmentRecoveryState, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return expected, recoveryFault("reset transaction", err)
	}
	defer tx.Rollback()
	if err := updateRecoveryStateTx(ctx, tx, expected, `generation=generation+1,state_revision=state_revision+1,
	 destructive_revision=0,certified_destructive_revision=0,coverage_status='rebuilding',
	 in_progress_snapshot_jid=0,in_progress_after_jid=0,completed_through_jid=0,recovery_complete=0`); err != nil {
		return expected, recoveryFault("reset compare-and-swap", err)
	}
	for _, table := range []string{"pasture_actor_assignment", "pasture_assignment_recovery_scan", "pasture_assignment_recovery_cache", "pasture_assignment_recovery_member"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE generation=?`, expected.Generation); err != nil {
			return expected, recoveryFault("reset retirement", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pasture_actor_assignment_watermark`); err != nil {
		return expected, recoveryFault("reset watermark", err)
	}
	next, err := readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return expected, recoveryFault("reset readback", err)
	}
	if err := tx.Commit(); err != nil {
		return expected, recoveryFault("reset commit", err)
	}
	return next, nil
}

type assignmentRecoveryScan struct {
	State assignmentRecoveryState
	Page  provenance.AssignmentStartPage
}

// beginAssignmentRecoveryScan always resumes an incomplete scan, including a
// valid-prefix tail. Scan identity, rather than a nonzero snapshot, establishes
// that a boundary was bound: a pinned zero is a real snapshot too.
func beginAssignmentRecoveryScan(ctx context.Context, db *sql.DB, journal provenance.Journal, limit int) (assignmentRecoveryScan, error) {
	var scan assignmentRecoveryScan
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return scan, recoveryFault("begin scan read", err)
	}
	defer tx.Rollback()
	scan.State, err = readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return scan, recoveryFault("begin scan state", err)
	}
	if scan.State.Status == assignmentCoverageDirty {
		return scan, recoveryFault("begin scan", fmt.Errorf("dirty generation requires reset"))
	}
	var snapshot, after provenance.JournalID
	err = tx.QueryRowContext(ctx, `SELECT snapshot_jid,after_jid FROM pasture_assignment_recovery_scan
	 WHERE generation=? AND kind='assignment' AND identity='all' AND complete=0 ORDER BY snapshot_jid LIMIT 1`, scan.State.Generation).Scan(&snapshot, &after)
	bound := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return scan, recoveryFault("saved scan read", err)
	}
	if bound && (scan.State.Snapshot != snapshot || scan.State.After != after || scan.State.Complete) {
		return scan, recoveryFault("saved scan read", fmt.Errorf("state does not match incomplete scan identity"))
	}
	if err := tx.Commit(); err != nil {
		return scan, recoveryFault("begin scan read commit", err)
	}
	api, ok := journal.(provenance.AssignmentStartQueryAPI)
	if !ok {
		return scan, recoveryFault("begin scan query", fmt.Errorf("journal lacks public assignment-start query"))
	}
	if !bound && scan.State.Status == assignmentCoverageValid {
		after = scan.State.Through
	}
	if err := ctx.Err(); err != nil {
		return scan, recoveryFault("begin scan cancellation", err)
	}
	scan.Page, err = api.QueryAssignmentStarts(provenance.AssignmentStartQuery{Page: provenance.AssignmentStartPageRequest{
		Limit: limit, SnapshotPinned: bound, SnapshotMaxJournalID: snapshot, AfterJournalID: after,
	}})
	if err != nil {
		return scan, recoveryFault("begin assignment page", err)
	}
	if bound {
		return scan, nil
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return scan, recoveryFault("bind scan transaction", err)
	}
	defer tx.Rollback()
	if err := updateRecoveryStateTx(ctx, tx, scan.State, `in_progress_snapshot_jid=?,in_progress_after_jid=?,recovery_complete=0,state_revision=state_revision+1`, scan.Page.SnapshotMaxJournalID, after); err != nil {
		return scan, recoveryFault("bind scan compare-and-swap", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pasture_assignment_recovery_scan(generation,snapshot_jid,kind,identity,after_jid,complete)
	 VALUES(?,?,'assignment','all',?,0) ON CONFLICT(generation,snapshot_jid,kind,identity)
	 DO UPDATE SET after_jid=excluded.after_jid,complete=0`, scan.State.Generation, scan.Page.SnapshotMaxJournalID, after)
	if err != nil {
		return scan, recoveryFault("bind scan identity", err)
	}
	scan.State, err = readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return scan, recoveryFault("bind scan readback", err)
	}
	if err := tx.Commit(); err != nil {
		return scan, recoveryFault("bind scan commit", err)
	}
	return scan, nil
}

// ensureAssignmentIndexStateOnOpen certifies an assignment-start-empty prefix,
// never an empty index alone. A legacy/rebuilding state does not prevent opening
// the store: its operator must be able to open it to repair it.
func ensureAssignmentIndexStateOnOpen(ctx context.Context, db *sql.DB, journal provenance.Journal) error {
	if err := ensureAssignmentRecoverySchema(ctx, db); err != nil {
		return err
	}
	if _, err := readAssignmentRecoveryState(ctx, db); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return recoveryFault("read factory state", err)
	}
	api, ok := journal.(provenance.AssignmentStartQueryAPI)
	if !ok {
		return recoveryFault("factory query capability", fmt.Errorf("the opened journal has no public assignment-start query"))
	}
	page, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{
		Page: provenance.AssignmentStartPageRequest{Limit: 1},
	})
	if err != nil {
		return recoveryFault("factory empty-prefix query", err)
	}
	if err := ctx.Err(); err != nil {
		return recoveryFault("factory query cancellation", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return recoveryFault("factory publication transaction", err)
	}
	defer tx.Rollback()
	if _, err := readAssignmentRecoveryState(ctx, tx); err == nil {
		return tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return recoveryFault("factory publication recheck", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pasture_actor_assignment`).Scan(&count); err != nil {
		return recoveryFault("factory local index recheck", err)
	}
	status, through, complete := assignmentCoverageRebuilding, provenance.JournalID(0), false
	if len(page.Rows) == 0 && page.Next == nil && count == 0 {
		status, through, complete = assignmentCoverageValid, page.SnapshotMaxJournalID, true
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pasture_actor_assignment_state
		(singleton_id,generation,state_revision,destructive_revision,certified_destructive_revision,
		coverage_status,in_progress_snapshot_jid,in_progress_after_jid,completed_through_jid,recovery_complete)
		VALUES(0,0,1,0,0,?,0,0,?,?)`, status, through, complete)
	if err != nil {
		return recoveryFault("factory state insert", err)
	}
	if err := tx.Commit(); err != nil {
		return recoveryFault("factory state commit", err)
	}
	return nil
}
