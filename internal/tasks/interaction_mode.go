// Package tasks — same-package AFK interaction-mode policy (#49).
//
// interaction_mode.go adds the concrete interaction-mode decision the #43 base
// decision-ledger types deliberately excluded. AFK ("away from keyboard") is an
// explicit, per-epoch INTERACTION POLICY derived from canonical ledger entries — it
// is never a permission layer, a disposable runtime session, or a one-shot
// authorization. There is no InteractionModeRevision row, task, relation, or table:
// the mode is a pure fold (EffectiveMode) over the immutable decision ledger.
//
// Default mode is normal/nil. Every one of the four source/target pairs
// (normal->normal, normal->afk, afk->normal, afk->afk) is an explicit user decision
// that appends a successor ledger entry; a mode-change entry records its From/To in
// the decision payload and (at the live-commit layer) the preceding cursor in the
// entry's generic context, so after commit the entry's own EntryID is the new cursor.
// Changing mode never stops, ends, recreates, or reauthorizes a runtime agent or
// logical assignment; only decision-bearing / interaction-sensitive commands derive
// mode.
//
// Seed dependency (provenance PR #12, user gate pending). This file delivers the pure
// POLICY layer — the mode enum, the typed decision, and the deterministic EffectiveMode
// fold — which is exhaustively testable with no journal and no seeded ordinal-zero
// actor. Actually COMMITTING a mode change is a decision-ledger append whose Decider is
// the epoch root's immutable registered UserActorID; that append (and the
// `pasture task epoch interaction-mode set` CLI it backs) is designed on top of these
// pure types but its end-to-end attribution genuinely needs the seeded user actor, so
// it is deferred pending-seed like the base package's live review-start path — never faked.
//
// Delivered-surface divergence #5 (signature). The issue sketches EffectiveMode as
// func EffectiveMode(entries []DecisionLedgerEntry) (InteractionModeCursor, error) — no
// PolicySet parameter. Delivered here (and on EvaluateRatify in afk_ratify.go) with an
// explicit leading PolicySet parameter instead: EffectiveMode decodes mode-change entries
// through PolicySet.modeChanged, and policy_set.go's own package doc states "There is NO
// init-time registry and NO process-global set: the PolicySet is constructed explicitly
// ... and passed by value" — a claim that must hold for every entry point in this package,
// not only PolicySet's own constructor. A prior revision closed that gap with an unexported
// package-level sync.Once-memoized PolicySet singleton (deterministic and side-effect-free,
// but still a process-global contradicting the no-global contract in the same PR); this
// revision removes it and threads the caller-constructed PolicySet explicitly instead, so
// the no-process-global claim holds everywhere in the package and every call site is
// independently testable with its own PolicySet.

package tasks

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// InteractionMode is the per-epoch autonomous-interaction policy. It is a string enum
// (matching the issue's canonical wire spelling) with exactly two members; the empty
// value is not a mode, and the default effective mode is InteractionNormal.
type InteractionMode string

const (
	// InteractionNormal is the default, full-interaction policy.
	InteractionNormal InteractionMode = "normal"
	// InteractionAFK is the reduced-interaction, autonomous-progress policy.
	InteractionAFK InteractionMode = "afk"
)

func (m InteractionMode) valid() bool {
	return m == InteractionNormal || m == InteractionAFK
}

// InteractionModeCursor is the effective mode plus the ledger entry that established it.
// Entry is nil exactly when the mode is the default normal (no mode entry has been
// appended); after a mode change, Entry is that change entry's immutable id.
type InteractionModeCursor struct {
	Entry *DecisionLedgerEntryID
	Mode  InteractionMode
}

// InteractionModeChanged is the typed payload of a mode-change decision: the exact
// source and target modes. It is the sole codec payload of DecisionInteractionModeChanged.
type InteractionModeChanged struct {
	From InteractionMode `json:"from"`
	To   InteractionMode `json:"to"`
}

// DecisionInteractionModeChanged is the stable decision-ledger kind of a mode change.
// It is a DecisionKindID (an internal decision-codec identity), NOT a provenance
// EventKind, so it retains the issue's canonical "/v1" spelling; the journal
// namespaced-name grammar (which forbids '/') governs only material EventKinds.
const DecisionInteractionModeChanged DecisionKindID = "pasture.interaction-mode.changed/v1"

// validateInteractionModeChanged rejects an unknown source or target mode. All four
// valid source/target combinations are accepted — including the same-mode transitions
// normal->normal and afk->afk — because each is an explicit, separately-recorded user
// decision, not a no-op.
func validateInteractionModeChanged(c InteractionModeChanged) error {
	if !c.From.valid() {
		return modeErr("InteractionModeChanged", fmt.Sprintf("the source mode %q is not a known interaction mode", c.From),
			"a mode change records the exact preceding mode, which must be normal or afk",
			"set From to the current effective mode (normal or afk)")
	}
	if !c.To.valid() {
		return modeErr("InteractionModeChanged", fmt.Sprintf("the target mode %q is not a known interaction mode", c.To),
			"a mode change records the exact desired mode, which must be normal or afk",
			"set To to the desired mode (normal or afk)")
	}
	return nil
}

// EffectiveMode folds an immutable, ledger-ordered slice of decision entries into the
// current interaction-mode cursor. It selects the latest valid interaction-mode entry;
// absence of any mode entry returns the default normal/nil cursor. Non-mode entries are
// ignored. The fold verifies the mode chain is consistent — each change's recorded
// source mode must equal the running effective mode — so a corrupted or mis-ordered
// ledger is reported rather than silently yielding a wrong mode. The input is never
// mutated.
//
// policy is the explicit PolicySet the caller constructed (typically via
// NewProductionPolicySet once and reused); EffectiveMode holds no package-level or
// process-global PolicySet of its own, matching policy_set.go's "constructed explicitly,
// passed by value" contract.
func EffectiveMode(policy PolicySet, entries []DecisionLedgerEntry) (InteractionModeCursor, error) {
	cursor := InteractionModeCursor{Entry: nil, Mode: InteractionNormal}
	for i := range entries {
		entry := entries[i]
		if entry.Decision.Kind != DecisionInteractionModeChanged {
			continue
		}
		changed, err := DecodeDecision(policy.Catalog, policy.modeChanged, entry.Decision)
		if err != nil {
			return InteractionModeCursor{}, fmt.Errorf("effective interaction mode: decode mode-change entry %q: %w", entry.ID, err)
		}
		if changed.From != cursor.Mode {
			return InteractionModeCursor{}, modeErr("EffectiveMode",
				fmt.Sprintf("mode-change entry %q records source mode %q but the effective mode at that point is %q", entry.ID, changed.From, cursor.Mode),
				"interaction-mode entries form a chain in which each change's source mode must equal the running effective mode",
				"append mode changes whose From matches the current effective mode, or repair the corrupted ledger")
		}
		id := entry.ID
		cursor = InteractionModeCursor{Entry: &id, Mode: changed.To}
	}
	return cursor, nil
}

func modeErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected an interaction-mode operation: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("Interaction-mode policy (internal/tasks/interaction_mode.go, %s).", where),
		Impact:   "The interaction-mode value was not derived or accepted; nothing was persisted.",
		Fix:      fix + ".",
	}
}
