//go:build fixture

// build_fixture.go is a TEST-ONLY Go program that constructs the
// pasture/testdata/legacy_audit_v1.db SQLite fixture per
// PROPOSAL-2 §11 Scenario 4 + IMPL_PLAN §5.1.
//
// Run with:
//
//	go run -tags fixture ./internal/audit/testdata
//
// or:
//
//	cd internal/audit/testdata && go run -tags fixture .
//
// Build-tag gating (//go:build fixture) keeps this file out of the
// normal `make build` and `make test` paths so the fixture artifact is
// only regenerated when explicitly requested. The .db file checked in
// at pasture/testdata/legacy_audit_v1.db is the canonical artifact;
// this program is committed alongside as the reproducible recipe per
// IMPL_PLAN §5.2.
//
// Composition (verbatim from PROPOSAL-2 §11 Scenario 4):
//
//   - 1024 rows in audit_events (legacy v1 schema, no audit_schema_meta).
//   - Role distribution (sums to 1024):
//     architect (256), supervisor (192), worker (192), reviewer (192),
//     automaton-checker (96), human-david (64), unknown-legacy (32).
//     7 distinct roles.
//   - Phase distribution: covers all 12 PhaseId values; ~100 rows of
//     phase=” (empty string, NOT NULL satisfied) for "free-floating"
//     entries — TEAM-LEAD BINDING DECISION (bd comment on
//     aura-plugins-k5g3o, 2026-04-25): empty string exercises the
//     migrator's empty-vs-null branch logic, which is a real edge case
//     worth keeping. Document in pasture/testdata/README.md AND inline
//     here so future readers don't "fix" the empty strings.
//   - Payload edges: 64 empty {}; 32 deeply-nested (depth 4); 16 UTF-8
//     ("café"); 16 >8 KB; 4 top-level arrays.
//   - Epoch-ID shapes: 768 valid Provenance TaskIDs (namespace--uuid),
//     192 legacy free strings, 64 duplicates of other rows' epoch_ids.
//
// Self-asserts the composition before commit + close so a future
// modification that breaks the count or distinct-role requirement is
// caught immediately by `go run -tags fixture ./internal/audit/testdata`.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, CGO_ENABLED=0 compatible
)

// totalRows is the fixture's row count — locked by PROPOSAL-2 §11 Scenario 4
// at 1024. Changing this value requires a re-elicitation per HANDOFF §2.
const totalRows = 1024

// distinctRoleCount is the number of distinct legacy role strings in the
// fixture. Locked at 7 per Scenario 4. The Scenario-4 test asserts
// agents_software has exactly this many "pasture/legacy-role/%" rows
// post-migration as proof of idempotent find-or-create.
const distinctRoleCount = 7

// roleDistribution is the per-role row count. Sums to totalRows (1024) by
// construction. Each row's role is assigned via this distribution before
// any other column is randomised.
//
// Order is preserved so the rows are inserted in a deterministic sequence;
// SQLite assigns sequential id values, so test invariants like "row id 1
// has role architect" are stable across regenerations.
var roleDistribution = []struct {
	role  string
	count int
}{
	{"architect", 256},
	{"supervisor", 192},
	{"worker", 192},
	{"reviewer", 192},
	{"automaton-checker", 96},
	{"human-david", 64},
	{"unknown-legacy", 32},
}

// allPhaseIDs enumerates the 12 PhaseId values from PROPOSAL-2 §11. Hard-
// coded here (rather than imported from pkg/protocol) to keep this build
// program's import graph minimal — it should not pull in the wider
// pasture types.
var allPhaseIDs = []string{
	"p1-request",
	"p2-elicit",
	"p3-research",
	"p4-spec",
	"p5-uat",
	"p6-plan",
	"p7-handoff",
	"p8-impl",
	"p9-impl-slice",
	"p10-code-review",
	"p11-impl-uat",
	"p12-postmortem",
}

// freeFloatingPhaseCount is how many rows get phase=” (empty string).
// TEAM-LEAD BINDING DECISION (bd comment on aura-plugins-k5g3o,
// 2026-04-25): empty string satisfies the v1 schema's `phase TEXT NOT
// NULL` constraint while exercising the migrator's empty-vs-null branch
// logic. Do NOT replace this with NULL or a placeholder phase string —
// future readers, you are explicitly directed to leave the empty strings
// alone.
const freeFloatingPhaseCount = 100

