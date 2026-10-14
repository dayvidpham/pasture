package idmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// pastureIdentityColumns is the CLOSED set of pasture-owned TEXT columns that
// hold a declared namespace-plus-UUID identity. A column is listed only because
// its owning type declares it as an identity, never because its name ends in
// "id": assignment, operation, authority and result-slot identifiers are opaque
// and are deliberately absent. context_edges.context_id, the epoch projection
// JSON and the audit payload JSON are handled separately because they are
// kind-aware or structured.
var pastureIdentityColumns = []struct{ table, column string }{
	{"audit_events", "agent_id"},
	{"pasture_agent_categories", "agent_id"},
	{"pasture_well_known_agents", "agent_id"},
	{"pasture_system_identity", "committer_actor_id"},
	{"pasture_session_claim", "actor"},
	{"pasture_governed_allocation_audit", "child_task_id"},
	{"pasture_governed_allocation_audit", "occupant_id"},
}

// runtimeStateTables are the DBOS execution/history tables whose non-empty rows
// can carry identities inside opaque vendor bytes. They are refused, never
// rewritten: pasture has no owner recipe for vendor serialized history, so a
// guess could silently re-point work. `queues` and `application_versions` are
// configuration/metadata with no workflow input, so they are not refused.
var runtimeStateTables = []string{
	"workflow_status",
	"operation_outputs",
	"workflow_events",
	"workflow_events_history",
	"streams",
	"notifications",
	"event_dispatch_kv",
	"workflow_schedules",
}

// refuseRuntimeState fails closed when the store holds non-empty serialized
// DBOS execution/history state. It names the table and the row count and tells
// the operator to drain with the previous build and export through a supported
// owner path; it never deletes vendor history.
//
// The Impact sentence is scoped for the cutover pipeline: P1's archive and P2's
// catch-up are already committed when this fires, so the conversion transaction
// (which wrote nothing and was rolled back) is the only thing this refusal
// undid.
func refuseRuntimeState(ctx context.Context, tx *sql.Tx, storePath string, archive Archive, appliedCatchUpSteps int) error {
	for _, table := range runtimeStateTables {
		exists, err := tableExists(ctx, tx, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&count); err != nil {
			return conversionError("Pasture couldn't inspect the runtime-state table "+table+".", "The read-only row count failed.", "No store data was changed; the conversion aborts before any conversion write.", "Confirm the store is readable and retry.", err)
		}
		if count > 0 {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The store holds %d row(s) of serialized runtime state in %q that the identity conversion refuses to rewrite.", count, table),
				Why: "DBOS execution and history rows can embed task and actor identities inside opaque vendor bytes. " +
					"Pasture has no owner recipe for that serialization, so rewriting it would be a guess, and a guess can re-point work.",
				Where:  "Preflighting runtime state (internal/idmigrate/pasture.go in idmigrate.refuseRuntimeState).",
				Impact: fmt.Sprintf("The conversion transaction wrote nothing and was rolled back. This run's archive (%s) and its %d applied audit catch-up step(s) remain committed; the store keeps its retired identities.", archive.Path, appliedCatchUpSteps),
				Fix: "1. Finish or drain the in-flight work with the PREVIOUS pasture build (stop the daemon and let queued workflows settle).\n" +
					"2. Export or archive the runtime history through its supported owner path.\n" +
					"3. Re-run `pasture migrate ids` once the runtime tables are empty; do not delete vendor history by hand.",
			}
		}
	}
	return nil
}

// convertPastureIdentities converts every pasture-owned typed identity column
// and declared structured field inside the conversion transaction. It returns
// the number of rows it rewrote.
func convertPastureIdentities(ctx context.Context, tx *sql.Tx) (int, error) {
	total := 0
	for _, col := range pastureIdentityColumns {
		n, err := convertIdentityColumn(ctx, tx, col.table, col.column)
		if err != nil {
			return total, err
		}
		total += n
	}
	// context_edges.context_id is kind-aware: task, actor and activity identities
	// are all the same "<ns>/<uuid>" wire shape, so a generic recognizable-retired
	// check is exactly right; epoch ids, session names and Git SHAs are not
	// identities and are left byte-exact.
	n, err := convertIdentityColumn(ctx, tx, "context_edges", "context_id")
	if err != nil {
		return total, err
	}
	total += n

	// epoch_state_projection.state_json is a typed owner document: its
	// review-cycle keys and slice-progress fields are declared task identities.
	n, err = convertEpochProjection(ctx, tx)
	if err != nil {
		return total, err
	}
	total += n

	// audit_events.payload may carry typed identities in the handful of declared
	// identity keys; anything else in the payload is prose or an opaque value and
	// is left byte-exact.
	n, err = convertAuditPayloads(ctx, tx)
	if err != nil {
		return total, err
	}
	total += n
	return total, nil
}

