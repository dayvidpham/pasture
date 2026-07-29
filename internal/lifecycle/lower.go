package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// ─── The middle-end ──────────────────────────────────────────────────────────
//
// This file is the lowering pass: the one place that decides what Pasture does
// about an occurrence which has already crossed the waist.
//
// OPERATION SELECTION LIVES HERE AND NOWHERE ELSE. Not in an environment
// variable, not in argv, not in a generated hook script, not at a call site. A
// caller hands over verified IR and receives an [Outcome]; it does not get to
// say what should happen. A caller that could choose would be a second place
// the decision lives, and the second place is the one nobody reviews.
//
// The pass is target-agnostic by construction:
//
//   - it MAY read [Event.Semantics] and [Event.Origin]'s coordinates, because
//     those are true regardless of which host produced the occurrence;
//   - it MUST NOT open the target-detail side of an [Event] — the pinned table
//     describing how to speak back to one specific host. See backend.go, which
//     names the single symbol that opens it and explains why that symbol has a
//     name at all. A middle-end that branches on those axes is not a
//     middle-end; it is a pile of per-host handlers wearing one function name;
//   - [Deps] carries no lifecycle-table accessor, so a lookup that has to vary
//     by host can only happen behind [ActorResolver] — one injected
//     collaborator rather than a branch in the pass.
//
// The last point is what makes the others enforceable rather than aspirational.
// The invariant reduces to a search of this file for that one symbol and for
// any host name — which is also why neither appears in the prose above. A
// comment that spelled them would leave the check permanently red, and a check
// that is always red is a check everybody learns to ignore.
//
// The other half of the design is that refusal is loud. Every path that cannot
// proceed returns an actionable error having written nothing. Silence — a pass
// that returns success when it was not configured to do anything — makes a
// broken integration indistinguishable from a working one, which is the exact
// failure mode this work exists to remove.

// lowerWhere locates lowering failures for the operator.
const lowerWhere = "Lowering a native lifecycle occurrence (internal/lifecycle/lower.go in lifecycle.Lower)."

// Deps are the injected collaborators one lowering needs.
//
// Every field is required and NONE has a fallback. In particular the clock is
// not defaulted to the wall clock: a pass that quietly supplies a dependency
// its caller forgot records something nobody asked for, and the caller never
// learns it was mis-wired. A missing collaborator is a wiring bug, and wiring
// bugs should be loud on the first invocation rather than latent.
//
// There is deliberately no accessor for the pinned lifecycle table here. Its
// absence is what confines host-varying behaviour to [ActorResolver]: a future
// author cannot reach for a per-host lookup through this struct, because this
// struct cannot offer one.
type Deps struct {
	// Recorder receives the single transactional write. Required.
	Recorder ObservationRecorder

	// Actors resolves the automaton an occurrence is attributed to.
	// Required.
	Actors ActorResolver

	// Clock supplies the observed-at timestamp. Required; there is no
	// time.Now fallback.
	//
	// Its only consumer is [ObservationRecord.ObservedAt]. It is deliberately
	// absent from the replay key, so a frozen clock in a test cannot change
	// deduplication behaviour and clock skew in production cannot split one
	// occurrence into two records.
	Clock func() time.Time
}

// ObservationRecorder durably records one observed occurrence.
//
// It is ONE call because it is ONE transaction. The forensic activity and the
// durable event describe the same occurrence; an interface that exposed them as
// two calls would invite an implementation to complete the first and fail the
// second, leaving a record asserting that an occurrence was observed by nobody.
// An implementation MUST make both visible or neither.
//
// Implementations MUST deduplicate on [ObservationRecord.ReplayKey]: a record
// already present under that key is left exactly as it is — timestamps
// included — and reported as [RecordReplayed]. Hooks DO fire more than once;
// retries, crash recovery, and a user re-running a session are all normal, and
// an integration that duplicated a fact per delivery would make its own record
// useless.
//
// Implementations MUST fail with an actionable error. The lowering pass returns
// that error unchanged, because an implementation knows what went wrong at the
// storage layer and the pass does not.
type ObservationRecorder interface {
	RecordObservation(ctx context.Context, o ObservationRecord) (RecordOutcome, error)
}

