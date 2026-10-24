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
// hold a declared namespace-plus-UUID identity, one identity per cell. A column
// is listed only because its owning type declares it as an identity, never
// because its name ends in "id": assignment, operation, authority and
// result-slot identifiers are opaque and are deliberately recorded in
// pasturePreservedColumns instead. The identity-array columns (one JSON array
// of identities per cell) live in pastureIdentityArrayColumns; the kind-aware
// context_edges.context_id, the epoch projection JSON and the audit payload
// JSON are handled separately because they are kind-aware or structured.
var pastureIdentityColumns = []struct{ table, column string }{
	{"audit_events", "agent_id"},
	{"pasture_agent_categories", "agent_id"},
	{"pasture_well_known_agents", "agent_id"},
	{"pasture_system_identity", "committer_actor_id"},
	{"pasture_session_claim", "actor"},
	// epoch_id names a task in all three binding relations: the explicit bind
	// refuses an epoch that does not exist in tasks, the inferred row is derived
	// from a Provenance ownership TaskID, and the project mapping binds a
	// project to an epoch task. A task id is a declared namespace-plus-UUID
	// identity, so every epoch_id converts with the tasks it names. The sibling
	// binding_id handles are minted or derived labels, not identities, and are
	// recorded in pasturePreservedColumns.
	{"pasture_session_epoch_binding", "epoch_id"},
	{"pasture_session_inferred_binding", "epoch_id"},
	{"pasture_project_epoch_binding", "epoch_id"},
}

// pastureIdentityArrayColumns is the CLOSED set of pasture-owned TEXT columns
// that hold a canonical JSON array of declared identities, one identity per
// element. GovernedAllocationAuditParticipant writes both from the committed
// closure's child TaskID and Occupant (internal/tasks/governed_create_slice.go),
// so every element is an owned identity and conversion must rewrite each one.
// A scalar "looks retired" check never sees an array, so these columns are
// handled by convertIdentityArrayColumn, never by convertIdentityColumn.
var pastureIdentityArrayColumns = []struct{ table, column string }{
	{"pasture_governed_allocation_audit", "child_task_id"},
	{"pasture_governed_allocation_audit", "occupant_id"},
}

// pasturePreservedColumns is the CLOSED set of pasture-owned *_id-named TEXT
// columns deliberately kept OUT of conversion: an opaque, equality-only handle
// whose declared domain is never parsed or resolved. Recording them (rather than
// omitting them) is what lets the red-by-default schema walk demand that every
// identity-looking pasture column be classified on purpose.
var pasturePreservedColumns = []struct{ table, column, reason string }{
	{"pasture_governed_allocation_audit", "operation_id", "opaque operation handle; equality-only projection key"},
	{"pasture_governed_allocation_audit", "child_assignment_id", "opaque assignment handles as a canonical JSON array; equality-only"},
	{"session_entries", "entry_id", "opaque audit session-entry key"},
	{"session_entries", "parent_entry_id", "opaque audit session-entry key"},
	{"session_entries", "session_id", "opaque audit session key"},
	{"session_entries", "tool_call_id", "opaque vendor tool-call handle"},
	// The explicit bind mints a bare UUID and the inference rebuild derives a
	// deterministic "inferred-<sha256>" handle; neither carries a namespace and
	// neither is ever parsed or resolved. Rewriting one would rename a binding
	// that no owner identifies as an identity and would break the deterministic
	// handle's stability across a conversion.
	{"pasture_session_epoch_binding", "binding_id", "minted UUID binding handle; equality-only projection key, never parsed or resolved"},
	{"pasture_session_inferred_binding", "binding_id", "derived deterministic binding handle; equality-only projection key, never parsed or resolved"},
	{"pasture_project_epoch_binding", "binding_id", "minted UUID project-binding handle; equality-only projection key, never parsed or resolved"},
}

// pastureSpecialColumns is the CLOSED set of pasture-owned identity-bearing
// columns conversion handles outside the plain scalar loop: kind-routed context
// identities and a disposable projection that is rebuilt from the converted
// journal inside the conversion transaction rather than rewritten in place.
var pastureSpecialColumns = []struct{ table, column, reason string }{
	{"context_edges", "context_id", "kind-aware: task/actor/activity identities convert, epoch/session/Git values stay byte-exact"},
	{"lifecycle_occurrences", "actor_id", "disposable projection rebuilt from the converted journal inside the conversion transaction, never rewritten in place"},
}

// IdentityColumn names one "table.column" pair in a conversion mirror.
type IdentityColumn struct{ Table, Column string }

// ConversionMirrors is the read surface the cross-module classification gate
// needs: every declared mirror that decides what the dry run and the conversion
// treat as identities. Production code reads the package-private slices
// directly; this accessor exists so a black-box gate can compare the mirrors to
// the dependency's schema and to pasture's own schema.
type ConversionMirrors struct {
	ProvenanceOwned        []IdentityColumn
	ProvenancePreserved    []IdentityColumn
	ProvenanceOwnedContext []IdentityColumn
	PastureOwned           []IdentityColumn
	PastureOwnedArray      []IdentityColumn
	PasturePreserved       []IdentityColumn
	PastureSpecial         []IdentityColumn
}