// convertIdentityColumn rewrites every recognizable retired identity in one
// column. Values that are not recognizable retired identities are preserved
// byte-exact, so opaque ids, prose and already-slash values are never touched.
// A collision (two distinct sources mapping to one target, or a source landing
// on an existing value) is refused before any write.
func convertIdentityColumn(ctx context.Context, tx *sql.Tx, table, column string) (int, error) {
	exists, err := tableExists(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	statement := fmt.Sprintf(`SELECT DISTINCT "%s" FROM "%s" WHERE "%s" IS NOT NULL`, column, table, column)
	rows, err := tx.QueryContext(ctx, statement)
	if err != nil {
		return 0, conversionError(
			fmt.Sprintf("Pasture couldn't read %s.%s for conversion.", table, column),
			"The preflight SELECT failed inside the conversion transaction.",
			"No store data was changed; the conversion aborts before any write.",
			"Confirm the store is readable and retry.", err)
	}
	var sources []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return 0, conversionError("Pasture couldn't scan a "+table+"."+column+" value.", err.Error(), "No store data was changed.", "Restore the store from the archive and retry.", err)
		}
		sources = append(sources, raw)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	targets := make(map[string]string, len(sources))
	type change struct{ old, new string }
	var changes []change
	for _, raw := range sources {
		target := raw
		if looksRetired(raw) {
			converted, err := convertRetiredIdentity(raw)
			if err != nil {
				return 0, typedValueError(table, column, raw, err)
			}
			target = converted
		}
		if previous, ok := targets[target]; ok && previous != raw {
			return 0, collisionError(table, column, previous, raw, target)
		}
		targets[target] = raw
		if target != raw {
			changes = append(changes, change{raw, target})
		}
	}
	// Deterministic application order keeps the converted result independent of
	// the map iteration order.
	sort.Slice(changes, func(i, j int) bool { return changes[i].old < changes[j].old })
	for _, c := range changes {
		update := fmt.Sprintf(`UPDATE "%s" SET "%s"=? WHERE "%s"=?`, table, column, column)
		if _, err := tx.ExecContext(ctx, update, c.new, c.old); err != nil {
			return 0, conversionError(
				fmt.Sprintf("Converting %s.%s key %q failed.", table, column, c.old),
				"SQLite rejected the rewrite, typically because the converted key collides with an existing row or violates a key or foreign-key constraint.",
				"The whole conversion transaction rolls back; no partial key change survives.",
				"Restore the complete archive, inspect the colliding key, and retry from the archive.", err)
		}
	}
	return len(changes), nil
}

// convertEpochProjection rewrites the declared identity fields of the epoch
// state projection through its typed owner (protocol.EpochState). Unknown JSON
// shapes are refused rather than guessed. The re-marshal is deterministic:
// structs serialise in declaration order and maps in sorted key order.
func convertEpochProjection(ctx context.Context, tx *sql.Tx) (int, error) {
	exists, err := tableExists(ctx, tx, "epoch_state_projection")
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT epoch_id, state_json FROM epoch_state_projection ORDER BY epoch_id`)
	if err != nil {
		return 0, conversionError("Pasture couldn't read the epoch state projection for conversion.", "The preflight SELECT failed inside the conversion transaction.", "No store data was changed.", "Confirm the store is readable and retry.", err)
	}
	type epochRow struct{ id, state string }
	var pending []epochRow
	for rows.Next() {
		var item epochRow
		if err := rows.Scan(&item.id, &item.state); err != nil {
			_ = rows.Close()
			return 0, conversionError("Pasture couldn't scan an epoch projection row.", err.Error(), "No store data was changed.", "Restore the store from the archive and retry.", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	changed := 0
	for _, item := range pending {
		var state protocol.EpochState
		if err := json.Unmarshal([]byte(item.state), &state); err != nil {
			return 0, conversionError(
				fmt.Sprintf("The epoch projection for %q could not be decoded for conversion.", item.id),
				"The stored state_json does not match the typed epoch-state owner, so its identity fields cannot be translated safely.",
				"Nothing was changed for this row; the whole conversion aborts.",
				"Repair or archive this projection row, then retry; a projection is disposable and is rebuilt from the journal.",
				err)
		}
		rewritten, err := convertEpochState(&state)
		if err != nil {
			return 0, err
		}
		if !rewritten {
			continue
		}
		blob, err := json.Marshal(state)
		if err != nil {
			return 0, conversionError("The converted epoch projection could not be encoded.", err.Error(), "The whole conversion aborts.", "Report the incompatible state type.", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE epoch_state_projection SET state_json=? WHERE epoch_id=?`, string(blob), item.id); err != nil {
			return 0, conversionError("The converted epoch projection could not be stored.", err.Error(), "The whole conversion transaction rolls back.", "Confirm the store is writable and retry.", err)
		}
		changed++
	}
	return changed, nil
}

