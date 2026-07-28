// Package tasks — review-round shape, review-subject sum, and slice-close authorization
// projections (#43).
//
// review_projection.go holds the DETERMINISTIC, pure projection layer for review: the
// authoritative review-round graph SHAPE (PlanReviewRound), the closed review-subject sum
// (DocumentRevisionSubject / ImplementationCandidateSubject with a derived
// ReviewSubjectRef), and the typed SliceCloseAuthorization. These are the read/plan
// projections review start/finalize/close are lowered onto; they compute WHAT the review
// graph is without touching the journal, so they are exhaustively testable independently
// of the atomically seeded ordinal-zero system actor used by the committing path.

package tasks

import (
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// ReviewAxis is the closed set of the three review axes every round has exactly one of.
type ReviewAxis int

const (
	reviewAxisInvalid ReviewAxis = iota
	// AxisCorrectness reviews behavioral correctness.
	AxisCorrectness
	// AxisTestQuality reviews test adequacy.
	AxisTestQuality
	// AxisElegance reviews design elegance.
	AxisElegance
)

// ReviewAxes is the canonical ordered triple of axes a round is composed of.
var ReviewAxes = [3]ReviewAxis{AxisCorrectness, AxisTestQuality, AxisElegance}

func (a ReviewAxis) valid() bool {
	return a == AxisCorrectness || a == AxisTestQuality || a == AxisElegance
}

func (a ReviewAxis) String() string {
	switch a {
	case AxisCorrectness:
		return "correctness"
	case AxisTestQuality:
		return "test-quality"
	case AxisElegance:
		return "elegance"
	default:
		return fmt.Sprintf("ReviewAxis(%d)", int(a))
	}
}

// FindingSeverity is the closed eager severity-group set an implementation review axis is
// blocked by. Plan reviews create no groups.
type FindingSeverity int

const (
	findingSeverityInvalid FindingSeverity = iota
	// SeverityBlocker is a merge-blocking finding.
	SeverityBlocker
	// SeverityImportant is a should-fix finding.
	SeverityImportant
	// SeverityMinor is an optional finding.
	SeverityMinor
)

// FindingSeverities is the canonical ordered triple of severities.
var FindingSeverities = [3]FindingSeverity{SeverityBlocker, SeverityImportant, SeverityMinor}

func (s FindingSeverity) valid() bool {
	return s == SeverityBlocker || s == SeverityImportant || s == SeverityMinor
}

func (s FindingSeverity) String() string {
	switch s {
	case SeverityBlocker:
		return "blocker"
	case SeverityImportant:
		return "important"
	case SeverityMinor:
		return "minor"
	default:
		return fmt.Sprintf("FindingSeverity(%d)", int(s))
	}
}

// ReviewTaskKind classifies a planned review task by its role in the graph.
type ReviewTaskKind int

const (
	reviewTaskInvalid ReviewTaskKind = iota
	// ReviewTaskRound is the review-round task.
	ReviewTaskRound
	// ReviewTaskAxis is one of the three axis tasks.
	ReviewTaskAxis
	// ReviewTaskGroup is one eager severity group task (implementation reviews only).
	ReviewTaskGroup
)

// ReviewRelation is the closed set of typed relationships a review-round plan wires.
type ReviewRelation int

const (
	reviewRelationInvalid ReviewRelation = iota
	// RelationSubject links the round to its reviewed subject.
	RelationSubject
	// RelationContains links the round to an axis it contains.
	RelationContains
	// RelationBlockedBy is the parent-blocked-by-child edge (parent first).
	RelationBlockedBy
)

func (r ReviewRelation) String() string {
	switch r {
	case RelationSubject:
		return "subject"
	case RelationContains:
		return "contains"
	case RelationBlockedBy:
		return "blocked-by"
	default:
		return fmt.Sprintf("ReviewRelation(%d)", int(r))
	}
}

// PlannedTask is one task the review-start batch mints, named by a stable batch-local
// handle (the real TaskID is assigned by the batch).
type PlannedTask struct {
	Handle   string
	Kind     ReviewTaskKind
	Axis     ReviewAxis      // set for axis and group tasks
	Severity FindingSeverity // set for group tasks
}

// PlannedEdge is one typed relationship the batch wires between two batch-local handles.
// For RelationBlockedBy the From handle is the parent (blocked) and To is the child
// (must finish first), matching parent-blocked-by-child direction.
type PlannedEdge struct {
	From     string
	To       string
	Relation ReviewRelation
}

// reviewRoundGraph is the persisted, typed identity binding for one materialized
// review round. Severity order is explicit so submission lowering never infers a
// group from mutable graph traversal or string handles.
type reviewRoundGraph [3]reviewAxisGraphBinding

type reviewAxisGraphBinding struct {
	Axis   ReviewAxis                    `json:"axis"`
	Task   provenance.TaskID             `json:"task"`
	Groups [3]reviewSeverityGroupBinding `json:"groups"`
}

type reviewSeverityGroupBinding struct {
	Severity FindingSeverity   `json:"severity"`
	Task     provenance.TaskID `json:"task"`
}

func newReviewRoundGraph(kind SubjectKind, axisTasks [3]provenance.TaskID, groups [3][3]provenance.TaskID) (reviewRoundGraph, error) {
	var graph reviewRoundGraph
	for i, axis := range canonicalReviewAxes() {
		graph[i] = reviewAxisGraphBinding{Axis: axis, Task: axisTasks[i]}
		if kind != SubjectImplementation {
			continue
		}
		for j, severity := range canonicalFindingSeverities() {
			graph[i].Groups[j] = reviewSeverityGroupBinding{Severity: severity, Task: groups[i][j]}
		}
	}
	if err := graph.validate(kind); err != nil {
		return reviewRoundGraph{}, err
	}
	return graph, nil
}

func (g reviewRoundGraph) validate(kind SubjectKind) error {
	if !kind.valid() {
		return reviewErr("reviewRoundGraph.validate", fmt.Sprintf("review kind %q is not plan or implementation", kind),
			"materialized review identities are closed by subject kind", "use SubjectPlan or SubjectImplementation")
	}
	seen := make(map[provenance.TaskID]struct{}, 12)
	recordTask := func(task provenance.TaskID, label string) error {
		if task == (provenance.TaskID{}) {
			return reviewErr("reviewRoundGraph.validate", label+" has a zero task identity",
				"every materialized review slot must bind one exact task", "persist the generated task identity")
		}
		if _, duplicate := seen[task]; duplicate {
			return reviewErr("reviewRoundGraph.validate", label+" repeats another review task identity",
				"each axis and severity group occupies one distinct graph slot", "persist distinct generated task identities")
		}
		seen[task] = struct{}{}
		return nil
	}
	for i, axis := range canonicalReviewAxes() {
		binding := g[i]
		if binding.Axis != axis {
			return reviewErr("reviewRoundGraph.validate", fmt.Sprintf("axis slot %d is %s", i, binding.Axis),
				"review axes use canonical correctness, test-quality, elegance order", "persist the canonical axis order")
		}
		if err := recordTask(binding.Task, fmt.Sprintf("axis slot %d", i)); err != nil {
			return err
		}
		if kind == SubjectPlan {
			if binding.Groups != [3]reviewSeverityGroupBinding{} {
				return reviewErr("reviewRoundGraph.validate", fmt.Sprintf("plan axis %s carries severity groups", axis),
					"plan reviews have no severity graph", "clear all plan-review severity bindings")
			}
			continue
		}
		for j, severity := range canonicalFindingSeverities() {
			group := binding.Groups[j]
			if group.Severity != severity {
				return reviewErr("reviewRoundGraph.validate", fmt.Sprintf("axis %s severity slot %d is %s", axis, j, group.Severity),
					"implementation severity groups use canonical blocker, important, minor order", "persist the canonical severity order")
			}
			if err := recordTask(group.Task, fmt.Sprintf("axis %s severity %s", axis, severity)); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalFindingSeverities() [3]FindingSeverity {
	return [3]FindingSeverity{SeverityBlocker, SeverityImportant, SeverityMinor}
}

// ReviewRoundPlan is the deterministic shape of one review round: the round task, exactly
// three axis tasks, the eager severity groups for an implementation review, and every
// typed subject/contains/blocked-by edge. The reviewed task is blocked by the round; the
// round is blocked by its three axes; each implementation axis is blocked by its three
// severity groups. Batch-local handles are stable and canonical so a re-plan is identical.
type ReviewRoundPlan struct {
	ReviewedTask provenance.TaskID
	Subject      ReviewSubjectRef
	Kind         SubjectKind
	RoundHandle  string
	AxisHandles  [3]string
	Tasks        []PlannedTask
	Edges        []PlannedEdge
}

const reviewedTaskHandle = "reviewed-task"

// PlanReviewRound computes the deterministic review-round graph for a reviewed task and
// subject. kind must be SubjectPlan or SubjectImplementation; an implementation review
// eagerly adds the three severity groups per axis. It touches no journal.
func PlanReviewRound(reviewedTask provenance.TaskID, subject ReviewSubjectRef, kind SubjectKind) (ReviewRoundPlan, error) {
	if reviewedTask == (provenance.TaskID{}) {
		return ReviewRoundPlan{}, reviewErr("PlanReviewRound", "the reviewed task id is zero",
			"a review round must be rooted at a real reviewed task", "supply the reviewed task id")
	}
	if err := subject.validate(); err != nil {
		return ReviewRoundPlan{}, err
	}
	if !kind.valid() {
		return ReviewRoundPlan{}, reviewErr("PlanReviewRound", fmt.Sprintf("review kind %q is not plan or implementation", kind),
			"a review round is either a plan review or an implementation review", "pass SubjectPlan or SubjectImplementation")
	}

	plan := ReviewRoundPlan{
		ReviewedTask: reviewedTask,
		Subject:      subject,
		Kind:         kind,
		RoundHandle:  "review-round",
	}
	plan.Tasks = append(plan.Tasks, PlannedTask{Handle: plan.RoundHandle, Kind: ReviewTaskRound})
	// The reviewed task is blocked by the round; the round carries the subject relation.
	plan.Edges = append(plan.Edges,
		PlannedEdge{From: reviewedTaskHandle, To: plan.RoundHandle, Relation: RelationBlockedBy},
		PlannedEdge{From: plan.RoundHandle, To: reviewedTaskHandle, Relation: RelationSubject},
	)

	for i, axis := range canonicalReviewAxes() {
		axisHandle := "axis-" + axis.String()
		plan.AxisHandles[i] = axisHandle
		plan.Tasks = append(plan.Tasks, PlannedTask{Handle: axisHandle, Kind: ReviewTaskAxis, Axis: axis})
		// The round contains each axis and is blocked by each axis.
		plan.Edges = append(plan.Edges,
			PlannedEdge{From: plan.RoundHandle, To: axisHandle, Relation: RelationContains},
			PlannedEdge{From: plan.RoundHandle, To: axisHandle, Relation: RelationBlockedBy},
		)
		if kind == SubjectImplementation {
			for _, sev := range canonicalFindingSeverities() {
				groupHandle := axisHandle + ".group-" + sev.String()
				plan.Tasks = append(plan.Tasks, PlannedTask{
					Handle: groupHandle, Kind: ReviewTaskGroup, Axis: axis, Severity: sev,
				})
				// Each implementation axis is blocked by all three severity groups.
				plan.Edges = append(plan.Edges,
					PlannedEdge{From: axisHandle, To: groupHandle, Relation: RelationBlockedBy})
			}
		}
	}
	return plan, nil
}

// DocumentRevisionID/ImplementationCandidateID identify the two review-subject snapshots.
// ImplementationCandidateID is the const-string id wrapping the implementation candidate
// snapshot task; DocumentRevisionID (decision_ledger.go) plays the same role for documents.
type ImplementationCandidateID string

// ReviewSubjectKind is the closed discriminator of the review-subject sum.
type ReviewSubjectKind int

const (
	reviewSubjectInvalid ReviewSubjectKind = iota
	// ReviewSubjectDocumentRevision is a document-revision snapshot subject.
	ReviewSubjectDocumentRevision
	// ReviewSubjectImplementationCandidate is an implementation-candidate snapshot subject.
	ReviewSubjectImplementationCandidate
)

func (k ReviewSubjectKind) String() string {
	switch k {
	case ReviewSubjectDocumentRevision:
		return "document-revision"
	case ReviewSubjectImplementationCandidate:
		return "implementation-candidate"
	default:
		return fmt.Sprintf("ReviewSubjectKind(%d)", int(k))
	}
}

// ReviewSubjectRef is the closed sum-level reference {Kind, SnapshotID}. It is DERIVED from
// a concrete subject's Ref() — never independently supplied — so the kind and snapshot id
// cannot drift apart.
type ReviewSubjectRef struct {
	Kind       ReviewSubjectKind
	SnapshotID string
}

func (r ReviewSubjectRef) validate() error {
	switch r.Kind {
	case ReviewSubjectDocumentRevision, ReviewSubjectImplementationCandidate:
	default:
		return reviewErr("ReviewSubjectRef", fmt.Sprintf("unknown subject kind %d", int(r.Kind)),
			"a review subject is a document revision or an implementation candidate", "derive the ref from a concrete subject via Ref()")
	}
	if r.SnapshotID == "" {
		return reviewErr("ReviewSubjectRef", "the snapshot id is empty",
			"a subject ref wraps a non-empty snapshot task id", "derive the ref from a concrete subject via Ref()")
	}
	return nil
}

// ReviewSubject is the sealed sum of review subjects. Each variant has exactly one
// canonical id wrapping its snapshot TaskID and derives its own ReviewSubjectRef.
type ReviewSubject interface {
	// Ref derives the closed sum-level reference.
	Ref() ReviewSubjectRef
	reviewSubject()
}

// DocumentRevisionSubject snapshots a reviewed document revision.
type DocumentRevisionSubject struct {
	ID            DocumentRevisionID
	DocumentTask  provenance.TaskID
	ContentDigest [32]byte
}

func (DocumentRevisionSubject) reviewSubject() {}

// Ref derives the document-revision subject reference.
func (s DocumentRevisionSubject) Ref() ReviewSubjectRef {
	return ReviewSubjectRef{Kind: ReviewSubjectDocumentRevision, SnapshotID: string(s.ID)}
}

// ImplementationCandidateSubject snapshots a reviewed implementation candidate.
type ImplementationCandidateSubject struct {
	ID         ImplementationCandidateID
	SliceTask  provenance.TaskID
	Repository string
	CommitOID  provenance.GitOID
	TreeDigest [32]byte
}

func (ImplementationCandidateSubject) reviewSubject() {}

// Ref derives the implementation-candidate subject reference.
func (s ImplementationCandidateSubject) Ref() ReviewSubjectRef {
	return ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(s.ID)}
}

// ReviewRoundID identifies one review round.
type ReviewRoundID string

// SliceCloseAuthorization is the typed authorization a slice close requires: the governing
// supervisor's owner-responsibility assignment, the clean review round, the canonical
// candidate, and the three canonical review EventIDs (one per axis). It carries no caller
// actor override — closure derives the actor from the governing assignment.
type SliceCloseAuthorization struct {
	GoverningSupervisor provenance.AssignmentID
	ReviewRound         ReviewRoundID
	Candidate           ImplementationCandidateID
	ReviewEvents        [3]provenance.JournalID
}

// Validate rejects a malformed authorization: a missing supervisor/round/candidate, or
// review events that are not three distinct non-zero journal ids.
func (a SliceCloseAuthorization) Validate() error {
	if a.GoverningSupervisor == "" {
		return reviewErr("SliceCloseAuthorization", "the governing supervisor assignment is empty",
			"slice close is authorized by the governing IMPL_PLAN supervisor's owner-responsibility assignment",
			"supply the governing supervisor assignment id")
	}
	if a.ReviewRound == "" {
		return reviewErr("SliceCloseAuthorization", "the review round id is empty",
			"slice close requires the clean current review round", "supply the review round id")
	}
	if a.Candidate == "" {
		return reviewErr("SliceCloseAuthorization", "the candidate id is empty",
			"slice close requires the exact current candidate", "supply the candidate id")
	}
	seen := map[provenance.JournalID]bool{}
	for i, ev := range a.ReviewEvents {
		if ev == 0 {
			return reviewErr("SliceCloseAuthorization", fmt.Sprintf("review event %d is zero", i),
				"a clean close cites three canonical review events, one per axis", "supply three non-zero review event ids")
		}
		if seen[ev] {
			return reviewErr("SliceCloseAuthorization", fmt.Sprintf("review event %d (%d) is a duplicate", i, ev),
				"the three cited review events must be distinct (one per axis)", "supply three distinct review event ids")
		}
		seen[ev] = true
	}
	return nil
}

func reviewErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected a review projection: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("Review projection (internal/tasks/review_projection.go, %s).", where),
		Impact:   "The review projection was not constructed; nothing was written.",
		Fix:      fix + ".",
	}
}
