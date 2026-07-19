// Package tasks — pasture.* material-event definitions and journal effect mapping (#43 / S3.3 stage a).
//
// material_events.go is the authoritative, closed algebra of Pasture's domain
// MATERIAL events: the typed, per-family records the task backend folds into the
// provenance journal alongside (never instead of) the journal's own lifecycle
// events. A material event is a caller-domain record — assignment start/completion,
// review, UAT, skill-run, git-evidence, and the task-closed domain marker — that
// Pasture validates BEFORE Provenance ever sees it, then hands to the journal as an
// opaque, canonically-encoded EffectTaskEvent payload (§5.1, §9.3.1).
//
// Fixed-kind discipline (the user's Gate-2 precedent). Every family owns exactly one
// fixed, versioned EventKind constant; the kind is derived from the FAMILY alone
// (MaterialEventFamily.EventKind), never read from a payload field. There is no
// single payload-generalized "pasture.event" kind with a discriminator inside the
// blob — a review and an assignment-completion are different kinds because they are
// different families, and adding a family is a deliberate, compile-checked change to
// the closed switch below. The issue's display spelling "pasture.task.closed/v1" maps
// to the journal-legal kind "pasture.task.closed.v1": Provenance's namespaced-name
// grammar (validateNamespacedName) forbids '/', so the version is a trailing dotted
// component, not a slash suffix. The '/v1' intent is preserved exactly.
//
// Non-lifecycle by construction. A pasture.* kind is NOT one of Provenance's status-
// changing lifecycle/transition kinds, so folding a material event never runs the
// status FSM and never mutates task status — worker completion records
// pasture.assignment.completed and leaves the slice open, exactly as the issue
// requires. Only the authorized close policy (#49) mutates status.

package tasks

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// MaterialEventFamily is the closed set of pasture.* material-event families. It is
// the single discriminator the fixed-kind mapping switches on; every value has
// exactly one EventKind and exactly one typed payload shape. The zero value is a
// deliberately invalid sentinel so an uninitialised family cannot silently map to a
// real kind.
type MaterialEventFamily int

const (
	// materialEventInvalid is the zero sentinel; it maps to no kind and is rejected.
	materialEventInvalid MaterialEventFamily = iota
	// FamilyAssignmentStarted marks a Provenance-backed assignment episode opening
	// under a distinct owner/supervisor/reviewer role (§ "Metadata, work, and contexts").
	FamilyAssignmentStarted
	// FamilyAssignmentCompleted is a worker's typed completion record. It accompanies
	// EndTaskActivity and, per the issue, does NOT mutate task status.
	FamilyAssignmentCompleted
	// FamilyReviewRecorded records one axis's binary plan/implementation review outcome.
	FamilyReviewRecorded
	// FamilyUATRecorded records a plan or implementation UAT resolution.
	FamilyUATRecorded
	// FamilySkillRun records a skill-run material event bound to a task.
	FamilySkillRun
	// FamilyGitRemoteRefVerified is the land-time evidence that a repository's remote
	// ref was verified at an exact commit ("pasture.git.remote-ref-verified/v1").
	FamilyGitRemoteRefVerified
	// FamilyTaskClosed is the task-closed domain marker ("pasture.task.closed/v1").
	FamilyTaskClosed
)

// materialEventFamilies is the canonical, ordered list of every real family. It is
// the single source the exhaustive-mapping tests iterate, so a new family that is
// added to the const block but not here is caught by the round-trip test.
var materialEventFamilies = []MaterialEventFamily{
	FamilyAssignmentStarted,
	FamilyAssignmentCompleted,
	FamilyReviewRecorded,
	FamilyUATRecorded,
	FamilySkillRun,
	FamilyGitRemoteRefVerified,
	FamilyTaskClosed,
}

