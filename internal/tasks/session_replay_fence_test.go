package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type replayClock struct{}

func (replayClock) Now() time.Time { return time.Unix(100, 0).UTC() }

type replayAtClock int64

func (at replayAtClock) Now() time.Time { return time.Unix(int64(at), 0).UTC() }

func replayStore(t *testing.T) (protocol.TaskTracker, lifecycleReceiptStore, *bytes.Buffer) {
	t.Helper()
	diagnostics := new(bytes.Buffer)
	tracker, err := OpenTaskTrackerWithOptions(filepath.Join(t.TempDir(), "replay.db"), WithMaxOpenConns(1), WithTimeoutProfile(timeouts.TestProfile()), WithDiagnosticSink(diagnostics))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	_, err = tracker.Create("file://replay-fence", "bootstrap", "initialize real journal", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	return tracker, tracker.(lifecycleReceiptStore), diagnostics
}

// Constructed legacy evidence committed through the real production journal,
// not an authentic host capture and not a second occurrence writer/codec.
func appendReplayFact(t *testing.T, tracker protocol.TaskTracker, op, event, source string, at int64) (provenance.JournalID, digest.Digest) {
	t.Helper()
	manifest := registration.ClaudeCode__2_1_261()
	var kind model.ContractEventKind
	for _, candidate := range manifest.Events {
		if candidate.NativeName == event {
			kind = candidate.Kind
		}
	}
	require.NotZero(t, kind)
	raw := []byte(fmt.Sprintf(`{"session_id":"replay-session","hook_event_name":%q,"source":%q}`, event, source))
	ref := digest.FromBytes(raw)
	blobs, err := NewLifecycleBlobStore(tracker)
	require.NoError(t, err)
	require.NoError(t, blobs.Put(t.Context(), ref, raw))
	payload, err := json.Marshal(map[string]any{"contract": manifest.Contract.String(), "event": kind, "capture": model.CaptureValid, "bindings": []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "replay-session"}}, "body_digest": ref.String(), "envelope": model.OccurrenceEnvelopeRef{Runtime: model.RuntimeContractDefinitionRef{Contract: manifest.Contract}}})
	require.NoError(t, err)
	identity, err := tracker.(lifecycleReceiptStore).ResolveLifecycleIdentity(t.Context())
	require.NoError(t, err)
	content := sha256.Sum256(payload)
	command := sha256.Sum256(append([]byte(op), payload...))
	result, err := tracker.Journal().Apply(provenance.OperationInput{OperationID: provenance.OperationID(op), ActorID: identity.Actor, AuthorityJournalID: &identity.Authority, CommandDigest: command[:], RecordedAt: time.Unix(at, 0).UnixNano(), Effects: []provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: "occurrence", EvidenceKind: "pasture.lifecycle.occurrence.v1", ContentDigest: content[:], Payload: payload}}})
	require.NoError(t, err)
	for _, slot := range result.ResultSlots {
		if slot.Slot == "occurrence" {
			return slot.ProducedJournalID, ref
		}
	}
	t.Fatal("journal returned no occurrence slot")
	return 0, ref
}

func TestSessionReplayFindsCommittedTailPendingFactsAfterCertifiedCursor(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	first, _ := appendReplayFact(t, tracker, "replay.first", "UserPromptSubmit", "", 100)
	initial, err := RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.GreaterOrEqual(t, initial.CompletedThroughJournalID, int64(first))
	second, _ := appendReplayFact(t, tracker, "replay.pending-tail", "UserPromptSubmit", "", 200)
	out, rebuilt, err := EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.True(t, rebuilt, "a nonzero old cursor must not become the current-query snapshot")
	require.GreaterOrEqual(t, out.CompletedThroughJournalID, int64(second))
	var prompt int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT last_prompt_journal_id FROM pasture_session_state WHERE session='replay-session'`).Scan(&prompt))
	require.Equal(t, int64(second), prompt)
}

func TestSessionReplayRejectsFactsCommittedAfterScanOnTheTransactionView(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "replay.before", "SessionStart", "startup", 100)
	var newest provenance.JournalID
	scans := 0
	out, _, err := refreshSessionState(t.Context(), tracker, true, false, func(stage sessionReplayStage) {
		if stage != sessionReplayScanned {
			return
		}
		scans++
		if scans == 1 {
			newest, _ = appendReplayFact(t, tracker, "replay.after-W", "UserPromptSubmit", "", 200)
		}
	})
	require.NoError(t, err)
	require.Equal(t, 2, scans, "the current post-W transaction fence must retry outside its connection")
	require.GreaterOrEqual(t, out.CompletedThroughJournalID, int64(newest))
	var prompt int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT last_prompt_journal_id FROM pasture_session_state WHERE session='replay-session'`).Scan(&prompt))
	require.Equal(t, int64(newest), prompt)
}

