package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rebuildCLI struct {
	binary string
	root   string
	db     string
	env    []string
}

func newRebuildCLI(t *testing.T) rebuildCLI {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"home", "xdg", "cwd", "database"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, dir), 0o700))
	}
	db := filepath.Join(root, "database", "pasture.db")
	return rebuildCLI{
		binary: raceLifecycleBinary(t),
		root:   root,
		db:     db,
		env: []string{
			"HOME=" + filepath.Join(root, "home"),
			"XDG_DATA_HOME=" + filepath.Join(root, "xdg"),
			"XDG_CONFIG_HOME=" + filepath.Join(root, "home", "config"),
			"PASTURE_DB_PATH=" + db,
		},
	}
}

func (cli rebuildCLI) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cli.binary, append([]string{"gate", "rebuild-index"}, args...)...)
	command.Dir = filepath.Join(cli.root, "cwd")
	command.Env = cli.env
	output, err := command.CombinedOutput()
	require.NoError(t, ctx.Err(), "built rebuild-index exceeded its bounded test wait: %s", output)
	return string(output), err
}

func rebuildTracker(t *testing.T, path string) protocol.TaskTracker {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	return tracker
}

// The authority and material event commit through the same public operation,
// using Pasture's production event mapper. No private journal table is read or
// changed. Task.Start alone is a status transition, not an assignment start.
func seedRebuildHistory(t *testing.T, tracker protocol.TaskTracker) []provenance.TaskID {
	t.Helper()
	human, err := tracker.RegisterHumanAgent("rebuild-owner", "Recovery operator", "operator@example.test")
	require.NoError(t, err)
	var ids []provenance.TaskID
	for i := range 2 {
		task, err := tracker.Create("file://rebuild-cli", fmt.Sprintf("work-%d", i), "operator recovery", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
		require.NoError(t, err)
		ids = append(ids, task.ID)
		_, err = tracker.Start(task.ID)
		require.NoError(t, err)
		genesis, err := tracker.Journal().LookupCommitted("pasture.system.genesis.v1")
		require.NoError(t, err)
		require.Equal(t, provenance.CommittedExact, genesis.Kind)
		require.Len(t, genesis.ResultSlots, 1)
		authority := genesis.ResultSlots[0].ProducedJournalID
		require.Positive(t, authority)
		actor := human.ID
		assignment := provenance.AssignmentID(fmt.Sprintf("rebuild-owner-%d", i))
		material, err := tasks.MapMaterialEvent(tasks.AssignmentStartedEvent{
			Task:       task.ID,
			Assignment: assignment,
			Role:       tasks.RoleOwnerResponsibility,
			Occupant:   actor,
		})
		require.NoError(t, err)
		_, err = tracker.Journal().Apply(provenance.OperationInput{
			OperationID:        provenance.OperationID(fmt.Sprintf("rebuild-start-%d", i)),
			ActorID:            actor,
			AuthorityJournalID: &authority,
			CommandDigest:      []byte(fmt.Sprintf("rebuild-start-%d", i)),
			Effects: []provenance.Effect{
				{
					Sort:         provenance.EffectAssignmentStart,
					ResultSlot:   "authority",
					TaskID:       task.ID,
					AssignmentID: assignment,
					SlotID:       provenance.SlotOwnerResponsibility,
					Occupant:     actor,
				},
				material,
			},
		})
		require.NoError(t, err)
	}
	_, err = tracker.Stop(ids[0])
	require.NoError(t, err)
	successor, err := tracker.RegisterHumanAgent("rebuild-successor", "Next owner", "successor@example.test")
	require.NoError(t, err)
	_, err = tracker.TransferTaskAssignment(t.Context(), protocol.TransferTaskAssignmentRequest{
		TaskID:           ids[1],
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "rebuild-successor",
		ActorID:          human.ID,
		NextOccupant:     successor.ID,
	})
	require.NoError(t, err)
	return ids
}

func rebuildStarts(t *testing.T, tracker protocol.TaskTracker) provenance.AssignmentStartPage {
	t.Helper()
	api, ok := tracker.Journal().(provenance.AssignmentStartQueryAPI)
	require.True(t, ok, "production journal must expose the public assignment-start query")
	page, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{
		Page: provenance.AssignmentStartPageRequest{Limit: 64},
	})
	require.NoError(t, err)
	require.Nil(t, page.Next, "this small history must be fully observed")
	return page
}

