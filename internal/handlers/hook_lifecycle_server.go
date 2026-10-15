package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// LifecycleWriteLane is the ONE serialized writer a served lifecycle record's
// durable work goes through. It is an interface here, and not the concrete
// lane, so this package states only the contract it needs: admit a record of
// bytes, run its two-phase commit on the single writer, and report the commit's
// result to the caller.
//
// THE CALLER LEARNS ONLY AFTER THE COMMIT, which is the whole point: Submit
// returns nil only once the record is durable, so a served answer of "proceed"
// cannot be produced for a record that has not committed. *writelane.Lane
// satisfies this interface; nothing else in production does.
type LifecycleWriteLane interface {
	Submit(ctx context.Context, bytes int64, record writelane.Record) error
}

// submitToLane admits one record and waits for its commit, converting the
// lane's own "nothing was staged" evidence into the sentinel the fault-stage
// classifier reads.
//
// THE CONVERSION IS THE HONESTY STEP. A Submit that returns ErrNotAdmitted
// staged nothing, so the refusal it produces is provably not-recorded; a
// Submit that returns anything else got as far as running the record's commit,
// whose durable state only the commit can report. Without this the two would
// collapse into one stage and an operator could not tell them apart.
func submitToLane(ctx context.Context, lane LifecycleWriteLane, bytes int64, record writelane.Record) error {
	err := lane.Submit(ctx, bytes, record)
	if err != nil && errors.Is(err, writelane.ErrNotAdmitted) {
		return fmt.Errorf("%w: %w", ErrLifecycleBeforeDurableWrite, err)
	}
	return err
}

// HookLifecycleServerConfig is one daemon's served-path wiring. Every member is
// required: a server that is missing one is refused at construction rather than
// at the first event.
type HookLifecycleServerConfig struct {
	// Tracker is the daemon's ONE long-lived unified store handle. It is NOT
	// closed per request: the open is the work the lane exists to remove from
	// the firehose.
	Tracker protocol.TaskTracker
	// Lane is the single serialized writer for this server's records.
	Lane LifecycleWriteLane
	// Clock and Operations are the injected receipt dependencies, exactly as
	// the direct path takes them.
	Clock      receipt.Clock
	Operations receipt.OperationIDSource
	// Profile is the ordered timeout profile the receipt path runs under.
	Profile timeouts.Profile
	// Readers builds the gate reader per request (its session identity comes
	// from the payload). Nil means tasks.NewGateReader, the production wiring.
	Readers lifecycleReaderFactory
	// Diagnostics is the best-effort sink for the session-claim mismatch line.
	Diagnostics io.Writer
}

// HookLifecycleServer owns the served lifecycle pipeline: re-validate the
// delivery, read the gate and take the decision from ONE open handle, encode
// the answer, admit the record to the lane, and answer only once the lane
// reports the commit.
type HookLifecycleServer struct {
	tracker     protocol.TaskTracker
	lane        LifecycleWriteLane
	clock       receipt.Clock
	operations  receipt.OperationIDSource
	profile     timeouts.Profile
	readers     lifecycleReaderFactory
	diagnostics io.Writer
}

// NewHookLifecycleServer validates and returns a served path. The receipt
// service is resolved ONCE here and reused per request, because every field it
// carries is immutable except the per-call Outcome.
func NewHookLifecycleServer(cfg HookLifecycleServerConfig) (*HookLifecycleServer, error) {
	if cfg.Tracker == nil {
		return nil, lifecycleError(validationCategory, "The lifecycle write lane server has no store handle.", "The served path reads the gate and commits receipts from one long-lived tracker.", "No event can be served.", "Open the unified tracker once and pass it to this constructor.", nil)
	}
	if cfg.Lane == nil {
		return nil, lifecycleError(validationCategory, "The lifecycle write lane server has no write lane.", "Without the serialized writer there is no single-writer property to serve, and a direct write would reintroduce the convoy the lane removes.", "No event can be served.", "Construct a writelane.Lane and pass it to this constructor.", nil)
	}
	if cfg.Clock == nil || cfg.Operations == nil {
		return nil, lifecycleError(validationCategory, "The lifecycle write lane server is missing its clock or operation identity source.", "Every served receipt needs an attributable time and a distinct operation identity.", "No event can be served.", "Pass the production clock and operation identity source.", nil)
	}
	if _, err := lifecycleReceiptServiceFor(cfg.Tracker, cfg.Clock, cfg.Operations, cfg.Profile); err != nil {
		return nil, err
	}
	readers := cfg.Readers
	if readers == nil {
		readers = tasks.NewGateReader
	}
	return &HookLifecycleServer{
		tracker: cfg.Tracker, lane: cfg.Lane, clock: cfg.Clock, operations: cfg.Operations,
		profile: cfg.Profile, readers: readers, diagnostics: cfg.Diagnostics,
	}, nil
}

