// Package tasks — well_known_interface_test.go
//
// White-box tests for the auditDBHolder interface introduced in the Phase 10
// MINOR fix (aura-plugins-3cuk3). These tests live in package tasks (not
// tasks_test) because auditDBHolder is an unexported interface — only code in
// the same package can declare a type that satisfies it.
//
// The key acceptance criterion is:
//
//	A type other than *trackerImpl can be passed to RegisterWellKnownAgents,
//	as long as it implements auditDBHandle() *sql.DB, without panicking or
//	returning a CategoryConfig error.
//
// This file is deliberately small. Correctness (row counts, idempotency) is
// already covered by the BDD tests in well_known_test.go; these tests only
// exercise the interface boundary.

package tasks

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	stderrors "errors"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// fakeAuditDBHolder is a test-only type that implements protocol.TaskTracker
// (by delegating to a real inner tracker) AND satisfies auditDBHolder by
// exposing its own *sql.DB via the unexported method. It is used to prove that
// RegisterWellKnownAgents works with any implementation of auditDBHolder — not
// only *trackerImpl.
//
// This type MUST be in package tasks (not tasks_test) because auditDBHolder
// uses an unexported method name; only same-package types can satisfy it.
type fakeAuditDBHolder struct {
	protocol.TaskTracker // inner real tracker for Provenance operations
	db                   *sql.DB
}

// auditDBHandle satisfies the auditDBHolder interface. The inner tracker
// provides RegisterSoftwareAgent; this *sql.DB provides the audit table target.
func (f *fakeAuditDBHolder) auditDBHandle() *sql.DB { return f.db }

// TestRegisterWellKnownAgents_NonTrackerImplSucceeds_WhiteBox verifies that a
// type other than *trackerImpl can be passed to RegisterWellKnownAgents without
// error, as long as it implements auditDBHolder. This is the acceptance test
// for aura-plugins-3cuk3 (Phase 10 MINOR fix).
//
// The test opens a real database (t.TempDir), constructs a fakeAuditDBHolder
// wrapping the inner tracker, and asserts that all 15 well-known agents are
// registered and cached without error.
func TestRegisterWellKnownAgents_NonTrackerImplSucceeds_WhiteBox(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// Open a real tracker to supply Provenance operations (RegisterSoftwareAgent).
	inner, err := OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() { _ = inner.Close() })

	// Open a second *sql.DB handle on the same file for the audit side.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	// fakeAuditDBHolder is NOT *trackerImpl — it is a distinct type defined in
	// this file. Before the fix, *trackerImpl assertion would fail here.
	// After the fix, the auditDBHolder interface assertion must succeed.
	fake := &fakeAuditDBHolder{TaskTracker: inner, db: db}

	cache := NewWellKnownAgentCache()
	if err := RegisterWellKnownAgents(context.Background(), fake, cache); err != nil {
		t.Fatalf("RegisterWellKnownAgents with non-*trackerImpl: %v", err)
	}

	if cache.Len() != WellKnownAgentCount {
		t.Errorf("cache.Len() = %d, want %d", cache.Len(), WellKnownAgentCount)
	}
}

// noAuditDBHolderFake is a test-only type that wraps a real tracker but does
// NOT implement auditDBHolder. Used to verify the error path.
type noAuditDBHolderFake struct {
	protocol.TaskTracker
}

// TestRegisterWellKnownAgents_NoAuditDBHolderReturnsConfigError_WhiteBox
// verifies that when the passed tracker does not implement auditDBHolder, the
// function returns a CategoryConfig *StructuredError mentioning "auditDBHolder".
func TestRegisterWellKnownAgents_NoAuditDBHolderReturnsConfigError_WhiteBox(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	inner, err := OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() { _ = inner.Close() })

	fake := &noAuditDBHolderFake{TaskTracker: inner}
	cache := NewWellKnownAgentCache()

	gotErr := RegisterWellKnownAgents(context.Background(), fake, cache)
	if gotErr == nil {
		t.Fatal("expected error for tracker without auditDBHolder, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(gotErr, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", gotErr, gotErr)
	}
	if se.Category != pasterrors.CategoryConfig {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryConfig)
	}
	if !strings.Contains(se.What, "auditDBHolder") {
		t.Errorf("What = %q, want it to mention 'auditDBHolder'", se.What)
	}
}
