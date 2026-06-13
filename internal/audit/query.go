package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// QueryEventsOn is the single canonical implementation of the post-v4
// context_edges JOIN query for audit events. Both SqliteAuditTrail.QueryEvents
// and StatusReader.QueryEvents delegate to this function so any schema change
// (a v5+ touching audit_events, context_edges, or the legacy-role naming
// scheme) is applied in one place.
//
// This function is SELECT-only: it issues no writes, DDL, migration, or any
// operation that could alter the database. It is safe to call on a read-only
// handle opened next to a running daemon.
//
// epochId is required and always part of the WHERE clause. phase and role are
// optional; nil means "no filter". Results are returned oldest-first.
//
// Legacy-role compatibility: the v3 schema dropped audit_events.role and
// replaced it with agent_id. To preserve the existing API where callers filter
// by role and read event.Role on the result, this function LEFT JOINs
// audit_events with agents_software (via agent_id) and:
//
//   - When role != nil, restricts the JOIN target to asw.name = "pasture/legacy-role/<role>".
//   - When reading rows, strips the "pasture/legacy-role/" prefix from the
//     joined name to repopulate event.Role. Agents whose name does not match
//     the legacy prefix (e.g. well-known automaton agents) report the full name
//     as-is so the caller still gets a non-empty Role for those events.
//
// LEFT JOIN (rather than INNER JOIN) defends against orphan agent_id values
// that have no agents_software row — those rows are returned with an empty
// Role rather than dropped silently.
func QueryEventsOn(ctx context.Context, db *sql.DB, epochId string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	var clauses []string
	var args []any

	// Post-v4 schema: audit_events.epoch_id is gone; epoch attachment is
	// recorded in context_edges with kind='EpochContext'. INNER JOIN
	// context_edges to restrict the result to events tied to the
	// requested epoch.
	clauses = append(clauses, "ce.context_kind = ? AND ce.context_id = ?")
	args = append(args, "EpochContext", epochId)

	if phase != nil {
		clauses = append(clauses, "ae.phase = ?")
		args = append(args, string(*phase))
	}
	if role != nil {
		clauses = append(clauses, "asw.name = ?")
		args = append(args, legacyRoleAgentNamePrefix+*role)
	}

	query := `SELECT ce.context_id, ae.phase, COALESCE(asw.name, ''), ae.event_type, ae.payload, ae.timestamp
	          FROM audit_events ae
	          INNER JOIN context_edges ce ON ce.event_id = ae.id
	          LEFT JOIN agents_software asw ON asw.agent_id = ae.agent_id
	          WHERE ` + strings.Join(clauses, " AND ") + `
	          ORDER BY ae.id ASC`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't read audit events for epoch %q.", epochId,
			),
			Why:    "The query linking audit events to their epoch returned an error.",
			Where:  "Reading audit events (internal/audit/query.go in audit.QueryEventsOn).",
			Impact: "No audit events can be returned for this epoch until the problem is fixed.",
			Fix: "1. Confirm the database is readable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the query once the database is healthy.",
			Cause: err,
		}
	}
	defer rows.Close()

	var events []protocol.AuditEvent
	for rows.Next() {
		var epochIDCol, phaseCol, agentName, eventTypeCol, payloadCol string
		var tsNano int64
		if err := rows.Scan(&epochIDCol, &phaseCol, &agentName, &eventTypeCol, &payloadCol, &tsNano); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't decode an audit-event row for epoch %q.", epochId,
				),
				Why:    "Scanning a result row from the audit-events query failed.",
				Where:  "Reading audit events (internal/audit/query.go in audit.QueryEventsOn).",
				Impact: "The event listing is incomplete.",
				Fix: "1. Retry the query.\n" +
					"2. If the error persists, check the database integrity.",
				Cause: err,
			}
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadCol), &payload); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"An audit-event payload is invalid JSON (epoch %q, event type %q).",
					epochIDCol, eventTypeCol,
				),
				Why:    "The payload column could not be parsed as JSON — it may be corrupted.",
				Where:  "Reading audit events (internal/audit/query.go in audit.QueryEventsOn).",
				Impact: "This event can't be returned; the event listing may be incomplete.",
				Fix:    "1. Inspect the row directly and repair or remove the bad payload.",
				Cause:  err,
			}
		}
		events = append(events, protocol.AuditEvent{
			EpochId:   epochIDCol,
			Phase:     protocol.PhaseId(phaseCol),
			Role:      stripLegacyRolePrefix(agentName),
			EventType: protocol.EventType(eventTypeCol),
			Payload:   payload,
			Timestamp: time.Unix(0, tsNano).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Lost the database stream while reading audit events for epoch %q.", epochId,
			),
			Why:    "The result iterator returned an error before all rows were read.",
			Where:  "Reading audit events (internal/audit/query.go in audit.QueryEventsOn).",
			Impact: "The event listing may be incomplete.",
			Fix: "1. Retry the query.\n" +
				"2. If the error persists, check the database integrity.",
			Cause: err,
		}
	}
	return events, nil
}
