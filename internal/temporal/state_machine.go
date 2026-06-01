// Package temporal implements the Temporal workflow layer for the Pasture epoch
// protocol. This file defines EpochStateMachine — a pure Go port of the Python
// state_machine.py.  No Temporal SDK dependency; pure state transitions only.
package temporal

import (
	"fmt"
	"time"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Phase transition table ───────────────────────────────────────────────────

// PhaseSpec describes the allowed forward/backward transitions from one phase.
type PhaseSpec struct {
	// Transitions lists all target PhaseIds reachable from this phase.
	Transitions []protocol.PhaseId
}

// PhaseSpecs is the canonical transition table for the 12-phase epoch lifecycle.
// Port of Python PHASE_SPECS from scripts/aura_protocol/types.py.
//
// Gate rules (enforced by EpochStateMachine, not by this table):
//   - PhaseReview→PhasePlanReview and PhaseCodeReview→PhaseImplUAT require all 3 review axes to ACCEPT (consensus gate).
//   - PhaseCodeReview→PhaseImplUAT additionally requires blocker_count == 0 (BLOCKER gate).
//   - At PhaseReview/PhaseCodeReview with any REVISE vote, only the backward transition is available.
var PhaseSpecs = map[protocol.PhaseId]PhaseSpec{
	protocol.PhaseRequest:      {Transitions: []protocol.PhaseId{protocol.PhaseElicit}},
	protocol.PhaseElicit:       {Transitions: []protocol.PhaseId{protocol.PhasePropose}},
	protocol.PhasePropose:      {Transitions: []protocol.PhaseId{protocol.PhaseReview}},
	protocol.PhaseReview:       {Transitions: []protocol.PhaseId{protocol.PhasePlanReview, protocol.PhasePropose}},
	protocol.PhasePlanReview:   {Transitions: []protocol.PhaseId{protocol.PhaseRatify}},
	protocol.PhaseRatify:       {Transitions: []protocol.PhaseId{protocol.PhaseHandoff}},
	protocol.PhaseHandoff:      {Transitions: []protocol.PhaseId{protocol.PhaseImplPlan}},
	protocol.PhaseImplPlan:     {Transitions: []protocol.PhaseId{protocol.PhaseWorkerSlices}},
	protocol.PhaseWorkerSlices: {Transitions: []protocol.PhaseId{protocol.PhaseCodeReview}},
	protocol.PhaseCodeReview:   {Transitions: []protocol.PhaseId{protocol.PhaseImplUAT, protocol.PhaseWorkerSlices}},
	protocol.PhaseImplUAT:      {Transitions: []protocol.PhaseId{protocol.PhaseLanding}},
	protocol.PhaseLanding:      {Transitions: []protocol.PhaseId{protocol.PhaseComplete}},
}

// consensusGated is the set of (from, to) transitions requiring all-ACCEPT consensus.
var consensusGated = map[[2]protocol.PhaseId]struct{}{
	{protocol.PhaseReview, protocol.PhasePlanReview}:  {},
	{protocol.PhaseCodeReview, protocol.PhaseImplUAT}: {},
}

// blockerGated is the set of (from, to) transitions blocked when blocker_count > 0.
var blockerGated = map[[2]protocol.PhaseId]struct{}{
	{protocol.PhaseCodeReview, protocol.PhaseImplUAT}: {},
}

// reviseDrivesBackPhases are review phases where a REVISE vote forces only the
// backward transition (revision loop).
var reviseDrivesBackPhases = map[protocol.PhaseId]struct{}{
	protocol.PhaseReview:     {},
	protocol.PhaseCodeReview: {},
}

// ─── EpochStateMachine ────────────────────────────────────────────────────────

// EpochStateMachine manages the 12-phase epoch lifecycle with phase transition
// validation and vote/blocker gate checks.  Pure Go — no Temporal dependency.
//
// Port of Python EpochStateMachine in scripts/aura_protocol/state_machine.py.
//
// Usage:
//
//	sm := NewEpochStateMachine("epoch-123")
//	record, err := sm.Advance(protocol.P2_Elicit, "architect", "classification confirmed")
//	sm.RecordVote(types.AxisCorrectness, types.VoteAccept)
//	sm.RecordVote(types.AxisTestQuality, types.VoteAccept)
//	sm.RecordVote(types.AxisElegance, types.VoteAccept)
//	record, err = sm.Advance(protocol.P5_Uat, "reviewer", "all 3 vote ACCEPT")
type EpochStateMachine struct {
	state *types.EpochState
	specs map[protocol.PhaseId]PhaseSpec
}

// NewEpochStateMachine creates a new EpochStateMachine initialized to PhaseRequest.
// Accepts an optional specs map for dependency injection in tests; pass nil to
// use the canonical PhaseSpecs.
func NewEpochStateMachine(epochId string, specs map[protocol.PhaseId]PhaseSpec) *EpochStateMachine {
	if specs == nil {
		specs = PhaseSpecs
	}
	return &EpochStateMachine{
		state: &types.EpochState{
			EpochId:           epochId,
			CurrentPhase:      protocol.PhaseRequest,
			CurrentRole:       types.RoleEpoch,
			CompletedPhases:   []protocol.PhaseId{},
			ReviewVotes:       make(map[types.ReviewAxis]types.VoteType),
			TransitionHistory: []types.TransitionRecord{},
		},
		specs: specs,
	}
}

// State returns the current epoch state. Callers must not modify the returned
// pointer directly; use RecordVote, RecordBlocker, and Advance instead.
func (sm *EpochStateMachine) State() *types.EpochState {
	return sm.state
}

// AvailableTransitions returns the transitions currently available from the
// current phase, filtered by vote/blocker/consensus state.
//
// Gate rule priority (highest first):
//  1. REVISE gate: If at p4/p10 with any REVISE vote, only backward transition.
//  2. Consensus gate: p4→p5 / p10→p11 excluded until all 3 axes ACCEPT.
//  3. BLOCKER gate: p10→p11 excluded while blocker_count > 0.
//
// Returns empty slice when current phase is Complete or has no spec.
func (sm *EpochStateMachine) AvailableTransitions() []protocol.PhaseId {
	current := sm.state.CurrentPhase
	if current == protocol.PhaseComplete {
		return nil
	}
	spec, ok := sm.specs[current]
	if !ok {
		return nil
	}

	all := make([]protocol.PhaseId, len(spec.Transitions))
	copy(all, spec.Transitions)

	// Rule 1: REVISE gate — at a review phase with any REVISE vote, only backward.
	if _, isReviewPhase := reviseDrivesBackPhases[current]; isReviewPhase && sm.hasAnyRevise() {
		var backward []protocol.PhaseId
		for _, to := range all {
			key := [2]protocol.PhaseId{current, to}
			_, isCons := consensusGated[key]
			_, isBlock := blockerGated[key]
			if !isCons && !isBlock {
				backward = append(backward, to)
			}
		}
		return backward
	}

	// Rule 2: Consensus gate.
	var filtered []protocol.PhaseId
	for _, to := range all {
		key := [2]protocol.PhaseId{current, to}
		if _, gated := consensusGated[key]; gated && !sm.HasConsensus() {
			continue
		}
		filtered = append(filtered, to)
	}
	all = filtered

	// Rule 3: BLOCKER gate.
	if sm.state.BlockerCount > 0 {
		var noBlock []protocol.PhaseId
		for _, to := range all {
			key := [2]protocol.PhaseId{current, to}
			if _, gated := blockerGated[key]; !gated {
				noBlock = append(noBlock, to)
			}
		}
		all = noBlock
	}

	return all
}

// ValidateAdvance returns a list of violation strings for a proposed transition.
// An empty list means the transition is valid and Advance would succeed.
//
// Checks (in order):
//  1. Current phase is not COMPLETE.
//  2. to_phase is in the transition table for the current phase.
//  3. Consensus gate: p4→p5 / p10→p11 require HasConsensus().
//  4. BLOCKER gate: p10→p11 requires BlockerCount == 0.
func (sm *EpochStateMachine) ValidateAdvance(toPhase protocol.PhaseId) []string {
	var violations []string
	current := sm.state.CurrentPhase

	if current == protocol.PhaseComplete {
		violations = append(violations,
			"epoch is already COMPLETE; no further transitions are possible")
		return violations
	}

	spec, ok := sm.specs[current]
	if !ok {
		violations = append(violations,
			fmt.Sprintf("current phase %q has no spec in the transition table", current))
		return violations
	}

	validTargets := make(map[protocol.PhaseId]struct{})
	for _, t := range spec.Transitions {
		validTargets[t] = struct{}{}
	}

	if _, ok := validTargets[toPhase]; !ok {
		var targets []string
		for t := range validTargets {
			targets = append(targets, string(t))
		}
		violations = append(violations,
			fmt.Sprintf("transition %q → %q is not in the transition table; valid targets: %v",
				current, toPhase, targets))
		return violations
	}

	// Consensus gate.
	key := [2]protocol.PhaseId{current, toPhase}
	if _, gated := consensusGated[key]; gated && !sm.HasConsensus() {
		var accepted []string
		for ax, v := range sm.state.ReviewVotes {
			if v == types.VoteAccept {
				accepted = append(accepted, string(ax))
			}
		}
		violations = append(violations, fmt.Sprintf(
			"consensus required for %q → %q: all 3 axes (correctness, test_quality, elegance) must ACCEPT; accepted so far: %v",
			current, toPhase, accepted))
	}

	// BLOCKER gate.
	if _, gated := blockerGated[key]; gated && sm.state.BlockerCount > 0 {
		violations = append(violations, fmt.Sprintf(
			"BLOCKER gate for %q → %q: %d unresolved blocker(s) must be resolved first",
			current, toPhase, sm.state.BlockerCount))
	}

	return violations
}

// Advance transitions the epoch to toPhase.
//
// Validates first; returns TransitionError if invalid. On success:
//   - Appends the current phase to CompletedPhases.
//   - Sets CurrentPhase = toPhase.
//   - Appends a TransitionRecord to TransitionHistory.
//   - When entering P10_CodeReview, initializes severity tracking (not in Python; handled by S7).
//   - Clears ReviewVotes (votes are phase-scoped).
//   - Clears LastError.
//
// timestamp is used for the record; pass time.Now() for production or a fixed
// time for determinism in tests. In the Temporal workflow, pass workflow.Now().
func (sm *EpochStateMachine) Advance(
	toPhase protocol.PhaseId,
	triggeredBy string,
	conditionMet string,
	timestamp time.Time,
) (*types.TransitionRecord, error) {
	violations := sm.ValidateAdvance(toPhase)
	if len(violations) > 0 {
		return nil, &TransitionError{Violations: violations}
	}

	record := types.TransitionRecord{
		FromPhase:    sm.state.CurrentPhase,
		ToPhase:      toPhase,
		Timestamp:    timestamp,
		TriggeredBy:  triggeredBy,
		ConditionMet: conditionMet,
		Success:      true,
	}

	sm.state.CompletedPhases = append(sm.state.CompletedPhases, sm.state.CurrentPhase)
	sm.state.CurrentPhase = toPhase
	sm.state.TransitionHistory = append(sm.state.TransitionHistory, record)

	// Clear votes — they are scoped to the phase in which they were cast.
	sm.state.ReviewVotes = make(map[types.ReviewAxis]types.VoteType)
	sm.state.LastError = nil

	return &record, nil
}

// RecordVote records a reviewer vote for the given axis.
// Overwrites any previous vote for the same axis.
//
// Returns an error if axis is not a valid ReviewAxis value.
func (sm *EpochStateMachine) RecordVote(axis types.ReviewAxis, vote types.VoteType) error {
	if !axis.IsValid() {
		return fmt.Errorf(
			"invalid review axis %q; must be one of %v — "+
				"use types.AxisCorrectness, types.AxisTestQuality, or types.AxisElegance",
			axis, types.AllReviewAxes,
		)
	}
	sm.state.ReviewVotes[axis] = vote
	return nil
}

// HasConsensus returns true if all 3 review axes have ACCEPT votes.
func (sm *EpochStateMachine) HasConsensus() bool {
	for _, ax := range types.AllReviewAxes {
		v, ok := sm.state.ReviewVotes[ax]
		if !ok || v != types.VoteAccept {
			return false
		}
	}
	return true
}

// RecordBlocker updates the blocker count.
// resolved=false: increment (new blocker); resolved=true: decrement (blocker resolved).
// Clamped to 0; cannot go negative.
func (sm *EpochStateMachine) RecordBlocker(resolved bool) {
	if resolved {
		if sm.state.BlockerCount > 0 {
			sm.state.BlockerCount--
		}
	} else {
		sm.state.BlockerCount++
	}
}

// RecordFailedTransition appends a failed TransitionRecord to the transition
// history and records the error message in LastError.
//
// This is the correct mutation path for failed advances in workflow.go — callers
// must not mutate State() directly (see State() doc). fromPhase and toPhase
// describe the attempted transition; err is the failure reason.
func (sm *EpochStateMachine) RecordFailedTransition(
	fromPhase, toPhase protocol.PhaseId,
	timestamp time.Time,
	triggeredBy string,
	err error,
) {
	failedRecord := types.TransitionRecord{
		FromPhase:    fromPhase,
		ToPhase:      toPhase,
		Timestamp:    timestamp,
		TriggeredBy:  triggeredBy,
		ConditionMet: fmt.Sprintf("FAILED: %s", err.Error()),
		Success:      false,
	}
	sm.state.TransitionHistory = append(sm.state.TransitionHistory, failedRecord)
	errMsg := err.Error()
	sm.state.LastError = &errMsg
}

// hasAnyRevise returns true if any recorded vote is REVISE.
func (sm *EpochStateMachine) hasAnyRevise() bool {
	for _, v := range sm.state.ReviewVotes {
		if v == types.VoteRevise {
			return true
		}
	}
	return false
}

// ─── TransitionError ─────────────────────────────────────────────────────────

// TransitionError is returned by Advance when a proposed transition is invalid.
// Violations is always non-empty when returned.
type TransitionError struct {
	Violations []string
}

func (e *TransitionError) Error() string {
	if len(e.Violations) == 1 {
		return e.Violations[0]
	}
	msg := fmt.Sprintf("%d transition violations: ", len(e.Violations))
	for i, v := range e.Violations {
		if i > 0 {
			msg += "; "
		}
		msg += v
	}
	return msg
}
