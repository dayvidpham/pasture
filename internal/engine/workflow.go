package engine

import (
	"context"
	"strconv"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// AdvanceStep is one scripted transition in an epoch plan. It carries the
// votes and blocker delta to apply (deterministically, before the advance) so a
// single plan can exercise the consensus and blocker gates without an external
// signal source — the signal-driven control surface is a later slice.
type AdvanceStep struct {
	// ToPhase is the target phase for this transition.
	ToPhase protocol.PhaseId
	// TriggeredBy identifies who/what drove the transition (recorded as the
	// forensic row's role; defaults to the epoch role when empty).
	TriggeredBy string
	// ConditionMet describes the satisfied transition condition.
	ConditionMet string
	// Votes are recorded (in order) before the advance, to satisfy the
	// consensus gate at p4/p10.
	Votes []protocol.ReviewVoteSignal
	// BlockerDelta adjusts the blocker count before the advance: a positive
	// value records that many new blockers, a negative value resolves that
	// many. Used to exercise the p10 blocker gate.
	BlockerDelta int
	// StallSeconds, when > 0, makes the durable step sleep before returning —
	// a deterministic mid-step window for the kill-9 recovery test to crash in.
	StallSeconds int
}

// EpochInput is the EpochWorkflow input: the epoch id and the ordered plan of
// transitions to drive.
type EpochInput struct {
	EpochId  string
	Advances []AdvanceStep
}

// EpochWorkflow is the durable workflow that drives the 12-phase epoch.
//
// For each planned transition it (1) records votes and the blocker delta and
// runs EpochStateMachine.Advance in the workflow BODY — pure, deterministic, so
// the phase sequence replays identically — then (2) performs the I/O in ONE
// durable step: persist the EpochState projection and record exactly one
// forensic row keyed by the deterministic dedup key. One step per transition
// means one forensic emission per (kind, step), preserving the dedup invariant.
//
// A failed advance (gate violation) is recorded as a failed transition and the
// plan continues; the durable step is skipped for that entry.
func (e *Engine) EpochWorkflow(ctx dbos.DBOSContext, in EpochInput) (protocol.EpochState, error) {
	sm := protocol.NewEpochStateMachine(in.EpochId, e.specs)

	for _, adv := range in.Advances {
		// Deterministic body: votes + blockers + the pure advance.
		for _, v := range adv.Votes {
			_ = sm.RecordVote(v.Axis, v.Vote)
		}
		applyBlockerDelta(sm, adv.BlockerDelta)

		fromPhase := sm.State().CurrentPhase
		triggeredBy := adv.TriggeredBy
		if triggeredBy == "" {
			triggeredBy = string(protocol.RoleEpoch)
		}

		// Capture the deterministic step sequence for the dedup key. DBOS
		// re-derives step ids identically on replay, so the same transition
		// always yields the same key.
		stepSeqInt, _ := dbos.GetStepID(ctx)
		stepSeq := strconv.Itoa(stepSeqInt)

		rec, err := sm.Advance(adv.ToPhase, triggeredBy, adv.ConditionMet, time.Now().UTC())
		if err != nil {
			sm.RecordFailedTransition(fromPhase, adv.ToPhase, time.Now().UTC(), triggeredBy, err)
			// Project the failed-attempt state so status surfaces see LastError.
			snapshot := *sm.State()
			if _, perr := dbos.RunAsStep(ctx, func(c context.Context) (struct{}, error) {
				return struct{}{}, WriteProjection(c, e.db, &snapshot, time.Now().UTC().UnixNano())
			}); perr != nil {
				return *sm.State(), perr
			}
			continue
		}

		snapshot := *sm.State()
		dedupKey := protocol.DedupKey(in.EpochId, string(rec.ToPhase), string(protocol.EventPhaseTransition), stepSeq)
		if _, err := dbos.RunAsStep(ctx, func(c context.Context) (struct{}, error) {
			if adv.StallSeconds > 0 {
				time.Sleep(time.Duration(adv.StallSeconds) * time.Second)
			}
			if err := WriteProjection(c, e.db, &snapshot, time.Now().UTC().UnixNano()); err != nil {
				return struct{}{}, err
			}
			if err := e.emitTransition(c, in.EpochId, triggeredBy, rec, dedupKey); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, nil
		}); err != nil {
			return *sm.State(), err
		}
	}

	return *sm.State(), nil
}

// emitTransition records exactly one forensic audit row for a completed
// transition. The dedup key makes the write idempotent: a crash-replay of the
// emitting step collapses onto the same row via the partial unique index.
func (e *Engine) emitTransition(ctx context.Context, epochId, role string, rec *protocol.TransitionRecord, dedupKey string) error {
	ev := protocol.AuditEvent{
		EpochId:   epochId,
		Phase:     rec.ToPhase,
		Role:      role,
		EventType: protocol.EventPhaseTransition,
		Payload: map[string]any{
			"from":         string(rec.FromPhase),
			"to":           string(rec.ToPhase),
			"conditionMet": rec.ConditionMet,
		},
		Timestamp: rec.Timestamp,
		DedupKey:  dedupKey,
	}
	_, err := e.trail.RecordEventReturningId(ctx, ev)
	return err
}

// applyBlockerDelta records (delta > 0) or resolves (delta < 0) blockers on sm.
func applyBlockerDelta(sm *protocol.EpochStateMachine, delta int) {
	if delta > 0 {
		for i := 0; i < delta; i++ {
			sm.RecordBlocker(false)
		}
	} else {
		for i := 0; i < -delta; i++ {
			sm.RecordBlocker(true)
		}
	}
}
