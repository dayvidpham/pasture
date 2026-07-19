package provadapter

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// legacyaudit_test.go exercises the read-only 'pasture legacy-audit event list'
// API: deterministic (RecordedAt, LegacyRowID) ordering, verbatim preservation of
// raw actor text / contexts / source identity, read-only isolation of the returned
// copy from the source, and error surfacing.

// fakeAuditSource is a caller-supplied read-only legacy audit source. It records
// whether it was asked to extract, and can be made to fail, so the tests can prove
// #14 never mutates the source and surfaces extraction errors.
type fakeAuditSource struct {
	rows []LegacyAuditEvent
	err  error
}

func (f *fakeAuditSource) ExtractLegacyAuditEvents() ([]LegacyAuditEvent, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func ts(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

// TestListLegacyAuditEvents_DeterministicOrder proves rows are returned in
// ascending (RecordedAt, LegacyRowID) order regardless of source order, with the
// LegacyRowID breaking equal-timestamp ties.
func TestListLegacyAuditEvents_DeterministicOrder(t *testing.T) {
	cases := []struct {
		name  string
		in    []LegacyAuditEvent
		order []string // expected LegacyRowIDs in order
	}{
		{
			name: "distinct timestamps sort ascending",
			in: []LegacyAuditEvent{
				{LegacyRowID: "r3", RecordedAt: ts(300)},
				{LegacyRowID: "r1", RecordedAt: ts(100)},
				{LegacyRowID: "r2", RecordedAt: ts(200)},
			},
			order: []string{"r1", "r2", "r3"},
		},
		{
			name: "equal timestamps break ties by LegacyRowID",
			in: []LegacyAuditEvent{
				{LegacyRowID: "b", RecordedAt: ts(100)},
				{LegacyRowID: "a", RecordedAt: ts(100)},
				{LegacyRowID: "c", RecordedAt: ts(100)},
			},
			order: []string{"a", "b", "c"},
		},
		{
			name:  "empty source yields empty list",
			in:    nil,
			order: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ListLegacyAuditEvents(&fakeAuditSource{rows: tc.in})
			if err != nil {
				t.Fatalf("ListLegacyAuditEvents: %v", err)
			}
			if len(got) != len(tc.order) {
				t.Fatalf("got %d rows, want %d", len(got), len(tc.order))
			}
			for i := range tc.order {
				if got[i].LegacyRowID != tc.order[i] {
					t.Fatalf("row[%d] = %q, want %q", i, got[i].LegacyRowID, tc.order[i])
				}
			}
		})
	}
}

// TestListLegacyAuditEvents_PreservesRawFields proves the raw actor text, contexts
// (order-preserved, not deduplicated), payload bytes, and source identity survive
// verbatim.
func TestListLegacyAuditEvents_PreservesRawFields(t *testing.T) {
	src := &fakeAuditSource{rows: []LegacyAuditEvent{{
		LegacyRowID: "row-42",
		SourceTable: "legacy_audit_log",
		RecordedAt:  ts(500),
		RawActor:    "  Free Text Actor (unresolved)  ",
		RawContexts: []string{"ctx-b", "ctx-a", "ctx-b"}, // unordered + duplicate, preserved as-is
		Payload:     json.RawMessage(`{"raw":"opaque","n":1}`),
	}}}

	got, err := ListLegacyAuditEvents(src)
	if err != nil {
		t.Fatalf("ListLegacyAuditEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	ev := got[0]
	if ev.RawActor != "  Free Text Actor (unresolved)  " {
		t.Fatalf("RawActor not preserved verbatim: %q", ev.RawActor)
	}
	wantCtx := []string{"ctx-b", "ctx-a", "ctx-b"}
	if len(ev.RawContexts) != len(wantCtx) {
		t.Fatalf("RawContexts len = %d, want %d", len(ev.RawContexts), len(wantCtx))
	}
	for i := range wantCtx {
		if ev.RawContexts[i] != wantCtx[i] {
			t.Fatalf("RawContexts[%d] = %q, want %q (order/duplicates must be preserved)", i, ev.RawContexts[i], wantCtx[i])
		}
	}
	if string(ev.Payload) != `{"raw":"opaque","n":1}` {
		t.Fatalf("Payload not byte-preserved: %s", ev.Payload)
	}
	if ev.SourceTable != "legacy_audit_log" {
		t.Fatalf("SourceTable not preserved: %q", ev.SourceTable)
	}
}

// TestListLegacyAuditEvents_ReturnedCopyIsIsolated proves the returned slice owns
// its buffers: mutating the result cannot reach back into the source's rows,
// keeping the extraction strictly read-only.
func TestListLegacyAuditEvents_ReturnedCopyIsIsolated(t *testing.T) {
	sourceRows := []LegacyAuditEvent{{
		LegacyRowID: "r1",
		RecordedAt:  ts(100),
		RawContexts: []string{"orig"},
		Payload:     json.RawMessage(`{"a":1}`),
	}}
	src := &fakeAuditSource{rows: sourceRows}

	got, err := ListLegacyAuditEvents(src)
	if err != nil {
		t.Fatalf("ListLegacyAuditEvents: %v", err)
	}
	// Mutate the returned copy's slice/byte fields.
	got[0].RawContexts[0] = "tampered"
	got[0].Payload[0] = 'X'

	// The source's row must be untouched.
	if sourceRows[0].RawContexts[0] != "orig" {
		t.Fatalf("source RawContexts was mutated through the returned copy: %q", sourceRows[0].RawContexts[0])
	}
	if string(sourceRows[0].Payload) != `{"a":1}` {
		t.Fatalf("source Payload was mutated through the returned copy: %s", sourceRows[0].Payload)
	}
}

// TestListLegacyAuditEvents_Errors proves a nil source and a source extraction
// error are both surfaced.
func TestListLegacyAuditEvents_Errors(t *testing.T) {
	if _, err := ListLegacyAuditEvents(nil); err == nil {
		t.Fatalf("expected nil source to be rejected")
	}

	sentinel := errors.New("legacy source offline")
	_, err := ListLegacyAuditEvents(&fakeAuditSource{err: sentinel})
	if err == nil {
		t.Fatalf("expected the extraction error to be surfaced")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("extraction error does not wrap the source error: %v", err)
	}
}
