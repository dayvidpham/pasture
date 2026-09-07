package tasks

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestNonemptyReviewRejectsRevokedActionWithActiveSibling(t *testing.T) {
	t.Parallel()
	store, service, input, action := newReviewAuthorityFixture(t)
	seedRecoveryAssignmentWithParent(t, store, action.task, "alternate-axis", RoleAxisReviewer,
		action.occupant, "alternate-axis-start", "review-plan")
	alternate, err := service.resolveAssignment(t.Context(), action.task, "alternate-axis", RoleAxisReviewer)
	require.NoError(t, err)
	before, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy: provenance.OrderByJournalID,
		TaskIDs: []provenance.TaskID{action.task},
		Limit:   provenance.MaxFactPageSize,
	})
	require.NoError(t, err)
	barrierCalls := 0
	service.barrier = &callbackEpochBarrier{after: func() error {
		barrierCalls++
		endAssignmentEpisode(t, store, action.task, input.Assignment, action.occupant, "revoke-action-after-preflight")
		active, err := store.Journal().AuthorityGovernsTaskAt(action.authority, action.task, provenance.JournalID(^uint64(0)>>1))
		require.NoError(t, err)
		require.False(t, active, "exact input authority must be inactive before submission Apply")
		active, err = store.Journal().AuthorityGovernsTaskAt(alternate.authority, action.task, provenance.JournalID(^uint64(0)>>1))
		require.NoError(t, err)
		require.True(t, active, "the same-parent alternate axis must remain active")
		return nil
	}}

	_, submitErr := service.SubmitReview(t.Context(), input)
	committed, err := store.Journal().LookupCommitted(input.Meta.OperationID)
	require.NoError(t, err)
	t.Logf("submission error=%v committed=%v", submitErr, committed.Kind)
	require.Equal(t, 1, barrierCalls)
	require.Error(t, submitErr, "revoked action must not borrow the alternate axis lineage through its parent")
	require.ErrorIs(t, submitErr, provenance.ErrConditionFailed)
	require.Equal(t, provenance.CommittedAbsent, committed.Kind)
	for _, kind := range []provenance.EvidenceKind{reviewSubmissionEvidenceKind, reviewAxisSubmissionEvidenceKind} {
		page, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
			Filter: provenance.FactFilter{
				TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: action.task},
				OperationIDs: []provenance.OperationID{input.Meta.OperationID},
			},
			Kinds: []provenance.EvidenceKind{kind},
			Page:  provenance.FactPageRequest{Limit: 2},
		})
		require.NoError(t, err)
		require.Empty(t, page.Rows)
		require.Nil(t, page.Next)
	}
	after, err := store.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy: provenance.OrderByJournalID,
		TaskIDs: []provenance.TaskID{action.task},
		Limit:   provenance.MaxFactPageSize,
	})
	require.NoError(t, err)
	require.Equal(t, before.Events, after.Events, "no axis material event may escape failed Apply")
	start, err := service.findReviewStart(t.Context(), input.Epoch, input.Round)
	require.NoError(t, err)
	for _, group := range start.graph[input.Axis-1].Groups {
		edges, err := store.Edges(group.Task, nil)
		require.NoError(t, err)
		for _, edge := range edges {
			require.NotEqual(t, input.Submission.(ImplementationReviewSubmission).Findings[0].Task.String(), edge.TargetID,
				"no finding edge may escape failed Apply")
		}
	}
}

