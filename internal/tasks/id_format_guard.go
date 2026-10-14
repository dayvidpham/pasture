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
	"github.com/dayvidpham/pasture/internal/idmigrate"
)

// refuseIncompleteFenceOnState maps a state-machine verdict to the guard's
// refusal. It is the testable core: it renders the ONE shared Outcome->refusal
// mapping (idmigrate.OutcomeRefusal), so the guard and both command frontends
// can never disagree about a store's verdict.
func refuseIncompleteFenceOnState(state idmigrate.CutoverState) error {
	return idmigrate.OutcomeRefusal(state, false)
}
