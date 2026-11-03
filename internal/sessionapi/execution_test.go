package sessionapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/sessionapi"
)

// executionRow writes one normalized observation exactly as the replay
// publication writes it: constructor-owned metadata JSON plus the journal row's
// own occurrence identity and observed time.
func (f fixture) executionRow(t *testing.T, session string, journal, at int64, kind execution.Kind, model string) {
	t.Helper()
	metadata, err := execution.Normalize(execution.Inputs{
		Kind: kind,
		Host: execution.Declaration{
			Provider: execution.Raw{Present: true, Member: execution.HostProvider, Value: "anthropic"},
			Model:    execution.Raw{Present: true, Member: execution.HostModelID, Value: model},
		},
	})
	require.NoError(t, err)
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	_, err = f.db.Exec(`
		INSERT INTO pasture_session_execution
			(journal_id, harness, session_id, kind, agent_id, turn_id, request_id, observed_at, execution_json)
		VALUES (?, 'claude-code', ?, ?, '', '', '', ?, ?)`,
		journal, session, string(kind), at, encoded)
	require.NoError(t, err)
}

func TestSummarySelectsOneCoherentExecutionObservation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "exec", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 1)
	f.executionRow(t, "exec", 10, now-int64(3*time.Minute), execution.Primary, "model-a")
	f.executionRow(t, "exec", 12, now-int64(2*time.Minute), execution.Selection, "model-b")
	// A newer child must never replace the parent summary.
	f.executionRow(t, "exec", 14, now-int64(time.Minute), execution.Child, "model-child")

	detail, err := f.service().Get(context.Background(), "claude-code", "exec")
	require.NoError(t, err)
	require.NotNil(t, detail.Session.Execution)
	summary := detail.Session.Execution
	require.Equal(t, int64(12), summary.OccurrenceID, "the newest primary or selection wins as one record")
	require.Equal(t, execution.Selection, summary.Kind)
	require.Equal(t, "model-b", summary.Model.Value)
	require.Equal(t, "anthropic", summary.Provider.Value)
	require.Equal(t, execution.Observed, summary.Model.Status)
	require.NotEqual(t, "model-child", summary.Model.Value)

	page, err := f.service().ExecutionHistory(context.Background(), "claude-code", "exec",
		sessionapi.ExecutionHistoryOptions{})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.Equal(t, []int64{14, 12, 10}, []int64{
		page.Items[0].OccurrenceID, page.Items[1].OccurrenceID, page.Items[2].OccurrenceID,
	}, "history is descending by journal id and includes auxiliary records")
	require.Equal(t, execution.Child, page.Items[0].Kind)
}

func TestExecutionHistoryCursorIsSnapshotPinned(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "paged", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 1)
	for _, row := range []struct {
		journal int64
		kind    execution.Kind
		model   string
	}{
		{10, execution.Primary, "model-10"},
		{12, execution.Selection, "model-12"},
		{14, execution.Compaction, "model-14"},
	} {
		f.executionRow(t, "paged", row.journal, now-int64(time.Minute), row.kind, row.model)
	}
	service := f.service()
	ctx := context.Background()

	first, err := service.ExecutionHistory(ctx, "claude-code", "paged", sessionapi.ExecutionHistoryOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.Equal(t, int64(14), first.Items[0].OccurrenceID)
	require.Equal(t, int64(12), first.Items[1].OccurrenceID)
	require.NotEmpty(t, first.Next)

	// A live replay commits a newer observation after the cursor was issued. The
	// open cursor stays pinned to its snapshot and must not surface it.
	f.executionRow(t, "paged", 20, now, execution.Primary, "model-20")

	second, err := service.ExecutionHistory(ctx, "claude-code", "paged",
		sessionapi.ExecutionHistoryOptions{Limit: 2, Cursor: first.Next})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, int64(10), second.Items[0].OccurrenceID)
	require.Empty(t, second.Next)
	for _, item := range second.Items {
		require.NotEqual(t, int64(20), item.OccurrenceID, "a snapshot cursor must not change page boundaries")
	}

	fresh, err := service.ExecutionHistory(ctx, "claude-code", "paged", sessionapi.ExecutionHistoryOptions{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, int64(20), fresh.Items[0].OccurrenceID, "a new walk sees the newer snapshot")
	require.NotEmpty(t, fresh.Next)
}

