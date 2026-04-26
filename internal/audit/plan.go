// Package audit — plan.go
//
// Public read-only helpers used by the `pasture migrate` CLI to render a
// dry-run plan WITHOUT modifying the file. They sit alongside Migrate and
// share the same migrationSteps() registry, so the dry-run output is always
// in sync with what an apply would actually do (no separate hard-coded list).
//
// The CLI handler in internal/handlers/migrate.go is the only consumer; the
// only reason these helpers are package-public (rather than *_test exports)
// is the handlers package needs to call them from a different package.
//
// PROPOSAL-2 §7.9: `pasture migrate --dry-run` prints the planned migrations
// (e.g. "v1->v2: add the schema-version tracker table") and exits 0 without
// modifying the file. PlanMigrations + ReadSchemaVersion give the handler
// everything it needs to compose that output.
//
// Wording note (Phase 11 R1-A, 2026-04-25): step descriptions are written in
// plain language for non-specialist users reading `pasture migrate --dry-run`.
// When a step both moves data and removes structure, the description states
// the backfill FIRST and the drop SECOND so users see what happens to their
// data before what gets removed (avoids misreading "drop X, write Y" as
// destructive-then-additive when the actual order is additive-then-destructive).

package audit

import (
	"database/sql"
)

// MigrationStepSummary describes one forward step the migrator WOULD apply
// against a database currently at FromVersion. Used by the `pasture migrate
// --dry-run` CLI subcommand to render the plan.
//
// Description is the human-readable summary keyed by FromVersion + ToVersion;
// it MUST stay in sync with the actual migration body in migrate_v*.go (the
// CI dry-run integration test in S6 asserts the wording). When workers add a
// new vN→vN+1 migration, they ALSO add a Description entry to the
// stepDescription() map below — there is no other place to add it.
type MigrationStepSummary struct {
	FromVersion int
	ToVersion   int
	Description string
}

// stepDescription returns the human-readable description for a (from, to)
// migration pair. Wording is plain-language for non-specialist users (Phase
// 11 R1-A, 2026-04-25) — when a step both backfills data AND drops a column,
// the backfill is named FIRST so users see "your data is preserved into <new
// table> before the old column is removed", not "drop happens first".
//
// When workers land a new vN→vN+1 migration, append the corresponding entry
// here AND register the step in migrationSteps() — the dry-run will then
// surface it automatically without any extra wiring.
func stepDescription(fromVersion, toVersion int) string {
	switch {
	case fromVersion == 1 && toVersion == 2:
		return "add the schema-version tracker table (no data is changed)"
	case fromVersion == 2 && toVersion == 3:
		return "add the context-edge, agent-category, and known-agent tables, then backfill agent IDs into audit events and drop the legacy role column"
	case fromVersion == 3 && toVersion == 4:
		return "backfill epoch IDs into the context-edge table, then drop the legacy epoch_id column"
	default:
		// Forward-compatible default: workers who add a new step will see
		// this generic text and know to add a tailored description here.
		return "schema migration"
	}
}

// ReadSchemaVersion returns the highest schema version recorded in
// audit_schema_meta. Returns 1 if the table does not exist (legacy
// pre-PROPOSAL-2 database). Returns 0 only on infrastructure failure (with a
// *pasterrors.StructuredError).
//
// This is the public face of readVersion (which is package-private) so the
// `pasture migrate` CLI handler can probe the on-disk version WITHOUT running
// the migrator. Used by the dry-run path AND by the post-apply success line
// (which prints "migrated <db> from v<from> to v<to>").
//
// Layer Integration Point owned by S1 (read helpers) + S6 (CLI consumer).
func ReadSchemaVersion(db *sql.DB) (int, error) {
	return readVersion(db)
}

// PlanMigrations returns the ordered list of forward steps the migrator WOULD
// apply against a database currently at currentVersion. An empty slice means
// the database is already at MaxKnownSchemaVersion (no work to do).
//
// The function is pure: it touches no on-disk state. It iterates the same
// registry as Migrate so the dry-run is guaranteed to match the apply.
//
// If currentVersion is greater than MaxKnownSchemaVersion (a future binary
// wrote the database), PlanMigrations returns an empty slice; the CLI handler
// is responsible for surfacing the newer-schema rejection error separately
// via Migrate (which returns the actionable *StructuredError).
//
// Layer Integration Point owned by S1 + S6.
func PlanMigrations(currentVersion int) []MigrationStepSummary {
	if currentVersion >= MaxKnownSchemaVersion {
		return nil
	}
	plan := make([]MigrationStepSummary, 0)
	for _, step := range migrationSteps() {
		if step.fromVersion < currentVersion {
			continue
		}
		if step.fromVersion >= MaxKnownSchemaVersion {
			break
		}
		plan = append(plan, MigrationStepSummary{
			FromVersion: step.fromVersion,
			ToVersion:   step.toVersion,
			Description: stepDescription(step.fromVersion, step.toVersion),
		})
	}
	return plan
}