// ObservationRecord is everything the recorder is given, and everything it is
// allowed to know.
//
// It is exactly the waist plus the two things the waist cannot carry: who is
// accountable for the write, and when it was made. Native payload content is
// absent, because it never crossed the waist and therefore never reaches
// storage either.
type ObservationRecord struct {
	// Actor is the automaton this occurrence is attributed to. Resolved
	// before this value is built, so an unattributable occurrence never
	// reaches a write.
	Actor provenance.AgentID

	// Observed is where the occurrence came from: pinned contract, native
	// event name, and payload digest.
	Observed Origin

	// Semantics is the target-agnostic meaning and correlation.
	Semantics Semantics

	// ObservedAt is the recorded timestamp, taken from [Deps.Clock].
	ObservedAt time.Time

	// ReplayKey is the deduplication key, derived only from what the host
	// actually sent. It is [Origin.ReplayKey], lifted into this record so an
	// implementation does not have to know which of several derivable keys is
	// the right one.
	ReplayKey string
}

// RecordOutcome reports what a recorder did with one observation.
type RecordOutcome uint8

const (
	// RecordCreated means this occurrence was not present and is now.
	RecordCreated RecordOutcome = iota + 1
	// RecordReplayed means this occurrence was already present under the
	// same replay key and nothing was written.
	RecordReplayed
)

// IsValid reports whether this is one of the defined outcomes. The lowering
// pass checks it rather than trusting the recorder: an implementation that
// returned its zero value would otherwise be reported to the caller as a
// successful record with no disposition.
func (o RecordOutcome) IsValid() bool { return o >= RecordCreated && o <= RecordReplayed }

// ActorResolver answers which automaton an occurrence is attributed to.
//
// It exists as an injected collaborator because the answer is the one thing in
// this pass that genuinely varies by host, and confining it here is what keeps
// the pass itself target-agnostic. It takes an [Origin] rather than a host name
// so the pass does not have to name hosts in order to ask.
//
// Implementations MUST namespace by harness — a native event spelling is not
// unique across hosts, so two hosts using the same spelling would otherwise
// collide onto one actor and misattribute both.
//
// Implementations MUST fail closed: an occurrence with no registered actor is
// an actionable error, never an invented attribution. Extending the reach of
// this pass to more events is a deliberate registration change, not a side
// effect of parsing a new payload.
type ActorResolver interface {
	ResolveHookActor(ctx context.Context, o Origin) (provenance.AgentID, error)
}

// Outcome reports what one lowering did.
//
// It is a struct rather than a bare enum so later work can add variants and
// carry a payload alongside them without breaking callers. Today exactly one
// kind exists; the observation path is settled, and the paths that answer a
// waiting host are not designed yet.
//
// It carries no storage identity — no row id, no activity id. Those are
// storage's business, and a caller that received them would be able to build
// behaviour on the shape of the database.
type Outcome struct {
	// Kind is what the pass did.
	Kind OutcomeKind
	// Record is what the recorder did, meaningful when Kind is
	// [OutcomeRecorded].
	Record RecordOutcome
}

// OutcomeKind enumerates what a lowering can do.
type OutcomeKind uint8

const (
	// OutcomeRecorded means the occurrence was durably recorded and the host
	// is not waiting for anything.
	OutcomeRecorded OutcomeKind = iota + 1
)

