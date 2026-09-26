// gate_reader.go is the read side of the gate decision: one snapshot of who
// holds which assignment episode, under which session claim.
//
// A lifecycle hook asks the gate "may this actor act now?". This file answers
// that question from the store and nothing else. It reads, it never writes, and
// it never runs a policy: the decision is taken by the gate policy from the
// value this file seals.
//
// The read has two halves with different owners. The SESSION CLAIM is a
// Pasture-owned table (pasture_session_claim), so it is read with Pasture's own
// SQL under Pasture's own busy tier. The ACTIVE OWNERSHIP is Provenance's, so it
// is read with exactly one call into Provenance's public query API. That single
// call is the whole read-side contract: no paging loop, no catch-up tail, no
// completeness certificate, and no private SQL against Provenance's tables.
//
// The claim connection is RELEASED before the Provenance call, because both
// subsystems borrow the same *sql.DB (open_unified.go, the Provenance open) and
// the default pool is one connection. A public call issued while Pasture still
// held its claim lease would queue behind the connection the reader itself
// holds, and the borrowed handle's liveness precheck pings with a background
// context, so nothing in the caller's context ends that wait.
//
// The release is a MECHANISM, not a guarantee, and the two-store read it belongs
// to has a semantic cost that is stated at the read itself in Snapshot: two
// reads, two snapshots, one decision.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// NewGateReader returns the reader the lifecycle gate consults. harness and
// session are the identity the hook was invoked with; the reader answers about
// exactly that session and no other.
//
// The tracker must be the unified Pasture store. A reader that cannot reach the
// Pasture-owned claim table would answer about ownership without knowing who it
// is answering for, so construction refuses such a tracker instead of deferring
// the fault to the first lifecycle invocation.
func NewGateReader(tracker protocol.TaskTracker, harness ir.HarnessID, session string) (gateauthority.Reader, error) {
	store, ok := tracker.(*trackerImpl)
	if !ok || store == nil || store.auditDB == nil {
		return nil, &GateReaderScopeError{Operation: "open a gate reader over a store that is not the unified Pasture task store"}
	}
	return &gateReader{tracker: store, harness: harness, session: session}, nil
}

// gateReader is the concrete reader. Every field is fixed at construction; the
// only mutable state is the test-only hook below.
type gateReader struct {
	tracker *trackerImpl
	harness ir.HarnessID
	session string

	// afterClaimRead runs after the claim read has been handled and before an
	// UNBOUND result is sealed. It is nil in production, and a source pin proves
	// no non-test file assigns it. It exists because the unbound path makes no
	// journal call, so a wrapping journal cannot observe that boundary; a test
	// needs one deterministic place to place a concurrent claim INSERT.
	//
	// It fires on BOTH unbound arms, and that is deliberate rather than a
	// leftover: the two arms are the same fact (this invocation has no claim to
	// read) reached two ways — an event that carried no session identity, and a
	// session with no claim row. A test proving the seam wants the claim read to
	// be over and its connection released, and that is true of either arm, so one
	// hook serves both rather than leaving the no-session arm unobservable.
	afterClaimRead func()
}

// GateReadStage names the read step that failed. It is a closed set because a
// caller acts on the two differently: a claim-stage fault means the session
// could not be identified at all, and an ownership-stage fault means the
// identity was known and its episodes could not be read.
type GateReadStage uint8

const (
	// gateReadStageUnset is the invalid zero.
	gateReadStageUnset GateReadStage = iota
	// GateReadStageClaim is the Pasture-owned session-claim read.
	GateReadStageClaim
	// GateReadStageOwnership is the Provenance-owned active-ownership read.
	GateReadStageOwnership
)

// String renders the stage as the operator-facing token.
func (s GateReadStage) String() string {
	switch s {
	case GateReadStageClaim:
		return "gate claim read"
	case GateReadStageOwnership:
		return "gate ownership read"
	case gateReadStageUnset:
		return "gate-read-stage-unset"
	default:
		return fmt.Sprintf("GateReadStage(%d)", uint8(s))
	}
}