func TestNonemptyReviewKeepsActionActiveAndReplaysAfterRevocation(t *testing.T) {
	t.Parallel()
	store, service, input, action := newReviewAuthorityFixture(t)
	result, err := service.SubmitReview(t.Context(), input)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	active, err := store.Journal().AuthorityGovernsTaskAt(action.authority, action.task, provenance.JournalID(^uint64(0)>>1))
	require.NoError(t, err)
	require.True(t, active, "submitting findings must not consume the action assignment")

	page, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{
			TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskAny},
			OperationIDs: []provenance.OperationID{input.Meta.OperationID},
		},
		Kinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind, reviewSubmissionEvidenceKind, reviewAxisSubmissionEvidenceKind},
		Page:  provenance.FactPageRequest{Limit: 4},
	})
	require.NoError(t, err)
	require.Nil(t, page.Next)
	require.Len(t, page.Rows, 3, "one command and two immutable axis evidence rows")
	for _, row := range page.Rows {
		switch row.EvidenceKind {
		case assignmentCommandEvidenceKind:
			var command assignmentCommandRecord
			require.NoError(t, json.Unmarshal(row.Payload, &command))
			require.Equal(t, provenance.AssignmentID("review-plan"), command.Assignment)
			require.Equal(t, RoleGoverningSupervisor, command.Role)
			require.Equal(t, action.occupant, command.Occupant)
			require.NotEqual(t, action.authority, command.Authority)
			require.NotNil(t, row.TaskID)
			require.Equal(t, command.Task, *row.TaskID)
			parent, err := service.resolveAssignment(t.Context(), command.Task, command.Assignment, RoleGoverningSupervisor)
			require.NoError(t, err)
			require.Equal(t, parent.authority, command.Authority)
		case reviewSubmissionEvidenceKind, reviewAxisSubmissionEvidenceKind:
			var binding struct {
				Assignment provenance.AssignmentID `json:"assignment"`
				Actor      provenance.ActorID      `json:"actor"`
			}
			require.NoError(t, json.Unmarshal(row.Payload, &binding))
			require.Equal(t, input.Assignment, binding.Assignment)
			require.Equal(t, action.occupant, binding.Actor)
			require.NotNil(t, row.TaskID)
			require.Equal(t, action.task, *row.TaskID)
		default:
			t.Fatalf("unexpected submission evidence kind %q", row.EvidenceKind)
		}
	}
	endAssignmentEpisode(t, store, action.task, input.Assignment, action.occupant, "revoke-submitted-action")
	active, err = store.Journal().AuthorityGovernsTaskAt(action.authority, action.task, provenance.JournalID(^uint64(0)>>1))
	require.NoError(t, err)
	require.False(t, active)
	service.barrier = &callbackEpochBarrier{after: func() error {
		t.Fatal("exact replay must precede mutable preflight and Apply")
		return nil
	}}
	replayed, err := service.SubmitReview(t.Context(), input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	result.Replayed = true
	require.Equal(t, result, replayed, "replay must restore the old immutable event and result slots")
	changed := input
	changed.Submission = ImplementationReviewSubmission{
		Verdict: VerdictAccept,
		Findings: []ReviewFinding{{
			Task:     input.Submission.(ImplementationReviewSubmission).Findings[0].Task,
			Severity: SeverityImportant,
			Summary:  "Changed finding summary",
		}},
	}
	_, err = service.SubmitReview(t.Context(), changed)
	require.Error(t, err, "changed retry must not replay the old submission")
	unchanged, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{
			TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskAny},
			OperationIDs: []provenance.OperationID{input.Meta.OperationID},
		},
		Kinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind, reviewSubmissionEvidenceKind, reviewAxisSubmissionEvidenceKind},
		Page:  provenance.FactPageRequest{Limit: 4},
	})
	require.NoError(t, err)
	require.Equal(t, page.Rows, unchanged.Rows)
}

func TestReviewActionConditionBindsExactPublicStart(t *testing.T) {
	t.Parallel()
	store, service, input, action := newReviewAuthorityFixture(t)
	start, err := service.findReviewStart(t.Context(), input.Epoch, input.Round)
	require.NoError(t, err)
	round, err := service.currentReviewRoundAuthority(start.subject)
	require.NoError(t, err)
	parent, condition, err := service.reviewParentForFindingSubmission(t.Context(), action, start, round)
	require.NoError(t, err)
	require.Equal(t, provenance.ConditionAssignmentActive, condition.Kind)
	require.NotNil(t, condition.AssignmentActive)
	expectedParent := provenance.AssignmentID("review-plan")
	require.Equal(t, provenance.AssignmentActiveAssertion{
		AssignmentID:       input.Assignment,
		TaskID:             action.task,
		SlotID:             provenance.SlotOwnerResponsibility,
		Occupant:           action.occupant,
		AuthorityJournalID: action.authority,
		ParentAssignmentID: &expectedParent,
	}, *condition.AssignmentActive)
	require.NotEqual(t, parent.authority, condition.AssignmentActive.AuthorityJournalID)

	otherActor := feasibilityActor(t, store, "other-reviewer")
	alternateAssignment := provenance.AssignmentID("alternate-axis")
	seedRecoveryAssignmentWithParent(t, store, action.task, alternateAssignment, RoleAxisReviewer,
		action.occupant, "alternate-axis-start", parent.id)
	for _, test := range []struct {
		name  string
		alter func(*provenance.AssignmentActiveAssertion)
	}{
		{"exact", nil},
		{"assignment", func(a *provenance.AssignmentActiveAssertion) {
			a.AssignmentID = alternateAssignment
		}},
		{"task", func(a *provenance.AssignmentActiveAssertion) {
			a.TaskID = parent.task
		}},
		{"occupant", func(a *provenance.AssignmentActiveAssertion) {
			a.Occupant = otherActor
		}},
		{"authority", func(a *provenance.AssignmentActiveAssertion) {
			a.AuthorityJournalID = parent.authority
		}},
		{"parent", func(a *provenance.AssignmentActiveAssertion) {
			a.ParentAssignmentID = &alternateAssignment
		}},
		{"no-parent", func(a *provenance.AssignmentActiveAssertion) {
			a.ParentAssignmentID = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertion := *condition.AssignmentActive
			if test.alter != nil {
				test.alter(&assertion)
			}
			payload := []byte(`{}`)
			digest := sha256.Sum256(payload)
			operation := provenance.OperationID("assert-review-" + test.name)
			_, err := store.Journal().Apply(provenance.OperationInput{
				OperationID:        operation,
				ActorID:            action.occupant,
				AuthorityJournalID: &parent.authority,
				CommandDigest:      []byte(operation),
				Conditions:         []provenance.Condition{provenance.AssignmentActiveCondition(assertion)},
				Effects: []provenance.Effect{{
					Sort:          provenance.EffectEvidence,
					TaskID:        action.task,
					EvidenceKind:  "pasture.review-condition-probe",
					Payload:       payload,
					ContentDigest: digest[:],
				}},
			})
			if test.alter == nil {
				require.NoError(t, err, "the exact condition must allow the real transaction")
			} else {
				require.ErrorIs(t, err, provenance.ErrConditionFailed, "a valid but wrongly bound identity must fail in the real transaction")
			}
			committed, err := store.Journal().LookupCommitted(operation)
			require.NoError(t, err)
			if test.alter == nil {
				require.Equal(t, provenance.CommittedExact, committed.Kind)
			} else {
				require.Equal(t, provenance.CommittedAbsent, committed.Kind)
			}
		})
	}
}

