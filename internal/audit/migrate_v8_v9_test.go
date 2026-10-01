package audit

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// retiredV8Objects is the retired assignment-index schema exactly as a
// version-8 file carries it: the index table, its index, the watermark, the
// coverage state, the three per-generation recovery tables, and the two
// triggers the index installed. Every statement here is copied from the
// sources that own the schema (internal/tasks/open_unified.go and
// internal/tasks/assignment_recovery.go) so the fixture is a real version-8
// shape and not an approximation of one.
//
// The name is declared beside the statement rather than parsed out of it, so a
// skip entry in a test case is the relation's real name and a typo fails the
// assertion instead of silently building the wrong fixture.
var retiredV8Objects = []struct {
	name string
	// requires names the relations whose absence makes this statement
	// impossible. SQLite refuses to create an index or a trigger over a table
	// that is not there, so a skip list has to carry the whole dependency
	// closure; declaring it here keeps a test case honest instead of relying
	// on the reader to know that the triggers sit on the index table.
	requires []string
	ddl      string
}{
	{name: "pasture_actor_assignment", ddl: `CREATE TABLE pasture_actor_assignment (
		assignment_id TEXT PRIMARY KEY,
		actor_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		role TEXT NOT NULL,
		authority_journal_id INTEGER NOT NULL CHECK (authority_journal_id > 0),
		generation INTEGER NOT NULL DEFAULT 0)`},
	{name: "idx_pasture_actor_assignment_actor", requires: []string{"pasture_actor_assignment"},
		ddl: `CREATE INDEX idx_pasture_actor_assignment_actor ON pasture_actor_assignment (actor_id)`},
	{name: "pasture_actor_assignment_watermark", ddl: `CREATE TABLE pasture_actor_assignment_watermark (
		singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 0),
		last_indexed_jid INTEGER NOT NULL)`},
	{name: "pasture_actor_assignment_state", ddl: `CREATE TABLE pasture_actor_assignment_state (
		singleton_id INTEGER PRIMARY KEY CHECK(singleton_id=0),
		generation INTEGER NOT NULL, state_revision INTEGER NOT NULL,
		destructive_revision INTEGER NOT NULL, certified_destructive_revision INTEGER NOT NULL,
		coverage_status TEXT NOT NULL CHECK(coverage_status IN ('valid','dirty','rebuilding')),
		in_progress_snapshot_jid INTEGER NOT NULL, in_progress_after_jid INTEGER NOT NULL,
		completed_through_jid INTEGER NOT NULL, recovery_complete INTEGER NOT NULL CHECK(recovery_complete IN (0,1)))`},
	{name: "pasture_assignment_recovery_scan", ddl: `CREATE TABLE pasture_assignment_recovery_scan (
		generation INTEGER NOT NULL,snapshot_jid INTEGER NOT NULL,kind TEXT NOT NULL,identity TEXT NOT NULL,
		after_jid INTEGER NOT NULL,complete INTEGER NOT NULL CHECK(complete IN (0,1)),
		PRIMARY KEY(generation,snapshot_jid,kind,identity))`},
	{name: "pasture_assignment_recovery_cache", ddl: `CREATE TABLE pasture_assignment_recovery_cache (
		generation INTEGER NOT NULL,snapshot_jid INTEGER NOT NULL,kind TEXT NOT NULL,identity TEXT NOT NULL,
		journal_id INTEGER NOT NULL,value BLOB NOT NULL,
		PRIMARY KEY(generation,snapshot_jid,kind,identity,journal_id))`},
	{name: "pasture_assignment_recovery_member", ddl: `CREATE TABLE pasture_assignment_recovery_member (
		generation INTEGER NOT NULL,snapshot_jid INTEGER NOT NULL,producer TEXT NOT NULL,
		assignment_id TEXT NOT NULL,task_id TEXT NOT NULL,actor_id TEXT NOT NULL,seen INTEGER NOT NULL CHECK(seen IN (0,1)),
		PRIMARY KEY(generation,snapshot_jid,producer,assignment_id))`},
	{name: "pasture_assignment_deleted", requires: []string{"pasture_actor_assignment", "pasture_actor_assignment_state"},
		ddl: `CREATE TRIGGER pasture_assignment_deleted AFTER DELETE ON pasture_actor_assignment
	BEGIN UPDATE pasture_actor_assignment_state SET coverage_status='dirty',
	 destructive_revision=destructive_revision+1,state_revision=state_revision+1
	 WHERE singleton_id=0 AND generation=OLD.generation; END`},
	{name: "pasture_assignment_changed", requires: []string{"pasture_actor_assignment", "pasture_actor_assignment_state"},
		ddl: `CREATE TRIGGER pasture_assignment_changed AFTER UPDATE ON pasture_actor_assignment
		WHEN OLD.assignment_id IS NOT NEW.assignment_id OR OLD.actor_id IS NOT NEW.actor_id
		 OR OLD.task_id IS NOT NEW.task_id OR OLD.role IS NOT NEW.role
		 OR OLD.authority_journal_id IS NOT NEW.authority_journal_id OR OLD.generation IS NOT NEW.generation
	BEGIN UPDATE pasture_actor_assignment_state SET coverage_status='dirty',
	 destructive_revision=destructive_revision+1,state_revision=state_revision+1
	 WHERE singleton_id=0 AND (generation=OLD.generation OR generation=NEW.generation); END`},
}