// GateReadError is a read fault that is not an integrity fault: the store was
// reachable but could not answer, the context ended, or the query was refused.
// It is never a policy denial.
type GateReadError struct {
	Stage   GateReadStage
	Harness ir.HarnessID
	Session string
	Cause   error
}

func (e *GateReadError) Error() string {
	return fmt.Sprintf(
		"Pasture could not read the gate facts (stage %s) for session %q on harness %q: %v. "+
			"Why: the gate reads the session claim and the actor's active ownership before it decides, and this read did not complete. "+
			"Where: internal/tasks/gate_reader.go, in Snapshot, at the %s step. "+
			"When: during the lifecycle invocation that consulted the gate, before any decision was taken. "+
			"Impact: the invocation reported a fault, so the gate decided nothing and nothing was written; under the default policy the action continued unjudged, and on an evidenced blocking host with PASTURE_HOOK_FAIL_CLOSED=1 the host refused the action instead. "+
			"Fix: retry the invocation once the store is reachable; if the read keeps failing, release other writers on the database file, then run `pasture migrate` to confirm the file is at the expected schema version.",
		e.Stage, e.Session, e.Harness, e.Cause, e.Stage,
	)
}

// Unwrap keeps the cause reachable, so errors.Is finds the context and store
// sentinels and errors.As finds a typed store or driver error.
func (e *GateReadError) Unwrap() error { return e.Cause }

// GateReadIntegrityError is a read fault caused by store contents that a
// supported writer cannot produce. Stage carries the Provenance read stage, so
// the operator's line names which part of the result was damaged.
//
// A ZERO Stage means the store read named none: the fault is a subtype fault
// that reached this boundary without the typed carrier that carries the stage,
// and the reader reports it as unreported rather than naming one of the three
// stages on the store's behalf. The field holds no Provenance stage in that case
// rather than a made-up one, so a programmatic reader sees "none" and an
// operator reads "unreported".
type GateReadIntegrityError struct {
	Stage provenance.ActorOwnershipStage
	Cause error
}

func (e *GateReadIntegrityError) Error() string {
	return fmt.Sprintf(
		"Pasture could not read the gate facts (stage gate ownership read (stage %s)): %v. "+
			"Why: the store holds ownership facts that disagree with each other, or a result larger than the reader's byte bound; a supported writer cannot produce either. "+
			"Where: internal/tasks/gate_reader.go, in Snapshot, at the Provenance active-ownership read. "+
			"When: during the lifecycle invocation that consulted the gate, before any decision was taken. "+
			"Impact: the invocation reported a fault, so no episode was reported and no decision was taken; under the default policy the action continued unjudged, and on an evidenced blocking host with PASTURE_HOOK_FAIL_CLOSED=1 the host refused the action instead. "+
			"Fix: run Journal.VerifyIntegrity and Journal.ReplayProjections on this store to find the damaged rows; both only read; then restore the database file from a known-good copy.",
		reportedStage(e.Stage), e.Cause,
	)
}

// reportedStage renders the stage the operator is told, and says so honestly
// when the store read named none. A subtype fault can reach the reader without
// the typed carrier, so "unreported" is a reachable answer and naming one of the
// three stages there would point the operator at damage the read never located.
func reportedStage(stage provenance.ActorOwnershipStage) string {
	if stage == "" {
		return "unreported"
	}
	return string(stage)
}

// Unwrap keeps the Provenance limit, projection-mismatch and subtype causes
// reachable through errors.As, and their sentinels through errors.Is.
func (e *GateReadIntegrityError) Unwrap() error { return e.Cause }

// GateClaimMalformedError is a fault on a claim row that exists but whose actor
// is not an actor identifier. A malformed claim is never downgraded to an
// unbound or an unknown session, because both of those are policy answers and
// this is a store fault.
type GateClaimMalformedError struct {
	Harness ir.HarnessID
	Session string
	Cause   error
}