// convertEpochState translates the declared identity fields of one epoch state:
// the review-cycle map keys and recorded slice ids, and the slice-progress
// slice/leaf task ids. Every other field, including the epoch id, is left
// byte-equivalent.
func convertEpochState(state *protocol.EpochState) (bool, error) {
	changed := false
	if len(state.ReviewCycles) > 0 {
		next := make(map[string][]protocol.ReviewCycleRecord, len(state.ReviewCycles))
		for key, records := range state.ReviewCycles {
			newKey, err := convertPossibleIdentity(key)
			if err != nil {
				return false, identityFieldError("epoch_state_projection.ReviewCycles", key, err)
			}
			if _, duplicate := next[newKey]; duplicate {
				return false, collisionError("epoch_state_projection", "ReviewCycles", key, key, newKey)
			}
			converted := records
			for i := range converted {
				sliceID, err := convertPossibleIdentity(converted[i].SliceId)
				if err != nil {
					return false, identityFieldError("epoch_state_projection.ReviewCycles[].SliceId", converted[i].SliceId, err)
				}
				if sliceID != converted[i].SliceId {
					converted[i].SliceId = sliceID
					changed = true
				}
			}
			if newKey != key {
				changed = true
			}
			next[newKey] = converted
		}
		state.ReviewCycles = next
	}
	for i := range state.SliceProgress {
		sliceID, err := convertPossibleIdentity(state.SliceProgress[i].SliceId)
		if err != nil {
			return false, identityFieldError("epoch_state_projection.SliceProgress[].SliceId", state.SliceProgress[i].SliceId, err)
		}
		leafID, err := convertPossibleIdentity(state.SliceProgress[i].LeafTaskId)
		if err != nil {
			return false, identityFieldError("epoch_state_projection.SliceProgress[].LeafTaskId", state.SliceProgress[i].LeafTaskId, err)
		}
		if sliceID != state.SliceProgress[i].SliceId {
			state.SliceProgress[i].SliceId = sliceID
			changed = true
		}
		if leafID != state.SliceProgress[i].LeafTaskId {
			state.SliceProgress[i].LeafTaskId = leafID
			changed = true
		}
	}
	return changed, nil
}

// auditPayloadIdentityKeys is the CLOSED set of normalized key names whose
// string values are declared identities in an audit payload. A key is listed
// because an event contract declares it as a task or actor reference; the
// payload's remaining keys are prose, opaque ids or free text and are left
// byte-exact.
var auditPayloadIdentityKeys = map[string]struct{}{
	"taskid":       {},
	"actorid":      {},
	"agentid":      {},
	"sliceid":      {},
	"leaftaskid":   {},
	"authorid":     {},
	"ownerid":      {},
	"assigneeid":   {},
	"reviewerid":   {},
	"childtaskid":  {},
	"parenttaskid": {},
	"occupantid":   {},
	"claimantid":   {},
	"targetid":     {},
	"sourceid":     {},
}