func TestPlanReviewSubmissionRetainsAxisAuthority(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "plan-reviewer")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "plan-supervisor", RoleGoverningSupervisor, actor, "plan-supervisor-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	started, err := service.StartReview(t.Context(), StartReviewInput{
		Meta:    CommandMeta{OperationID: "plan-review"},
		Epoch:   EpochRootID(epoch.String()),
		Subject: ReviewSubjectRef{Kind: ReviewSubjectDocumentRevision, SnapshotID: plan.String()},
	})
	require.NoError(t, err)
	axis := canonicalReviewAxes()[0]
	axisTask := deterministicTask(started.OperationID, "axis-"+axis.String())
	// A separate actor with no parent may submit plan feedback. This route must
	// not acquire the nonempty implementation finding-group requirement.
	other := feasibilityActor(t, store, "independent-plan-reviewer")
	seedAssignmentEpisode(t, store, axisTask, "plan-axis", RoleAxisReviewer, other, "plan-axis-start")
	input := SubmitReviewInput{
		Meta:       CommandMeta{OperationID: "plan-submit"},
		Epoch:      EpochRootID(epoch.String()),
		Round:      started.Round,
		Axis:       axis,
		Assignment: "plan-axis",
		Submission: PlanReviewSubmission{Verdict: VerdictRevise, Feedback: []PlanReviewFeedback{{Body: "Specify acceptance"}}},
	}
	result, err := service.SubmitReview(t.Context(), input)
	require.NoError(t, err)
	page, err := store.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{
			TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: axisTask},
			OperationIDs: []provenance.OperationID{input.Meta.OperationID},
		},
		Kinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind},
		Page:  provenance.FactPageRequest{Limit: 2},
	})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	require.Nil(t, page.Next)
	var command assignmentCommandRecord
	require.NoError(t, json.Unmarshal(page.Rows[0].Payload, &command))
	require.Equal(t, input.Assignment, command.Assignment)
	require.Equal(t, RoleAxisReviewer, command.Role)
	require.Equal(t, other, command.Occupant)
	endAssignmentEpisode(t, store, axisTask, input.Assignment, other, "plan-axis-end")
	replayed, err := service.SubmitReview(t.Context(), input)
	require.NoError(t, err)
	result.Replayed = true
	require.Equal(t, result, replayed)
}

// The seed is an explicit administrative plan prerequisite. All children and
// review groups are produced by the real commands and native review allocation.
func newReviewAuthorityFixture(t *testing.T) (*trackerImpl, *epochAssignmentService, SubmitReviewInput, assignmentResolution) {
	t.Helper()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "review-parent")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "review-plan", RoleGoverningSupervisor, actor, "review-plan-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	slice, err := service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "review-slice"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "review-plan",
	})
	require.NoError(t, err)
	member, err := service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
		Meta:       CommandMeta{OperationID: "review-member"},
		Epoch:      EpochRootID(epoch.String()),
		Slice:      slice.Slice,
		Repository: "repo",
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Assignment: "review-slice-slice-owner",
	})
	require.NoError(t, err)
	started, err := service.StartReview(t.Context(), StartReviewInput{
		Meta:    CommandMeta{OperationID: "review-start"},
		Epoch:   EpochRootID(epoch.String()),
		Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(member.Candidate)},
	})
	require.NoError(t, err)
	axis := canonicalReviewAxes()[0]
	action := provenance.AssignmentID(string(started.OperationID) + "-axis-" + axis.String())
	axisTask := deterministicTask(started.OperationID, "axis-"+axis.String())
	reader := service.(*epochService).EpochAssignmentService.(*epochAssignmentService)
	resolution, err := reader.resolveAssignment(t.Context(), axisTask, action, RoleAxisReviewer)
	require.NoError(t, err)
	finding := createHumanTestTask(t, store, "finding")
	input := SubmitReviewInput{
		Meta:       CommandMeta{OperationID: "submit-review"},
		Epoch:      EpochRootID(epoch.String()),
		Round:      started.Round,
		Axis:       axis,
		Assignment: action,
		Submission: ImplementationReviewSubmission{
			Verdict: VerdictAccept,
			Findings: []ReviewFinding{
				{Task: finding, Severity: SeverityImportant, Summary: "A real finding"},
			},
		},
	}
	return store, reader, input, resolution
}
