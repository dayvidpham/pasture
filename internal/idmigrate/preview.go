package idmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// reader is the read-only query surface Preview needs: an open database or a
// transaction both satisfy it.
type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Preview is the strictly read-only dry-run report. It writes nothing: no
// transaction, no DDL, no marker, no backup. Every count is what conversion
// WOULD change.
type Preview struct {
	Inspection
	// Columns counts, per "table.column", the distinct values that would convert.
	Columns map[string]int
	// EpochProjectionRows and AuditPayloadRows count structured documents that
	// declare identity fields that would convert.
	EpochProjectionRows int
	AuditPayloadRows    int
	// RecordedVersion is the audit schema version this run read.
	RecordedVersion int
	// PendingCatchUpSteps is how many audit steps the deferred catch-up phase
	// (P2) would apply before the conversion transaction runs.
	PendingCatchUpSteps int
	// EpochIDLowerBound is a LOWER-BOUND count of work the catch-up exposes: the
	// retired-shaped audit_events.epoch_id values a pre-v4 store still carries.
	// It is informational only — never a verdict — because the catch-up moves
	// them into context_edges where the conversion then translates them.
	EpochIDLowerBound int
	// Blockers names runtime-state tables that hold rows and would refuse the
	// conversion.
	Blockers []string
}

// WouldChange reports whether conversion has any work to do on this store.
func (p Preview) WouldChange() bool {
	for _, count := range p.Columns {
		if count > 0 {
			return true
		}
	}
	return p.EpochProjectionRows > 0 || p.AuditPayloadRows > 0
}

// previewDependencyColumns mirrors the dependency's own closed set of
// namespaced-identity columns. The dry run reports them so the operator sees the
// FULL affected count: ConvertIDsInTx rewrites these, and the dry run must not
// under-report by listing only pasture-owned tables.
var previewDependencyColumns = []struct{ table, column string }{
	{"tasks", "id"}, {"tasks", "owner_id"}, {"agents", "id"},
	{"agents_human", "agent_id"}, {"agents_ml", "agent_id"}, {"agents_software", "agent_id"},
	{"activities", "id"}, {"activities", "agent_id"},
	{"comments", "id"}, {"comments", "task_id"}, {"comments", "author_id"},
	{"labels", "task_id"}, {"edges", "source_id"}, {"edges", "target_id"},
	{"task_attributions", "task_id"}, {"task_attributions", "actor_id"},
	{"actor_namespace_claims", "claimant_id"}, {"fixed_actor_manifest_entries", "actor_id"},
	{"journal", "actor_id"}, {"journal_activity_creations", "activity_id"},
	{"journal_authority_assignment_episodes", "task_id"}, {"journal_authority_assignment_episodes", "actor_id"},
	{"journal_decisions", "task_id"}, {"journal_evidence", "task_id"}, {"journal_task_events", "task_id"},
	{"governed_allocation_operations", "actor_id"},
}

// DryRun runs the read-only dry run. It never writes and never begins a
// transaction, so the store's bytes (and its SHA-256) are unchanged by it.
func DryRun(ctx context.Context, db *sql.DB, storePath string) (Preview, error) {
	return preview(ctx, db, storePath)
}

func preview(ctx context.Context, db *sql.DB, storePath string) (Preview, error) {
	inspection, err := Inspect(ctx, db, storePath)
	if err != nil {
		return Preview{}, err
	}
	out := Preview{Inspection: inspection, Columns: map[string]int{}}
	if out.RecordedVersion, err = readRecordedVersion(ctx, db); err != nil {
		return Preview{}, err
	}
	if target := audit.MaxKnownSchemaVersion - 1; out.RecordedVersion < target {
		out.PendingCatchUpSteps = target - out.RecordedVersion
	}
	if out.EpochIDLowerBound, err = countLegacyEpochIDs(ctx, db, out.RecordedVersion); err != nil {
		return Preview{}, err
	}
	if !inspection.Retired() {
		return out, nil
	}
	cols := make([]struct{ table, column string }, 0, len(pastureIdentityColumns)+len(previewDependencyColumns))
	cols = append(cols, pastureIdentityColumns...)
	cols = append(cols, previewDependencyColumns...)
	for _, col := range cols {
		key := col.table + "." + col.column
		count, err := countRetired(ctx, db, col.table, col.column)
		if err != nil {
			return Preview{}, err
		}
		if count > 0 {
			out.Columns[key] = count
		}
	}
	if count, err := countRetired(ctx, db, "context_edges", "context_id"); err != nil {
		return Preview{}, err
	} else if count > 0 {
		out.Columns["context_edges.context_id"] = count
	}
	if out.EpochProjectionRows, err = countEpochProjectionChanges(ctx, db); err != nil {
		return Preview{}, err
	}
	if out.AuditPayloadRows, err = countAuditPayloadChanges(ctx, db); err != nil {
		return Preview{}, err
	}
	blockers, err := runtimeStateBlockers(ctx, db)
	if err != nil {
		return Preview{}, err
	}
	out.Blockers = blockers
	sort.Strings(out.Blockers)
	return out, nil
}