func TestSessionReplayDirtyAndBindingMutationsFenceTheScannedGeneration(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "replay.generation", "UserPromptSubmit", "", 100)
	epoch, err := tracker.Create("file://replay-fence", "epoch", "bind while scanned", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	api := NewSessionAPIStoreWithDB(store.auditDBHandle(), timeouts.TestProfile(), replayClock{})
	scans := 0
	out, _, err := refreshSessionState(t.Context(), tracker, true, false, func(stage sessionReplayStage) {
		if stage != sessionReplayScanned {
			return
		}
		scans++
		if scans == 1 {
			require.NoError(t, MarkSessionStateDirty(t.Context(), tracker))
			_, err := api.Bind(t.Context(), "claude-code", "binding-during-replay", SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
			require.NoError(t, err)
		}
	})
	require.NoError(t, err)
	require.Equal(t, 2, scans)
	require.Equal(t, int64(3), out.Revision)
	var count int
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_state WHERE session='binding-during-replay'`).Scan(&count))
	require.Equal(t, 1, count, "publication folds transaction-current bindings")
}

func TestSessionReplayExhaustionPreservesThePreviousCertifiedCache(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	first, _ := appendReplayFact(t, tracker, "replay.old", "UserPromptSubmit", "", 100)
	old, err := RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	appendReplayFact(t, tracker, "replay.new", "UserPromptSubmit", "", 200)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, _, err = refreshSessionState(ctx, tracker, false, true, func(stage sessionReplayStage) {
		if stage == sessionReplayScanned {
			cancel()
		}
	})
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "stale")
	require.ErrorIs(t, err, context.Canceled)
	status, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, old.Revision, status.Revision)
	require.Equal(t, old.CompletedThroughJournalID, status.CompletedThroughJournalID)
	var prompt int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT last_prompt_journal_id FROM pasture_session_state`).Scan(&prompt))
	require.Equal(t, int64(first), prompt)
}