func (e *GateClaimMalformedError) Error() string {
	return fmt.Sprintf(
		"Pasture could not read the actor claimed for session %q on harness %q: %v. "+
			"Why: the stored claim row carries an actor value that is not a canonical actor identifier, so the session cannot be attributed to any actor. "+
			"Where: internal/tasks/session_claim.go, reading pasture_session_claim for the gate claim read. "+
			"When: during the lifecycle invocation that consulted the gate, before any decision was taken. "+
			"Impact: the invocation reported a fault, so the session could not be attributed and nothing was written; under the default policy the action continued unjudged, and on an evidenced blocking host with PASTURE_HOOK_FAIL_CLOSED=1 the host refused the action instead. "+
			"Fix: inspect the row with `SELECT harness, session, actor FROM pasture_session_claim`, then start a new session so the host writes a fresh claim.",
		e.Session, e.Harness, e.Cause,
	)
}

// Unwrap keeps the parse cause reachable through errors.As.
func (e *GateClaimMalformedError) Unwrap() error { return e.Cause }

// GateReaderCapabilityError is a build-time fault: the store's journal does not
// carry the public active-ownership query. There is no fallback read, because a
// second read path would answer the gate from a different source than the one
// this design binds it to.
type GateReaderCapabilityError struct {
	Missing string
}

func (e *GateReaderCapabilityError) Error() string {
	return fmt.Sprintf(
		"Pasture could not read the gate facts because this build's store does not offer %q. "+
			"Why: the gate reads active ownership through one public store capability, and a store without it has no supported way to answer. "+
			"Where: internal/tasks/gate_reader.go, in Snapshot, at the active-ownership read. "+
			"When: during the lifecycle invocation that consulted the gate, before any decision was taken. "+
			"Impact: the invocation reported a fault, so no decision was taken; under the default policy the action continued unjudged, and on an evidenced blocking host with PASTURE_HOOK_FAIL_CLOSED=1 the host refused the action instead. "+
			"Fix: upgrade pasture to a build that includes the active-ownership read, then retry the invocation.",
		e.Missing,
	)
}

// GateRoleSourceError is a fault on the role of one owned episode. The episode
// exists, but the role that decides whether the actor may act was written by
// no source the reader accepts.
type GateRoleSourceError struct {
	Task       provenance.TaskID
	Assignment provenance.AssignmentID
	Reason     string
}

func (e *GateRoleSourceError) Error() string {
	return fmt.Sprintf(
		"Pasture could not establish the role of assignment %q on task %q: %s. "+
			"Why: the episode is active, but its role is written by no assignment-start record and no accepted command record the reader can read. "+
			"Where: internal/tasks/gate_reader.go, in Snapshot, while mapping an owned task into an episode. "+
			"When: during the lifecycle invocation that consulted the gate, before any decision was taken. "+
			"Impact: the invocation reported a fault, so no decision was taken; under the default policy the action continued unjudged, and on an evidenced blocking host with PASTURE_HOOK_FAIL_CLOSED=1 the host refused the action instead. "+
			"Fix: read the assignment's own start fact and command evidence in the store, correct the missing or malformed record, then retry the invocation.",
		e.Assignment, e.Task, e.Reason,
	)
}

// GateReaderScopeError is API misuse: the reader was asked something outside the
// one session and one actor it was bound to, or it was asked without a context.
type GateReaderScopeError struct {
	Operation string
}

func (e *GateReaderScopeError) Error() string {
	return fmt.Sprintf(
		"Pasture refused to %s. "+
			"Why: one gate reader answers for exactly the session and the actor it was opened with; any other question would read facts this snapshot does not hold. "+
			"Where: internal/tasks/gate_reader.go, in Snapshot and in the sealed snapshot's accessors. "+
			"When: at the call site that asked the question. "+
			"Impact: nothing was read and nothing was decided; this is a wiring fault, not a policy decision. "+
			"Fix: open a reader with the harness and session of the invocation, and ask it only about the actor it resolved for that session.",
		e.Operation,
	)
}

// GateSnapshotClosedError is a lifecycle fault on an already-sealed snapshot.
// The reader released everything it held when it was closed, so it has nothing
// left to answer with.
type GateSnapshotClosedError struct{}