func countRetired(ctx context.Context, q reader, table, column string) (int, error) {
	exists, err := tableExistsReader(ctx, q, table)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`SELECT DISTINCT "%s" FROM "%s" WHERE "%s" IS NOT NULL`, column, table, column))
	if err != nil {
		return 0, conversionError(fmt.Sprintf("Pasture couldn't count convertible values in %s.%s.", table, column), "The read-only dry-run SELECT failed.", "Nothing was changed by the dry run.", "Confirm the store is readable and retry.", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		if looksRetired(raw) {
			count++
		}
	}
	return count, rows.Err()
}

func countEpochProjectionChanges(ctx context.Context, q reader) (int, error) {
	exists, err := tableExistsReader(ctx, q, "epoch_state_projection")
	if err != nil || !exists {
		return 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT state_json FROM epoch_state_projection`)
	if err != nil {
		return 0, conversionError("Pasture couldn't read the epoch state projection for the dry run.", "The read-only SELECT failed.", "Nothing was changed by the dry run.", "Confirm the store is readable and retry.", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return 0, err
		}
		var decoded protocol.EpochState
		if err := json.Unmarshal([]byte(state), &decoded); err != nil {
			return 0, conversionError("An epoch projection row could not be decoded for the dry run.", "The stored state_json does not match the typed epoch-state owner.", "Nothing was changed by the dry run.", "Repair or archive the projection row; a projection is disposable and is rebuilt from the journal.", err)
		}
		changed, err := convertEpochState(&decoded)
		if err != nil {
			return 0, err
		}
		if changed {
			count++
		}
	}
	return count, rows.Err()
}

func countAuditPayloadChanges(ctx context.Context, q reader) (int, error) {
	exists, err := tableExistsReader(ctx, q, "audit_events")
	if err != nil || !exists {
		return 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT payload FROM audit_events`)
	if err != nil {
		return 0, conversionError("Pasture couldn't read the audit event payloads for the dry run.", "The read-only SELECT failed.", "Nothing was changed by the dry run.", "Confirm the store is readable and retry.", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return 0, err
		}
		var document any
		if err := json.Unmarshal([]byte(payload), &document); err != nil {
			return 0, conversionError("An audit event payload could not be decoded for the dry run.", "The stored payload is not valid JSON.", "Nothing was changed by the dry run.", "Repair the event payload from the archive, then retry.", err)
		}
		changed, err := convertPayloadNode(document)
		if err != nil {
			return 0, err
		}
		if changed {
			count++
		}
	}
	return count, rows.Err()
}

func runtimeStateBlockers(ctx context.Context, q reader) ([]string, error) {
	var blockers []string
	for _, table := range runtimeStateTables {
		exists, err := tableExistsReader(ctx, q, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		var count int64
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&count); err != nil {
			return nil, conversionError("Pasture couldn't inspect the runtime-state table "+table+" for the dry run.", "The read-only row count failed.", "Nothing was changed by the dry run.", "Confirm the store is readable and retry.", err)
		}
		if count > 0 {
			blockers = append(blockers, fmt.Sprintf("%s (%d row(s))", table, count))
		}
	}
	return blockers, nil
}

func tableExistsReader(ctx context.Context, q reader, table string) (bool, error) {
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", table).Scan(&exists); err != nil {
		return false, conversionError("Pasture couldn't inspect the store's table catalog.", err.Error(), "Nothing was changed by the dry run.", "Confirm the store is readable and retry.", err)
	}
	return exists, nil
}

// countLegacyEpochIDs reports a LOWER-BOUND count of work the catch-up phase
// exposes: the recognizable retired-shaped audit_events.epoch_id values a store
// below v4 still carries. The column is dropped by the v3→v4 step, which copies
// the values into context_edges; this count is informational, never a verdict,
// so the dry run does not claim an audit-only legacy store is empty.
func countLegacyEpochIDs(ctx context.Context, q reader, recordedVersion int) (int, error) {
	if recordedVersion >= 4 {
		return 0, nil
	}
	exists, err := tableExistsReader(ctx, q, "audit_events")
	if err != nil || !exists {
		return 0, err
	}
	hasColumn, err := columnExistsReader(ctx, q, "audit_events", "epoch_id")
	if err != nil || !hasColumn {
		return 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT epoch_id FROM audit_events WHERE epoch_id IS NOT NULL`)
	if err != nil {
		return 0, conversionError("Pasture couldn't read the legacy audit epoch attachments for the dry run.", err.Error(), "Nothing was changed by the dry run.", "Confirm the store is readable and retry.", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		if looksRetired(raw) {
			count++
		}
	}
	return count, rows.Err()
}

func columnExistsReader(ctx context.Context, q reader, table, column string) (bool, error) {
	var existing string
	err := q.QueryRowContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&existing)
	switch err {
	case sql.ErrNoRows:
		return false, nil
	case nil:
		return true, nil
	default:
		return false, err
	}
}