// EventKind returns the one fixed journal EventKind for this family. The kind is a
// total function of the family alone (never of any payload field), so the fixed-kind
// discipline cannot be subverted by data. materialEventInvalid and any unknown value
// return the empty kind, which MapMaterialEvent rejects.
func (f MaterialEventFamily) EventKind() provenance.EventKind {
	switch f {
	case FamilyAssignmentStarted:
		return "pasture.assignment.started.v1"
	case FamilyAssignmentCompleted:
		return "pasture.assignment.completed.v1"
	case FamilyReviewRecorded:
		return "pasture.review.recorded.v1"
	case FamilyUATRecorded:
		return "pasture.uat.recorded.v1"
	case FamilySkillRun:
		return "pasture.skill.run.v1"
	case FamilyGitRemoteRefVerified:
		return "pasture.git.remote-ref-verified.v1"
	case FamilyTaskClosed:
		return "pasture.task.closed.v1"
	default:
		return ""
	}
}

// String renders the family for diagnostics.
func (f MaterialEventFamily) String() string {
	if k := f.EventKind(); k != "" {
		return string(k)
	}
	return fmt.Sprintf("MaterialEventFamily(%d)", int(f))
}

// AssignmentRole is the closed set of Pasture assignment roles. It distinguishes the
// owner-responsibility, governing-parent supervisor, and axis-reviewer slots the
// issue names; it is a Pasture-level classification carried in the material payload
// and is distinct from any Provenance assignment slot id.
type AssignmentRole int

const (
	roleInvalid AssignmentRole = iota
	// RoleOwnerResponsibility is the atomic authority for Task.Owner.
	RoleOwnerResponsibility
	// RoleGoverningSupervisor is the governing-parent supervisor slot.
	RoleGoverningSupervisor
	// RoleAxisReviewer is one of the three axis reviewer slots.
	RoleAxisReviewer
)

func (r AssignmentRole) valid() bool {
	switch r {
	case RoleOwnerResponsibility, RoleGoverningSupervisor, RoleAxisReviewer:
		return true
	default:
		return false
	}
}

// String renders the role as its canonical payload token.
func (r AssignmentRole) String() string {
	switch r {
	case RoleOwnerResponsibility:
		return "owner-responsibility"
	case RoleGoverningSupervisor:
		return "governing-supervisor"
	case RoleAxisReviewer:
		return "axis-reviewer"
	default:
		return fmt.Sprintf("AssignmentRole(%d)", int(r))
	}
}

// SubjectKind is the closed review/UAT subject discriminator: a plan document or an
// implementation slice. The issue fixes review kind to exactly plan or implementation;
// UAT reuses the same two-valued distinction.
type SubjectKind int

const (
	subjectInvalid SubjectKind = iota
	// SubjectPlan is a plan-document review or plan UAT.
	SubjectPlan
	// SubjectImplementation is a slice/implementation review or implementation UAT.
	SubjectImplementation
)

func (k SubjectKind) valid() bool {
	return k == SubjectPlan || k == SubjectImplementation
}

func (k SubjectKind) String() string {
	switch k {
	case SubjectPlan:
		return "plan"
	case SubjectImplementation:
		return "implementation"
	default:
		return fmt.Sprintf("SubjectKind(%d)", int(k))
	}
}

// Verdict is the closed binary review/UAT verdict. The issue fixes review submit to
// one binary ACCEPT/REVISE and UAT to an accept/revise resolution; both reuse this
// two-valued enum rather than duplicating a per-context boolean.
type Verdict int

const (
	verdictInvalid Verdict = iota
	// VerdictAccept is the accepting disposition.
	VerdictAccept
	// VerdictRevise is the revising disposition.
	VerdictRevise
)

func (v Verdict) valid() bool {
	return v == VerdictAccept || v == VerdictRevise
}

func (v Verdict) String() string {
	switch v {
	case VerdictAccept:
		return "accept"
	case VerdictRevise:
		return "revise"
	default:
		return fmt.Sprintf("Verdict(%d)", int(v))
	}
}

// SkillOutcome is the closed outcome of a recorded skill run.
type SkillOutcome int

const (
	skillOutcomeInvalid SkillOutcome = iota
	// SkillSucceeded marks a skill run that completed successfully.
	SkillSucceeded
	// SkillFailed marks a skill run that failed.
	SkillFailed
)

func (o SkillOutcome) valid() bool {
	return o == SkillSucceeded || o == SkillFailed
}

func (o SkillOutcome) String() string {
	switch o {
	case SkillSucceeded:
		return "succeeded"
	case SkillFailed:
		return "failed"
	default:
		return fmt.Sprintf("SkillOutcome(%d)", int(o))
	}
}