// retiredV8Seeds insert one row into every table the retired index kept, so a
// drop that took the data with it, or a rollback that left the data behind,
// is visible rather than assumed. The table name is declared beside the
// statement so a skip entry names the same thing the DDL entry does.
var retiredV8Seeds = []struct{ table, statement string }{
	{"pasture_actor_assignment", `INSERT INTO pasture_actor_assignment VALUES('a-1','actor-1','task-1','RoleSliceWorker',1,0)`},
	{"pasture_actor_assignment_watermark", `INSERT INTO pasture_actor_assignment_watermark VALUES(0,42)`},
	{"pasture_actor_assignment_state", `INSERT INTO pasture_actor_assignment_state VALUES(0,0,1,0,0,'valid',0,0,42,1)`},
	{"pasture_assignment_recovery_scan", `INSERT INTO pasture_assignment_recovery_scan VALUES(0,7,'kind','identity',0,1)`},
	{"pasture_assignment_recovery_cache", `INSERT INTO pasture_assignment_recovery_cache VALUES(0,7,'kind','identity',3,X'01')`},
	{"pasture_assignment_recovery_member", `INSERT INTO pasture_assignment_recovery_member VALUES(0,7,'producer','a-1','task-1','actor-1',1)`},
}

// sessionClaimDDL is the relation the v8 → v9 step must NOT touch. Its rows
// are what a session-bound gate read depends on.
const sessionClaimDDL = `CREATE TABLE pasture_session_claim (
	harness TEXT NOT NULL,
	session TEXT NOT NULL,
	actor TEXT NOT NULL,
	claimed_at INTEGER NOT NULL,
	PRIMARY KEY (harness, session)
)`