func (e *GateSnapshotClosedError) Error() string {
	return "Pasture could not answer the gate from a closed snapshot. " +
		"Why: closing a gate snapshot releases everything the read held, so a later question has no facts behind it. " +
		"Where: internal/tasks/gate_reader.go, in the sealed snapshot's accessors. " +
		"When: at the call site that asked after the snapshot was closed. " +
		"Impact: nothing was read and nothing was decided; this is a lifecycle fault on the caller, not a policy decision. " +
		"Fix: take one snapshot per lifecycle invocation, answer every question from it, and close it only once that invocation has finished."
}

// gateSnapshotValue is the sealed, immutable answer. Every field is filled
// before the value is handed out, so a reader of it never observes a partial
// read. The mutex guards only the closed flag: Close may be called twice and
// concurrently with a question, and both callers must get a defined answer.
type gateSnapshotValue struct {
	claim     gateauthority.SessionClaim
	authority gateauthority.ActorAuthority
	// through is the journal id the ownership read observed. It is the
	// instant this snapshot describes; it is kept so a future caller can say
	// which journal state it decided on, and it is never read from the
	// current store.
	through provenance.JournalID

	harness ir.HarnessID
	session string
	actor   provenance.ActorID

	mu     sync.Mutex
	closed bool
}

var _ gateauthority.Snapshot = (*gateSnapshotValue)(nil)

// Snapshot reads the gate facts once.
//
// The read is exactly two steps against two different owners: one Pasture-owned
// claim read, and at most one Provenance-owned ownership read. There is no
// paging, no catch-up loop and no completeness certificate, because a single
// read transaction answers both questions or neither.
//
// An actor that owns no task is not an error. It is a complete fact with an
// empty episode list, and the policy answers it.
func (r *gateReader) Snapshot(ctx context.Context) (gateauthority.Snapshot, error) {
	if ctx == nil {
		return nil, &GateReaderScopeError{Operation: "take a gate snapshot without a context"}
	}
	if r.tracker == nil || r.tracker.auditDB == nil {
		return nil, &GateReaderScopeError{Operation: "take a gate snapshot over a store that is not the unified Pasture task store"}
	}
	// An event that carried no session identity is UNBOUND at once, with no
	// SQL: there is no session to look up.
	if r.session == "" {
		return r.sealUnbound(), nil
	}
	claimed, present, err := r.tracker.readLifecycleSessionClaim(ctx, r.harness, r.session)
	if err != nil {
		return nil, &GateReadError{Stage: GateReadStageClaim, Harness: r.harness, Session: r.session, Cause: err}
	}
	if !present {
		return r.sealUnbound(), nil
	}
	actor, err := provenance.ParseActorID(claimed)
	if err != nil {
		return nil, &GateClaimMalformedError{Harness: r.harness, Session: r.session, Cause: err}
	}
	// TWO STORES, TWO SNAPSHOTS, ONE DECISION. The claim read above saw
	// pasture_session_claim as of its own instant, and the ownership read below
	// sees Provenance's facts as of a later one, with no shared fence between the
	// two reads and no transaction spanning them. A claim or a transfer committed
	// in that window is invisible to this snapshot, so the gate can answer from a
	// pair of facts that never coexisted: a session whose actor has since lost
	// the assignment it is being asked about, or an assignment gained after the
	// claim was read. That is the accepted cost of two owners, it is why the
	// snapshot is a whole answer and not a transaction, and it is the reason the
	// claim connection is released before the public call rather than held across
	// it: holding it would trade this window for a deadlock, not for a fence.
	api, ok := r.tracker.Journal().(provenance.ActorOwnershipQueryAPI)
	if !ok {
		return nil, &GateReaderCapabilityError{Missing: "provenance.ActorOwnershipQueryAPI"}
	}
	// The invocation context bounds the ownership read, not a Pasture tier: the
	// read is Provenance's, and a Provenance read has no Pasture deadline to
	// borrow.
	ownership, err := api.QueryActorOwnership(ctx, provenance.ActorOwnershipQuery{
		Actor:         actor,
		MaterialKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		EvidenceKinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind},
	})
	if err != nil {
		return nil, ownershipReadFault(err, r.harness, r.session)
	}
	episodes := make([]gateauthority.Episode, 0, len(ownership.Tasks))
	for _, row := range ownership.Tasks {
		episode, err := ownedTaskEpisode(actor, row)
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, episode)
	}
	return &gateSnapshotValue{
		claim: gateauthority.SessionClaim{
			Bound:   true,
			Actor:   actor,
			Session: r.session,
			Known:   ownership.ActorKnown,
		},
		authority: gateauthority.ActorAuthority{Actor: actor, Episodes: episodes},
		through:   ownership.Through,
		harness:   r.harness,
		session:   r.session,
		actor:     actor,
	}, nil
}

