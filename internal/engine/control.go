package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// controlPollInterval bounds how long the control loop blocks on the
// advance-phase topic before waking to re-drain the side-channel topics. It is
// only the IDLE wake cadence: an incoming signal wakes a blocked receive
// immediately, so a large interval keeps idle bookkeeping cheap without delaying
// delivery. It also bounds how stale a session/slice-progress projection can be
// while no phase advance is pending.
const controlPollInterval = 30 * time.Second

// ControlInput is the EpochControlWorkflow input: the epoch id whose lifecycle
// this durable workflow drives. The workflow ID is set to the epoch ID by the
// caller, so senders address signals to the epoch by its own id.
type ControlInput struct {
	EpochId string
}

// EpochControlWorkflow is the signal-driven durable driver for one epoch.
//
// Unlike the scripted EpochWorkflow (which replays a fixed plan), this workflow
// advances only in response to durable signals delivered by topic:
//   - advance_phase     drives one FSM transition.
//   - submit_vote       records a phase-scoped review vote (consumed before the
//     gated advance that needs it).
//   - register_session  registers a session (idempotent by session id).
//   - slice_progress    appends a slice-progress event.
//
// The slice-level start_slice / complete_slice topics are consumed by the slice
// sub-workflows, not this epoch loop.
//
// Each loop blocks for the next advance_phase signal. When one arrives it first
// drains the three side-channel topics (non-blocking) — so any votes sent just
// before the advance are recorded and the consensus gate sees them — then
// applies the advance. On an idle timeout it drains the side channels anyway so
// sessions and slice progress reported between advances reach the projection.
// Every successful transition funnels through commitTransition, so the
// projection, the exactly-once forensic emit, and the activity hook are
// identical to the scripted driver. The loop ends when the FSM reaches the
// terminal phase.
func (e *Engine) EpochControlWorkflow(ctx dbos.DBOSContext, in ControlInput) (protocol.EpochState, error) {
	sm := protocol.NewEpochStateMachine(in.EpochId, e.specs)

	for sm.State().CurrentPhase != protocol.PhaseComplete {
		adv, err := dbos.Recv[protocol.PhaseAdvanceSignal](ctx, protocol.SignalAdvancePhase.String(), controlPollInterval)
		if err != nil {
			if !isRecvTimeout(err) {
				return *sm.State(), err // cancellation or a real delivery failure ends the workflow
			}
			// Idle: no advance pending. Drain side channels so sessions/slice
			// progress reported while parked still reach the projection.
			changed, derr := e.drainSideChannels(ctx, in.EpochId, sm)
			if derr != nil {
				return *sm.State(), derr
			}
			if changed {
				if perr := e.projectState(ctx, sm.State()); perr != nil {
					return *sm.State(), perr
				}
			}
			continue
		}

		// An advance arrived: drain the votes/sessions/progress queued ahead of
		// it BEFORE applying, so a gated advance sees the votes that preceded it.
		if _, derr := e.drainSideChannels(ctx, in.EpochId, sm); derr != nil {
			return *sm.State(), derr
		}

		if adv.ToPhase == "" {
			// Defensive: an empty advance still flushes the drained side channels.
			if perr := e.projectState(ctx, sm.State()); perr != nil {
				return *sm.State(), perr
			}
			continue
		}

		triggeredBy := adv.TriggeredBy
		if triggeredBy == "" {
			triggeredBy = string(protocol.RoleEpoch)
		}
		fromPhase := sm.State().CurrentPhase

		// Capture the step ordinal for the dedup key before the (pure) advance.
		// DBOS re-derives the same ordinal on replay, so the same transition
		// always yields the same key.
		stepSeqInt, err := dbos.GetStepID(ctx)
		if err != nil {
			return *sm.State(), err
		}
		stepSeq := strconv.Itoa(stepSeqInt)

		rec, err := sm.Advance(adv.ToPhase, triggeredBy, adv.ConditionMet, time.Now().UTC())
		if err != nil {
			// A gate violation is recorded as a failed attempt; the epoch stays
			// put and waits for a corrected advance (e.g. after votes land).
			sm.RecordFailedTransition(fromPhase, adv.ToPhase, time.Now().UTC(), triggeredBy, err)
			if perr := e.projectState(ctx, sm.State()); perr != nil {
				return *sm.State(), perr
			}
			continue
		}

		if err := e.commitTransition(ctx, in.EpochId, triggeredBy, rec, sm.State(), stepSeq); err != nil {
			return *sm.State(), err
		}
	}

	return *sm.State(), nil
}