// openV8AssignmentIndex builds a version-8 file carrying retiredV8Objects and
// retiredV8Seeds, plus the session-claim table with one row, and returns the
// open handle. Pass an empty skip list to leave named relations out, which is
// how the missing-relation cases are built. The DSN carries
// _txlock=immediate because runStep relies on it, exactly as
// NewSqliteAuditTrail's does.
func openV8AssignmentIndex(t *testing.T, skip ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pasture.db")+"?_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	skipped := make(map[string]bool, len(skip))
	for _, name := range skip {
		skipped[name] = true
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	exec(schemaMetaDDL)
	exec(`INSERT INTO audit_schema_meta(version,applied_at) VALUES(8,1)`)
	exec(sessionClaimDDL)
	exec(`INSERT INTO pasture_session_claim VALUES('claude-code','sess-1','actor-1',99)`)
	for _, object := range retiredV8Objects {
		blocked := skipped[object.name]
		for _, dependency := range object.requires {
			if skipped[dependency] {
				blocked = true
			}
		}
		if blocked {
			continue
		}
		exec(object.ddl)
	}
	for _, seed := range retiredV8Seeds {
		if skipped[seed.table] {
			continue
		}
		exec(seed.statement)
	}
	return db
}

// relationExists reports whether sqlite_schema carries name as a table, an
// index or a trigger — the three kinds this step removes. The retired index
// idx_pasture_actor_assignment_actor goes with its table, so "no relation of
// that name under any kind" is the state the step promises.
func relationExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_schema WHERE name = ? AND type IN ('table','index','trigger')`, name,
	).Scan(&count); err != nil {
		t.Fatalf("probe relation %q: %v", name, err)
	}
	return count > 0
}

func requireNoRetiredRelations(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range retiredAssignmentIndexTriggers {
		if relationExists(t, db, name) {
			t.Errorf("retired trigger %q survived the v8 to v9 step", name)
		}
	}
	for _, name := range retiredAssignmentIndexTables {
		if relationExists(t, db, name) {
			t.Errorf("retired table %q survived the v8 to v9 step", name)
		}
	}
	// The index is removed by the drop of its table, never by a statement of
	// its own; assert it separately so a future re-add of the index alone is
	// caught.
	if relationExists(t, db, "idx_pasture_actor_assignment_actor") {
		t.Error("idx_pasture_actor_assignment_actor survived the v8 to v9 step")
	}
}

// TestMigrateV8ToV9DropsTheRetiredIndexRelations is the populated case: a
// version-8 file carrying every retired relation AND a row in each, plus a
// session claim. After Migrate the file is at 9, no retired relation remains
// under any kind, the session claim and its row are untouched, and the
// version table grew by exactly one row.
func TestMigrateV8ToV9DropsTheRetiredIndexRelations(t *testing.T) {
	t.Parallel()
	db := openV8AssignmentIndex(t)

	// Pre-condition: every retired relation is really on the file, so a pass
	// cannot come from a fixture that never had them.
	for _, name := range retiredAssignmentIndexTriggers {
		if !relationExists(t, db, name) {
			t.Fatalf("pre-condition: trigger %q is absent from the fixture", name)
		}
	}
	for _, name := range retiredAssignmentIndexTables {
		if !relationExists(t, db, name) {
			t.Fatalf("pre-condition: table %q is absent from the fixture", name)
		}
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	version, err := readVersion(db)
	if err != nil || version != MaxKnownSchemaVersion {
		t.Fatalf("version=%d want=%d err=%v", version, MaxKnownSchemaVersion, err)
	}
	if version != 9 {
		t.Fatalf("version=%d want 9 (MaxKnownSchemaVersion moved on but the step must stamp 9)", version)
	}
	requireNoRetiredRelations(t, db)

	var claimRows int
	if err := db.QueryRow(`SELECT count(*) FROM pasture_session_claim WHERE harness='claude-code' AND session='sess-1' AND actor='actor-1' AND claimed_at=99`).Scan(&claimRows); err != nil {
		t.Fatalf("read session claim: %v", err)
	}
	if claimRows != 1 {
		t.Errorf("session claim rows=%d want 1; the step must keep session claims", claimRows)
	}

	// One row per applied step: the fixture recorded 8, the step added 9, and
	// nothing else. A step that stamped twice, or not at all, is caught here.
	var metaRows int
	if err := db.QueryRow(`SELECT count(*) FROM audit_schema_meta`).Scan(&metaRows); err != nil {
		t.Fatalf("read version rows: %v", err)
	}
	if metaRows != 2 {
		t.Errorf("audit_schema_meta rows=%d want 2 (the fixture's 8 and this step's 9)", metaRows)
	}
}

// TestMigrateV8ToV9KeepsEveryUnrelatedTable is the second half of the
// populated case: the drop is confined to the retired relations, so an audit
// table nobody asked to remove keeps its rows. Two relations are checked, one
// that the gate path never reads and one that the epoch timeline reads, so a
// drop list that grew by one name is caught whichever side it grew on.
func TestMigrateV8ToV9KeepsEveryUnrelatedTable(t *testing.T) {
	t.Parallel()
	db := openV8AssignmentIndex(t)
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY, payload TEXT NOT NULL)`,
		`CREATE TABLE context_edges (id INTEGER PRIMARY KEY, event_id INTEGER NOT NULL, note TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO audit_events VALUES(1,'kept')`,
		`INSERT INTO context_edges VALUES(1,1,'kept')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for table, column := range map[string]string{"audit_events": "payload", "context_edges": "note"} {
		if !relationExists(t, db, table) {
			t.Errorf("table %q is gone; the step must drop only the retired relations", table)
			continue
		}
		var rows int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE ` + column + `='kept'`).Scan(&rows); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if rows != 1 {
			t.Errorf("table %q rows=%d want 1", table, rows)
		}
	}
}