// Lower selects and performs the operation for one verified occurrence.
//
// It is the only exported entry point of the pass. Per-semantic behaviour lives
// in unexported helpers, so a caller cannot reach past the legalization checks
// and select an operation itself — moving that choice from an environment
// variable to a Go call site would change its spelling and not its nature.
//
// The order below is load-bearing, not stylistic:
//
//  1. collaborators are validated, so a mis-wired caller fails before anything
//     is inspected;
//  2. the event is checked for verification, so unchecked semantics cannot be
//     acted on;
//  3. an occurrence the host is waiting on is refused, because there is no way
//     yet to send a reply it will honour, and recording it silently would
//     leave the host waiting for an answer that never comes;
//  4. the semantic is dispatched, and anything without a reviewed meaning is
//     refused rather than approximated;
//  5. the actor is resolved BEFORE any write, so a failed resolution cannot
//     leave a partial record;
//  6. one transactional write happens.
//
// Every refusal returns having written nothing. On success the occurrence is
// recorded exactly once, whether this is the first delivery or the fifth.
func Lower(ctx context.Context, deps Deps, event Event) (Outcome, error) {
	if err := preflight(ctx, deps); err != nil {
		return Outcome{}, err
	}
	if !event.IsValid() {
		return Outcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An unchecked lifecycle event reached the recorder.",
			Why:      "Only an event that passed the waist's verifier has been checked against the pinned host contract; acting on anything else would record a meaning nobody reviewed.",
			Where:    lowerWhere,
			Impact:   "Nothing was recorded.",
			Fix:      "Parse the native payload through this host's lifecycle frontend, and record the event that produces.",
		}
	}

	semantics := event.Semantics()

	// A host that waits for a reply must get one. Until Pasture can encode a
	// reply a host will honour, accepting the occurrence would strand it.
	if semantics.Blocking() != runtime.NonBlocking {
		return Outcome{}, awaitedReplyError(event)
	}

	switch semantics.Semantic() {
	case runtime.SemanticObservation:
		return lowerObservation(ctx, deps, event)
	default:
		// Unreachable through the currently pinned contracts, where every
		// non-observation is also awaited and is refused above. It is kept
		// because that alignment is a property of today's tables, not of the
		// design: a contract pinning a non-awaited decision request must be
		// refused on its meaning, not slip through on its blocking mode.
		return Outcome{}, unsupportedSemanticError(event)
	}
}

// lowerObservation records one observed occurrence.
//
// An observation is evidence, not authority. It is a report that something
// happened in a host, and the only honest thing to do with it is write it down:
// no task is created, no assignment moves, no phase advances. A hook firing
// must never be able to move the workflow, because the process that fired it
// proved nothing about who asked for it.
func lowerObservation(ctx context.Context, deps Deps, event Event) (Outcome, error) {
	origin := event.Origin()

	// Resolution precedes the write so a failure here cannot leave a partial
	// record — there is nothing to be partial about yet.
	actor, err := deps.Actors.ResolveHookActor(ctx, origin)
	if err != nil {
		// Returned unchanged. The resolver knows which attribution is missing
		// and how to register it; wrapping that in another error would bury
		// the only text that tells the operator what to do.
		return Outcome{}, err
	}
	if (actor == provenance.AgentID{}) {
		return Outcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("No one could be credited with observing the %s event %q.", origin.Harness(), origin.NativeEventName()),
			Why:      "Attribution reported success but produced an empty identity. Every recorded observation names who observed it, and an unattributed one could not be answered for later.",
			Where:    lowerWhere,
			Impact:   "Nothing was recorded. No partial record was left behind.",
			Fix:      "Confirm the built-in automaton that handles this event is registered, then re-run the hook.",
		}
	}

	record, err := deps.Recorder.RecordObservation(ctx, ObservationRecord{
		Actor:      actor,
		Observed:   origin,
		Semantics:  event.Semantics(),
		ObservedAt: deps.Clock().UTC(),
		ReplayKey:  origin.ReplayKey(),
	})
	if err != nil {
		// Returned unchanged, for the same reason as the resolver's error: the
		// recorder knows whether the store was unreachable, unmigrated, or
		// read-only, and only it can say how to repair that.
		return Outcome{}, err
	}
	if !record.IsValid() {
		return Outcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("It is not clear whether the %s event %q was recorded.", origin.Harness(), origin.NativeEventName()),
			Why:      "The recorder reported neither a new record nor a repeat delivery, so whether anything was written is unknown.",
			Where:    lowerWhere,
			Impact:   "The occurrence may or may not be in the recorded history. Re-running the same hook will record it once, not twice.",
			Fix: "1. Confirm the recorded history is readable and current:\n" +
				"     pasture migrate\n" +
				"2. Re-run the hook once the database is healthy.",
		}
	}
	return Outcome{Kind: OutcomeRecorded, Record: record}, nil
}