// drainSideChannels consumes all currently-queued non-advancing signals (votes,
// sessions, slice progress) without blocking, applying each to sm's state. It
// reports whether any signal mutated the state (so the caller can persist a
// fresh projection). Votes are drained before the caller blocks for an advance,
// guaranteeing the consensus gate sees every vote sent ahead of the advance.
func (e *Engine) drainSideChannels(ctx dbos.DBOSContext, epochId string, sm *protocol.EpochStateMachine) (bool, error) {
	changed := false

	for {
		vote, err := dbos.Recv[protocol.ReviewVoteSignal](ctx, protocol.SignalSubmitVote.String(), 0)
		if err != nil {
			if isRecvTimeout(err) {
				break
			}
			return changed, err
		}
		if vote.Axis == "" { // zero value: queue drained
			break
		}
		if err := sm.RecordVote(vote.Axis, vote.Vote); err != nil {
			continue
		}
		stepSeqInt, err := dbos.GetStepID(ctx)
		if err != nil {
			return changed, err
		}
		stepSeq := strconv.Itoa(stepSeqInt)
		if err := e.emitVoteRecorded(ctx, epochId, sm.State().CurrentPhase, vote, stepSeq); err != nil {
			return changed, err
		}
		changed = true
	}

	state := sm.State()

	for {
		sess, err := dbos.Recv[protocol.RegisterSessionSignal](ctx, protocol.SignalRegisterSession.String(), 0)
		if err != nil {
			if isRecvTimeout(err) {
				break
			}
			return changed, err
		}
		if sess.SessionId == "" { // zero value: queue drained
			break
		}
		if addSession(state, sess) {
			changed = true
		}
	}

	for {
		prog, err := dbos.Recv[protocol.SliceProgressSignal](ctx, protocol.SignalSliceProgress.String(), 0)
		if err != nil {
			if isRecvTimeout(err) {
				break
			}
			return changed, err
		}
		if prog.SliceId == "" && prog.LeafTaskId == "" { // zero value: queue drained
			break
		}
		state.SliceProgress = append(state.SliceProgress, prog)
		changed = true
	}

	return changed, nil
}

// addSession appends sess to the epoch's session list if its session id is not
// already registered (idempotent), keeping ActiveSessionCount equal to the list
// length. It reports whether a new session was added.
func addSession(state *protocol.EpochState, sess protocol.RegisterSessionSignal) bool {
	for _, existing := range state.ActiveSessions {
		if existing.SessionId == sess.SessionId {
			return false
		}
	}
	state.ActiveSessions = append(state.ActiveSessions, sess)
	state.ActiveSessionCount = len(state.ActiveSessions)
	return true
}

// projectState persists the current EpochState to the projection in one durable
// step. Used after side-channel updates so the query and status surfaces observe
// votes, sessions, and slice progress without waiting for the next transition.
func (e *Engine) projectState(ctx dbos.DBOSContext, state *protocol.EpochState) error {
	snapshot := *state
	_, err := dbos.RunAsStep(ctx, func(c context.Context) (struct{}, error) {
		return struct{}{}, WriteProjection(c, e.db, &snapshot, time.Now().UTC().UnixNano())
	})
	return err
}

// isRecvTimeout reports whether err is a receive timeout (the queue was empty
// within the deadline) rather than a real failure. A timeout is the expected
// signal for "no message right now" and must not end the workflow.
//
// On a fresh execution the substrate returns a typed timeout error; on replay
// it returns the recorded error as a plain value, so the typed check is paired
// with a message check to classify both identically (required for the workflow
// to make the same decisions on replay as on first run).
func isRecvTimeout(err error) bool {
	if err == nil {
		return false
	}
	var dbosErr *dbos.DBOSError
	if errors.As(err, &dbosErr) && dbosErr.Code == dbos.TimeoutError {
		return true
	}
	return strings.Contains(err.Error(), "no message received within")
}