func TestSessionReplayResumeParityAndMissingBodyNeverInventsResume(t *testing.T) {
	t.Parallel()
	tracker, store, diagnostics := replayStore(t)
	prompt, _ := appendReplayFact(t, tracker, "replay.prompt", "UserPromptSubmit", "", 100)
	end, _ := appendReplayFact(t, tracker, "replay.end", "SessionEnd", "", 200)
	appendReplayFact(t, tracker, "replay.background-startup", "SessionStart", "startup", 250)
	_, err := RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	var unsuperseded bool
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT host_end_journal_id>last_resume_journal_id FROM pasture_session_state`).Scan(&unsuperseded))
	require.True(t, unsuperseded, "a never-bound host end survives later non-resume activity in full replay")
	resume, body := appendReplayFact(t, tracker, "replay.resume", "SessionStart", "resume", 300)
	_, rebuilt, err := EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.True(t, rebuilt)
	read := func() [6]int64 {
		var row [6]int64
		require.NoError(t, store.auditDBHandle().QueryRow(`SELECT last_prompt_journal_id,last_prompt_at,host_end_journal_id,host_end_at,last_resume_journal_id,last_resume_at FROM pasture_session_state`).Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5]))
		return row
	}
	before := read()
	require.Equal(t, [6]int64{int64(prompt), int64(100 * time.Second), int64(end), int64(200 * time.Second), int64(resume), int64(300 * time.Second)}, before)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, before, read(), "full and suffix replay retain the same independent facts")
	_, err = store.auditDBHandle().Exec(`DELETE FROM lifecycle_payload_blobs WHERE digest=?`, body.String())
	require.NoError(t, err)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.Zero(t, read()[4], "missing legacy body cannot prove source=resume")
	require.Contains(t, diagnostics.String(), "resume")
	require.Contains(t, diagnostics.String(), "restore")
}

func TestSessionReplayDefersToANewerConcurrentGeneration(t *testing.T) {
	t.Parallel()
	tracker, _, _ := replayStore(t)
	appendReplayFact(t, tracker, "replay.concurrent", "UserPromptSubmit", "", 100)
	scans := 0
	var peer SessionStateRebuildOutcome
	out, _, err := refreshSessionState(t.Context(), tracker, true, false, func(stage sessionReplayStage) {
		if stage != sessionReplayScanned {
			return
		}
		scans++
		if scans == 1 {
			var err error
			peer, err = RebuildSessionState(t.Context(), tracker)
			require.NoError(t, err)
		}
	})
	// A peer published a generation at or beyond this scan's pinned snapshot, so
	// its work subsumes ours: this replay reports that generation and never
	// overwrites it with an older scan.
	require.NoError(t, err)
	require.Equal(t, 1, scans, "a subsumed scan is not repeated")
	require.Equal(t, peer.Revision, out.Revision)
	require.Equal(t, peer.CompletedThroughJournalID, out.CompletedThroughJournalID)
	_, rebuilt, err := EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.False(t, rebuilt, "a certified suffix with no new facts is inert")
}

func TestSessionReplayPublishesACoherentSnapshotUnderSustainedWrites(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "replay.seed", "UserPromptSubmit", "", 100)
	scans := 0
	out, changed, err := refreshSessionState(t.Context(), tracker, true, false, func(stage sessionReplayStage) {
		if stage != sessionReplayScanned {
			return
		}
		scans++
		appendReplayFact(t, tracker, fmt.Sprintf("replay.interference.%d", scans), "UserPromptSubmit", "", int64(100+scans))
	})
	// A writer appending during the scan no longer invalidates it: the replay
	// pins one snapshot, publishes a coherent cache at that watermark, and
	// leaves the newer rows to the next suffix refresh.
	require.NoError(t, err)
	require.True(t, changed)
	require.GreaterOrEqual(t, scans, 1)
	require.LessOrEqual(t, scans, sessionReplayMaxAttempts)
	var revision, cursor int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT revision,completed_through_jid FROM pasture_session_api_state`).Scan(&revision, &cursor))
	require.NotZero(t, revision)
	require.NotZero(t, cursor, "a pinned snapshot is certified even while writers append")
	require.Equal(t, out.CompletedThroughJournalID, cursor)
	var count int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_state`).Scan(&count))
	require.NotZero(t, count, "the published cache holds the scanned snapshot, never a partial one")
}

func TestSessionReplayCorruptLegacyBodyPreservesTheOldCache(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "replay.corrupt-start", "SessionStart", "startup", 100)
	old, err := RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	_, ref := appendReplayFact(t, tracker, "replay.corrupt-resume", "SessionStart", "resume", 200)
	_, err = store.auditDBHandle().Exec(`UPDATE lifecycle_payload_blobs SET body=?,byte_count=? WHERE digest=?`, []byte("invalid"), len("invalid"), ref.String())
	require.NoError(t, err)
	_, _, err = EnsureSessionState(t.Context(), tracker)
	require.ErrorContains(t, err, "authentication")
	status, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, old.Revision, status.Revision)
	require.Equal(t, old.CompletedThroughJournalID, status.CompletedThroughJournalID)
}

func TestSessionReplayScanBarrierJoinsTheWriterBeforeStoreCleanup(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "replay.barrier.before", "UserPromptSubmit", "", 100)
	ctx, cancel := context.WithCancel(t.Context())
	scanned, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	type result struct {
		out SessionStateRebuildOutcome
		err error
	}
	done := make(chan result, 1)
	joined := false
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		if !joined {
			<-done
		}
	})
	go func() {
		first := true
		out, _, err := refreshSessionState(ctx, tracker, true, false, func(stage sessionReplayStage) {
			if stage == sessionReplayScanned && first {
				first = false
				close(scanned)
				<-release
			}
		})
		done <- result{out, err}
	}()
	ceiling := time.NewTimer(timeouts.TestProfile().WorkflowResult())
	defer ceiling.Stop()
	select {
	case <-scanned:
	case <-ceiling.C:
		t.Fatal("replay did not reach its scan barrier")
	}
	// A real second goroutine commits a receipt while the replay's first W is
	// held. No tail is run; the journal fence, not a revision change, must see it.
	newest, _ := appendReplayFact(t, tracker, "replay.barrier.after", "UserPromptSubmit", "", 200)
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-done:
		joined = true
		require.NoError(t, got.err)
		require.GreaterOrEqual(t, got.out.CompletedThroughJournalID, int64(newest))
	case <-ceiling.C:
		t.Fatal("replay failed to join after releasing its scan barrier")
	}
	var prompt int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT last_prompt_journal_id FROM pasture_session_state`).Scan(&prompt))
	require.Equal(t, int64(newest), prompt)
}

