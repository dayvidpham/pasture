package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
)

// The fault record was UNBOUNDED: one line per faulting invocation, nothing
// rotating or trimming, measured at 581 MiB and 528,562 lines on the default
// store and growing at roughly 165 MiB a day. boundLifecycleFaultRecord rotates
// an over-large live record onto its single `.1` backup so the pair is bounded,
// and these tests hold three properties that bound must not break:
//
//  1. The rotation itself, over a cap the test chooses (the parallel core test).
//  2. The newest record is never the one dropped, driven through
//     recordLifecycleFault on the production cap (the serial path test).
//  3. A record that cannot be written is still reported on standard error, so a
//     full store cannot be silenced by the fact that rotation exists (the
//     serial loss test).

// TestTheFaultRecordRotatesOnceItReachesItsCap tests the rotation core with a
// cap the test chooses, so the production cap can move without rewriting this.
//
// SERIAL-FREE: it touches no shared process state and uses its own temporary
// directory, so it runs in parallel.
func TestTheFaultRecordRotatesOnceItReachesItsCap(t *testing.T) {
	t.Parallel()

	const cap int64 = 64

	t.Run("under the cap nothing moves", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, lifecycleFaultRecordFile)
		require.NoError(t, os.WriteFile(path, []byte("a short history\n"), 0o644))

		boundLifecycleFaultRecord(path, cap)

		live, err := os.ReadFile(path)
		require.NoError(t, err, "the live record must survive an under-cap check")
		assert.Equal(t, "a short history\n", string(live))
		_, statErr := os.Stat(path + lifecycleFaultRecordBackupSuffix)
		assert.True(t, os.IsNotExist(statErr),
			"no backup may appear until the live record is over the cap")
	})

	t.Run("at the cap the record rotates onto its backup", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, lifecycleFaultRecordFile)
		old := bytes.Repeat([]byte("x"), int(cap))
		require.NoError(t, os.WriteFile(path, old, 0o644))

		boundLifecycleFaultRecord(path, cap)

		_, statErr := os.Stat(path)
		assert.True(t, os.IsNotExist(statErr),
			"rotation renames the live record away; the caller reopens it with O_CREATE")
		backup, readErr := os.ReadFile(path + lifecycleFaultRecordBackupSuffix)
		require.NoError(t, readErr,
			"the history must be retained, only bounded: it moves to the backup")
		assert.Equal(t, old, backup)
	})

	t.Run("over the cap it rotates and the older backup is replaced", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, lifecycleFaultRecordFile)
		over := bytes.Repeat([]byte("y"), int(cap)+1)
		require.NoError(t, os.WriteFile(path, over, 0o644))
		require.NoError(t, os.WriteFile(path+lifecycleFaultRecordBackupSuffix,
			[]byte("an older backup\n"), 0o644))

		boundLifecycleFaultRecord(path, cap)

		backup, readErr := os.ReadFile(path + lifecycleFaultRecordBackupSuffix)
		require.NoError(t, readErr)
		assert.Equal(t, over, backup,
			"a single fixed backup name is what bounds the pair; the older backup is replaced")
	})

	t.Run("a non-regular record path is left alone", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// A DIRECTORY at the record path is the root-proof way to make the open
		// arm fail; rotation must not rename it out from under that test.
		path := filepath.Join(dir, lifecycleFaultRecordFile)
		require.NoError(t, os.Mkdir(path, 0o755))

		boundLifecycleFaultRecord(path, cap)

		info, statErr := os.Stat(path)
		require.NoError(t, statErr, "the directory must still stand at the record path")
		assert.True(t, info.IsDir(), "rotation must only ever move a REGULAR file")
		_, backupErr := os.Stat(path + lifecycleFaultRecordBackupSuffix)
		assert.True(t, os.IsNotExist(backupErr))
	})

	t.Run("an absent record path is left alone", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, lifecycleFaultRecordFile)

		boundLifecycleFaultRecord(path, cap)

		_, statErr := os.Stat(path)
		assert.True(t, os.IsNotExist(statErr),
			"rotation must not create a record where none exists; the writer owns creation")
	})
}