// TestMigrateV8ToV9ToleratesMissingRelations is the IF EXISTS contract: a
// version-8 file that never carried some of the retired relations still
// records 9, because every statement behind the drops is conditional.
func TestMigrateV8ToV9ToleratesMissingRelations(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"none of the retired relations existed": {
			"pasture_assignment_changed", "pasture_assignment_deleted",
			"pasture_assignment_recovery_member", "pasture_assignment_recovery_cache",
			"pasture_assignment_recovery_scan", "pasture_actor_assignment_state",
			"pasture_actor_assignment_watermark", "pasture_actor_assignment",
		},
		"the triggers were already gone": {
			"pasture_assignment_changed", "pasture_assignment_deleted",
		},
		"the recovery tables were already gone": {
			"pasture_assignment_recovery_member", "pasture_assignment_recovery_cache",
			"pasture_assignment_recovery_scan",
		},
		"the index table was already gone but its watermark was not": {
			"pasture_actor_assignment",
		},
		"the index table and its state table were already gone": {
			"pasture_actor_assignment", "pasture_actor_assignment_state",
		},
	}
	for name, skip := range cases {
		skip := skip
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := openV8AssignmentIndex(t, skip...)
			if err := Migrate(db); err != nil {
				t.Fatalf("Migrate over a file missing %v: %v", skip, err)
			}
			version, err := readVersion(db)
			if err != nil || version != 9 {
				t.Fatalf("version=%d want 9 err=%v", version, err)
			}
			requireNoRetiredRelations(t, db)
			var claimRows int
			if err := db.QueryRow(`SELECT count(*) FROM pasture_session_claim`).Scan(&claimRows); err != nil {
				t.Fatalf("read session claim: %v", err)
			}
			if claimRows != 1 {
				t.Errorf("session claim rows=%d want 1", claimRows)
			}
		})
	}
}

// TestMigrateV8ToV9IsIdempotentOnAVersion9Database is the no-op case: a second
// open of an already-upgraded file changes nothing. Migrate returns nil
// without opening a transaction, the version stays 9, and the version table
// does not grow.
func TestMigrateV8ToV9IsIdempotentOnAVersion9Database(t *testing.T) {
	t.Parallel()
	db := openV8AssignmentIndex(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	var rowsAfterFirst int
	if err := db.QueryRow(`SELECT count(*) FROM audit_schema_meta`).Scan(&rowsAfterFirst); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(db); err != nil {
			t.Fatalf("repeat Migrate %d: %v", i, err)
		}
	}
	version, err := readVersion(db)
	if err != nil || version != 9 {
		t.Fatalf("version=%d want 9 err=%v", version, err)
	}
	var rowsAfterRepeat int
	if err := db.QueryRow(`SELECT count(*) FROM audit_schema_meta`).Scan(&rowsAfterRepeat); err != nil {
		t.Fatal(err)
	}
	if rowsAfterRepeat != rowsAfterFirst {
		t.Errorf("audit_schema_meta rows=%d want %d; a repeat migrate must change nothing", rowsAfterRepeat, rowsAfterFirst)
	}
	requireNoRetiredRelations(t, db)
}