func TestSessionReplayPublicationFailureRollsBackRowsAndDirtyCertification(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	first, _ := appendReplayFact(t, tracker, "replay.rollback.old", "UserPromptSubmit", "", 100)
	_, err := RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	appendReplayFact(t, tracker, "replay.rollback.new", "UserPromptSubmit", "", 200)
	require.NoError(t, MarkSessionStateDirty(t.Context(), tracker))
	before, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	_, err = store.auditDBHandle().Exec(`CREATE TRIGGER refuse_session_certification BEFORE UPDATE OF completed_through_jid ON pasture_session_api_state BEGIN SELECT RAISE(ABORT,'constructed publication refusal'); END`)
	require.NoError(t, err)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.ErrorContains(t, err, "constructed publication refusal")
	after, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, before, after, "the failed certificate cannot clear dirty or advance revision/cursor")
	var prompt int64
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT last_prompt_journal_id FROM pasture_session_state`).Scan(&prompt))
	require.Equal(t, int64(first), prompt, "replacement rows roll back with the certificate")
}

func TestSessionReplayPageCancellationDoesNotNeedTheBorrowedConnection(t *testing.T) {
	t.Parallel()
	_, store, _ := replayStore(t)
	lease, err := store.auditDBHandle().Conn(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = lease.Close()
		if !joined {
			<-done
		}
	})
	go func() {
		close(entered)
		_, err := readSessionOccurrencePage(ctx, store.auditDBHandle(), provenance.FactPageRequest{Limit: 1})
		done <- err
	}()
	<-entered
	cancel()
	ceiling := time.NewTimer(timeouts.TestProfile().WorkflowResult())
	defer ceiling.Stop()
	select {
	case err := <-done:
		joined = true
		require.True(t, errors.Is(err, context.Canceled), "page acquisition must retain caller cancellation: %v", err)
	case <-ceiling.C:
		t.Fatal("cancelled page acquisition waited for the held connection")
	}
}

func TestSessionReplayPreservesClosedManualAndLaterBindingIntervals(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	epoch, err := tracker.Create("file://replay-history", "epoch", "binding intervals", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	bind := func(at int64) SessionBindingResult {
		api := NewSessionAPIStoreWithDB(store.auditDBHandle(), timeouts.TestProfile(), replayAtClock(at))
		out, err := api.Bind(t.Context(), "claude-code", "replay-session", SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
		require.NoError(t, err)
		return out
	}
	openCount := func(want int) {
		var count int
		require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_epoch_binding WHERE unbound_at IS NULL`).Scan(&count))
		require.Equal(t, want, count)
	}
	first := bind(50)
	appendReplayFact(t, tracker, "history.end.first", "SessionEnd", "", 100)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	openCount(0)
	appendReplayFact(t, tracker, "history.resume.first", "SessionStart", "resume", 200)
	_, _, err = EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	openCount(0)
	second := bind(300)
	appendReplayFact(t, tracker, "history.end.before-binding", "SessionEnd", "", 250)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	openCount(1)
	appendReplayFact(t, tracker, "history.resume.second", "SessionStart", "resume", 400)
	api := NewSessionAPIStoreWithDB(store.auditDBHandle(), timeouts.TestProfile(), replayAtClock(500))
	_, err = api.Unbind(t.Context(), "claude-code", "replay-session", second.BindingID)
	require.NoError(t, err)
	appendReplayFact(t, tracker, "history.resume.after-manual-unbind", "SessionStart", "resume", 600)
	_, _, err = EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	openCount(0)
	var reason string
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id=?`, first.BindingID).Scan(&reason))
	require.Equal(t, "session_end", reason)
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id=?`, second.BindingID).Scan(&reason))
	require.Equal(t, "unbind", reason)
	bind(700)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	openCount(1)
}

func TestSessionReplayUnbindAfterScanCannotBeOverwrittenByPublication(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	epoch, err := tracker.Create("file://replay-unbind", "epoch", "manual unbind race", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	api := NewSessionAPIStoreWithDB(store.auditDBHandle(), timeouts.TestProfile(), replayAtClock(50))
	bound, err := api.Bind(t.Context(), "claude-code", "replay-session", SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
	require.NoError(t, err)
	appendReplayFact(t, tracker, "replay.unbind-race", "UserPromptSubmit", "", 100)
	api = NewSessionAPIStoreWithDB(store.auditDBHandle(), timeouts.TestProfile(), replayAtClock(200))
	var unbound SessionBindingResult
	scans := 0
	out, _, err := refreshSessionState(t.Context(), tracker, false, true, func(stage sessionReplayStage) {
		if stage != sessionReplayScanned {
			return
		}
		scans++
		if scans == 1 {
			var err error
			unbound, err = api.Unbind(t.Context(), "claude-code", "replay-session", bound.BindingID)
			require.NoError(t, err)
		}
	})
	require.NoError(t, err)
	require.Equal(t, 2, scans)
	require.Equal(t, unbound.Revision+1, out.Revision)
	var open int
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_epoch_binding WHERE unbound_at IS NULL`).Scan(&open))
	require.Zero(t, open)
	var reason string
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id=?`, bound.BindingID).Scan(&reason))
	require.Equal(t, "unbind", reason)
}