// MaterialEvent is the sealed sum of every pasture.* material event. Concrete family
// structs live in this package and cannot be added from outside (the unexported
// isMaterialEvent marker seals the set), so the exhaustive mapping switch is total
// against a fixed, compile-checked universe. Every implementation is validated and
// mapped to exactly one journal Effect by MapMaterialEvent.
type MaterialEvent interface {
	// Family returns the closed family discriminator; the fixed EventKind derives
	// from it alone.
	Family() MaterialEventFamily
	// SourceTask is the task the event is authorized against and attributed to (§9.3);
	// it is the effect's TaskID.
	SourceTask() provenance.TaskID
	// validate rejects a malformed payload before any journal encoding.
	validate() error
	// canonicalPayload returns the deterministic, opaque payload bytes Provenance
	// stores without interpretation.
	canonicalPayload() (json.RawMessage, error)
	// contexts returns the validated secondary event contexts for this event.
	contexts() ([]provenance.EventContext, error)
	// isMaterialEvent seals the interface to this package.
	isMaterialEvent()
}

// AssignmentStartedEvent records a Provenance-backed assignment episode opening. It
// carries the assignment id, the distinct role, and the occupant actor; the
// occupant is journaled both in the payload and as an actor context.
type AssignmentStartedEvent struct {
	Task       provenance.TaskID
	Assignment provenance.AssignmentID
	Role       AssignmentRole
	Occupant   provenance.ActorID
}

func (AssignmentStartedEvent) Family() MaterialEventFamily     { return FamilyAssignmentStarted }
func (e AssignmentStartedEvent) SourceTask() provenance.TaskID { return e.Task }
func (AssignmentStartedEvent) isMaterialEvent()                {}

func (e AssignmentStartedEvent) validate() error {
	if err := validateTaskPresent("AssignmentStartedEvent.Task", e.Task); err != nil {
		return err
	}
	if !e.Role.valid() {
		return invalidFieldErr("AssignmentStartedEvent.Role", e.Role.String(), "a known assignment role")
	}
	return validateActorPresent("AssignmentStartedEvent.Occupant", e.Occupant)
}

func (e AssignmentStartedEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		Assignment string `json:"assignment"`
		Role       string `json:"role"`
		Occupant   string `json:"occupant"`
	}{
		Assignment: string(e.Assignment),
		Role:       e.Role.String(),
		Occupant:   e.Occupant.String(),
	})
}

func (e AssignmentStartedEvent) contexts() ([]provenance.EventContext, error) {
	return buildContexts(taskCtx(e.Task), actorCtx(e.Occupant))
}

// AssignmentCompletedEvent is a worker's typed completion record. It accompanies
// EndTaskActivity and never mutates task status. It carries the assignment and the
// activity it ended, plus the completing occupant.
type AssignmentCompletedEvent struct {
	Task       provenance.TaskID
	Assignment provenance.AssignmentID
	Activity   provenance.ActivityID
	Occupant   provenance.ActorID
}

func (AssignmentCompletedEvent) Family() MaterialEventFamily     { return FamilyAssignmentCompleted }
func (e AssignmentCompletedEvent) SourceTask() provenance.TaskID { return e.Task }
func (AssignmentCompletedEvent) isMaterialEvent()                {}

func (e AssignmentCompletedEvent) validate() error {
	if err := validateTaskPresent("AssignmentCompletedEvent.Task", e.Task); err != nil {
		return err
	}
	if e.Assignment == "" {
		return invalidFieldErr("AssignmentCompletedEvent.Assignment", "", "a non-empty assignment id")
	}
	return validateActorPresent("AssignmentCompletedEvent.Occupant", e.Occupant)
}

func (e AssignmentCompletedEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		Assignment string `json:"assignment"`
		Activity   string `json:"activity"`
		Occupant   string `json:"occupant"`
	}{
		Assignment: string(e.Assignment),
		Activity:   e.Activity.String(),
		Occupant:   e.Occupant.String(),
	})
}