// TestMigrateV8ToV9RollsBackEveryDropWhenTheStepFails is the all-or-nothing
// case. A step is run on a populated version-8 file that performs the real
// drop and THEN fails, which is the worst position for a non-transactional
// drop: the first statement has already taken effect. After the error the
// version is still 8 and every relation, every trigger and every row is
// still there. A drop outside the transaction would leave the file half
// removed at version 8, and this test would find it.
func TestMigrateV8ToV9RollsBackEveryDropWhenTheStepFails(t *testing.T) {
	t.Parallel()
	db := openV8AssignmentIndex(t)
	injected := errors.New("injected failure after the first drop")

	err := runStep(db, migrationStep{
		fromVersion: 8,
		toVersion:   9,
		apply: func(tx *sql.Tx, _ int64) error {
			if err := dropRetiredAssignmentIndex(tx); err != nil {
				t.Fatalf("the real drop failed on the fixture: %v", err)
			}
			return injected
		},
	})
	if !errors.Is(err, injected) {
		t.Fatalf("runStep error=%v, want the injected failure", err)
	}

	version, err := readVersion(db)
	if err != nil {
		t.Fatalf("readVersion after rollback: %v", err)
	}
	if version != 8 {
		t.Errorf("version=%d want 8; a failed step must not record its version", version)
	}
	for _, name := range retiredAssignmentIndexTriggers {
		if !relationExists(t, db, name) {
			t.Errorf("trigger %q was lost to the rollback", name)
		}
	}
	for _, name := range retiredAssignmentIndexTables {
		if !relationExists(t, db, name) {
			t.Fatalf("table %q was lost to the rollback", name)
		}
		var rows int
		if err := db.QueryRow(`SELECT count(*) FROM ` + name).Scan(&rows); err != nil {
			t.Fatalf("read %q after rollback: %v", name, err)
		}
		if rows != 1 {
			t.Errorf("table %q rows=%d want 1; the rollback must restore the data too", name, rows)
		}
	}
	// The file is whole, not merely un-migrated: the triggers still work.
	var status string
	if _, err := db.Exec(`UPDATE pasture_actor_assignment SET task_id='task-2' WHERE assignment_id='a-1'`); err != nil {
		t.Fatalf("the retired trigger no longer fires after rollback: %v", err)
	}
	if err := db.QueryRow(`SELECT coverage_status FROM pasture_actor_assignment_state WHERE singleton_id=0`).Scan(&status); err != nil {
		t.Fatalf("read coverage status: %v", err)
	}
	if status != "dirty" {
		t.Errorf("coverage_status=%q want %q; the trigger body was not restored", status, "dirty")
	}
}

