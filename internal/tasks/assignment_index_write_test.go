package tasks

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

func indexWriteStore(t *testing.T) (*trackerImpl, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	opened, err := OpenTaskTrackerWithOptions(path, WithTimeoutProfile(timeouts.DeadlineTestProfile()))
	require.NoError(t, err)
	store := opened.(*trackerImpl)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store, path
}

func indexWritePage(t *testing.T, store *trackerImpl) assignmentIndexPage {
	t.Helper()
	actor := feasibilityActor(t, store, "index-writer")
	task := createHumanTestTask(t, store, "index-write")
	seed := seedFeasibilityEpisode(t, store, task, "first", actor, "index-first")
	return assignmentIndexPage{Through: journalMaximum(t, store), Rows: []startedEpisode{{Assignment: "first", Actor: actor, Task: task, Role: RoleOwnerResponsibility, Authority: seed.authority}}}
}

func assertIndexPage(t *testing.T, store *trackerImpl, page assignmentIndexPage) {
	t.Helper()
	rows, err := store.auditDB.Query(`SELECT assignment_id, actor_id, task_id, role, authority_journal_id FROM pasture_actor_assignment ORDER BY assignment_id`)
	require.NoError(t, err)
	var got []string
	for rows.Next() {
		var assignment, actor, task, role string
		var authority int64
		require.NoError(t, rows.Scan(&assignment, &actor, &task, &role, &authority))
		got = append(got, fmt.Sprintf("%s/%s/%s/%s/%d", assignment, actor, task, role, authority))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	want := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		want = append(want, fmt.Sprintf("%s/%s/%s/%s/%d", row.Assignment, row.Actor, row.Task, row.Role, row.Authority))
	}
	require.ElementsMatch(t, want, got, "persisted rows must equal the input page, without duplicates")
	var watermark provenance.JournalID
	require.NoError(t, store.auditDB.QueryRow(`SELECT COALESCE((SELECT last_indexed_jid FROM pasture_actor_assignment_watermark WHERE singleton_id = 0), 0)`).Scan(&watermark))
	require.Equal(t, page.Through, watermark, "watermark must cover exactly the persisted page")
}

// RED: remove ON CONFLICT, skip the insert, or replace max(stored,new) with new.
func TestAssignmentIndexPersistIsIdempotentAndConcurrent(t *testing.T) {
	t.Parallel()
	store, _ := indexWriteStore(t)
	page := indexWritePage(t, store)
	start := make(chan struct{})
	done := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			done <- persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), page)
		}()
	}
	close(start)
	for range 2 {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(timeouts.TestProfile().WorkflowResult()):
			t.Fatal("concurrent index persist did not return before the test ceiling")
		}
	}
	require.NoError(t, persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), page))
	older := page
	older.Through--
	require.NoError(t, persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), older))
	assertIndexPage(t, store, page)
}

// RED: commit rows before updating the watermark, ignore the watermark error,
// or let a page jump over a gap. The failure must leave no partial page.
func TestAssignmentIndexPersistRollsBackAndRefusesGaps(t *testing.T) {
	t.Parallel()
	store, _ := indexWriteStore(t)
	page := indexWritePage(t, store)
	_, err := store.auditDB.Exec(`CREATE TRIGGER refuse_watermark BEFORE INSERT ON pasture_actor_assignment_watermark BEGIN SELECT RAISE(ABORT, 'held watermark'); END`)
	require.NoError(t, err)
	err = persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), page)
	var stale *IndexStaleError
	require.ErrorAs(t, err, &stale)
	require.ErrorContains(t, err, "no partial page or watermark is committed")
	assertIndexPage(t, store, assignmentIndexPage{})
	_, err = store.auditDB.Exec(`DROP TRIGGER refuse_watermark`)
	require.NoError(t, err)
	page.From = 1
	err = persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), page)
	require.ErrorAs(t, err, &stale)
	assertIndexPage(t, store, assignmentIndexPage{})
}

// RED: remove the page cap. The cap reads the production catch-up bound.
func TestAssignmentIndexPersistCapsOnePage(t *testing.T) {
	t.Parallel()
	store, _ := indexWriteStore(t)
	page := indexWritePage(t, store)
	row := page.Rows[0]
	page.Rows = make([]startedEpisode, gateauthority.CatchUpPageSize+1)
	for i := range page.Rows {
		page.Rows[i] = row
		page.Rows[i].Assignment = provenance.AssignmentID(fmt.Sprintf("episode-%d", i))
	}
	var stale *IndexStaleError
	require.ErrorAs(t, persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), page), &stale)
	assertIndexPage(t, store, assignmentIndexPage{})
	page.Rows = page.Rows[:gateauthority.CatchUpPageSize]
	require.NoError(t, persistAssignmentIndex(t.Context(), store.auditDB, timeouts.TestProfile(), page))
	assertIndexPage(t, store, page)
}

// RED: suppress the busy error or retry until the lock is released. The lock
// stays held until the result is observed, including the bounded failure case.
func TestAssignmentIndexPersistFaultsUnderHeldLock(t *testing.T) {
	t.Parallel()
	store, path := indexWriteStore(t)
	page := indexWritePage(t, store)
	profile := timeouts.DeadlineTestProfile()
	holder, err := dbconn.OpenSharedDBWithProfile(path, profile)
	require.NoError(t, err)
	defer holder.Close()
	tx, err := holder.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE pasture_actor_assignment_watermark SET last_indexed_jid = last_indexed_jid`)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- persistAssignmentIndex(t.Context(), store.auditDB, profile, page) }()
	select {
	case err = <-done:
		var stale *IndexStaleError
		require.ErrorAs(t, err, &stale, "held write lock must return an index fault, not success")
	case <-time.After(profile.WorkflowResult()):
		t.Fatal("index persist did not return while the write lock stayed held")
	}
	require.NoError(t, tx.Rollback())
	assertIndexPage(t, store, assignmentIndexPage{})
	require.NoError(t, persistAssignmentIndex(t.Context(), store.auditDB, profile, page))
	assertIndexPage(t, store, page)
}

// RED: add a second transaction or a write in a loop. This narrow check reads
// the two write APIs only. The reader's call count is checked when it exists;
// this test does not claim to measure the whole gate's cost.
func TestAssignmentIndexAndClaimWriteCountsAreBounded(t *testing.T) {
	for _, subject := range []struct {
		file, function string
		want           map[string]int
	}{
		{"assignment_index_write.go", "persistAssignmentIndex", map[string]int{"BeginTx": 1, "ExecContext": 2, "Commit": 1}},
		{"session_claim.go", "RecordLifecycleSessionClaim", map[string]int{"ExecContext": 1}},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), subject.file, nil, 0)
		require.NoError(t, err)
		found := false
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != subject.function {
				continue
			}
			found = true
			calls := map[string]int{}
			ast.Inspect(fn, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						switch sel.Sel.Name {
						case "BeginTx", "ExecContext", "Commit":
							calls[sel.Sel.Name]++
						}
					}
				}
				switch n.(type) {
				case *ast.ForStmt, *ast.RangeStmt:
					ast.Inspect(n, func(child ast.Node) bool {
						if sel, ok := child.(*ast.SelectorExpr); ok {
							switch sel.Sel.Name {
							case "BeginTx", "ExecContext", "Commit":
								t.Errorf("%s performs %s in a loop", subject.function, sel.Sel.Name)
							}
						}
						return true
					})
				}
				return true
			})
			require.Equal(t, subject.want, calls, "write API must keep one bounded attempt")
		}
		require.True(t, found, "write-count guard must read its production function")
	}
}