// sealUnbound seals the answer for a session that claimed no actor. It is a
// complete fact, not a fault and not an unknown actor: the gate proceeds and
// records that the session was unbound.
func (r *gateReader) sealUnbound() *gateSnapshotValue {
	// The claim read and its connection have been handled by the time this
	// runs. The hook exists so an in-process test can place a concurrent claim
	// INSERT at exactly this boundary.
	if r.afterClaimRead != nil {
		r.afterClaimRead()
	}
	return &gateSnapshotValue{
		claim:     gateauthority.SessionClaim{Bound: false, Session: r.session},
		authority: gateauthority.ActorAuthority{},
		harness:   r.harness,
		session:   r.session,
	}
}

// ownershipReadFault maps a Provenance ownership read error onto the Pasture
// kind that says what the operator should do about it.
//
// Three Provenance outcomes are integrity faults: the result byte bound, a
// disagreement between the owner projection and the active episode, and a
// journal row that violates its class table. Everything else — a dead context,
// refused SQL, an unavailable store — is a read fault. The split is by cause,
// not by message, so a wrapped store error stays a read fault.
//
// The STAGE an integrity fault reports is the store's answer, not a Pasture
// guess. Two of the three Provenance fault types carry it: the byte bound in
// ActorOwnershipLimitError.Stage, and the subtype fault in
// ActorOwnershipIntegrityError.Stage, which Provenance sets at every one of its
// thirteen raise sites to the stage of the function the site is written in —
// seven owned-tasks, three materials, three evidence. The third type,
// OwnerProjectionMismatchError, carries no stage field, and its own text and
// both of its raise sites pin it to the owned-tasks stage, so that is stated
// here rather than read from the fault.
func ownershipReadFault(err error, harness ir.HarnessID, session string) error {
	var limit *provenance.ActorOwnershipLimitError
	if errors.As(err, &limit) {
		return &GateReadIntegrityError{Stage: limit.Stage, Cause: err}
	}
	var mismatch *provenance.OwnerProjectionMismatchError
	if errors.As(err, &mismatch) {
		return &GateReadIntegrityError{Stage: provenance.ActorOwnershipStageOwnedTasks, Cause: err}
	}
	if errors.Is(err, provenance.ErrSubtypeIntegrity) {
		// A subtype fault carries its own stage, in
		// ActorOwnershipIntegrityError.Stage. It is asked for here, not assumed:
		// the read refuses rows at three stages and a hardcoded stage would send
		// the operator to the owned-task rows for damage the materials or
		// evidence rows rejected.
		var subtype *provenance.ActorOwnershipIntegrityError
		if errors.As(err, &subtype) {
			return &GateReadIntegrityError{Stage: subtype.Stage, Cause: err}
		}
		// REACHABLE, so it is not papered over: the sentinel is exported and any
		// error matching it without the typed carrier lands here — a Provenance
		// that has not adopted the carrier, or a subtype fault raised outside
		// the ownership read and wrapped into it. Nothing here can name the
		// stage, so the fault is reported with the zero stage and the operator
		// reads "unreported". Naming owned-tasks for the same reason this arm
		// used to is what the comment above refuses to do.
		return &GateReadIntegrityError{Cause: err}
	}
	return &GateReadError{Stage: GateReadStageOwnership, Harness: harness, Session: session, Cause: err}
}