// TestMigrateV8ToV9RefusesADatabaseNewerThanItKnows is the ceiling. A file at
// version 10 is refused by the same newerSchemaError an older binary would
// get from a version-9 file, and the refusal names the supported version the
// constant now carries.
func TestMigrateV8ToV9RefusesADatabaseNewerThanItKnows(t *testing.T) {
	t.Parallel()
	db := openV8AssignmentIndex(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO audit_schema_meta(version,applied_at) VALUES(10,1)`); err != nil {
		t.Fatal(err)
	}
	err := Migrate(db)
	if err == nil {
		t.Fatal("Migrate accepted a version 10 database")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("Migrate returned %T, want *pasterrors.StructuredError", err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category=%q want %q", se.Category, pasterrors.CategoryStorage)
	}
	want := "This audit database was written by a newer pasture (version 10) than this build supports (version 9)."
	if se.What != want {
		t.Errorf("What=%q want %q", se.What, want)
	}
	// The refusal must not have removed anything on its way out.
	for _, name := range retiredAssignmentIndexTables {
		if relationExists(t, db, name) {
			t.Errorf("table %q reappeared; a refused migrate must not touch the file", name)
		}
	}
}

// TestPlanMigrationsListsTheV8ToV9Step pins the dry-run wording for the new
// step. PlanMigrations(8) must offer exactly one step, and its description
// must say all three halves of the promise: the retired tables and triggers go,
// session claims stay, and the floor moves far enough that an older binary
// refuses the whole file. The last clause is the one the migrator cannot warn
// about anywhere else, because it runs on every open.
func TestPlanMigrationsListsTheV8ToV9Step(t *testing.T) {
	t.Parallel()
	plan := PlanMigrations(8)
	if len(plan) != 1 {
		t.Fatalf("PlanMigrations(8) returned %d steps, want 1: %+v", len(plan), plan)
	}
	if plan[0].FromVersion != 8 || plan[0].ToVersion != 9 {
		t.Errorf("step is v%d->v%d, want v8->v9", plan[0].FromVersion, plan[0].ToVersion)
	}
	const want = "remove the retired gate assignment-index tables and triggers; session claims are kept; " +
		"an older pasture binary will then refuse this database for every command until it is upgraded"
	if plan[0].Description != want {
		t.Errorf("Description=%q want %q", plan[0].Description, want)
	}
	// A database already at 9 plans nothing, which is what makes a second
	// `pasture migrate` a no-op.
	if got := PlanMigrations(MaxKnownSchemaVersion); len(got) != 0 {
		t.Errorf("PlanMigrations(9) returned %d steps, want 0: %+v", len(got), got)
	}
	// The registry and the constant cannot disagree: every step the plan
	// offers must be a real step, and the last one must reach the constant.
	steps := migrationSteps()
	last := steps[len(steps)-1]
	if last.fromVersion != 8 || last.toVersion != 9 {
		t.Errorf("last registry step is v%d->v%d, want v8->v9", last.fromVersion, last.toVersion)
	}
	if MaxKnownSchemaVersion != last.toVersion {
		t.Errorf("MaxKnownSchemaVersion=%d but the last step reaches %d", MaxKnownSchemaVersion, last.toVersion)
	}
}

// TestMigrateV8ToV9DoesNotRecordTheVersionWhenADropItselfFails drives a real
// drop failure instead of an injected one, so the guarantee under test is the
// production path's: a step whose DROP is refused must leave the file at
// version 8 with everything intact, and must name the relation it could not
// remove.
//
// The failure is planted by putting a VIEW where a retired table is expected.
// SQLite refuses DROP TABLE on a view by name, so the step stops at that
// relation with the earlier drops already attempted — the exact position the
// version stamp has to survive.
func TestMigrateV8ToV9DoesNotRecordTheVersionWhenADropItselfFails(t *testing.T) {
	t.Parallel()
	db := openV8AssignmentIndex(t)
	if _, err := db.Exec(`DROP TABLE pasture_assignment_recovery_scan`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIEW pasture_assignment_recovery_scan AS SELECT 1 AS refused`); err != nil {
		t.Fatal(err)
	}

	err := Migrate(db)
	if err == nil {
		t.Fatal("Migrate succeeded although a retired relation could not be dropped as a table")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("Migrate returned %T, want *pasterrors.StructuredError", err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category=%q want %q", se.Category, pasterrors.CategoryStorage)
	}
	for _, want := range []string{
		"pasture_assignment_recovery_scan", "version 8 to 9",
		"rolled back", "stays at version 8",
	} {
		if !strings.Contains(se.What+se.Impact, want) {
			t.Errorf("the refusal must say %q so the operator knows which relation failed and that nothing was removed; What=%q Impact=%q", want, se.What, se.Impact)
		}
	}

	version, err := readVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	if version != 8 {
		t.Errorf("version=%d want 8; a step whose drop failed must not record its version", version)
	}
	// The relations the step had already reached are back, data included.
	for _, name := range retiredAssignmentIndexTriggers {
		if !relationExists(t, db, name) {
			t.Errorf("trigger %q was lost to the rollback", name)
		}
	}
	for _, name := range []string{
		"pasture_assignment_recovery_member", "pasture_assignment_recovery_cache",
		"pasture_actor_assignment_state", "pasture_actor_assignment_watermark",
		"pasture_actor_assignment",
	} {
		if !relationExists(t, db, name) {
			t.Fatalf("table %q was lost to the rollback", name)
		}
		var rows int
		if err := db.QueryRow(`SELECT count(*) FROM ` + name).Scan(&rows); err != nil {
			t.Fatalf("read %q: %v", name, err)
		}
		if rows != 1 {
			t.Errorf("table %q rows=%d want 1", name, rows)
		}
	}
	var claims int
	if err := db.QueryRow(`SELECT count(*) FROM pasture_session_claim`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 1 {
		t.Errorf("session claim rows=%d want 1", claims)
	}
}

// TestDropRetiredAssignmentIndexFailureNamesTheRelationAndTheKind pins the
// error a failed drop produces, for BOTH kinds the helper can be handed.
//
// The end-to-end failure test above can only provoke a table drop: with IF
// EXISTS, dropping a trigger that is not there is a silent no-op in SQLite, so
// no fixture can make the trigger branch fail. This direct test is what gives
// the trigger branch its coverage: the operator-facing text is asserted for
// each kind, so a helper that lost the kind, the relation's name, the version
// span or the "nothing was removed" promise is RED.
func TestDropRetiredAssignmentIndexFailureNamesTheRelationAndTheKind(t *testing.T) {
	t.Parallel()
	cause := errors.New("the database refused")
	for _, tc := range []struct{ kind, name string }{
		{"trigger", "pasture_assignment_changed"},
		{"table", "pasture_actor_assignment_state"},
	} {
		err := dropFailure(tc.name, tc.kind, cause)
		var se *pasterrors.StructuredError
		if !errors.As(err, &se) {
			t.Fatalf("%s: dropFailure returned %T, want *pasterrors.StructuredError", tc.kind, err)
		}
		if se.Category != pasterrors.CategoryStorage {
			t.Errorf("%s: Category=%q want %q", tc.kind, se.Category, pasterrors.CategoryStorage)
		}
		if !errors.Is(se, cause) {
			t.Errorf("%s: the driver error must stay in the chain", tc.kind)
		}
		for _, want := range []string{tc.name, tc.kind, "version 8 to 9"} {
			if !strings.Contains(se.What, want) {
				t.Errorf("%s: What=%q must name %q", tc.kind, se.What, want)
			}
		}
		for _, want := range []string{"rolled back", "stays at version 8", "Nothing was removed"} {
			if !strings.Contains(se.Impact, want) {
				t.Errorf("%s: Impact=%q must say %q", tc.kind, se.Impact, want)
			}
		}
		if !strings.Contains(se.Fix, "pasture migrate") {
			t.Errorf("%s: Fix=%q must tell the operator how to retry", tc.kind, se.Fix)
		}
		if !strings.Contains(se.Where, "migrate_v8_v9.go") {
			t.Errorf("%s: Where=%q must point at the step that failed", tc.kind, se.Where)
		}
	}
}

// TestMigrateV8ToV9DropsInTheDeclaredOrder pins the two orderings the step
// promises, by reading the production source rather than by observing the
// database.
//
// Both orderings are real requirements, and neither has an observable
// consequence under SQLite's own semantics, so a behavioural test could not
// fail when one is broken:
//
//   - The triggers sit on pasture_actor_assignment and write into
//     pasture_actor_assignment_state, so they are dropped before either table.
//     SQLite drops a table's own triggers along with the table and treats a
//     DROP TRIGGER of a missing trigger as a no-op, so the reversed order
//     would also succeed here — silently, and only for as long as SQLite
//     accepts it.
//   - writeVersion is the step's LAST statement, the rule every step in this
//     migrator obeys. Moving it first would still roll back with the
//     transaction, so the rollback test above cannot tell the two apart.
//
// Reading the two ordered literals and the step body is therefore the only
// way to make a reordering RED, and a reordering is exactly the kind of drift
// this file exists to prevent.
func TestMigrateV8ToV9DropsInTheDeclaredOrder(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("migrate_v8_v9.go")
	if err != nil {
		t.Fatalf("read the step source: %v", err)
	}
	text := string(source)

	wantTriggers := []string{"pasture_assignment_changed", "pasture_assignment_deleted"}
	wantTables := []string{
		"pasture_assignment_recovery_member",
		"pasture_assignment_recovery_cache",
		"pasture_assignment_recovery_scan",
		"pasture_actor_assignment_state",
		"pasture_actor_assignment_watermark",
		"pasture_actor_assignment",
	}
	if got := retiredAssignmentIndexTriggers; !slices.Equal(got, wantTriggers) {
		t.Errorf("retiredAssignmentIndexTriggers=%v want %v", got, wantTriggers)
	}
	if got := retiredAssignmentIndexTables; !slices.Equal(got, wantTables) {
		t.Errorf("retiredAssignmentIndexTables=%v want %v", got, wantTables)
	}

	// Every declared name is written in the source, in the declared order,
	// with the two triggers before the six tables.
	previous := -1
	for _, name := range append(append([]string{}, wantTriggers...), wantTables...) {
		at := strings.Index(text, `"`+name+`"`)
		if at < 0 {
			t.Errorf("%q is not declared in migrate_v8_v9.go", name)
			continue
		}
		if at <= previous {
			t.Errorf("%q is out of the declared drop order in migrate_v8_v9.go", name)
		}
		previous = at
	}

	// writeVersion is the last statement of the step body.
	bodyStart := strings.Index(text, "func migrateV8toV9Step(")
	if bodyStart < 0 {
		t.Fatal("migrateV8toV9Step is missing from migrate_v8_v9.go")
	}
	body := functionBody(t, text, bodyStart)
	dropAt := strings.Index(body, "dropRetiredAssignmentIndex(tx)")
	writeAt := strings.Index(body, "writeVersion(tx, 9, now)")
	if dropAt < 0 || writeAt < 0 {
		t.Fatalf("the step body must call both dropRetiredAssignmentIndex and writeVersion(9); found drop=%d write=%d", dropAt, writeAt)
	}
	if writeAt < dropAt {
		t.Error("the step must drop the retired relations first and record version 9 last, so a crash before the stamp rolls the drops back with it")
	}
	if after := body[writeAt:]; strings.Contains(after, "dropRetiredAssignmentIndex") {
		t.Error("the step must not run any drop after the version stamp")
	}

	// The trigger loop runs before the table loop. The declared order of the
	// two slices does not establish this on its own — both loops read a
	// different slice, so swapping the loops leaves every name in the file
	// exactly where the reader expects it and only the execution order
	// changes.
	helperStart := strings.Index(text, "func dropRetiredAssignmentIndex(")
	if helperStart < 0 {
		t.Fatal("dropRetiredAssignmentIndex is missing from migrate_v8_v9.go")
	}
	helper := functionBody(t, text, helperStart)
	triggerLoop := strings.Index(helper, "range retiredAssignmentIndexTriggers")
	tableLoop := strings.Index(helper, "range retiredAssignmentIndexTables")
	if triggerLoop < 0 || tableLoop < 0 {
		t.Fatalf("the drop helper must range over both lists; found trigger loop=%d table loop=%d", triggerLoop, tableLoop)
	}
	if triggerLoop > tableLoop {
		t.Error("the two triggers must be dropped before the tables they sit on and write to")
	}
}

// functionBody returns the source of the function whose declaration starts at
// start, bounded by its own closing brace. Bounding there rather than at the
// next declaration keeps the NEXT function's doc comment, which names the same
// helpers, out of the slice.
func functionBody(t *testing.T, text string, start int) string {
	t.Helper()
	body := text[start:]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatalf("no closing brace found for the function at offset %d", start)
	}
	return body[:end+3]
}
