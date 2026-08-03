package audit

import (
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

var lifecycleV6Statements = []string{
	`CREATE TABLE lifecycle_payload_blobs (
		digest TEXT PRIMARY KEY,
		body BLOB NOT NULL,
		byte_count INTEGER NOT NULL CHECK (byte_count >= 0 AND byte_count <= 1048576),
		CHECK (length(body) = byte_count)
	) WITHOUT ROWID`,
	`CREATE TABLE lifecycle_occurrences (
		journal_id INTEGER PRIMARY KEY,
		contract TEXT NOT NULL,
		event_kind INTEGER NOT NULL,
		received_at INTEGER NOT NULL,
		actor_id TEXT NOT NULL,
		capture_disposition INTEGER NOT NULL,
		payload_digest TEXT NOT NULL REFERENCES lifecycle_payload_blobs(digest),
		envelope_json BLOB NOT NULL,
		bindings_json BLOB NOT NULL,
		snapshot_journal_id INTEGER NOT NULL,
		CHECK (journal_id > 0),
		CHECK (snapshot_journal_id >= journal_id)
	)`,
	`CREATE INDEX lifecycle_occurrences_contract_event_journal
		ON lifecycle_occurrences(contract, event_kind, journal_id)`,
}

func migrateV5toV6(tx *sql.Tx, _ int64) error {
	for _, statement := range lifecycleV6Statements {
		if _, err := tx.Exec(statement); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "The lifecycle receipt schema could not be created while upgrading the database from version 5 to 6.",
				Why:      "SQLite rejected a table or index required for content-addressed payloads and replay-derived occurrence reads.",
				Where:    "Upgrading the audit database from version 5 to 6 (internal/audit/migrate_v5_v6.go in audit.migrateV5toV6).",
				Impact:   "The upgrade was rolled back atomically at version 5; existing task and audit data is unchanged.",
				Fix:      "Confirm the database is writable and has free space, inspect the error details, then run `pasture migrate` again.",
				Cause:    fmt.Errorf("execute lifecycle v6 schema statement: %w", err),
			}
		}
	}
	return nil
}

func migrateV5toV6Step(tx *sql.Tx, nowUnixNano int64) error {
	if err := migrateV5toV6(tx, nowUnixNano); err != nil {
		return err
	}
	return writeVersion(tx, 6, nowUnixNano)
}