func TestSessionActorClassificationUsesTheStoredClaim(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "generic", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 1)
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
		VALUES ('claude-code', 'generic', ?, ?)`, provadapter.DefaultLLMActorID().String(), now)
	require.NoError(t, err)
	f.state(t, "explicit", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 2)
	f.claim(t, "explicit", now)
	f.state(t, "unclaimed", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 3)

	service := f.service()
	ctx := context.Background()

	generic, err := service.Get(ctx, "claude-code", "generic")
	require.NoError(t, err)
	require.NotNil(t, generic.Session.Actor)
	require.Equal(t, sessionapi.ActorGenericLLM, generic.Session.Actor.Classification)
	require.Equal(t, provadapter.DefaultLLMActorID().String(), generic.Session.Actor.ID)

	explicit, err := service.Get(ctx, "claude-code", "explicit")
	require.NoError(t, err)
	require.NotNil(t, explicit.Session.Actor)
	require.Equal(t, sessionapi.ActorExplicit, explicit.Session.Actor.Classification)

	unclaimed, err := service.Get(ctx, "claude-code", "unclaimed")
	require.NoError(t, err)
	require.Nil(t, unclaimed.Session.Actor, "no claim must not fabricate an actor")
	require.Nil(t, unclaimed.Session.Execution, "no execution evidence must not fabricate attribution")
}

func TestExecutionHistoryRejectsBadInput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "bad", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 1)
	service := f.service()
	ctx := context.Background()

	_, err := service.ExecutionHistory(ctx, "claude-code", "bad", sessionapi.ExecutionHistoryOptions{Limit: 101})
	require.True(t, errors.Is(err, sessionapi.ErrInvalidFilter), "%v", err)
	_, err = service.ExecutionHistory(ctx, "claude-code", "bad", sessionapi.ExecutionHistoryOptions{Cursor: "not-base64"})
	require.True(t, errors.Is(err, sessionapi.ErrInvalidPageToken), "%v", err)
	_, err = service.ExecutionHistory(ctx, "", "bad", sessionapi.ExecutionHistoryOptions{})
	require.True(t, errors.Is(err, sessionapi.ErrNotFound), "%v", err)

	page, err := service.ExecutionHistory(ctx, "claude-code", "bad", sessionapi.ExecutionHistoryOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Equal(t, page.Revision, int64(7))
}

func TestExecutionHistoryRejectsCorruptCacheRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "corrupt", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 1)
	_, err := f.db.Exec(`
		INSERT INTO pasture_session_execution
			(journal_id, harness, session_id, kind, agent_id, turn_id, request_id, observed_at, execution_json)
		VALUES (5, 'claude-code', 'corrupt', 'primary', '', '', '', ?, ?)`,
		now, []byte(`{"kind":"primary","provider":{"status":"observed","source":"host"},"model":{"status":"unavailable","source":"none"},"variant":{"status":"unavailable","source":"none"},"role":{"status":"unavailable","source":"none"},"protocolRole":{"status":"unavailable","source":"none"}}`))
	require.NoError(t, err)

	_, err = f.service().ExecutionHistory(context.Background(), "claude-code", "corrupt", sessionapi.ExecutionHistoryOptions{})
	require.Error(t, err, "a partially observed execution value must not be exposed")

	// The summary path is equally strict.
	_, err = f.service().Get(context.Background(), "claude-code", "corrupt")
	require.Error(t, err)
}

func TestExecutionHistoryRoute(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	f.state(t, "route", now-int64(time.Minute), now-int64(time.Minute), "PreToolUse", 1)
	f.executionRow(t, "route", 10, now-int64(2*time.Minute), execution.Primary, "model-10")
	f.executionRow(t, "route", 12, now-int64(time.Minute), execution.Selection, "model-12")
	server := httptest.NewServer(sessionapi.NewHandler(f.service()))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/sessions/claude-code/route/execution?limit=1")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var page sessionapi.ExecutionHistoryPage
	require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	require.Equal(t, int64(12), page.Items[0].OccurrenceID)
	require.NotEmpty(t, page.Next)

	// The summary on the session route marshals the embedded metadata at the
	// top level, including status and source.
	detailResponse, err := http.Get(server.URL + "/api/v1/sessions/claude-code/route")
	require.NoError(t, err)
	defer detailResponse.Body.Close()
	require.Equal(t, http.StatusOK, detailResponse.StatusCode)
	var raw map[string]any
	require.NoError(t, json.NewDecoder(detailResponse.Body).Decode(&raw))
	session := raw["session"].(map[string]any)
	executionPayload := session["execution"].(map[string]any)
	require.Equal(t, "selection", executionPayload["kind"])
	require.Equal(t, "model-12", executionPayload["model"].(map[string]any)["value"])
	require.Equal(t, "observed", executionPayload["model"].(map[string]any)["status"])

	badLimit, err := http.Get(server.URL + "/api/v1/sessions/claude-code/route/execution?limit=0")
	require.NoError(t, err)
	badLimit.Body.Close()
	require.Equal(t, http.StatusBadRequest, badLimit.StatusCode)

	badCursor, err := http.Get(server.URL + "/api/v1/sessions/claude-code/route/execution?cursor=nope")
	require.NoError(t, err)
	badCursor.Body.Close()
	require.Equal(t, http.StatusBadRequest, badCursor.StatusCode)
}