// HookLifecycleServeInput is one request's re-validated coordinates, plus the
// host's payload bytes verbatim. Everything else the served path needs it
// re-derives from the store, so a hook cannot hand it a verdict.
type HookLifecycleServeInput struct {
	Harness           ir.HarnessID
	Event             string
	HostVersion       string
	HostVersionSource model.HostVersionSource
	// Payload is the host's stdin bytes exactly as the hook read them.
	Payload    []byte
	ActorClaim tasks.ActorClaim
}

// HookLifecycleServed is one committed answer: the encoded Outcome the host
// must receive, and the identities of what was written.
type HookLifecycleServed struct {
	Outcome      hostexit.Outcome
	OccurrenceID string
	ClaimState   string
}

// Serve runs one consult-and-record request and returns only after the record
// is durable.
//
// THE ANSWER IS PRODUCED BEFORE THE COMMIT AND PUBLISHED AFTER IT. The encoded
// Outcome is computed from the decision taken at request time, exactly as the
// direct path computes it, but it is returned only once the lane reports that
// the record committed. There is no path that returns it earlier.
func (s *HookLifecycleServer) Serve(ctx context.Context, in HookLifecycleServeInput) (served HookLifecycleServed, err error) {
	// durablePossible holds the one fact the caller cannot otherwise learn:
	// whether this request ever ATTEMPTED a write, and so whether a row can
	// exist for it. Identical in meaning to the direct path's marker: a refusal
	// raised before the first lane admission is provably not-recorded, and one
	// raised after it is not.
	durablePossible := false
	defer func() {
		if err != nil && !durablePossible {
			err = fmt.Errorf("%w: %w", ErrLifecycleBeforeDurableWrite, err)
		}
	}()

	if s == nil || s.tracker == nil || s.lane == nil {
		return HookLifecycleServed{}, lifecycleError(validationCategory, "The lifecycle write lane server is incompletely wired.", "A store handle and a write lane are both required to serve a record.", "Nothing was read or recorded.", "Construct the server through NewHookLifecycleServer.", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	prepared, err := prepareLifecycleDelivery(lifecycleAdmission{
		Harness:           in.Harness,
		Event:             in.Event,
		HostVersion:       in.HostVersion,
		HostVersionSource: in.HostVersionSource,
		Input:             bytes.NewReader(in.Payload),
	})
	if err != nil {
		return HookLifecycleServed{}, err
	}
	dispatch, event, capture := prepared.dispatch, prepared.event, prepared.capture

	service, err := lifecycleReceiptServiceFor(s.tracker, s.clock, s.operations, s.profile)
	if err != nil {
		return HookLifecycleServed{}, err
	}
	// NO SETTLEMENT. The direct path shares a settlement with the command's
	// timeout choice, which is a decision only the process that owns the host
	// bytes can take. In the daemon the equivalent fence is the lane's Submit
	// return, which cannot be read before the commit.
	service.Settlement = nil

	// The invalid-capture arm writes the disposition row and nothing else. It
	// is the same write class as the direct path's, reached through the same
	// warrant, and it never evaluates the event.
	if capture.disposition != model.CaptureValid {
		warrant, err := deliveryWarrant(capture.delivery)
		if err != nil {
			return HookLifecycleServed{}, err
		}
		durablePossible = true
		record := &servedLifecycleRecord{
			service:  service,
			warrant:  warrant,
			delivery: capture.delivery,
		}
		if commitErr := submitToLane(ctx, s.lane, int64(len(in.Payload)), record); commitErr != nil {
			return HookLifecycleServed{}, commitErr
		}
		return HookLifecycleServed{}, fmt.Errorf("%w: %w", ErrLifecycleDeliveryRefused,
			unbindableCaptureError(dispatch, event, in.HostVersion, capture))
	}

	// THE GATE IS READ AND THE DECISION TAKEN SYNCHRONOUSLY, from the SAME
	// handle the record will be committed against. Only the writes are queued,
	// so the freshness of the verdict is exactly the direct path's: a snapshot
	// at request arrival.
	decision, err := evaluateGate(ctx, s.tracker, s.readers, dispatch, event, capture.delivery.Bindings, in.Harness)
	if err != nil {
		return HookLifecycleServed{}, err
	}
	warrant, preparedCommit, err := prepareServedDelivery(dispatch, event, capture.delivery, decision)
	if err != nil {
		return HookLifecycleServed{}, err
	}
	service.Outcome = preparedCommit.outcome

	var occurrence model.OccurrenceID
	var claim tasks.SessionClaimOutcome
	durablePossible = true
	record := &servedLifecycleRecord{
		service:         service,
		tracker:         s.tracker,
		warrant:         warrant,
		delivery:        capture.delivery,
		effects:         preparedCommit.effects,
		ensureMetamodel: true,
		claim:           in.ActorClaim,
		clock:           s.clock,
		harness:         in.Harness,
		eventKind:       event.Kind,
	}
	if commitErr := submitToLane(ctx, s.lane, int64(len(in.Payload)), record); commitErr != nil {
		return HookLifecycleServed{}, commitErr
	}
	occurrence = record.occurrence
	claim = record.claimOutcome

	if line := sessionClaimMismatchDiagnostic(in.Harness, in.ActorClaim, capture.delivery.Bindings, claim); line != "" && s.diagnostics != nil {
		fmt.Fprintln(s.diagnostics, line)
	}
	return HookLifecycleServed{Outcome: preparedCommit.outcome, OccurrenceID: strconv.FormatInt(int64(occurrence.JournalID()), 10), ClaimState: claim.State.String()}, nil
}

// servedLifecycleRecord is ONE served request's durable work, expressed as the
// lane's two-phase writelane.Record so several requests' receipt appends can
// share ONE transaction.
//
// THE PREPARE/SETTLE SPLIT IS THE PHASE-2 SHAPE. Phase 1 ran the whole commit —
// blob, metamodel, receipt append, session claim — in one closure per record, so
// every record took its own journal transaction. Phase 2 splits it: Prepare
// writes the blob and builds the canonical operation (everything that must
// precede the transaction), the lane commits the flush's operations in ONE
// ApplyBatchContext, and Settle maps this record's own outcome back and runs the
// continuation (the session claim) only for a record that is durably recorded.
//
// The compatibility-sensitive ordering is preserved: the metamodel activation
// still commits BEFORE the receipt's operation, because Prepare runs it before
// it returns the operation the batch will fold.
type servedLifecycleRecord struct {
	service   receipt.Service
	tracker   protocol.TaskTracker
	warrant   gate.Warrant
	delivery  receipt.Delivery
	effects   []provenance.Effect
	claim     tasks.ActorClaim
	clock     receipt.Clock
	harness   ir.HarnessID
	eventKind model.ContractEventKind

	// ensureMetamodel is set on the valid-capture arm, whose interpreted.v2
	// record must never cite an unjournaled metamodel. The invalid-capture arm
	// writes only the disposition row.
	ensureMetamodel bool

	pending      receipt.PendingReceipt
	occurrence   model.OccurrenceID
	claimOutcome tasks.SessionClaimOutcome
}

// Prepare runs everything that must precede the shared transaction and returns
// the record's canonical journal operation.
func (r *servedLifecycleRecord) Prepare(ctx context.Context) (provenance.OperationInput, error) {
	if r.ensureMetamodel {
		if _, err := receipt.EnsureActiveMetamodel(ctx, r.service); err != nil {
			return provenance.OperationInput{}, err
		}
	}
	pending, err := r.service.PrepareReceipt(ctx, r.warrant, r.delivery, r.effects...)
	if err != nil {
		return provenance.OperationInput{}, err
	}
	r.pending = pending
	return pending.Operation(), nil
}

// Settle reports this record's own outcome and runs its continuation. It is
// called exactly once, with this record's own BatchRecordResult, whatever the
// batch did:
//
//   - committed or short-circuited: writelane.Report returns nil, the receipt's
//     occurrence is resolved, and the session claim runs. A failure AFTER the
//     commit is ErrLifecycleCommittedWithoutContinuation (recorded), exactly as
//     the direct path reports it.
//   - conflict, failed, or not-recorded: the record is not durably recorded and
//     writelane.Report returns its typed error, so the served path answers
//     record-unknown and NEVER claims a commit the batch rolled back. A
//     short-circuited record is not in this class.
func (r *servedLifecycleRecord) Settle(ctx context.Context, outcome provenance.BatchRecordResult) error {
	if err := writelane.Report(outcome); err != nil {
		return err
	}
	settled, err := r.pending.Settle(outcome.Result)
	if err != nil {
		return err
	}
	r.occurrence = settled.OccurrenceID
	if r.tracker == nil {
		return nil
	}
	claim, err := tasks.RecordLifecycleSessionClaim(ctx, r.tracker, r.harness, r.eventKind, r.delivery.Bindings, r.claim, r.clock)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLifecycleCommittedWithoutContinuation, err)
	}
	r.claimOutcome = claim
	return nil
}