// preflight rejects a call that cannot be started, before anything is
// inspected and before any collaborator is touched.
//
// Each missing thing is named individually rather than reported as "incomplete
// dependencies", so the operator learns which one it is instead of being told
// to go and look.
func preflight(ctx context.Context, deps Deps) error {
	if ctx == nil {
		return preflightError(
			"The recorder was called without an execution context.",
			"The recorder needs a cancellable context so a shutdown partway through a write is not left ambiguous.",
			"Pass the command's context to the recorder.",
			nil,
		)
	}
	if deps.Recorder == nil {
		return preflightError(
			"The recorder was called with nowhere to record.",
			"Recording is done through the collaborator the caller supplies; without one there is nowhere for the occurrence to go.",
			"Construct the recorder against an open store and pass it in.",
			nil,
		)
	}
	if deps.Actors == nil {
		return preflightError(
			"The recorder was called with no way to credit the observation.",
			"Every recorded observation names who observed it, and the caller supplies the means of working that out.",
			"Construct the attribution resolver against an open store and pass it in.",
			nil,
		)
	}
	if deps.Clock == nil {
		return preflightError(
			"The recorder was called without a clock.",
			"The recorded time is taken from the clock the caller supplies. Substituting the system clock here would hide the missing wiring and record a time nobody chose.",
			"Pass a clock to the recorder; production callers pass the system clock explicitly.",
			nil,
		)
	}
	if err := ctx.Err(); err != nil {
		return preflightError(
			"The occurrence was abandoned because the command had already been cancelled.",
			"The supplied context had ended before anything was attempted.",
			"Re-run the hook.",
			err,
		)
	}
	return nil
}

// preflightError renders one refusal from [preflight]. They all share an
// impact — nothing was recorded — because they all happen before any
// collaborator is touched.
func preflightError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why,
		Where:    lowerWhere,
		Impact:   "Nothing was recorded.",
		Fix:      fix,
		Cause:    cause,
	}
}

// awaitedReplyError refuses an occurrence whose host is waiting for a reply.
//
// This is a legalization failure in the compiler sense: the occurrence is
// well-formed, and this pass has no legal lowering for it. Refusing is not a
// gap in the implementation, it is the behaviour the design requires — the
// alternative is recording the occurrence, returning nothing, and leaving the
// host blocked on an answer that will never arrive.
func awaitedReplyError(event Event) error {
	origin := event.Origin()
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What: fmt.Sprintf(
			"The %s event %q waits for a reply from Pasture, and replying is not yet supported.",
			origin.Harness(), origin.NativeEventName(),
		),
		Why:    "The host holds its own work until it receives a result it can act on, and Pasture cannot yet encode a result this host will honour. Recording the occurrence and returning nothing would leave the host waiting for an answer that never arrives.",
		Where:  lowerWhere,
		Impact: "Nothing was recorded. No partial record was left behind.",
		Fix:    "Register this hook for an event the host reports without waiting for a reply. Events the host waits on become usable once Pasture can send a reply the host will honour.",
	}
}

// unsupportedSemanticError refuses an occurrence whose meaning this pass has no
// reviewed handling for.
//
// The refusal names the capability that is missing rather than the meaning
// alone, because "this is a decision request" tells an operator what they have,
// and they need to be told what they can do instead.
func unsupportedSemanticError(event Event) error {
	origin := event.Origin()
	semantics := event.Semantics()

	var why string
	switch semantics.Semantic() {
	case runtime.SemanticGateConsultation:
		why = "This event asks Pasture for a decision. Recording it as an observation would answer nothing, and deciding it needs workflow authority that an occurrence reported by a host does not carry."
	case runtime.SemanticExplicitHumanResponse:
		why = "This event reports a person's answer to a request Pasture made. Recording it as an observation would either discard the answer or manufacture a decision nobody gave."
	default:
		why = "This pass records observations only. It has no reviewed meaning for any other kind of occurrence, and approximating one would store a claim nobody approved."
	}

	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What: fmt.Sprintf(
			"The %s event %q is a %s, which cannot be handled yet.",
			origin.Harness(), origin.NativeEventName(), semantics.Semantic(),
		),
		Why:    why,
		Where:  lowerWhere,
		Impact: "Nothing was recorded. No partial record was left behind.",
		Fix:    "Register this hook for an event the pinned host contract classifies as an observation. Deciding and answering become usable once Pasture can send a reply the host will honour.",
	}
}
