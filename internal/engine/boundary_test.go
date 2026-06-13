package engine_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// tableSet returns the set of user table names in the SQLite file (excluding
// SQLite's internal sqlite_* tables).
func tableSet(t *testing.T, db *sql.DB) map[string]struct{} {
	t.Helper()
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("introspect sqlite_master: %v", err)
	}
	defer rows.Close()
	set := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		set[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	return set
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestProvenanceEngineBoundary asserts the Provenance↔engine table-ownership
// boundary in the single pasture.db file. The DBOS-owned table set is derived
// EMPIRICALLY (the tables that appear only after Launch, minus the one
// pasture-owned table the engine adds), never a hardcoded list or count, so the
// test still passes if a future DBOS version shifts its schema — what it pins is
// the boundary, not the substrate's internal table set.
func TestProvenanceEngineBoundary(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// 1. Create the pasture + Provenance tables (audit_events, tasks,
	//    activities, agents, …) by opening the unified tracker.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()

	// Snapshot the file BEFORE the engine touches it: this is the
	// pasture + Provenance owned set.
	probe, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("probe open: %v", err)
	}
	defer probe.Close()
	pastureProvenance := tableSet(t, probe)

	// 2. Bring up the engine (creates epoch_state_projection) and Launch
	//    (creates the DBOS system tables).
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "boundary-v1",
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer e.Shutdown(5 * time.Second)
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}

	full := tableSet(t, probe)

	// 3. Derive the engine-added set, then split off the single pasture-owned
	//    table the engine adds (the projection) to leave the DBOS-owned set.
	const engineProjectionTable = "epoch_state_projection"
	dbos := map[string]struct{}{}
	for name := range full {
		if _, preexisting := pastureProvenance[name]; preexisting {
			continue
		}
		if name == engineProjectionTable {
			continue
		}
		dbos[name] = struct{}{}
	}
	t.Logf("observed DBOS-owned tables (v0.16.0): %v", sortedKeys(dbos))

	// (1) NON-EMPTY: DBOS created at least one table.
	if len(dbos) == 0 {
		t.Fatal("no DBOS-owned tables observed after Launch — the substrate created nothing")
	}

	// (2) DISJOINT: the DBOS set shares no name with the pasture/Provenance set
	//     (nor with the engine projection table).
	for name := range dbos {
		if _, clash := pastureProvenance[name]; clash {
			t.Errorf("table %q is owned by BOTH DBOS and pasture/Provenance — boundary violated", name)
		}
		if name == engineProjectionTable {
			t.Errorf("the engine projection table leaked into the DBOS-owned set")
		}
	}

	// (3) CO-PRESENT: the engine-critical DBOS tables AND the pasture/Provenance
	//     tables all exist in the same file. A vacuous (empty-set) pass can't
	//     sneak through because each named table must be present.
	for _, name := range []string{"workflow_status", "operation_outputs"} {
		if _, ok := dbos[name]; !ok {
			t.Errorf("expected DBOS-owned table %q to be present", name)
		}
	}
	for _, name := range []string{"audit_events", "activities", "tasks"} {
		if _, ok := pastureProvenance[name]; !ok {
			t.Errorf("expected pasture/Provenance table %q to be present", name)
		}
	}

	// All co-present and readable from ONE handle (the shared engine handle).
	for _, name := range []string{"workflow_status", "operation_outputs", "audit_events", "activities", "tasks"} {
		var n int
		if err := e.DB().QueryRow(`SELECT count(*) FROM ` + name).Scan(&n); err != nil {
			t.Errorf("table %q not readable from the shared handle: %v", name, err)
		}
	}
}