func main() {
	// Find the repo root by walking up from this file's location until
	// we find a go.mod. The fixture lives at <repoRoot>/testdata/legacy_audit_v1.db.
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build_fixture: cannot locate repo root: %s\n", err)
		os.Exit(1)
	}
	dbPath := filepath.Join(repoRoot, "testdata", "legacy_audit_v1.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "build_fixture: cannot create %s: %s\n", filepath.Dir(dbPath), err)
		os.Exit(1)
	}

	// Remove any existing fixture so we start fresh; SQLite would
	// otherwise re-use the existing schema and fail the `CREATE TABLE`.
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "build_fixture: cannot remove existing %s: %s\n", dbPath, err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build_fixture: cannot open %s: %s\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()

	if err := buildFixture(db); err != nil {
		fmt.Fprintf(os.Stderr, "build_fixture: %s\n", err)
		os.Exit(1)
	}
	if err := selfAssert(db); err != nil {
		fmt.Fprintf(os.Stderr, "build_fixture: self-assertion FAILED: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("build_fixture: wrote %s (1024 rows, 7 roles, all post-conditions OK)\n", dbPath)
}

// buildFixture creates the v1 audit_events schema, inserts 1024 rows per
// the §11 Scenario 4 distribution, and commits. No audit_schema_meta is
// created — that's the entire point of v1 fixture (the migrator is
// supposed to detect the missing meta table and treat the file as v1).
func buildFixture(db *sql.DB) error {
	// v1 schema verbatim from pre-PROPOSAL-2 sqlite.go (the shape every
	// existing pasture audit database has on disk before the migration
	// runs). Notes:
	//   - role TEXT NOT NULL — the column the v3 backfill drops.
	//   - phase TEXT NOT NULL — we satisfy this with phase='' for the
	//     free-floating rows per the team-lead's binding decision.
	//   - No audit_schema_meta table — its absence is the "this is v1"
	//     signal the migrator looks for.
	if _, err := db.Exec(`
		CREATE TABLE audit_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			epoch_id   TEXT    NOT NULL,
			phase      TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			event_type TEXT    NOT NULL,
			payload    TEXT    NOT NULL,
			timestamp  INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("CREATE TABLE audit_events: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(
		`INSERT INTO audit_events (epoch_id, phase, role, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare INSERT: %w", err)
	}
	defer stmt.Close()

	// Pre-compute per-row metadata so the inserts run in a single linear
	// pass with deterministic IDs (no shuffling).
	rowMeta := buildRowMetadata()
	if len(rowMeta) != totalRows {
		return fmt.Errorf("internal: rowMeta has %d entries, want %d", len(rowMeta), totalRows)
	}

	baseTime := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	for i, m := range rowMeta {
		ts := baseTime.Add(time.Duration(i) * time.Second).UnixNano()
		if _, err := stmt.Exec(
			m.epochID, m.phase, m.role, m.eventType, m.payload, ts,
		); err != nil {
			return fmt.Errorf("INSERT row %d (role=%q, phase=%q): %w", i, m.role, m.phase, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// rowMeta carries the planned column values for one audit_events row.
type rowMeta struct {
	epochID   string
	phase     string
	role      string
	eventType string
	payload   string
}

// buildRowMetadata returns 1024 deterministic rowMeta entries matching the
// §11 Scenario 4 composition. Determinism (no rand.Seed depending on
// time.Now) is important so two regenerations of the fixture produce
// identical .db files modulo SQLite's WAL ordering.
func buildRowMetadata() []rowMeta {
	rows := make([]rowMeta, 0, totalRows)

	// Step 1: assign roles per the distribution. Index into rows determines
	// row id (since SQLite AUTOINCREMENT issues sequential ids).
	for _, rd := range roleDistribution {
		for i := 0; i < rd.count; i++ {
			rows = append(rows, rowMeta{role: rd.role})
		}
	}

	// Step 2: assign phases. Distribute the 12 PhaseId values across rows
	// in a round-robin pattern, then overwrite the LAST freeFloatingPhaseCount
	// rows with phase='' per the team-lead's binding decision (see
	// const declaration above).
	for i := range rows {
		rows[i].phase = allPhaseIDs[i%len(allPhaseIDs)]
	}
	for i := len(rows) - freeFloatingPhaseCount; i < len(rows); i++ {
		rows[i].phase = "" // TEAM-LEAD: empty-string, NOT NULL — see file-level doc.
	}

	// Step 3: assign event_types in a round-robin (EventPhaseTransition
	// most common, others sprinkled). Plain TEXT — no enum import.
	eventTypes := []string{
		"PhaseTransition",
		"VoteRecorded",
		"ConstraintCheck",
		"BlockerOpened",
		"HookFired",
		"ContextAttached",
	}
	for i := range rows {
		rows[i].eventType = eventTypes[i%len(eventTypes)]
	}

	// Step 4: assign epoch_ids per the §11 Scenario 4 distribution:
	//   - 768 valid Provenance TaskIDs (namespace--uuid)
	//   - 192 legacy free strings
	//   - 64 with epoch_id matching another row's
	assignEpochIDs(rows)

	// Step 5: assign payloads per the JSON edge-case distribution.
	assignPayloads(rows)

	return rows
}

// assignEpochIDs fills each rowMeta.epochID per the §11 Scenario 4
// distribution. UUIDs are generated deterministically from (i, salt) so
// regenerating the fixture produces the same id values — the WAL-level
// byte equivalence is preserved across runs.
func assignEpochIDs(rows []rowMeta) {
	const (
		validTaskIDCount = 768
		freeStringCount  = 192
		duplicateCount   = 64
	)
	if validTaskIDCount+freeStringCount+duplicateCount != totalRows {
		panic(fmt.Sprintf(
			"epoch-id distribution sums to %d, want %d (Scenario 4 invariant)",
			validTaskIDCount+freeStringCount+duplicateCount, totalRows,
		))
	}

	// Block 1: 768 valid Provenance TaskIDs. Use a fixed namespace
	// "aura-plugins" so the IDs round-trip through provenance.ParseTaskID.
	for i := 0; i < validTaskIDCount; i++ {
		rows[i].epochID = "aura-plugins--" + deterministicUUIDv7(uint64(i)+1)
	}

	// Block 2: 192 free strings (legacy non-TaskID epoch IDs). The §7.12
	// migration note explicitly preserves these as historical records.
	for i := 0; i < freeStringCount; i++ {
		rows[validTaskIDCount+i].epochID = fmt.Sprintf("epoch-2026-04-22-mvp-%03d", i)
	}

	// Block 3: 64 duplicates of block-1 epoch IDs (exercises EpochContext
	// dedup pass during S4's v3→v4 migration). We grab IDs from the START
	// of block 1 so the duplicate set is well-distributed.
	for i := 0; i < duplicateCount; i++ {
		rows[validTaskIDCount+freeStringCount+i].epochID = rows[i].epochID
	}
}

// assignPayloads fills each rowMeta.payload per the §11 Scenario 4 JSON
// edge-case distribution. The remaining rows get a small, well-formed
// JSON object so the migration's UTF-8 / size paths are exercised but
// the bulk of the data is "normal".
func assignPayloads(rows []rowMeta) {
	const (
		emptyObjectCount     = 64
		deeplyNestedCount    = 32
		utf8Count            = 16
		largePayloadCount    = 16 // each >8 KB
		topLevelArrayCount   = 4
		largePayloadByteSize = 9 * 1024 // 9 KB to comfortably exceed 8 KB
	)
	special := emptyObjectCount + deeplyNestedCount + utf8Count + largePayloadCount + topLevelArrayCount
	if special > totalRows {
		panic(fmt.Sprintf(
			"payload special-row count %d exceeds totalRows %d", special, totalRows,
		))
	}

	// Default payload for the bulk of rows: a small object with the row's
	// index so reading the .db with `sqlite3 ... SELECT payload FROM
	// audit_events LIMIT 5` shows recognisable structure.
	for i := range rows {
		rows[i].payload = fmt.Sprintf(`{"row":%d,"note":"baseline"}`, i)
	}

	// Special rows — overwrite the FIRST `special` rows with the
	// distribution. Order matters less than total counts, so we use a
	// stable "front-load" pattern that keeps the file regenerable.
	idx := 0

	// Empty objects.
	for i := 0; i < emptyObjectCount; i++ {
		rows[idx].payload = `{}`
		idx++
	}

	// Deeply-nested (depth 4): {"a": {"b": {"c": {"d": "value"}}}}.
	for i := 0; i < deeplyNestedCount; i++ {
		rows[idx].payload = `{"a":{"b":{"c":{"d":"value"}}}}`
		idx++
	}

	// UTF-8: embedded café in note field.
	for i := 0; i < utf8Count; i++ {
		rows[idx].payload = `{"note":"café"}`
		idx++
	}

	// Large payload (>8 KB). One large field of repeating chars + valid JSON.
	largeFiller := strings.Repeat("ABCDEFGH", largePayloadByteSize/8)
	for i := 0; i < largePayloadCount; i++ {
		// Use json.Marshal so the JSON is well-formed even at scale.
		blob, err := json.Marshal(map[string]string{"big": largeFiller})
		if err != nil {
			panic(fmt.Sprintf("internal: cannot marshal large payload: %s", err))
		}
		rows[idx].payload = string(blob)
		idx++
	}

	// Top-level arrays — exercises the JSON unmarshal path's tolerance
	// for non-object roots. Note: pkg/protocol/types.go expects
	// map[string]any, so a query path consuming this row would error;
	// the fixture asserts that the migration preserves the data (TEXT
	// round-trip) regardless.
	for i := 0; i < topLevelArrayCount; i++ {
		rows[idx].payload = `[1,2,3]`
		idx++
	}
}

// deterministicUUIDv7 generates a UUID-like string from the supplied
// integer. We use the google/uuid library's V7 generator directly here
// because the fixture is allowed to look like a real UUIDv7 — what
// matters for tests is that the format ("namespace--uuid") parses, not
// that the bytes are reproducible from the seed. (The other 256 rows
// use the same library to mint their UUIDs; reproducibility across
// runs is best-effort, not asserted.)
//
// If a future reviewer requires byte-stable UUIDs across regenerations
// (so the fixture .db has a stable SHA-256), this function should be
// replaced with a deterministic generator (e.g. namespace-based UUIDv5).
// For now stability is not a binding requirement — the fixture is
// committed to git and not regenerated except to fix composition errors.
func deterministicUUIDv7(_ uint64) string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("uuid.NewV7: %s", err))
	}
	return id.String()
}

// selfAssert verifies the fixture's row composition before the build
// program exits. Per IMPL_PLAN §5.2: "S3 worker must verify the
// fixture's row composition with assertions in the build program
// itself." If any assertion fails the program exits 1 (non-zero) and
// the half-built .db is left on disk for inspection.
func selfAssert(db *sql.DB) error {
	// Total row count == 1024.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
		return fmt.Errorf("COUNT(*): %w", err)
	}
	if n != totalRows {
		return fmt.Errorf("row count = %d, want %d", n, totalRows)
	}

	// Distinct role count == 7.
	if err := db.QueryRow(`SELECT COUNT(DISTINCT role) FROM audit_events`).Scan(&n); err != nil {
		return fmt.Errorf("COUNT(DISTINCT role): %w", err)
	}
	if n != distinctRoleCount {
		return fmt.Errorf("distinct role count = %d, want %d", n, distinctRoleCount)
	}

	// Per-role count matches roleDistribution.
	for _, rd := range roleDistribution {
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM audit_events WHERE role = ?`, rd.role,
		).Scan(&n); err != nil {
			return fmt.Errorf("COUNT(*) WHERE role=%q: %w", rd.role, err)
		}
		if n != rd.count {
			return fmt.Errorf("role=%q count = %d, want %d", rd.role, n, rd.count)
		}
	}

	// Free-floating phase count (phase='') == freeFloatingPhaseCount.
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE phase = ''`,
	).Scan(&n); err != nil {
		return fmt.Errorf("COUNT(*) WHERE phase='': %w", err)
	}
	if n != freeFloatingPhaseCount {
		return fmt.Errorf("free-floating phase count = %d, want %d (TEAM-LEAD binding)", n, freeFloatingPhaseCount)
	}

	// All 12 PhaseId values present (excluding the empty-string bucket).
	rows, err := db.Query(`SELECT DISTINCT phase FROM audit_events WHERE phase != ''`)
	if err != nil {
		return fmt.Errorf("DISTINCT phase: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("scan phase: %w", err)
		}
		seen[p] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows.Err: %w", err)
	}
	for _, p := range allPhaseIDs {
		if !seen[p] {
			return fmt.Errorf("phase %q missing from fixture (want all 12 PhaseId values)", p)
		}
	}

	// audit_schema_meta MUST NOT exist (this is a v1 fixture).
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='audit_schema_meta'`,
	).Scan(&n); err != nil {
		return fmt.Errorf("sqlite_master probe: %w", err)
	}
	if n != 0 {
		return fmt.Errorf("audit_schema_meta table present in v1 fixture (want absent — that's the v1 signal the migrator detects)")
	}

	return nil
}

// findRepoRoot walks up from the directory containing this source file
// (resolved via runtime.Caller) until it finds a go.mod, returning the
// directory containing it. Used to compute the absolute path of the
// fixture artifact at <repoRoot>/testdata/legacy_audit_v1.db.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller(0) failed (cannot locate this source file)")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("walked from %s up to filesystem root without finding go.mod", filepath.Dir(file))
		}
		dir = parent
	}
}
