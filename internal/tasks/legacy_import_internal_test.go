package tasks

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/provadapter"
)

// TestLegacyImportMutationRef_NoConcatenationCollision guards the source-keyed
// OperationID derivation against the naive-concatenation ambiguity: two distinct
// (SourceTable, LegacyRowID) pairs whose fields differ only in where a separator falls
// must NOT hash to the same mutation ref (and therefore the same OperationID). Under a
// plain SourceTable+"\x00"+LegacyRowID concatenation the two pairs below both spell
// "A\x00B\x00C" and collide; length-delimited framing keeps them distinct.
func TestLegacyImportMutationRef_NoConcatenationCollision(t *testing.T) {
	a := provadapter.LegacyAuditEvent{SourceTable: "A\x00B", LegacyRowID: "C"}
	b := provadapter.LegacyAuditEvent{SourceTable: "A", LegacyRowID: "B\x00C"}

	refA, err := legacyImportMutationRef(a)
	if err != nil {
		t.Fatalf("legacyImportMutationRef(a): %v", err)
	}
	refB, err := legacyImportMutationRef(b)
	if err != nil {
		t.Fatalf("legacyImportMutationRef(b): %v", err)
	}
	if refA.String() == refB.String() {
		t.Fatalf("distinct source rows produced the same mutation ref %q — "+
			"two different rows collide onto one OperationID", refA.String())
	}
}

// TestLegacyImportMutationRef_Stable confirms the ref is a pure function of the source
// identity: the same row always yields the same ref, which is what makes a re-import
// replay idempotently.
func TestLegacyImportMutationRef_Stable(t *testing.T) {
	row := provadapter.LegacyAuditEvent{SourceTable: "audit_events", LegacyRowID: "42"}
	first, err := legacyImportMutationRef(row)
	if err != nil {
		t.Fatalf("legacyImportMutationRef first: %v", err)
	}
	second, err := legacyImportMutationRef(row)
	if err != nil {
		t.Fatalf("legacyImportMutationRef second: %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("same row produced different refs: %q vs %q", first.String(), second.String())
	}
}