type rebuildRow struct {
	Assignment string
	Actor      string
	Task       string
	Role       string
	Authority  int64
	Generation int64
}

type rebuildCoverage struct {
	Generation           int64
	Destructive          int64
	CertifiedDestructive int64
	Status               string
	Snapshot             int64
	After                int64
	Through              int64
	Complete             bool
	Watermark            int64
	HasWatermark         bool
}

func rebuildDerived(t *testing.T, path string) ([]rebuildRow, rebuildCoverage) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Query(`SELECT assignment_id,actor_id,task_id,role,authority_journal_id,generation
		FROM pasture_actor_assignment ORDER BY assignment_id`)
	require.NoError(t, err)
	var result []rebuildRow
	for rows.Next() {
		var row rebuildRow
		require.NoError(t, rows.Scan(&row.Assignment, &row.Actor, &row.Task, &row.Role, &row.Authority, &row.Generation))
		result = append(result, row)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	// state_revision is a CAS attempt counter, not coverage. Normal maintenance
	// may advance it without changing any indexed assignment or coverage claim.
	var state rebuildCoverage
	require.NoError(t, db.QueryRow(`SELECT generation,destructive_revision,
		certified_destructive_revision,coverage_status,in_progress_snapshot_jid,
		in_progress_after_jid,completed_through_jid,recovery_complete
		FROM pasture_actor_assignment_state WHERE singleton_id=0`).Scan(
		&state.Generation, &state.Destructive, &state.CertifiedDestructive, &state.Status,
		&state.Snapshot, &state.After, &state.Through, &state.Complete,
	))
	err = db.QueryRow(`SELECT last_indexed_jid FROM pasture_actor_assignment_watermark
		WHERE singleton_id=0`).Scan(&state.Watermark)
	if err != sql.ErrNoRows {
		require.NoError(t, err)
		state.HasWatermark = true
	}
	return result, state
}

func rebuildAuxiliary(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	result := map[string][]string{}
	for _, table := range []string{
		"pasture_assignment_recovery_scan",
		"pasture_assignment_recovery_cache",
		"pasture_assignment_recovery_member",
	} {
		rows, err := db.Query("SELECT * FROM " + table)
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		var contents []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			require.NoError(t, rows.Scan(pointers...))
			encoded, err := json.Marshal(values)
			require.NoError(t, err)
			contents = append(contents, string(encoded))
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		sort.Strings(contents)
		result[table] = contents
	}
	return result
}

func TestGateRebuildIndexBuiltCLINonemptyRepeat(t *testing.T) {
	cli := newRebuildCLI(t)
	tracker := rebuildTracker(t, cli.db)
	seedRebuildHistory(t, tracker)
	before := rebuildStarts(t, tracker)
	require.Len(t, before.Rows, 3, "normal repeat must retain both original starts and the production transfer")
	for _, row := range before.Rows {
		if row.AssignmentID == "rebuild-owner-1" {
			governs, err := tracker.Journal().AuthorityGovernsTaskAt(row.AuthorityJournalID, row.TaskID, before.SnapshotMaxJournalID)
			require.NoError(t, err)
			require.False(t, governs, "the transferred predecessor must be inactive but still indexed as a started episode")
		}
	}
	require.NoError(t, tracker.Close())

	output, err := cli.run(t)
	require.NoError(t, err, output)
	firstRows, firstCoverage := rebuildDerived(t, cli.db)
	firstAuxiliary := rebuildAuxiliary(t, cli.db)
	require.Len(t, firstRows, len(before.Rows))
	for _, start := range before.Rows {
		require.Contains(t, firstRows, rebuildRow{
			Assignment: string(start.AssignmentID),
			Actor:      start.Occupant.String(),
			Task:       start.TaskID.String(),
			Role:       tasks.RoleOwnerResponsibility.String(),
			Authority:  int64(start.AuthorityJournalID),
			Generation: firstCoverage.Generation,
		})
	}
	require.Equal(t, "valid", firstCoverage.Status)
	require.True(t, firstCoverage.Complete)
	require.EqualValues(t, before.SnapshotMaxJournalID, firstCoverage.Through)
	require.Equal(t, firstCoverage.Through, firstCoverage.Watermark)
	require.True(t, firstCoverage.HasWatermark)

	output, err = cli.run(t)
	require.NoError(t, err, output)
	secondRows, secondCoverage := rebuildDerived(t, cli.db)
	require.Equal(t, firstRows, secondRows, "every derived assignment column must survive normal repeat")
	require.Equal(t, firstCoverage, secondCoverage, "watermark and coverage must be idempotent over stable history")
	require.Equal(t, firstAuxiliary, rebuildAuxiliary(t, cli.db), "every auxiliary recovery row must remain identical")
	after := rebuildStarts(t, rebuildTracker(t, cli.db))
	require.Equal(t, before, after, "normal rebuild must not write authoritative history")
}