// prepareServedDelivery composes the same pure verification and encoding steps
// deliveryCommit runs, WITHOUT the metamodel write: the served path performs
// that write inside the lane's record Prepare, so the store is touched only by
// the lane's one goroutine.
//
// The compatibility-sensitive ordering is preserved. deliveryCommit sequences
// warrant → metamodel activation → derivation → encode → receive; the served
// path sequences warrant → derivation → encode → [lane: metamodel activation →
// receive]. The invariant the ordering protects is that a committed
// interpreted.v2 record never cites an unjournaled metamodel, and the
// activation still commits immediately before the receipt in the same lane
// turn. Derivation and encoding are pure, so moving them ahead of a write
// changes nothing they can observe.
func prepareServedDelivery(dispatch lifecycleDispatch, event registration.Event, delivery receipt.Delivery, decision *backend.Decision) (gate.Warrant, preparedDelivery, error) {
	if decision == nil {
		mapping, err := dispatch.mapping(event.NativeName)
		if err != nil {
			return gate.Warrant{}, preparedDelivery{}, err
		}
		if mapping.Semantic() != semanticObservation {
			return gate.Warrant{}, preparedDelivery{}, fmt.Errorf(
				"%w: lifecycle delivery: the %s gate event %q reached the commit without an evaluated decision; "+
					"no receipt committed; the verdict is taken from the store by evaluateGate and is not optional on a gate",
				ErrLifecycleBeforeDurableWrite, dispatch.name, event.NativeName)
		}
	}
	warrant, err := deliveryWarrant(delivery)
	if err != nil {
		return gate.Warrant{}, preparedDelivery{}, err
	}
	derivation, err := deliveryDerive(dispatch, event, delivery)
	if err != nil {
		return gate.Warrant{}, preparedDelivery{}, err
	}
	prepared, err := prepareDelivery(dispatch, event, derivation, decision)
	if err != nil {
		return gate.Warrant{}, preparedDelivery{}, err
	}
	return warrant, prepared, nil
}

