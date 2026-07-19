package tasks

// projections_test.go exercises the deterministic stage-d projections: the review-round
// graph shape (plan vs implementation), the derived review-subject sum, slice-close
// authorization validation, the composed task-centric timeline, and the assignment-episode
// owner projection. Every case is pure — no journal, no seeded actor.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
)

func projTask(t *testing.T) provenance.TaskID {
	t.Helper()
	return provenance.TaskID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}
}

func projActor(t *testing.T) provenance.ActorID {
	t.Helper()
	return provenance.ActorID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())}
}

// countKind counts planned tasks of a kind.
func countKind(tasks []PlannedTask, kind ReviewTaskKind) int {
	n := 0
	for _, tk := range tasks {
		if tk.Kind == kind {
			n++
		}
	}
	return n
}

func countRelation(edges []PlannedEdge, rel ReviewRelation) int {
	n := 0
	for _, e := range edges {
		if e.Relation == rel {
			n++
		}
	}
	return n
}

// TestPlanReviewRound_PlanShape proves a plan review has one round, three axes, no groups,
// and the round-blocked-by-axis + reviewed-blocked-by-round edges.
func TestPlanReviewRound_PlanShape(t *testing.T) {
	t.Parallel()
	reviewed := projTask(t)
	subject := DocumentRevisionSubject{ID: "doc-rev-1", DocumentTask: projTask(t)}.Ref()

	plan, err := PlanReviewRound(reviewed, subject, SubjectPlan)
	if err != nil {
		t.Fatalf("PlanReviewRound: %v", err)
	}
	if countKind(plan.Tasks, ReviewTaskRound) != 1 {
		t.Errorf("want exactly one round task, got %d", countKind(plan.Tasks, ReviewTaskRound))
	}
	if countKind(plan.Tasks, ReviewTaskAxis) != 3 {
		t.Errorf("want exactly three axis tasks, got %d", countKind(plan.Tasks, ReviewTaskAxis))
	}
	if countKind(plan.Tasks, ReviewTaskGroup) != 0 {
		t.Errorf("plan review must create no severity groups, got %d", countKind(plan.Tasks, ReviewTaskGroup))
	}
	// round contains each axis (3) and round blocked-by each axis (3) + reviewed blocked-by round (1) = 4 blocked-by.
	if countRelation(plan.Edges, RelationContains) != 3 {
		t.Errorf("want three contains edges, got %d", countRelation(plan.Edges, RelationContains))
	}
	if countRelation(plan.Edges, RelationBlockedBy) != 4 {
		t.Errorf("want four blocked-by edges (reviewed<-round + round<-3 axes), got %d", countRelation(plan.Edges, RelationBlockedBy))
	}
	if countRelation(plan.Edges, RelationSubject) != 1 {
		t.Errorf("want one subject edge, got %d", countRelation(plan.Edges, RelationSubject))
	}
	if plan.Subject.Kind != ReviewSubjectDocumentRevision {
		t.Errorf("subject kind = %v, want document-revision", plan.Subject.Kind)
	}
}

// TestPlanReviewRound_ImplementationShape proves an implementation review eagerly adds three
// severity groups per axis (9) and blocks each axis by its three groups.
func TestPlanReviewRound_ImplementationShape(t *testing.T) {
	t.Parallel()
	reviewed := projTask(t)
	subject := ImplementationCandidateSubject{
		ID:        "impl-cand-1",
		SliceTask: projTask(t),
		CommitOID: provenance.GitOID("0123456789abcdef0123456789abcdef01234567"),
	}.Ref()

	plan, err := PlanReviewRound(reviewed, subject, SubjectImplementation)
	if err != nil {
		t.Fatalf("PlanReviewRound: %v", err)
	}
	if countKind(plan.Tasks, ReviewTaskGroup) != 9 {
		t.Errorf("implementation review must create 9 severity groups (3 axes x 3), got %d", countKind(plan.Tasks, ReviewTaskGroup))
	}
	// blocked-by: reviewed<-round (1) + round<-3 axes (3) + each axis<-3 groups (9) = 13.
	if countRelation(plan.Edges, RelationBlockedBy) != 13 {
		t.Errorf("want 13 blocked-by edges, got %d", countRelation(plan.Edges, RelationBlockedBy))
	}
	if plan.Subject.Kind != ReviewSubjectImplementationCandidate {
		t.Errorf("subject kind = %v, want implementation-candidate", plan.Subject.Kind)
	}
}