// convertAuditPayloads rewrites the declared identity keys of every audit event
// payload. It is a defined reference-field parser, not a global text replace: a
// value is converted only when its key is a declared identity key AND the value
// is a recognizable retired identity. Everything else stays byte-exact.
func convertAuditPayloads(ctx context.Context, tx *sql.Tx) (int, error) {
	exists, err := tableExists(ctx, tx, "audit_events")
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, payload FROM audit_events ORDER BY id`)
	if err != nil {
		return 0, conversionError("Pasture couldn't read the audit events for conversion.", "The preflight SELECT failed inside the conversion transaction.", "No store data was changed.", "Confirm the store is readable and retry.", err)
	}
	type auditRow struct {
		id      int64
		payload string
	}
	var pending []auditRow
	for rows.Next() {
		var item auditRow
		if err := rows.Scan(&item.id, &item.payload); err != nil {
			_ = rows.Close()
			return 0, conversionError("Pasture couldn't scan an audit event row.", err.Error(), "No store data was changed.", "Restore the store from the archive and retry.", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	changed := 0
	for _, item := range pending {
		var document any
		if err := json.Unmarshal([]byte(item.payload), &document); err != nil {
			return 0, conversionError(
				fmt.Sprintf("The audit payload for event %d could not be decoded for conversion.", item.id),
				"The stored payload is not valid JSON, so its declared identity fields cannot be translated safely.",
				"Nothing was changed for this row; the whole conversion aborts.",
				"Repair the event payload from the archive, then retry.", err)
		}
		rewritten, err := convertPayloadNode(document)
		if err != nil {
			return 0, identityFieldError(fmt.Sprintf("audit_events.payload (event %d)", item.id), "", err)
		}
		if !rewritten {
			continue
		}
		blob, err := json.Marshal(document)
		if err != nil {
			return 0, conversionError("The converted audit payload could not be encoded.", err.Error(), "The whole conversion aborts.", "Report the incompatible payload type.", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE audit_events SET payload=? WHERE id=?`, string(blob), item.id); err != nil {
			return 0, conversionError("The converted audit payload could not be stored.", err.Error(), "The whole conversion transaction rolls back.", "Confirm the store is writable and retry.", err)
		}
		changed++
	}
	return changed, nil
}

// convertPayloadNode walks a decoded JSON document and rewrites declared
// identity keys. It reports whether anything changed.
func convertPayloadNode(node any) (bool, error) {
	changed := false
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if text, ok := value.(string); ok && isIdentityKey(key) {
				converted, err := convertPossibleIdentity(text)
				if err != nil {
					return false, identityFieldError("audit payload key "+key, text, err)
				}
				if converted != text {
					typed[key] = converted
					changed = true
				}
				continue
			}
			nested, err := convertPayloadNode(value)
			if err != nil {
				return false, err
			}
			changed = changed || nested
		}
	case []any:
		for i := range typed {
			nested, err := convertPayloadNode(typed[i])
			if err != nil {
				return false, err
			}
			changed = changed || nested
		}
	}
	return changed, nil
}

// isIdentityKey reports whether a payload key is a declared identity reference.
// Comparison is case-insensitive and ignores underscores so taskId, task_id and
// TaskID are the same declared key.
func isIdentityKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	_, ok := auditPayloadIdentityKeys[normalized]
	return ok
}

// convertPossibleIdentity converts a recognizable retired identity and passes
// every other value through byte-exact.
func convertPossibleIdentity(raw string) (string, error) {
	if raw == "" || !looksRetired(raw) {
		return raw, nil
	}
	return convertRetiredIdentity(raw)
}

func typedValueError(table, column, raw string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("A typed identity in %s.%s could not be converted.", table, column),
		Why:      fmt.Sprintf("The value %q is a recognizable retired identity but cannot be translated without changing its namespace or UUID.", raw),
		Where:    "Converting typed identity columns (internal/idmigrate/pasture.go in idmigrate.convertIdentityColumn).",
		Impact:   "No store data was changed; the conversion aborts before writing this column.",
		Fix:      "Repair this row from the archive so its namespace and UUID are intact, then retry.",
		Cause:    cause,
	}
}

func collisionError(table, column, first, second, target string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("Two distinct identities in %s.%s would collide after conversion.", table, column),
		Why:      fmt.Sprintf("%q and %q both map to %q.", first, second, target),
		Where:    "Converting typed identity columns (internal/idmigrate/pasture.go in idmigrate.convertIdentityColumn).",
		Impact:   "No store data was changed; the conversion aborts before any write.",
		Fix:      "Restore the complete archive, resolve the duplicate identities by hand, and retry from the archive.",
	}
}

func identityFieldError(field, value string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("A declared identity field %s could not be converted.", field),
		Why:      fmt.Sprintf("The value %q is a recognizable retired identity that cannot be translated safely.", value),
		Where:    "Converting structured identity fields (internal/idmigrate/pasture.go in idmigrate.convertPayloadNode).",
		Impact:   "Nothing was changed for this field; the whole conversion aborts.",
		Fix:      "Repair this field from the archive so its namespace and UUID are intact, then retry.",
		Cause:    cause,
	}
}

// tableExists reports whether table is present in the store.
func tableExists(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	return tableExistsReader(ctx, tx, table)
}