func (e AssignmentCompletedEvent) contexts() ([]provenance.EventContext, error) {
	ctxs := []ctxBuilder{taskCtx(e.Task), actorCtx(e.Occupant)}
	if e.Activity != (provenance.ActivityID{}) {
		ctxs = append(ctxs, activityCtx(e.Activity))
	}
	return buildContexts(ctxs...)
}

// ReviewRecordedEvent records one axis's binary plan/implementation review outcome
// against a reviewed task.
type ReviewRecordedEvent struct {
	ReviewedTask provenance.TaskID
	AxisTask     provenance.TaskID
	Kind         SubjectKind
	Verdict      Verdict
}

func (ReviewRecordedEvent) Family() MaterialEventFamily     { return FamilyReviewRecorded }
func (e ReviewRecordedEvent) SourceTask() provenance.TaskID { return e.ReviewedTask }
func (ReviewRecordedEvent) isMaterialEvent()                {}

func (e ReviewRecordedEvent) validate() error {
	if err := validateTaskPresent("ReviewRecordedEvent.ReviewedTask", e.ReviewedTask); err != nil {
		return err
	}
	if err := validateTaskPresent("ReviewRecordedEvent.AxisTask", e.AxisTask); err != nil {
		return err
	}
	if !e.Kind.valid() {
		return invalidFieldErr("ReviewRecordedEvent.Kind", e.Kind.String(), "plan or implementation")
	}
	if !e.Verdict.valid() {
		return invalidFieldErr("ReviewRecordedEvent.Verdict", e.Verdict.String(), "accept or revise")
	}
	return nil
}

func (e ReviewRecordedEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		AxisTask string `json:"axis_task"`
		Kind     string `json:"kind"`
		Verdict  string `json:"verdict"`
	}{
		AxisTask: e.AxisTask.String(),
		Kind:     e.Kind.String(),
		Verdict:  e.Verdict.String(),
	})
}

func (e ReviewRecordedEvent) contexts() ([]provenance.EventContext, error) {
	return buildContexts(taskCtx(e.ReviewedTask), taskCtx(e.AxisTask))
}

// UATRecordedEvent records a plan or implementation UAT resolution against a subject
// task.
type UATRecordedEvent struct {
	Subject    provenance.TaskID
	Kind       SubjectKind
	Resolution Verdict
}

func (UATRecordedEvent) Family() MaterialEventFamily     { return FamilyUATRecorded }
func (e UATRecordedEvent) SourceTask() provenance.TaskID { return e.Subject }
func (UATRecordedEvent) isMaterialEvent()                {}

func (e UATRecordedEvent) validate() error {
	if err := validateTaskPresent("UATRecordedEvent.Subject", e.Subject); err != nil {
		return err
	}
	if !e.Kind.valid() {
		return invalidFieldErr("UATRecordedEvent.Kind", e.Kind.String(), "plan or implementation")
	}
	if !e.Resolution.valid() {
		return invalidFieldErr("UATRecordedEvent.Resolution", e.Resolution.String(), "accept or revise")
	}
	return nil
}

func (e UATRecordedEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		Kind       string `json:"kind"`
		Resolution string `json:"resolution"`
	}{
		Kind:       e.Kind.String(),
		Resolution: e.Resolution.String(),
	})
}

func (e UATRecordedEvent) contexts() ([]provenance.EventContext, error) {
	return buildContexts(taskCtx(e.Subject))
}

// SkillRunEvent records a skill-run material event bound to a task and an actor.
type SkillRunEvent struct {
	Task    provenance.TaskID
	Skill   string
	Actor   provenance.ActorID
	Outcome SkillOutcome
}

func (SkillRunEvent) Family() MaterialEventFamily     { return FamilySkillRun }
func (e SkillRunEvent) SourceTask() provenance.TaskID { return e.Task }
func (SkillRunEvent) isMaterialEvent()                {}

func (e SkillRunEvent) validate() error {
	if err := validateTaskPresent("SkillRunEvent.Task", e.Task); err != nil {
		return err
	}
	if e.Skill == "" {
		return invalidFieldErr("SkillRunEvent.Skill", "", "a non-empty skill identifier")
	}
	if !e.Outcome.valid() {
		return invalidFieldErr("SkillRunEvent.Outcome", e.Outcome.String(), "succeeded or failed")
	}
	return validateActorPresent("SkillRunEvent.Actor", e.Actor)
}