func TestGateRebuildIndexBuiltCLIEmptyBootstrap(t *testing.T) {
	cli := newRebuildCLI(t)
	tracker := rebuildTracker(t, cli.db)
	empty := rebuildStarts(t, tracker)
	require.Empty(t, empty.Rows)
	_, initial := rebuildDerived(t, cli.db)
	require.Equal(t, "valid", initial.Status, "proven-empty first use must initialize automatically")
	require.True(t, initial.Complete)
	require.EqualValues(t, empty.SnapshotMaxJournalID, initial.Through)
	factory, ok := tracker.(tasks.EpochServiceFactory)
	require.True(t, ok)
	_, err := factory.NewEpochService(tasks.EpochServiceOptions{})
	require.NoError(t, err)
	bootstrapped := rebuildStarts(t, tracker)
	require.Empty(t, bootstrapped.Rows, "bootstrap identity is not an assignment")
	require.Greater(t, bootstrapped.SnapshotMaxJournalID, empty.SnapshotMaxJournalID)
	events, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy: provenance.OrderByJournalID,
		Limit:   64,
	})
	require.NoError(t, err)
	require.Empty(t, events.Events, "bootstrap identity is not task history")
	require.NoError(t, tracker.Close())

	output, err := cli.run(t, "--db=")
	require.NoError(t, err, output)
	firstRows, first := rebuildDerived(t, cli.db)
	require.Empty(t, firstRows)
	require.EqualValues(t, bootstrapped.SnapshotMaxJournalID, first.Watermark)
	require.True(t, first.HasWatermark)
	require.Equal(t, "valid", first.Status)
	require.True(t, first.Complete)

	output, err = cli.run(t)
	require.NoError(t, err, output)
	secondRows, second := rebuildDerived(t, cli.db)
	require.Equal(t, firstRows, secondRows)
	require.Equal(t, first, second)
	require.Equal(t, bootstrapped, rebuildStarts(t, rebuildTracker(t, cli.db)))
}