// ownedTaskEpisode maps one owned task into the episode the policy reads.
//
// The phase is a straight translation: a phase this build does not know stays
// unset, and the policy refuses an unset phase rather than guessing.
//
// The role is an inference in three ordered steps, and the order is the
// contract. A transfer successor names its predecessor, and the transfer command
// that wrote it only ever hands over an owner episode, so that naming is enough
// and no material is read. Failing that, the assignment-start material the
// producing operation wrote carries the role. Failing that too, a legacy
// assignment whose material is absent still names its role in the command
// evidence its producing operation wrote.
//
// RESIDUAL (stated, not hidden), and it lives HERE because this is where the
// privilege is granted: the first step trusts the predecessor naming, so a
// transfer written through Provenance's RAW API by a writer that is not this
// build's transfer command can move a REVIEWER episode, and this function then
// hands the successor the owner role. The landed write path refuses that move;
// the correction belongs to the write path, not to this read, and until it lands
// the residual is accepted rather than prevented. A maintainer tightening this
// function must know the premise is that every predecessor naming came from the
// transfer command; that premise is foreign-writer-dependent, not a property of
// the store. See the acceptance test beside it.
func ownedTaskEpisode(actor provenance.ActorID, row provenance.OwnedTaskRow) (gateauthority.Episode, error) {
	// ok is deliberately dropped: an unmapped phase is the policy's unset phase
	// and its rule 6 fault, which belongs to the policy rather than to this read.
	// The arm is unreachable while the ownership read validates phase first and
	// refuses an out-of-range value as a subtype fault, so it is kept as the
	// translation boundary for a second read contract that hands phases through
	// unchecked, not as a branch that is nearly live.
	phase, _ := gateauthority.PhaseFromProvenance(row.Phase)
	role := gateauthority.RoleOwnerResponsibility
	if row.PredecessorAssignmentID == nil {
		mapped, err := ownedTaskRole(actor, row)
		if err != nil {
			return gateauthority.Episode{}, err
		}
		role = mapped
	}
	return gateauthority.Episode{
		Assignment: row.AssignmentID,
		Task:       row.TaskID,
		Role:       role,
		Phase:      phase,
	}, nil
}

// ownedTaskRole establishes the role of one owned episode from the records its
// producing operation wrote.
func ownedTaskRole(actor provenance.ActorID, row provenance.OwnedTaskRow) (gateauthority.AssignmentRole, error) {
	// Material. Every material row the producing operation wrote is decoded,
	// including rows for other assignments: present but unreadable material is a
	// fault, not a row to skip past.
	var matched []assignmentStartPayload
	for _, material := range row.Materials {
		start, err := decodeAssignmentStart(material.Payload)
		if err != nil {
			return 0, roleSourceFault(row, "the assignment-start material its producing operation wrote could not be read: "+err.Error())
		}
		if provenance.AssignmentID(start.Assignment) == row.AssignmentID {
			matched = append(matched, start)
		}
	}
	switch {
	case len(matched) > 1:
		return 0, roleSourceFault(row, fmt.Sprintf("%d assignment-start material rows name this assignment, and one episode has one role", len(matched)))
	case len(matched) == 1:
		role, ok := gateauthority.RoleFromToken(matched[0].Role)
		if !ok {
			return 0, roleSourceFault(row, fmt.Sprintf("the assignment-start material carries the role token %q, which this build does not know", matched[0].Role))
		}
		return role, nil
	}
	return legacyReviewRole(actor, row)
}

