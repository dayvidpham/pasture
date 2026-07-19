package provadapter

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/dayvidpham/provenance"
)

// migrate.go is the finite serial legacy-baseline migration coordinator over the
// pinned Provenance MigrateLegacyBaseline surface (pasture#14, §13). It is a
// bounded, terminating orchestration that adds four guarantees around the released
// whole-batch migration primitive:
//
//  1. Read-only source integrity. The caller supplies a SourceHasher over the
//     external pre-journal source; the coordinator hashes it before and after the
//     migration and fails closed if the bytes differ, proving the migration
//     extracted the source WITHOUT mutating it (§13 "source tables remain
//     byte-identical").
//  2. Deterministic ordering. The legacy rows are sorted by (RecordedAt,
//     LegacyRowID) — the honest per-row RecordedAt (UpdatedAt, §13) with the legacy
//     task id as the total-order tiebreak — before the batch is presented, so a
//     re-run and an audit see the identical, stable order regardless of input
//     order.
//  3. Stop-on-first-failure. MigrateLegacyBaseline is whole-batch fail-closed: an
//     unmappable owner or a schema mismatch aborts the ENTIRE batch and commits
//     nothing (a fortiori "stops on the first failure" with zero partial writes).
//     The coordinator surfaces that typed failure verbatim (errors.Is/As over
//     provenance.ErrMigrationOwnerUnmappable / *MigrationOwnerUnmappableError,
//     ErrSchemaPreflight / *SchemaPreflightError) and re-verifies source integrity
//     even on the failure path.
//  4. Idempotent re-run surfacing. MigrationResult distinguishes freshly written
//     baseline anchors from anchors an idempotent re-run returned unchanged via the
//     §9.4 short-circuit; the coordinator returns that result so a caller can prove
//     a second identical run created zero new anchors.
//
// Scope boundary (pasture#14, deferred to #43). The coordinator neither defines
// pasture.* events nor derives per-row OperationIDs nor batches audit-event fan-out
// nor owns replay/conflict tests for the non-task import — those are #43's. It only
// drives the released task-baseline migration deterministically with the integrity
// and idempotency guarantees above.

// SourceHasher returns a stable byte digest of the external pre-journal source the
// migration reads from. The coordinator calls it immediately before and after the
// migration and requires byte-equality, so it must be a deterministic function of
// the source's persisted bytes (e.g. a hash of the ordered legacy rows or a
// content hash of the source tables). It reads only; it must never mutate the
// source.
type SourceHasher func() ([]byte, error)

// BaselineMigrationRequest is the coordinator's whole-batch request. It mirrors the
// released provenance.MigrationInput but names the committing system actor and the
// established genesis bootstrap authority explicitly, and takes the legacy rows in
// any order (the coordinator sorts them deterministically before presenting them).
type BaselineMigrationRequest struct {
	// System is the migration's committing actor (the pasture-system actor, §2.1),
	// validated with ValidateActorID before the batch runs.
	System provenance.ActorID
	// BootstrapAuthority is the genesis-established bootstrap authority JournalID the
	// per-task baseline anchors execute under (§4.6, §13).
	BootstrapAuthority provenance.JournalID
	// Owners resolves a legacy owner string to a registered ActorID. A non-empty
	// legacy owner absent from this map fails the WHOLE batch (§13 item 4).
	Owners map[string]provenance.ActorID
	// Rows are the legacy task rows to migrate, in any input order.
	Rows []provenance.LegacyTaskRow
}

// BaselineMigrationOutcome reports how a coordinated migration resolved. Result is
// the released whole-batch MigrationResult (fresh vs short-circuited anchor counts,
// §13); OrderedRowIDs is the deterministic (RecordedAt, LegacyRowID) presentation
// order the coordinator applied; SourceUnchanged is true once the post-migration
// source hash matched the pre-migration hash.
type BaselineMigrationOutcome struct {
	Result          provenance.MigrationResult
	OrderedRowIDs   []provenance.TaskID
	SourceUnchanged bool
}

// ErrSourceMutatedDuringMigration is the sentinel the coordinator raises when the
// external source's bytes changed across the migration, violating the read-only
// extraction invariant (§13). It is distinct from any Provenance migration
// sentinel so a caller can tell an integrity breach from a domain rejection.
var ErrSourceMutatedDuringMigration = errors.New("provadapter: legacy source mutated during migration")

