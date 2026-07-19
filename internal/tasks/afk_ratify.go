// Package tasks — AFK Plan deferral and ratification predicates (#49).
//
// afk_ratify.go holds the two pure policy predicates that govern the agent-attributed AFK
// Plan-UAT deferral lifecycle:
//
//   - EvaluatePlanDeferral gates recording a deferral: the exact latest mode cursor must be
//     AFK, there must be at least one stable held question, no FIX-NOW feedback, and an
//     exact proposal + input/output ledger revision.
//   - EvaluateRatify gates ratifying a prior deferral: folding the current ledger, the exact
//     mode cursor entry the deferral was anchored to must still be the latest mode entry AND
//     still be AFK.
//
// Together these give the issue's required behavior with no extra state: AFK R1 -> defer ->
// normal R2 cannot ratify (the latest entry is R2/normal, not R1/AFK); AFK R1 -> defer ->
// normal R2 -> AFK R3 cannot revive R1 either (the latest entry is R3, not R1), so a fresh
// deferral under R3 is required. A mode change committed after ratification does not
// retroactively undo ratified history because ratification is evaluated against the ledger
// as of its own transaction.
//
// Both predicates are pure functions over typed inputs and the immutable ledger, so they
// are exhaustively testable with no seeded ordinal-zero actor.

package tasks

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// PlanDeferralInput is the exact state EvaluatePlanDeferral checks before a deferral is
// recorded. Mode is the current effective interaction-mode cursor (as folded by
// EffectiveMode); it must be AFK and anchored to a concrete mode entry.
type PlanDeferralInput struct {
	Mode          InteractionModeCursor
	HeldQuestions []HeldUATQuestion
	Feedback      []UATFeedbackItem
	Snapshot      PlanUATSnapshot
}

// EvaluatePlanDeferral reports whether an AFK Plan deferral is eligible to be recorded. It
// returns nil when eligible and an actionable error naming the exact failed precondition
// otherwise.
func EvaluatePlanDeferral(in PlanDeferralInput) error {
	if in.Mode.Mode != InteractionAFK {
		return afkErr("EvaluatePlanDeferral", fmt.Sprintf("the current interaction mode is %q, not afk", in.Mode.Mode),
			"an AFK deferral is only valid while the epoch's effective interaction mode is afk",
			"set the interaction mode to afk before deferring, or accept/request changes instead")
	}
	if in.Mode.Entry == nil {
		return afkErr("EvaluatePlanDeferral", "the afk mode cursor has no anchoring entry",
			"a deferral is anchored to the exact AFK mode entry ratification later re-checks, so a default (nil) cursor cannot anchor one",
			"defer only when the AFK mode was set by an explicit mode-change entry")
	}
	if !hasStableHeldQuestion(in.HeldQuestions) {
		return afkErr("EvaluatePlanDeferral", "there is no stable held question",
			"an AFK deferral defers genuine open questions, so at least one held question must be stable",
			"defer only with at least one stable held question")
	}
	if hasFixNowFeedback(in.Feedback) {
		return afkErr("EvaluatePlanDeferral", "the plan UAT carries FIX-NOW feedback",
			"FIX-NOW feedback forces a changes-requested verdict and cannot be deferred",
			"resolve FIX-NOW feedback with a changes-requested verdict rather than deferring")
	}
	if err := validatePlanSnapshot("EvaluatePlanDeferral.Snapshot", in.Snapshot); err != nil {
		return err
	}
	return nil
}

// RatifyInput is the state EvaluateRatify checks: the recorded deferral and the current
// ledger it is being ratified against.
type RatifyInput struct {
	Deferred      PlanDeferredByAFK
	CurrentLedger []DecisionLedgerEntry
}

// EvaluateRatify reports whether a recorded AFK Plan deferral may be ratified against the
// current ledger. It folds the current ledger's effective mode cursor and requires that
// cursor to still be AFK and anchored to the exact mode entry the deferral recorded. Any
// later mode entry (of any target mode) makes the deferral ineligible; a fresh deferral is
// then required. It returns nil when eligible and an actionable error otherwise.
//
// policy is passed through to EffectiveMode explicitly; EvaluateRatify holds no
// package-level or process-global PolicySet of its own.
func EvaluateRatify(policy PolicySet, in RatifyInput) error {
	if err := validatePlanDeferredByAFK(in.Deferred); err != nil {
		return err
	}
	cursor, err := EffectiveMode(policy, in.CurrentLedger)
	if err != nil {
		return err
	}
	if cursor.Mode != InteractionAFK {
		return afkErr("EvaluateRatify", fmt.Sprintf("the current effective interaction mode is %q, not afk", cursor.Mode),
			"ratifying an AFK deferral requires the epoch to still be in afk mode under the exact anchoring entry",
			"record a fresh AFK deferral under the current mode entry before ratifying")
	}
	if cursor.Entry == nil {
		return afkErr("EvaluateRatify", "the current afk mode cursor has no anchoring entry",
			"a deferral is ratified only against a concrete AFK mode entry", "record a fresh AFK deferral under the current mode entry")
	}
	if *cursor.Entry != in.Deferred.ModeEntry {
		return afkErr("EvaluateRatify", fmt.Sprintf("the current afk mode entry %q is not the entry %q the deferral was anchored to", *cursor.Entry, in.Deferred.ModeEntry),
			"a later mode change replaced the anchoring entry, so the recorded deferral is stale even though the mode is again afk",
			"record a fresh AFK deferral under the current mode entry before ratifying")
	}
	return nil
}

func afkErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected an AFK deferral/ratify operation: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("AFK deferral/ratify policy (internal/tasks/afk_ratify.go, %s).", where),
		Impact:   "The AFK deferral or ratification was not accepted; nothing was persisted.",
		Fix:      fix + ".",
	}
}
