// Package tasks — id_format_guard.go
//
// The normal-open identity-format guard. It runs BEFORE the audit schema
// migrator and BEFORE any Provenance activation, fixed-actor registration,
// genesis reconciliation or journal decode: a store whose fence evidence is
// incomplete is refused here, with a storage-category (exit 5) error that names
// the path, the converting command and the unchanged state.
//
// PLACEMENT IS THE WHOLE POINT. The guard reads the store as the PREVIOUS
// build left it. It cannot run inside a migration step, because a legacy
// store's first open legitimately creates populated Provenance tables through
// the v2→v3 backfill; the identity-format marker and receipt are published
// later, by this build's cutover pipeline. A guard that ran after the migrator
// would refuse the store the migrator had just populated. Running it first also
// means a genuinely old-format store never reaches schema work, actor
// registration or a single journal decode.
//
// It is read-only: it opens its own short-lived handle (mode=ro) so it never
// writes, and it evaluates the state machine in ONE deferred read snapshot (see
// idmigrate.EvaluateCutoverOnHandle).
//
// The refusal TEXTS are owned by internal/idmigrate/refusal.go; this file is the
// guard frontend that renders them, and the command frontend renders the same
// functions, so the populated and unbacked-mark refusals can never drift apart.
package tasks

import (
	"context"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/idmigrate"
)

// refuseIncompleteFence inspects dbPath read-only and returns the storage
// refusal when the store's fence evidence is incomplete (a populated markerless
// store, or a marker/receipt inconsistency). A store that does not exist yet, an
// empty unmarked store, and a store already carrying the complete cutover
// evidence are allowed through.
func refuseIncompleteFence(ctx context.Context, dbPath string) error {
	state, err := idmigrate.InspectOpenState(ctx, dbPath)
	if err != nil {
		return err
	}
	return refuseIncompleteFenceOnState(state)
}

// refuseIncompleteFenceOnState maps a state-machine verdict to the guard's
// refusal. It is the testable core: only the two REFUSE outcomes and the
// newer-schema outcome produce an error.
func refuseIncompleteFenceOnState(state idmigrate.CutoverState) error {
	switch state.Outcome {
	case idmigrate.OutcomeRefusePopulated:
		return idmigrate.RefusePopulatedRefusal(state.StorePath)
	case idmigrate.OutcomeRefuseUnbackedMark:
		return idmigrate.UnbackedMarkRefusal(state.StorePath)
	case idmigrate.OutcomeNewerSchema:
		return audit.NewerSchemaError(state.RecordedVersion, audit.MaxKnownSchemaVersion)
	default:
		return nil
	}
}