func TestGateRebuildIndexBuiltCLIResetAndGeneration(t *testing.T) {
	cli := newRebuildCLI(t)
	tracker := rebuildTracker(t, cli.db)
	ids := seedRebuildHistory(t, tracker)
	authorityBefore := rebuildStarts(t, tracker)
	eventsBefore, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy: provenance.OrderByJournalID,
		Limit:   64,
	})
	require.NoError(t, err)
	require.Nil(t, eventsBefore.Next)
	var tasksBefore []provenance.Task
	for _, id := range ids {
		task, err := tracker.Show(id)
		require.NoError(t, err)
		tasksBefore = append(tasksBefore, task)
	}
	require.NoError(t, tracker.Close())
	output, err := cli.run(t)
	require.NoError(t, err, output)
	rowsBefore, stateBefore := rebuildDerived(t, cli.db)
	require.Len(t, rowsBefore, 3)

	db, err := sql.Open("sqlite", cli.db)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM pasture_actor_assignment WHERE assignment_id=?`, rowsBefore[0].Assignment)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	dirtyRows, dirty := rebuildDerived(t, cli.db)
	require.Len(t, dirtyRows, 2, "reset must have actual missing derived data to repair")
	require.Equal(t, "dirty", dirty.Status)
	require.Greater(t, dirty.Destructive, dirty.CertifiedDestructive)

	output, err = cli.run(t)
	require.Error(t, err)
	require.Contains(t, output, "dirty or unexplained legacy index generation requires reset")
	require.Contains(t, output, "--reset")
	unchangedRows, unchanged := rebuildDerived(t, cli.db)
	require.Equal(t, dirtyRows, unchangedRows)
	require.Equal(t, dirty, unchanged, "refusal must not silently reset a dirty generation")

	wrong := fmt.Sprintf("--generation=%d", dirty.Generation+1)
	output, err = cli.run(t, "--reset", wrong)
	require.Error(t, err)
	require.Contains(t, output, fmt.Sprintf("generation changed from %d to %d", dirty.Generation+1, dirty.Generation))
	unchangedRows, unchanged = rebuildDerived(t, cli.db)
	require.Equal(t, dirtyRows, unchangedRows)
	require.Equal(t, dirty, unchanged, "generation guard must precede reset")
	assert.Contains(t, output, "confirm the database path")
	assert.Contains(t, output, "use the reported current value with --generation")
	assert.Contains(t, output, "recovery of database")
	assert.Contains(t, output, cli.db)
	assert.NotContains(t, output, "during bounded catch-up persistence", "operator failure must not be mislabeled as hook persistence")

	output, err = cli.run(t, "--reset", fmt.Sprintf("--generation=%d", dirty.Generation))
	require.NoError(t, err, output)
	resetRows, reset := rebuildDerived(t, cli.db)
	require.Equal(t, stateBefore.Generation+1, reset.Generation)
	require.Equal(t, "valid", reset.Status)
	require.True(t, reset.Complete)
	require.Equal(t, reset.Destructive, reset.CertifiedDestructive)
	require.Equal(t, stateBefore.Watermark, reset.Watermark)
	for i := range rowsBefore {
		rowsBefore[i].Generation = reset.Generation
	}
	require.Equal(t, rowsBefore, resetRows, "reset must reconstruct every retired assignment column")
	tracker = rebuildTracker(t, cli.db)
	require.Equal(t, authorityBefore, rebuildStarts(t, tracker))
	eventsAfter, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy: provenance.OrderByJournalID,
		Limit:   64,
	})
	require.NoError(t, err)
	require.Equal(t, eventsBefore, eventsAfter, "reset must not erase authoritative task or assignment events")
	for i, id := range ids {
		task, err := tracker.Show(id)
		require.NoError(t, err)
		require.Equal(t, tasksBefore[i], task)
	}
	require.NoError(t, tracker.Journal().VerifyIntegrity())
}

func TestGateRebuildIndexBuiltCLIResumeCapturedSnapshot(t *testing.T) {
	cli := newRebuildCLI(t)
	tracker := rebuildTracker(t, cli.db)
	seedRebuildHistory(t, tracker)
	captured := rebuildStarts(t, tracker)
	require.NoError(t, tracker.Close())
	db, err := sql.Open("sqlite", cli.db)
	require.NoError(t, err)
	// Fault injection touches a Pasture-derived table only. The real CLI binds
	// its scan and drains auxiliary history before this publication fails.
	_, err = db.Exec(`CREATE TRIGGER rebuild_test_interrupt BEFORE INSERT ON pasture_actor_assignment
		BEGIN SELECT RAISE(ABORT,'test interrupted assignment publication'); END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	output, err := cli.run(t)
	require.Error(t, err)
	require.Contains(t, output, "test interrupted assignment publication")
	_, interrupted := rebuildDerived(t, cli.db)
	require.False(t, interrupted.Complete)
	require.EqualValues(t, captured.SnapshotMaxJournalID, interrupted.Snapshot)

	db, err = sql.Open("sqlite", cli.db)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TRIGGER rebuild_test_interrupt`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	tracker = rebuildTracker(t, cli.db)
	_, err = tracker.Create("file://rebuild-cli", "later history", "outside the captured prefix", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	later := rebuildStarts(t, tracker)
	require.Greater(t, later.SnapshotMaxJournalID, captured.SnapshotMaxJournalID)
	require.NoError(t, tracker.Close())

	output, err = cli.run(t)
	require.NoError(t, err, output)
	rows, resumed := rebuildDerived(t, cli.db)
	require.Len(t, rows, len(captured.Rows))
	require.Equal(t, interrupted.Generation, resumed.Generation, "normal resume must not reset")
	require.True(t, resumed.Complete)
	require.EqualValues(t, captured.SnapshotMaxJournalID, resumed.Through, "resume must finish the captured snapshot, not a fresh one")
	output, err = cli.run(t)
	require.NoError(t, err, output)
	_, tail := rebuildDerived(t, cli.db)
	require.EqualValues(t, later.SnapshotMaxJournalID, tail.Through, "the next normal run covers later history")
	require.Equal(t, later, rebuildStarts(t, rebuildTracker(t, cli.db)))
}

type rebuildFile struct {
	Mode fs.FileMode
	Hash [32]byte
}

func rebuildInventory(t *testing.T, root string) map[string]rebuildFile {
	t.Helper()
	result := map[string]rebuildFile{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file := rebuildFile{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file.Hash = sha256.Sum256(contents)
		}
		result[relative] = file
		return nil
	})
	require.NoError(t, err)
	return result
}

func assertRebuildPathRefused(t *testing.T, cli rebuildCLI, path string) string {
	t.Helper()
	before := rebuildInventory(t, cli.root)
	output, err := cli.run(t, "--db", path)
	assert.Error(t, err, "malformed path must not rebuild another database: %s", output)
	assert.Equal(t, before, rebuildInventory(t, cli.root), "no cwd, HOME, XDG, environment fallback or stray database may be created")
	assert.Contains(t, output, path)
	assert.Contains(t, output, "gate rebuild-index")
	assert.Contains(t, output, "--db")
	assert.Contains(t, output, "No assignment rebuild was started")
	return output
}

func TestGateRebuildIndexBuiltCLINonDirectoryParent(t *testing.T) {
	cli := newRebuildCLI(t)
	parent := filepath.Join(cli.root, "not-a-directory")
	require.NoError(t, os.WriteFile(parent, []byte("keep this file"), 0o600))
	output := assertRebuildPathRefused(t, cli, filepath.Join(parent, "pasture.db"))
	assert.Contains(t, output, "not a directory")
	assert.Contains(t, output, "intended database file in a writable directory")
}

func TestGateRebuildIndexBuiltCLIRejectsConnectionStringPath(t *testing.T) {
	cli := newRebuildCLI(t)
	output := assertRebuildPathRefused(t, cli, filepath.Join(cli.root, "database", "wrong.db?ignored=1"))
	assert.Contains(t, output, "contains URI syntax")
	assert.Contains(t, output, "filesystem path without those characters")
}

func TestGateRebuildIndexBuiltCLIRejectsFragmentPath(t *testing.T) {
	cli := newRebuildCLI(t)
	assertRebuildPathRefused(t, cli, filepath.Join(cli.root, "database", "wrong.db#fragment"))
}

func TestGateRebuildIndexBuiltCLIRejectsEscapedPath(t *testing.T) {
	cli := newRebuildCLI(t)
	assertRebuildPathRefused(t, cli, filepath.Join(cli.root, "database", "wrong%2edb"))
}

func TestGateRebuildIndexBuiltCLIFlagPrecedesEnvironment(t *testing.T) {
	cli := newRebuildCLI(t)
	chosen := filepath.Join(cli.root, "database", "explicit.db")
	output, err := cli.run(t, "--db", chosen)
	require.NoError(t, err, output)
	_, err = os.Stat(chosen)
	require.NoError(t, err)
	_, err = os.Stat(cli.db)
	require.True(t, os.IsNotExist(err), "nonempty --db must override PASTURE_DB_PATH")
	files := rebuildInventory(t, cli.root)
	for path, file := range files {
		if file.Mode.IsRegular() {
			relative := filepath.Join("database", "explicit.db")
			require.True(t, path == relative || path == relative+"-wal" || path == relative+"-shm", "unexpected file %q", path)
		}
	}
}

func TestGateRebuildIndexBuiltCLIHelp(t *testing.T) {
	cli := newRebuildCLI(t)
	before := rebuildInventory(t, cli.root)
	output, err := cli.run(t, "--help")
	require.NoError(t, err, output)
	text := strings.Join(strings.Fields(output), " ")
	assert.Contains(t, text, "Recover the derived index of authenticated started assignments")
	assert.Contains(t, text, "does not determine whether an assignment is active")
	assert.Contains(t, text, "leaves authoritative task and assignment history unchanged")
	assert.Contains(t, text, "Repeating without --reset over unchanged history preserves assignment rows and the coverage watermark")
	assert.Contains(t, text, "An incomplete run resumes its captured journal snapshot from the durable cursor")
	assert.Contains(t, text, "Run again after completion to include later history")
	assert.Contains(t, text, "A dirty generation requires --reset")
	assert.Contains(t, text, "--reset retires only derived data and starts a new generation")
	assert.Contains(t, text, "confirm the database path and use the reported current value with --generation")
	assert.Contains(t, text, "A proven-empty assignment history is initialized automatically")
	assert.Contains(t, text, "The database path must not contain ?, #, or %")
	assert.Contains(t, text, "pasture gate rebuild-index --db /path/to/pasture.db")
	assert.NotRegexp(t, `aura-plugins-|beads://|PROPOSAL-[0-9]|SLICE-[0-9]`, output)
	require.Equal(t, before, rebuildInventory(t, cli.root), "help must not open a database")
}