// legacyReviewRole establishes the role of a legacy started review whose
// assignment-start material is absent. Such an assignment was written by a
// command whose role lives in its command evidence, so the evidence is the
// source.
//
// The evidence is decoded, not authenticated. The writer is Pasture's own
// command, and the read is asking what that writer recorded, not whether the
// record survived tampering; a digest check would answer a different question
// and cost a second read.
func legacyReviewRole(actor provenance.ActorID, row provenance.OwnedTaskRow) (gateauthority.AssignmentRole, error) {
	var commands []provenance.OwnedEvidenceRow
	for _, evidence := range row.Evidence {
		if evidence.EvidenceKind == assignmentCommandEvidenceKind {
			commands = append(commands, evidence)
		}
	}
	if len(commands) != 1 {
		return 0, roleSourceFault(row, fmt.Sprintf("it has no assignment-start material and %d of its command records name a role; want exactly one", len(commands)))
	}
	var record assignmentCommandRecord
	if err := decodeRecoveryJSON(commands[0].Payload, &record); err != nil {
		return 0, roleSourceFault(row, "the command record that names its role could not be read: "+err.Error())
	}
	if record.Mutation != MutationStartReview {
		return 0, roleSourceFault(row, "it has no assignment-start material and the command record that produced it did not start a review")
	}
	operation := row.ProducingOperationID
	commandRole, members, err := commandRecoveryBinding(&record, &operation)
	if err != nil {
		return 0, roleSourceFault(row, "the command record that started the review could not be read as a review command: "+err.Error())
	}
	// The command's own role vocabulary and the gate's are bridged through the
	// canonical payload token, so a role this build does not know is refused
	// here exactly as it is on the material path.
	role, ok := gateauthority.RoleFromToken(commandRole.String())
	if !ok {
		return 0, roleSourceFault(row, fmt.Sprintf("the command record carries the role token %q, which this build does not know", commandRole.String()))
	}
	for _, member := range members {
		if member.Assignment == row.AssignmentID && member.Task == row.TaskID && member.Actor == actor {
			return role, nil
		}
	}
	return 0, roleSourceFault(row, "the command record that started the review does not declare this assignment, task and actor as one of its members")
}

func roleSourceFault(row provenance.OwnedTaskRow, reason string) error {
	return &GateRoleSourceError{Task: row.TaskID, Assignment: row.AssignmentID, Reason: reason}
}

// ─── sealed snapshot ──────────────────────────────────────────────────────────

// ResolveSession answers about the one session this snapshot was bound to.
// Asking about any other session would need facts this snapshot does not hold.
func (s *gateSnapshotValue) ResolveSession(harness ir.HarnessID, session string) (gateauthority.SessionClaim, error) {
	if s.isClosed() {
		return gateauthority.SessionClaim{}, &GateSnapshotClosedError{}
	}
	if harness != s.harness || session != s.session {
		return gateauthority.SessionClaim{}, &GateReaderScopeError{
			Operation: fmt.Sprintf("resolve the session claim of %q on harness %q through a snapshot bound to %q on harness %q", session, harness, s.session, s.harness),
		}
	}
	return s.claim, nil
}

// Authority answers about the claimed actor and about nothing else.
//
// It is OWNER-ONLY: the episodes are the assignments that actor currently owns,
// so a non-owner role the actor holds on somebody else's task is invisible here
// and cannot grant anything. A caller that needs the full relationship must ask
// the store, not this snapshot.
//
// An UNBOUND snapshot refuses every actor, the zero actor included. It claimed
// no actor, so it has none to answer about, and this accessor's contract is
// "the claimed actor; any other call is a scope fault" — with no claim there is
// no "the", and the permissive alternative would hand back an
// ActorAuthority{Actor: ""} that reads as an answer about an actor when it is an
// absence of one. A caller that is asking at all on this path is already the
// fault: the policy's unbound rule answers an unbound claim before any rule
// reads authority, so a caller that asks is one that skipped the check the
// policy requires of it, and this is where that is caught rather than answered.
// Nothing is lost by refusing: the unbound answer is SessionClaim{Bound: false},
// which this snapshot already returns.
func (s *gateSnapshotValue) Authority(actor provenance.ActorID) (gateauthority.ActorAuthority, error) {
	if s.isClosed() {
		return gateauthority.ActorAuthority{}, &GateSnapshotClosedError{}
	}
	if !s.claim.Bound {
		return gateauthority.ActorAuthority{}, &GateReaderScopeError{
			Operation: fmt.Sprintf("read the authority of %q through a snapshot of session %q on harness %q, which claimed no actor", actor, s.session, s.harness),
		}
	}
	if actor != s.actor {
		return gateauthority.ActorAuthority{}, &GateReaderScopeError{
			Operation: fmt.Sprintf("read the authority of %q through a snapshot bound to %q", actor, s.actor),
		}
	}
	return s.authority, nil
}

// Close releases the snapshot. It is idempotent, it holds nothing, and every
// later question is refused with GateSnapshotClosedError.
func (s *gateSnapshotValue) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *gateSnapshotValue) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