// TestPlanReviewRound_Deterministic proves the plan is a pure function of its inputs.
func TestPlanReviewRound_Deterministic(t *testing.T) {
	t.Parallel()
	reviewed := projTask(t)
	subject := DocumentRevisionSubject{ID: "doc-rev-9", DocumentTask: projTask(t)}.Ref()
	a, err := PlanReviewRound(reviewed, subject, SubjectPlan)
	if err != nil {
		t.Fatalf("plan a: %v", err)
	}
	b, err := PlanReviewRound(reviewed, subject, SubjectPlan)
	if err != nil {
		t.Fatalf("plan b: %v", err)
	}
	if a.RoundHandle != b.RoundHandle || a.AxisHandles != b.AxisHandles {
		t.Fatalf("plan handles not deterministic")
	}
	if len(a.Tasks) != len(b.Tasks) || len(a.Edges) != len(b.Edges) {
		t.Fatalf("plan graph size not deterministic")
	}
	for i := range a.Edges {
		if a.Edges[i] != b.Edges[i] {
			t.Fatalf("edge %d differs across plans: %+v vs %+v", i, a.Edges[i], b.Edges[i])
		}
	}
}

// TestPlanReviewRound_Rejects proves invalid inputs are rejected.
func TestPlanReviewRound_Rejects(t *testing.T) {
	t.Parallel()
	subject := DocumentRevisionSubject{ID: "doc", DocumentTask: projTask(t)}.Ref()
	if _, err := PlanReviewRound(provenance.TaskID{}, subject, SubjectPlan); err == nil {
		t.Errorf("expected rejection for zero reviewed task")
	}
	if _, err := PlanReviewRound(projTask(t), ReviewSubjectRef{}, SubjectPlan); err == nil {
		t.Errorf("expected rejection for zero subject ref")
	}
	if _, err := PlanReviewRound(projTask(t), subject, SubjectKind(0)); err == nil {
		t.Errorf("expected rejection for invalid review kind")
	}
}

// TestReviewSubjectRefDerived proves the sum-level ref is derived from each concrete subject.
func TestReviewSubjectRefDerived(t *testing.T) {
	t.Parallel()
	doc := DocumentRevisionSubject{ID: "doc-rev-2", DocumentTask: projTask(t)}
	if got := doc.Ref(); got.Kind != ReviewSubjectDocumentRevision || got.SnapshotID != "doc-rev-2" {
		t.Errorf("doc ref = %+v", got)
	}
	impl := ImplementationCandidateSubject{ID: "impl-3", SliceTask: projTask(t)}
	if got := impl.Ref(); got.Kind != ReviewSubjectImplementationCandidate || got.SnapshotID != "impl-3" {
		t.Errorf("impl ref = %+v", got)
	}
	var _ ReviewSubject = doc
	var _ ReviewSubject = impl
}

// TestSliceCloseAuthorizationValidate proves the authorization requires a supervisor, round,
// candidate, and three distinct non-zero review events.
func TestSliceCloseAuthorizationValidate(t *testing.T) {
	t.Parallel()
	good := SliceCloseAuthorization{
		GoverningSupervisor: "sup-1",
		ReviewRound:         "round-1",
		Candidate:           "cand-1",
		ReviewEvents:        [3]provenance.JournalID{10, 20, 30},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid authorization rejected: %v", err)
	}

	bad := []SliceCloseAuthorization{
		{ReviewRound: "r", Candidate: "c", ReviewEvents: [3]provenance.JournalID{1, 2, 3}},                           // no supervisor
		{GoverningSupervisor: "s", Candidate: "c", ReviewEvents: [3]provenance.JournalID{1, 2, 3}},                   // no round
		{GoverningSupervisor: "s", ReviewRound: "r", ReviewEvents: [3]provenance.JournalID{1, 2, 3}},                 // no candidate
		{GoverningSupervisor: "s", ReviewRound: "r", Candidate: "c", ReviewEvents: [3]provenance.JournalID{1, 0, 3}}, // zero event
		{GoverningSupervisor: "s", ReviewRound: "r", Candidate: "c", ReviewEvents: [3]provenance.JournalID{1, 1, 3}}, // duplicate
	}
	for i, a := range bad {
		if err := a.Validate(); err == nil {
			t.Errorf("case %d: expected rejection", i)
		}
	}
}