func (e SkillRunEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		Skill   string `json:"skill"`
		Actor   string `json:"actor"`
		Outcome string `json:"outcome"`
	}{
		Skill:   e.Skill,
		Actor:   e.Actor.String(),
		Outcome: e.Outcome.String(),
	})
}

func (e SkillRunEvent) contexts() ([]provenance.EventContext, error) {
	return buildContexts(taskCtx(e.Task), actorCtx(e.Actor))
}

// GitRemoteRefVerifiedEvent is the land-time evidence that a repository's remote ref
// was verified at an exact commit. It is the typed pasture.git.remote-ref-verified/v1
// event #49's land handler requires (one per repository).
type GitRemoteRefVerifiedEvent struct {
	Task       provenance.TaskID
	Repository string
	Ref        string
	CommitOID  provenance.GitOID
}

func (GitRemoteRefVerifiedEvent) Family() MaterialEventFamily     { return FamilyGitRemoteRefVerified }
func (e GitRemoteRefVerifiedEvent) SourceTask() provenance.TaskID { return e.Task }
func (GitRemoteRefVerifiedEvent) isMaterialEvent()                {}

func (e GitRemoteRefVerifiedEvent) validate() error {
	if err := validateTaskPresent("GitRemoteRefVerifiedEvent.Task", e.Task); err != nil {
		return err
	}
	if e.Repository == "" {
		return invalidFieldErr("GitRemoteRefVerifiedEvent.Repository", "", "a non-empty repository identifier")
	}
	if e.Ref == "" {
		return invalidFieldErr("GitRemoteRefVerifiedEvent.Ref", "", "a non-empty git ref")
	}
	// GitContext validates the object-id grammar; surface a validation failure early.
	if _, err := provenance.GitContext(e.CommitOID); err != nil {
		return invalidFieldErr("GitRemoteRefVerifiedEvent.CommitOID", string(e.CommitOID),
			"a canonical lower-case SHA-1 (40 hex) or SHA-256 (64 hex) git object id")
	}
	return nil
}

func (e GitRemoteRefVerifiedEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		Repository string `json:"repository"`
		Ref        string `json:"ref"`
		CommitOID  string `json:"commit_oid"`
	}{
		Repository: e.Repository,
		Ref:        e.Ref,
		CommitOID:  string(e.CommitOID),
	})
}

func (e GitRemoteRefVerifiedEvent) contexts() ([]provenance.EventContext, error) {
	return buildContexts(taskCtx(e.Task), gitCtx(e.CommitOID))
}

// TaskClosedEvent is the task-closed domain marker (pasture.task.closed/v1). It is a
// non-lifecycle domain record; the authorized close policy separately drives the
// journal's own status transition.
type TaskClosedEvent struct {
	Task   provenance.TaskID
	Reason string
}

func (TaskClosedEvent) Family() MaterialEventFamily     { return FamilyTaskClosed }
func (e TaskClosedEvent) SourceTask() provenance.TaskID { return e.Task }
func (TaskClosedEvent) isMaterialEvent()                {}

func (e TaskClosedEvent) validate() error {
	return validateTaskPresent("TaskClosedEvent.Task", e.Task)
}

func (e TaskClosedEvent) canonicalPayload() (json.RawMessage, error) {
	return canonicalJSON(struct {
		Reason string `json:"reason"`
	}{Reason: e.Reason})
}

func (e TaskClosedEvent) contexts() ([]provenance.EventContext, error) {
	return buildContexts(taskCtx(e.Task))
}