// RunBaselineMigration drives one finite, deterministic, read-only-verified
// legacy-baseline migration over the pinned Provenance MigrateLegacyBaseline. It
// sorts the rows by (RecordedAt, LegacyRowID), hashes the source before and after,
// runs the whole-batch migration, and re-verifies source integrity on both the
// success and failure paths.
//
// On a Provenance migration rejection (unmappable owner, schema mismatch, injected
// fault) the batch commits nothing and the typed error is returned verbatim for
// errors.Is/As; the coordinator still re-checks source integrity so a caller learns
// if a partial/aborted run somehow perturbed the source. On success the returned
// outcome carries the whole-batch MigrationResult and SourceUnchanged=true.
func RunBaselineMigration(j provenance.JournalAPI, req BaselineMigrationRequest, hashSource SourceHasher) (BaselineMigrationOutcome, error) {
	if j == nil {
		return BaselineMigrationOutcome{}, errors.New(
			"provadapter: cannot run baseline migration — what: the Provenance JournalAPI is nil; " +
				"why: the coordinator drives MigrateLegacyBaseline on the underlying journal; " +
				"where: internal/provadapter RunBaselineMigration; when: before the migration batch; " +
				"impact: no baseline is migrated; fix: pass Tracker.Journal() from an open Provenance tracker")
	}
	if hashSource == nil {
		return BaselineMigrationOutcome{}, errors.New(
			"provadapter: cannot run baseline migration — what: the SourceHasher is nil; " +
				"why: the coordinator proves the migration is read-only by comparing the source hash before and " +
				"after, and cannot skip that guarantee; where: internal/provadapter RunBaselineMigration; " +
				"when: before the migration batch; impact: no baseline is migrated; fix: pass a deterministic " +
				"read-only SourceHasher over the pre-journal source")
	}
	if err := ValidateActorID(req.System); err != nil {
		return BaselineMigrationOutcome{}, fmt.Errorf(
			"provadapter: baseline migration: the committing system actor is invalid: %w", err)
	}

	before, err := hashSource()
	if err != nil {
		return BaselineMigrationOutcome{}, fmt.Errorf(
			"provadapter: baseline migration: hash source before migration: %w", err)
	}

	ordered := sortedLegacyRows(req.Rows)
	orderedIDs := make([]provenance.TaskID, len(ordered))
	for i := range ordered {
		orderedIDs[i] = ordered[i].ID
	}

	res, migErr := j.MigrateLegacyBaseline(provenance.MigrationInput{
		System:             req.System,
		BootstrapAuthority: req.BootstrapAuthority,
		Owners:             req.Owners,
		Legacy:             ordered,
	})

	// Re-verify read-only integrity on BOTH paths: a fail-closed rejection must
	// leave the source byte-identical just as a success must.
	after, hashErr := hashSource()
	if hashErr != nil {
		return BaselineMigrationOutcome{}, fmt.Errorf(
			"provadapter: baseline migration: hash source after migration: %w", hashErr)
	}
	if !bytes.Equal(before, after) {
		return BaselineMigrationOutcome{OrderedRowIDs: orderedIDs}, fmt.Errorf(
			"%w — what: the legacy source's byte hash changed across the migration; why: pasture#14 migration "+
				"extracts the source strictly read-only (§13) and any change means a row was mutated or deleted; "+
				"where: internal/provadapter RunBaselineMigration integrity re-check; when: after MigrateLegacyBaseline "+
				"returned; impact: the source is no longer trustworthy as the migration's authoritative pre-image; "+
				"fix: investigate the writer that perturbed the source and re-run against an untouched snapshot",
			ErrSourceMutatedDuringMigration)
	}

	if migErr != nil {
		// Whole-batch fail-closed: nothing committed. Round-trip the typed provenance
		// migration error verbatim (errors.Is/As over the migration sentinels).
		return BaselineMigrationOutcome{OrderedRowIDs: orderedIDs, SourceUnchanged: true},
			fmt.Errorf("provadapter: baseline migration batch failed (nothing committed): %w", migErr)
	}

	return BaselineMigrationOutcome{
		Result:          res,
		OrderedRowIDs:   orderedIDs,
		SourceUnchanged: true,
	}, nil
}

// sortedLegacyRows returns a copy of rows in deterministic (RecordedAt,
// LegacyRowID) order: ascending honest RecordedAt (the legacy UpdatedAt, §13) with
// the legacy task id's canonical string as a total-order tiebreak so equal
// timestamps still yield one stable order. It never mutates the caller's slice.
func sortedLegacyRows(rows []provenance.LegacyTaskRow) []provenance.LegacyTaskRow {
	ordered := make([]provenance.LegacyTaskRow, len(rows))
	copy(ordered, rows)
	sort.SliceStable(ordered, func(a, b int) bool {
		ra, rb := ordered[a].UpdatedAt, ordered[b].UpdatedAt
		if !ra.Equal(rb) {
			return ra.Before(rb)
		}
		return ordered[a].ID.String() < ordered[b].ID.String()
	})
	return ordered
}