// TestComposeTimelineDeterministicAndDeduped proves timeline composition orders by
// (RecordedAt, EventID), preserves EventIDs, and emits each event once.
func TestComposeTimelineDeterministicAndDeduped(t *testing.T) {
	t.Parallel()
	task := projTask(t)
	end := int64(100)
	sources := []TimelineSource{
		{EventID: 30, Task: task, RecordedAt: 5, Kind: "pasture.review.recorded.v1"},
		{EventID: 10, Task: task, RecordedAt: 5, Kind: "provenance.task.created"}, // equal time, lower id first
		{EventID: 20, Task: task, RecordedAt: 3, Kind: "provenance.task.started",
			Activity: &ActivityInterval{ActivityID: provenance.ActivityID{}, StartAt: 3, EndAt: &end}},
		{EventID: 10, Task: task, RecordedAt: 5, Kind: "provenance.task.created"}, // duplicate EventID
	}
	tl := ComposeTimeline(sources)
	if len(tl) != 3 {
		t.Fatalf("timeline length = %d, want 3 (duplicate EventID collapsed)", len(tl))
	}
	// Expected order: (3,20), (5,10), (5,30).
	wantIDs := []provenance.JournalID{20, 10, 30}
	for i, want := range wantIDs {
		if tl[i].EventID != want {
			t.Errorf("entry %d EventID = %d, want %d", i, tl[i].EventID, want)
		}
	}
	// The closed activity interval is preserved.
	if tl[0].EntryKind != TimelineActivity || tl[0].Interval == nil || tl[0].Interval.IsOpen() {
		t.Errorf("entry 0 should be a closed activity interval: %+v", tl[0])
	}
}

// TestProjectAssignmentsOwnerTransfer proves Task.Owner follows the active
// owner-responsibility episode across a transfer.
func TestProjectAssignmentsOwnerTransfer(t *testing.T) {
	t.Parallel()
	task := projTask(t)
	worker1 := projActor(t)
	worker2 := projActor(t)

	transitions := []AssignmentTransition{
		{Assignment: "A", Task: task, Role: RoleOwnerResponsibility, Occupant: worker1, Kind: AssignmentStarted, Ordinal: 1},
		{Assignment: "A", Task: task, Role: RoleOwnerResponsibility, Kind: AssignmentEnded, Ordinal: 2},
		{Assignment: "B", Task: task, Role: RoleOwnerResponsibility, Occupant: worker2, Predecessor: "A", Kind: AssignmentStarted, Ordinal: 3},
	}
	proj := ProjectAssignments(transitions)
	owner, ok := proj.OwnerOf(task)
	if !ok {
		t.Fatalf("task has no owner after transfer")
	}
	if owner != worker2 {
		t.Fatalf("owner = %v, want worker2 (transfer target)", owner)
	}
	if ep := proj.Episodes["A"]; ep.Active {
		t.Errorf("episode A should be inactive after end")
	}
	if ep := proj.Episodes["B"]; !ep.Active || ep.Predecessor != "A" {
		t.Errorf("episode B should be active with predecessor A: %+v", ep)
	}
}

// TestProjectAssignmentsRolesDistinct proves reviewer/supervisor episodes do not become the
// owner; only the owner-responsibility slot drives Task.Owner.
func TestProjectAssignmentsRolesDistinct(t *testing.T) {
	t.Parallel()
	task := projTask(t)
	reviewer := projActor(t)
	supervisor := projActor(t)
	owner := projActor(t)

	proj := ProjectAssignments([]AssignmentTransition{
		{Assignment: "R", Task: task, Role: RoleAxisReviewer, Occupant: reviewer, Kind: AssignmentStarted, Ordinal: 1},
		{Assignment: "S", Task: task, Role: RoleGoverningSupervisor, Occupant: supervisor, Kind: AssignmentStarted, Ordinal: 2},
		{Assignment: "O", Task: task, Role: RoleOwnerResponsibility, Occupant: owner, Kind: AssignmentStarted, Ordinal: 3},
	})
	got, ok := proj.OwnerOf(task)
	if !ok || got != owner {
		t.Fatalf("owner = %v (ok=%v), want %v", got, ok, owner)
	}
	// Ending the owner episode leaves the task unowned even though reviewer/supervisor remain.
	proj = ProjectAssignments([]AssignmentTransition{
		{Assignment: "O", Task: task, Role: RoleOwnerResponsibility, Occupant: owner, Kind: AssignmentStarted, Ordinal: 1},
		{Assignment: "O", Task: task, Role: RoleOwnerResponsibility, Kind: AssignmentEnded, Ordinal: 2},
	})
	if _, ok := proj.OwnerOf(task); ok {
		t.Fatalf("task should be unowned after owner-responsibility ends")
	}
}