// LifecycleFaultStageForError classifies a served-path error into the durable
// state it can support. It is the daemon's half of the fault-stage table the
// hook keeps for its own sentinels, and it exists so the daemon answers with a
// stage rather than the hook guessing one from a text it cannot see the
// sentinel of.
//
// THE DEFAULT IS THE WEAKEST CLAIM. A served error this function does not
// recognise may follow a commit (the journal appender can fail after the commit
// succeeds and carries no sentinel), so record-unknown is the only honest
// answer for it.
func LifecycleFaultStageForError(err error) hostexit.FaultStage {
	switch {
	case errors.Is(err, ErrLifecycleBeforeDurableWrite):
		return hostexit.FaultStageNotRecorded
	case errors.Is(err, ErrLifecycleCommittedWithoutContinuation):
		return hostexit.FaultStageRecorded
	case errors.Is(err, ErrLifecycleDeliveryRefused):
		return hostexit.FaultStageRecorded
	default:
		return hostexit.FaultStageRecordUnknown
	}
}

// validationCategory is the error category every construction refusal in this
// file carries; it names the local import once so the refusals stay readable.
const validationCategory = pasterrors.CategoryValidation

// semanticObservation is the observation semantic the middle end answers on its
// own. It is named here so the served gate guard reads the same value the
// direct path's guard reads.
const semanticObservation = pastureruntime.SemanticObservation