// DeclaredConversionMirrors returns the complete set of declared conversion
// mirrors. It is the gate's read surface, not a production entry point.
func DeclaredConversionMirrors() ConversionMirrors {
	toIdentityColumns := func(cols []struct{ table, column string }) []IdentityColumn {
		out := make([]IdentityColumn, 0, len(cols))
		for _, c := range cols {
			out = append(out, IdentityColumn{Table: c.table, Column: c.column})
		}
		return out
	}
	toIdentityColumnsWithReason := func(cols []struct{ table, column, reason string }) []IdentityColumn {
		out := make([]IdentityColumn, 0, len(cols))
		for _, c := range cols {
			out = append(out, IdentityColumn{Table: c.table, Column: c.column})
		}
		return out
	}
	return ConversionMirrors{
		ProvenanceOwned:        toIdentityColumns(previewDependencyColumns),
		ProvenancePreserved:    toIdentityColumns(preservedDependencyColumns),
		ProvenanceOwnedContext: toIdentityColumns(previewDependencyContextColumns),
		PastureOwned:           toIdentityColumns(pastureIdentityColumns),
		PastureOwnedArray:      toIdentityColumns(pastureIdentityArrayColumns),
		PasturePreserved:       toIdentityColumnsWithReason(pasturePreservedColumns),
		PastureSpecial:         toIdentityColumnsWithReason(pastureSpecialColumns),
	}
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
	// The governed-allocation audit columns hold a canonical JSON array of
	// identities, so each element converts and the row is re-encoded; a scalar
	// recognizable-retired check would leave every retired element in place.
	for _, col := range pastureIdentityArrayColumns {
		n, err := convertIdentityArrayColumn(ctx, tx, col.table, col.column)
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

// convertIdentityArrayColumn rewrites the declared identities inside one column
// that stores a canonical JSON array of identity strings. It parses the array,
// converts every element, and re-encodes canonically; a value whose elements are
// already current is left byte-exact. It returns the number of rows it rewrote.
func convertIdentityArrayColumn(ctx context.Context, tx *sql.Tx, table, column string) (int, error) {
	exists, err := tableExists(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT rowid, "%s" FROM "%s" WHERE "%s" IS NOT NULL`, column, table, column))
	if err != nil {
		return 0, conversionError(
			fmt.Sprintf("Pasture couldn't read %s.%s for conversion.", table, column),
			"The preflight SELECT failed inside the conversion transaction.",
			"No store data was changed; the conversion aborts before any write.",
			"Confirm the store is readable and retry.", err)
	}
	type arrayRow struct {
		rowid int64
		raw   string
	}
	var pending []arrayRow
	for rows.Next() {
		var item arrayRow
		if err := rows.Scan(&item.rowid, &item.raw); err != nil {
			_ = rows.Close()
			return 0, conversionError("Pasture couldn't scan a "+table+"."+column+" value.", err.Error(), "No store data was changed.", "Restore the store from the archive and retry.", err)
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
		target, rewritten, err := convertIdentityArrayValue(item.raw)
		if err != nil {
			return 0, identityFieldError(table+"."+column, item.raw, err)
		}
		if !rewritten {
			continue
		}
		update := fmt.Sprintf(`UPDATE "%s" SET "%s"=? WHERE rowid=?`, table, column)
		if _, err := tx.ExecContext(ctx, update, target, item.rowid); err != nil {
			return 0, conversionError(
				fmt.Sprintf("Converting %s.%s failed.", table, column),
				"SQLite rejected the rewrite of the identity array.",
				"The whole conversion transaction rolls back; no partial key change survives.",
				"Restore the complete archive, inspect the row, and retry from the archive.", err)
		}
		changed++
	}
	return changed, nil
}

// convertIdentityArrayValue decodes one declared identity-array value and
// rewrites each element that is a recognizable retired identity, returning the
// canonical re-encoding and whether anything changed. A value that is not a JSON
// string array is preserved byte-exact unless it is itself a recognizable
// retired scalar (a malformed declared identity), which is refused rather than
// guessed.
func convertIdentityArrayValue(raw string) (string, bool, error) {
	var elements []string
	if err := json.Unmarshal([]byte(raw), &elements); err != nil {
		if looksRetired(raw) {
			return "", false, fmt.Errorf("the declared identity array %q is not a JSON string array", raw)
		}
		return raw, false, nil
	}
	changed := false
	for i, element := range elements {
		converted, err := convertPossibleIdentity(element)
		if err != nil {
			return "", false, err
		}
		if converted != element {
			elements[i] = converted
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	blob, err := json.Marshal(elements)
	if err != nil {
		return "", false, err
	}
	return string(blob), true, nil
}

// identityArrayWouldChange reports whether one declared identity-array value
// carries a recognizable retired element. It shares convertIdentityArrayValue so
// the dry run's count and the conversion's rewrite can never disagree.
func identityArrayWouldChange(raw string) bool {
	_, changed, err := convertIdentityArrayValue(raw)
	return err == nil && changed
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
// byte-exact. An opaque, equality-only key is deliberately absent: claimantId
// (actor_namespace_claims.claimant_id) holds a bare namespace label, not an
// identity, and must never be parsed or rewritten.
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