// MapMaterialEvent validates a material event and maps it to exactly one journal
// EffectTaskEvent: the family's fixed EventKind, the event's source task, its opaque
// canonical payload, and its validated secondary contexts. Family() is a method on
// each sealed concrete type and returns a compile-time constant, so a value cannot
// carry a family that disagrees with its payload shape; a family that fails to
// resolve to a legal journal kind (the zero/unknown sentinel, or a fixed kind that
// somehow breaks the journal grammar) is rejected rather than journaled under a
// wrong or empty kind. This is the single place the portable material-event domain
// and the durable journal domain meet.
func MapMaterialEvent(ev MaterialEvent) (provenance.Effect, error) {
	if ev == nil {
		return provenance.Effect{}, materialEventErr("",
			"the material event is nil",
			"the closed material-event algebra has no nil member",
			"pass a constructed material event value")
	}
	family := ev.Family()
	kind := family.EventKind()
	if kind == "" {
		return provenance.Effect{}, materialEventErr(family.String(),
			fmt.Sprintf("the material event reports unknown family %d", int(family)),
			"only the closed set of families has a fixed journal kind",
			"construct one of the exported material-event types")
	}
	if err := provenance.ValidateEventKind(kind); err != nil {
		// A fixed kind that fails the journal grammar is a programming error in this file.
		return provenance.Effect{}, materialEventErr(string(kind),
			fmt.Sprintf("the fixed kind %q is not a legal journal event kind: %v", kind, err),
			"every family's fixed kind must satisfy the journal namespaced-name grammar",
			"correct the family's EventKind constant")
	}
	if err := ev.validate(); err != nil {
		return provenance.Effect{}, err
	}

	source := ev.SourceTask()
	payload, err := ev.canonicalPayload()
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("map material event %q: encode payload: %w", kind, err)
	}
	contexts, err := ev.contexts()
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("map material event %q: build contexts: %w", kind, err)
	}

	return provenance.Effect{
		Sort:      provenance.EffectTaskEvent,
		TaskID:    source,
		EventKind: kind,
		Payload:   payload,
		Contexts:  contexts,
	}, nil
}

// canonicalJSON renders v to deterministic, HTML-unescaped JSON bytes. Struct field
// order is stable and no maps are used, so the encoding is a pure function of the
// value — a golden-comparable canonical payload.
func canonicalJSON(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder appends a trailing newline; trim it so the canonical bytes are the
	// exact JSON value with no incidental whitespace.
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// ctxBuilder is a deferred, error-returning event-context constructor. Collecting
// builders lets each family declare its contexts declaratively and buildContexts run
// them, canonicalise the result, and surface the first construction failure.
type ctxBuilder func() (provenance.EventContext, error)

func taskCtx(id provenance.TaskID) ctxBuilder {
	return func() (provenance.EventContext, error) { return provenance.TaskContext(id) }
}
func actorCtx(id provenance.ActorID) ctxBuilder {
	return func() (provenance.EventContext, error) { return provenance.ActorContext(id) }
}
func activityCtx(id provenance.ActivityID) ctxBuilder {
	return func() (provenance.EventContext, error) { return provenance.ActivityContext(id) }
}
func gitCtx(id provenance.GitOID) ctxBuilder {
	return func() (provenance.EventContext, error) { return provenance.GitContext(id) }
}

// buildContexts runs each builder and returns the canonicalised (deduped, sorted)
// context set the journal stores.
func buildContexts(builders ...ctxBuilder) ([]provenance.EventContext, error) {
	out := make([]provenance.EventContext, 0, len(builders))
	for _, b := range builders {
		ctx, err := b()
		if err != nil {
			return nil, err
		}
		out = append(out, ctx)
	}
	return provenance.CanonicalEventContexts(out)
}

// validateTaskPresent rejects a zero task id, which no material event may reference.
func validateTaskPresent(field string, id provenance.TaskID) error {
	if id == (provenance.TaskID{}) {
		return invalidFieldErr(field, "", "a non-zero task id")
	}
	return nil
}

// validateActorPresent rejects a zero actor id.
func validateActorPresent(field string, id provenance.ActorID) error {
	if id == (provenance.ActorID{}) {
		return invalidFieldErr(field, "", "a non-zero actor id")
	}
	return nil
}

func invalidFieldErr(field, got, want string) error {
	return materialEventErr(field,
		fmt.Sprintf("%s is %q", field, got),
		fmt.Sprintf("%s must be %s", field, want),
		fmt.Sprintf("set %s to %s before recording the event", field, want))
}

func materialEventErr(subject, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected a material event: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("Mapping a pasture.* material event to a journal effect (internal/tasks/material_events.go, subject %q).", subject),
		Impact:   "The event is not journaled; nothing was written.",
		Fix:      fix + ".",
	}
}
