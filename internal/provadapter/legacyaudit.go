package provadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// legacyaudit.go is the separately-named, read-only 'pasture legacy-audit event
// list' API over NON-TASK legacy audit rows (pasture#14, §13). It is the surface
// #43's CLI wires; there is NO CLI wiring here, and nothing in this file mutates
// any source or invokes #43.
//
// Non-task vs task migration. The legacy-baseline migration coordinator
// (migrate.go) folds legacy TASK rows into journal baselines. This API is the
// distinct read-only extraction of the generic, non-task legacy audit rows (raw
// audit events that were never Pasture tasks). #14 extracts them WITHOUT mutation
// and preserves each row's ORIGINAL actor text, contexts, and source identity
// verbatim — it does not interpret, normalise, classify, or import them. #43 alone
// constructs and validates its typed import over these raw rows (derives the
// OperationID, batches the fan-out, and owns replay/conflict tests).

// LegacyAuditEvent is one raw non-task legacy audit row, preserved verbatim for
// #43's typed import. Every field is the source's original value with no
// interpretation: RawActor keeps the legacy actor string exactly (it is NOT
// resolved to an ActorID here — an unmapped legacy actor is a first-class value),
// RawContexts keeps the original context strings in source order, Payload is the
// opaque source bytes byte-preserved, and SourceTable + LegacyRowID together are
// the row's source identity.
type LegacyAuditEvent struct {
	// LegacyRowID is the row's identity within its source table; it is the
	// total-order tiebreak in the deterministic listing order.
	LegacyRowID string
	// SourceTable is the source-identity qualifier the row was extracted from, kept
	// so #43 can attribute a raw row to its origin.
	SourceTable string
	// RecordedAt is the row's original recorded wall-clock time; it is the primary
	// key of the deterministic (RecordedAt, LegacyRowID) listing order.
	RecordedAt time.Time
	// RawActor is the legacy actor text exactly as stored — uninterpreted and
	// unresolved. An empty string is preserved as an empty string.
	RawActor string
	// RawContexts are the original context strings in source order, preserved
	// verbatim (never deduplicated or reordered).
	RawContexts []string
	// Payload is the opaque source payload, byte-preserved. It is copied defensively
	// on extraction so the returned event cannot alias the source's buffer.
	Payload json.RawMessage
}

// LegacyAuditSource is the read-only extractor #43's CLI supplies for the concrete
// pre-journal audit source (the released Provenance surface does not own the legacy
// audit schema, so the source is provided by the caller). The coordinator calls it
// exactly once per listing and never mutates it.
type LegacyAuditSource interface {
	// ExtractLegacyAuditEvents returns every non-task legacy audit row read-only, in
	// any order. It must not mutate the source. An extraction failure is returned as
	// an error, which ListLegacyAuditEvents surfaces verbatim.
	ExtractLegacyAuditEvents() ([]LegacyAuditEvent, error)
}

// ListLegacyAuditEvents extracts all non-task legacy audit rows from src read-only
// and returns them in the deterministic (RecordedAt, LegacyRowID) display order,
// preserving each row's raw actor text, contexts, and source identity verbatim. It
// is the API #43's CLI wires; it performs no import, no normalisation, and no CLI
// I/O.
//
// The returned slice is a fresh, independently-owned copy: RawContexts and Payload
// are deep-copied so a caller mutating the result can never reach back into the
// source's buffers, keeping the extraction strictly read-only.
func ListLegacyAuditEvents(src LegacyAuditSource) ([]LegacyAuditEvent, error) {
	if src == nil {
		return nil, errors.New(
			"provadapter: cannot list legacy audit events — what: the LegacyAuditSource is nil; " +
				"why: the read-only listing extracts rows from a caller-supplied pre-journal audit source and " +
				"has no source to read; where: internal/provadapter ListLegacyAuditEvents; when: before " +
				"extraction; impact: no legacy audit rows are listed; fix: pass the #43-supplied read-only " +
				"LegacyAuditSource")
	}
	rows, err := src.ExtractLegacyAuditEvents()
	if err != nil {
		return nil, fmt.Errorf(
			"provadapter: list legacy audit events: extract from source — what: the source extraction failed; "+
				"why: %w; where: internal/provadapter ListLegacyAuditEvents; when: reading the legacy audit "+
				"source; impact: no rows are listed and nothing is mutated; fix: resolve the source extraction "+
				"error and retry", err)
	}

	out := make([]LegacyAuditEvent, len(rows))
	for i := range rows {
		out[i] = cloneLegacyAuditEvent(rows[i])
	}
	sort.SliceStable(out, func(a, b int) bool {
		ra, rb := out[a].RecordedAt, out[b].RecordedAt
		if !ra.Equal(rb) {
			return ra.Before(rb)
		}
		return out[a].LegacyRowID < out[b].LegacyRowID
	})
	return out, nil
}

// cloneLegacyAuditEvent deep-copies a row's slice/byte fields so the returned event
// owns its buffers independently of the source, preserving the read-only contract.
func cloneLegacyAuditEvent(in LegacyAuditEvent) LegacyAuditEvent {
	out := LegacyAuditEvent{
		LegacyRowID: in.LegacyRowID,
		SourceTable: in.SourceTable,
		RecordedAt:  in.RecordedAt,
		RawActor:    in.RawActor,
	}
	if in.RawContexts != nil {
		out.RawContexts = make([]string, len(in.RawContexts))
		copy(out.RawContexts, in.RawContexts)
	}
	if in.Payload != nil {
		out.Payload = make(json.RawMessage, len(in.Payload))
		copy(out.Payload, in.Payload)
	}
	return out
}
