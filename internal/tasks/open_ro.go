// Package tasks — open_ro.go
//
// StatusReader is a lightweight, read-only reader for the audit trail used
// by the `pasture status` verb. It deliberately does NOT open a full
// protocol.TaskTracker (which runs schema migrations and creates directories):
// a pure-read status view must never modify the database.
//
// Callers open the database themselves (typically via dbconn.OpenReadOnlyDB),
// verify the schema version with CheckSchemaVersion, and then wrap the handle
// with NewStatusReaderFromDB. The caller retains ownership of the *sql.DB
// and is responsible for closing it; StatusReader.Close is a no-op.
//
// All queries are forwarded to the underlying *sql.DB in read-only mode.
package tasks

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// StatusReader is a read-only audit-trail reader for the status verb. The
// zero value is invalid; use NewStatusReaderFromDB to construct one.
type StatusReader struct {
	db *sql.DB
}

// NewStatusReaderFromDB wraps an already-open *sql.DB as a StatusReader. The
// caller retains ownership of db and is responsible for closing it; Close on
// the returned reader is a no-op. This constructor exists so callers that have
// already verified the schema version and opened the database (e.g. EpochStatus)
// can reuse the handle for enrichment queries without a second open.
func NewStatusReaderFromDB(db *sql.DB) *StatusReader {
	return &StatusReader{db: db}
}

// CheckSchemaVersion reads the audit schema version from an already-open
// read-only handle and returns a CategoryStorage error when it does not match
// audit.MaxKnownSchemaVersion.
//
// This function is SELECT-only: it issues no writes, DDL, migration, or any
// operation that could alter the database. It is safe to call on a read-only
// handle opened next to a running daemon.
func CheckSchemaVersion(db *sql.DB, dbPath string) error {
	version, verErr := readSchemaVersion(db)
	if verErr != nil {
		return verErr
	}
	if version < audit.MaxKnownSchemaVersion {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The database at %q is at schema version %d, but this build of pasture requires version %d.",
				dbPath, version, audit.MaxKnownSchemaVersion,
			),
			Why: "The on-disk schema is older than what this binary supports. The database may need " +
				"to be migrated.",
			Where:  "Checking database schema version (internal/tasks/open_ro.go in tasks.CheckSchemaVersion).",
			Impact: "Status can't read audit events until the schema is upgraded.",
			Fix: fmt.Sprintf(
				"1. Run the migration to upgrade the schema:\n"+
					"     pasture migrate\n"+
					"2. Then retry the status command.\n"+
					"   Database: %q", dbPath),
		}
	}
	if version > audit.MaxKnownSchemaVersion {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The database at %q was written by a newer pasture (schema version %d) than this build supports (version %d).",
				dbPath, version, audit.MaxKnownSchemaVersion,
			),
			Why: fmt.Sprintf(
				"A newer pasture binary upgraded the schema to version %d. This build only understands "+
					"up to version %d and cannot safely read a schema it does not know.",
				version, audit.MaxKnownSchemaVersion,
			),
			Where:  "Checking database schema version (internal/tasks/open_ro.go in tasks.CheckSchemaVersion).",
			Impact: "Status can't read audit events until you run a matching binary.",
			Fix: fmt.Sprintf(
				"1. Upgrade pasture to a version that supports schema version %d.\n"+
					"2. Do not downgrade the database file — there is no safe way to undo an upgrade.\n"+
					"   Database: %q", version, dbPath),
		}
	}
	return nil
}

// QueryEvents returns all audit events for epochId, oldest first, filtered
// optionally by phase and role (nil = no filter).
//
// Delegates to audit.QueryEventsOn, the single canonical query implementation.
// Both StatusReader and SqliteAuditTrail use the same shared function so any
// schema change (v5+) is applied in one place.
func (r *StatusReader) QueryEvents(ctx context.Context, epochId string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	return audit.QueryEventsOn(ctx, r.db, epochId, phase, role)
}

// CountEventsByEpoch returns a map from epochId to event count for all epochs
// that have at least one event. A single COUNT(*) GROUP BY query avoids the
// N+1 anti-pattern of loading every event payload for the listing view.
func (r *StatusReader) CountEventsByEpoch(ctx context.Context) (map[string]int, error) {
	rows, qErr := r.db.QueryContext(ctx,
		`SELECT ce.context_id, COUNT(*) AS cnt
		 FROM audit_events ae
		 INNER JOIN context_edges ce ON ce.event_id = ae.id
		 WHERE ce.context_kind = 'EpochContext'
		 GROUP BY ce.context_id`,
	)
	if qErr != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't count audit events by epoch.",
			Why:      "The grouped COUNT query on context_edges + audit_events failed.",
			Where:    "Enriching the epoch listing (internal/tasks/open_ro.go in tasks.StatusReader.CountEventsByEpoch).",
			Impact:   "Event counts won't appear in the epoch listing — epochs are still shown.",
			Fix: "1. Confirm the database is readable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the listing once the database is healthy.",
			Cause: qErr,
		}
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var epochId string
		var cnt int
		if err := rows.Scan(&epochId, &cnt); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Couldn't read an epoch event-count row.",
				Why:      "Scanning a result row from the grouped COUNT query failed.",
				Where:    "Enriching the epoch listing (internal/tasks/open_ro.go in tasks.StatusReader.CountEventsByEpoch).",
				Impact:   "Event counts won't appear in the epoch listing.",
				Fix:      "1. Retry the listing. If the error persists, check the database integrity.",
				Cause:    err,
			}
		}
		counts[epochId] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Lost the database stream while reading epoch event counts.",
			Why:      "The result iterator for the grouped COUNT query returned an error.",
			Where:    "Enriching the epoch listing (internal/tasks/open_ro.go in tasks.StatusReader.CountEventsByEpoch).",
			Impact:   "Event counts won't appear in the epoch listing.",
			Fix:      "1. Retry the listing. If the error persists, check the database integrity.",
			Cause:    err,
		}
	}
	return counts, nil
}

// Close is a no-op. StatusReader does not own its database handle — the
// caller that constructed it via NewStatusReaderFromDB retains ownership
// and is responsible for closing the underlying *sql.DB.
// Safe to call multiple times.
func (r *StatusReader) Close() error {
	return nil
}

// readSchemaVersion reads MAX(version) from audit_schema_meta. Returns 1 when
// the table is absent (legacy pre-v2 database). Returns 0 and an error on
// infrastructure failure.
func readSchemaVersion(db *sql.DB) (int, error) {
	var tableName string
	switch err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='audit_schema_meta'`,
	).Scan(&tableName); {
	case err == sql.ErrNoRows:
		return 1, nil
	case err != nil:
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't check the schema version of the database.",
			Why:      "The read of the schema-version tracking table failed.",
			Where:    "Checking schema version (internal/tasks/open_ro.go in tasks.readSchemaVersion).",
			Impact:   "Status can't verify the database is at the expected schema version.",
			Fix:      "1. Confirm the database file is not corrupted.\n2. Retry the command.",
			Cause:    err,
		}
	}

	var version sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't read the current schema version from the database.",
			Why:      "The SELECT on audit_schema_meta failed.",
			Where:    "Checking schema version (internal/tasks/open_ro.go in tasks.readSchemaVersion).",
			Impact:   "Status can't verify the database is at the expected schema version.",
			Fix:      "1. Confirm the database file is not corrupted.\n2. Retry the command.",
			Cause:    err,
		}
	}
	if !version.Valid {
		return 1, nil
	}
	return int(version.Int64), nil
}
