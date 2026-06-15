//go:build recovery

package engine

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

const recoveryVoteWorkflowName = "pasture.recovery_vote_test.v1"

type recoveryVoteInput struct {
	EpochID string
}

type voteRecordedSnapshot struct {
	ID        int64
	Phase     string
	AgentID   string
	EventType string
	Payload   string
	Timestamp int64
	DedupKey  string
}

func TestRecovery_MultiVoteCrashReplay(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	workflowID := "epoch-recover-multi-vote"

	ready := armRecoveryVoteReadySignal(t)
	victim := exec.Command(os.Args[0], "-test.run", "^TestRecoveryVoteHelperProcess$")
	victim.Env = append(os.Environ(),
		"PASTURE_RECOVERY_VOTE_HELPER=1",
		"PASTURE_RECOVERY_VOTE_DB="+dbPath,
		"PASTURE_RECOVERY_VOTE_WFID="+workflowID,
		"PASTURE_RECOVERY_VOTE_STALL=120",
	)
	victim.Stderr = os.Stderr
	if err := victim.Start(); err != nil {
		t.Fatalf("start recovery-vote victim: %v", err)
	}

	waitForRecoveryVoteReady(t, ready, 60*time.Second)
	before := voteRecordedRows(t, dbPath)
	if len(before) != len(protocol.AllReviewAxes) {
		_ = victim.Process.Kill()
		_ = victim.Wait()
		t.Fatalf("VoteRecorded rows before kill = %d, want %d: %+v", len(before), len(protocol.AllReviewAxes), before)
	}

	if err := victim.Process.Kill(); err != nil {
		t.Fatalf("kill recovery-vote victim: %v", err)
	}
	_ = victim.Wait()

	resumer := exec.Command(os.Args[0], "-test.run", "^TestRecoveryVoteHelperProcess$")
	resumer.Env = append(os.Environ(),
		"PASTURE_RECOVERY_VOTE_HELPER=1",
		"PASTURE_RECOVERY_VOTE_DB="+dbPath,
		"PASTURE_RECOVERY_VOTE_WFID="+workflowID,
		"PASTURE_RECOVERY_VOTE_STALL=0",
	)
	out, err := resumer.CombinedOutput()
	if err != nil {
		t.Fatalf("recovery-vote resumer failed: %v\n%s", err, out)
	}
	if !containsLine(string(out), "VOTE COMPLETE 3") {
		t.Fatalf("recovery-vote resumer did not complete the recovered workflow; output:\n%s", out)
	}

	after := voteRecordedRows(t, dbPath)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("VoteRecorded rows changed across crash replay:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestRecoveryVoteHelperProcess(t *testing.T) {
	if os.Getenv("PASTURE_RECOVERY_VOTE_HELPER") != "1" {
		return
	}

	dbPath := os.Getenv("PASTURE_RECOVERY_VOTE_DB")
	workflowID := os.Getenv("PASTURE_RECOVERY_VOTE_WFID")
	stallSeconds, _ := strconv.Atoi(os.Getenv("PASTURE_RECOVERY_VOTE_STALL"))
	if dbPath == "" || workflowID == "" {
		t.Fatal("PASTURE_RECOVERY_VOTE_DB and PASTURE_RECOVERY_VOTE_WFID are required")
	}

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	defer tracker.Close()

	e, err := New(context.Background(), Config{
		DBPath:             dbPath,
		ApplicationVersion: "recovery-vote-test-v1",
		ExecutorID:         "pasture-recovery-vote-test",
		Trail:              tracker,
		Tracker:            tracker,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer e.Shutdown(5 * time.Second)

	workflow := recoveryVoteWorkflow(t, e, stallSeconds)
	dbos.RegisterWorkflow(e.DBOS(), workflow, dbos.WithWorkflowName(recoveryVoteWorkflowName))

	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}

	handle, err := dbos.RunWorkflow(e.DBOS(), workflow,
		recoveryVoteInput{EpochID: workflowID},
		dbos.WithWorkflowID(workflowID))
	if err != nil {
		t.Fatalf("RunWorkflow(recovery votes): %v", err)
	}
	count, err := handle.GetResult(dbos.WithHandleTimeout(120 * time.Second))
	if err != nil {
		t.Fatalf("GetResult(recovery votes): %v", err)
	}
	fmt.Printf("VOTE COMPLETE %d\n", count)
}

func recoveryVoteWorkflow(t *testing.T, e *Engine, stallSeconds int) func(dbos.DBOSContext, recoveryVoteInput) (int, error) {
	t.Helper()
	return func(ctx dbos.DBOSContext, in recoveryVoteInput) (int, error) {
		for i, axis := range protocol.AllReviewAxes {
			axis := axis
			reviewerID := "recovery-" + string(axis)
			stepSeqInt, err := dbos.GetStepID(ctx)
			if err != nil {
				return 0, err
			}
			stepSeq := strconv.Itoa(stepSeqInt)
			dedupKey := protocol.DedupKey(in.EpochID, string(protocol.PhaseReview), string(protocol.EventVoteRecorded), stepSeq)
			_, err = dbos.RunAsStep(ctx, func(c context.Context) (struct{}, error) {
				_, err := e.trail.RecordEventReturningId(c, protocol.AuditEvent{
					EpochId:   in.EpochID,
					Phase:     protocol.PhaseReview,
					Role:      reviewerID,
					EventType: protocol.EventVoteRecorded,
					Payload: map[string]any{
						"axis":       string(axis),
						"vote":       string(protocol.VoteAccept),
						"reviewerId": reviewerID,
					},
					Timestamp: time.Now().UTC(),
					DedupKey:  dedupKey,
				})
				if err != nil {
					return struct{}{}, err
				}
				if stallSeconds > 0 && i == len(protocol.AllReviewAxes)-1 {
					if err := signalRecoveryVoteReady(); err != nil {
						return struct{}{}, err
					}
					select {
					case <-time.After(time.Duration(stallSeconds) * time.Second):
					case <-c.Done():
						return struct{}{}, c.Err()
					}
				}
				return struct{}{}, nil
			})
			if err != nil {
				return 0, err
			}
		}
		return len(protocol.AllReviewAxes), nil
	}
}

func armRecoveryVoteReadySignal(t *testing.T) chan os.Signal {
	t.Helper()
	ready := make(chan os.Signal, 1)
	signal.Notify(ready, syscall.SIGUSR1)
	t.Cleanup(func() {
		signal.Stop(ready)
	})
	return ready
}

func waitForRecoveryVoteReady(t *testing.T, ready chan os.Signal, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
	case <-timer.C:
		t.Fatal("recovery-vote victim never signalled readiness within timeout")
	}
}

func signalRecoveryVoteReady() error {
	parent := os.Getppid()
	if parent <= 1 {
		return fmt.Errorf("cannot signal readiness: invalid parent pid %d", parent)
	}
	if err := syscall.Kill(parent, syscall.SIGUSR1); err != nil {
		return fmt.Errorf("signal readiness to parent pid %d: %w", parent, err)
	}
	return nil
}

func voteRecordedRows(t *testing.T, dbPath string) []voteRecordedSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, phase, agent_id, event_type, payload, timestamp, dedup_key
		  FROM audit_events
		 WHERE event_type = ?
		 ORDER BY dedup_key`, string(protocol.EventVoteRecorded))
	if err != nil {
		t.Fatalf("query VoteRecorded rows: %v", err)
	}
	defer rows.Close()

	var out []voteRecordedSnapshot
	for rows.Next() {
		var row voteRecordedSnapshot
		if err := rows.Scan(&row.ID, &row.Phase, &row.AgentID, &row.EventType, &row.Payload, &row.Timestamp, &row.DedupKey); err != nil {
			t.Fatalf("scan VoteRecorded row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate VoteRecorded rows: %v", err)
	}
	return out
}

func containsLine(out, line string) bool {
	for _, got := range splitLines(out) {
		if got == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