// TestARotatingFaultRecordStoreKeepsTheNewestRecord drives the production path:
// a store whose live record is already over the production cap, one real fault
// through recordLifecycleFault, and then the byte-level facts the bound must
// preserve.
//
// SERIAL: this test drives the shared hookLifecycleCmd and writes flagDBPath, so
// it must not use t.Parallel. TestEveryTestThatReachesSharedProcessStateStaysSerial
// holds that.
func TestARotatingFaultRecordStoreKeepsTheNewestRecord(t *testing.T) {
	coords := lifecycleCoordinates{Harness: ir.HarnessClaudeCode, Event: "PreToolUse", HostVersion: "2.1.261"}
	failure := lifecycleFailurePolicy(coords)

	store := t.TempDir()
	recordPath := filepath.Join(store, lifecycleFaultRecordFile)
	// A sparse file of exactly the cap is enough: only its size is read by the
	// rotation, and this keeps the test fast while still crossing the real cap.
	require.NoError(t, os.WriteFile(recordPath, nil, 0o644))
	require.NoError(t, os.Truncate(recordPath, lifecycleFaultRecordMaxBytes),
		"the live record must start AT the production cap, or the rotation under test cannot run")

	cmd := lifecycleTestCommand(t, string(coords.Harness), coords.Event, coords.HostVersion,
		filepath.Join(store, "pasture.db"))
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	outcome := lifecycleFault(cmd, coords, failure, hostexit.FaultFailOpen,
		lifecycleContinuation(coords, failure), hostexit.FaultStageRecordUnknown,
		errors.New("the store could not be opened"))

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"bounding the record must not change the host outcome; it is evidence, not a condition of the exit")

	backupInfo, backupErr := os.Stat(recordPath + lifecycleFaultRecordBackupSuffix)
	require.NoError(t, backupErr, "the over-cap history must be rotated to the backup, not discarded")
	assert.Equal(t, int64(lifecycleFaultRecordMaxBytes), backupInfo.Size(),
		"the whole over-cap record is retained as the backup")

	liveInfo, liveErr := os.Stat(recordPath)
	require.NoError(t, liveErr, "the caller must reopen a fresh live record for this fault")
	assert.Less(t, liveInfo.Size(), int64(lifecycleFaultRecordMaxBytes),
		"the live record starts fresh, so the pair stays bounded at about twice the cap")

	records := readFaultRecords(t, store)
	require.Len(t, records, 1, "the newest record must land in the live file, and only it")
	assert.Equal(t, "PreToolUse", records[0]["event"],
		"the record kept is the fault just written, not one the rotation dropped")
	assert.Equal(t, "fault", records[0]["outcomeClass"])
	assert.NotContains(t, stderr.String(), faultRecordLossSuffix,
		"the record WAS written, so no loss may be reported")
}

// TestAFullFaultRecordStoreStillReportsTheLoss pins the contract the bound must
// not swallow: when the record cannot be written, the operator is still told on
// standard error, even though the rotation code now stands in the path.
//
// The input is the REAL append failure the suite already uses elsewhere: a
// symlink at the record path to /dev/full, which opens and then refuses every
// write with ENOSPC. Nothing is simulated and nothing is mocked. The rotation
// leaves this path alone because the symlink resolves to a character device,
// not a regular file, so the write failure reaches exactly the same arm it did
// before the bound existed.
//
// SERIAL: this test drives the shared hookLifecycleCmd and writes flagDBPath.
func TestAFullFaultRecordStoreStillReportsTheLoss(t *testing.T) {
	probe, probeErr := os.OpenFile(devFull, os.O_WRONLY, 0)
	if probeErr != nil {
		t.Skipf("this platform has no writable %s, which is what makes a real ENOSPC "+
			"available without a loopback filesystem or a quota: %v", devFull, probeErr)
	}
	require.NoError(t, probe.Close())

	coords := lifecycleCoordinates{Harness: ir.HarnessClaudeCode, Event: "PreToolUse", HostVersion: "2.1.261"}
	failure := lifecycleFailurePolicy(coords)

	store := t.TempDir()
	recordPath := filepath.Join(store, lifecycleFaultRecordFile)
	require.NoError(t, os.Symlink(devFull, recordPath),
		"the record path must resolve to a device that opens and then refuses every write")

	cmd := lifecycleTestCommand(t, string(coords.Harness), coords.Event, coords.HostVersion,
		filepath.Join(store, "pasture.db"))
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	outcome := lifecycleFault(cmd, coords, failure, hostexit.FaultFailOpen,
		lifecycleContinuation(coords, failure), hostexit.FaultStageRecordUnknown,
		errors.New("the store could not be opened"))

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"a full record store is fail-open: durable evidence must never decide the host outcome")
	text := stderr.String()
	assert.Contains(t, text, "could not append to its fault record",
		"the append arm must still reach its own diagnostic with the bound in the path")
	assert.Contains(t, text, faultRecordLossSuffix,
		"a full store must not be silenced by the existence of rotation: the loss is reported on this stream only")

	_, backupErr := os.Stat(recordPath + lifecycleFaultRecordBackupSuffix)
	assert.True(t, os.IsNotExist(backupErr),
		"a character device is not a regular record, so rotation must not have moved it")
	link, linkErr := os.Lstat(recordPath)
	require.NoError(t, linkErr)
	assert.NotZero(t, link.Mode()&os.ModeSymlink,
		"the record path must still be the symlink this test made")
}
