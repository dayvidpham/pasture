package handlers_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/types"
)

type sessionBindingSeed struct {
	bindingID  string
	harness    string
	session    string
	epoch      string
	role       string
	boundAt    int64
	unboundAt  *int64
	unboundWhy *string
}

type sessionClaimSeed struct {
	harness   string
	session   string
	actor     string
	claimedAt int64
}

// seedSessionReadStore builds one store through the production opener, then
// writes the bindings and claims the session cache is derived from. The
// handler's own EnsureSessionState rebuild is what turns them into cache rows,
// so the tests exercise the production read path rather than a hand-built cache.
func seedSessionReadStore(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UnixNano()
	minute := int64(time.Minute)
	endedAt := now - 5*minute
	endedReason := "session_end"
	epochOne := "file://epoch/one"
	epochTwo := "file://epoch/two"

	bindings := []sessionBindingSeed{
		{bindingID: "binding-active", harness: "claude-code", session: "active-session", epoch: epochOne, role: "implementer", boundAt: now - minute},
		{bindingID: "binding-idle", harness: "claude-code", session: "idle-session", epoch: epochTwo, role: "reviewer", boundAt: now - 20*minute},
		{bindingID: "binding-ended", harness: "opencode", session: "ended-session", epoch: epochTwo, role: "worker", boundAt: now - 30*minute, unboundAt: &endedAt, unboundWhy: &endedReason},
	}
	for _, b := range bindings {
		_, err := db.ExecContext(t.Context(), `
			INSERT INTO pasture_session_epoch_binding
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			b.bindingID, b.harness, b.session, b.epoch, b.role, b.boundAt, b.unboundAt, b.unboundWhy)
		require.NoError(t, err)
	}

	claims := []sessionClaimSeed{
		{harness: "codex", session: "unknown-session", actor: "file://actor/session-read", claimedAt: now - 2*minute},
	}
	for _, c := range claims {
		_, err := db.ExecContext(t.Context(), `
			INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
			VALUES (?, ?, ?, ?)`, c.harness, c.session, c.actor, c.claimedAt)
		require.NoError(t, err)
	}

	return path
}

func decodeSessionPage(t *testing.T, raw string) sessionapi.Page {
	t.Helper()
	var page sessionapi.Page
	require.NoError(t, json.Unmarshal([]byte(raw), &page), "body: %s", raw)
	return page
}

func sessionStates(page sessionapi.Page) map[string]sessionapi.ActivityState {
	states := make(map[string]sessionapi.ActivityState, len(page.Items))
	for _, item := range page.Items {
		states[item.NativeSessionID] = item.Activity.State
	}
	return states
}

func TestSessionListTextAndJSONShareTheSameRows(t *testing.T) {
	t.Parallel()

	dbPath := seedSessionReadStore(t)

	var text bytes.Buffer
	code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Output: &text}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	for _, want := range []string{
		"STATE", "HARNESS", "SESSION", "EPOCH", "LAST EVENT", "LAST SEEN",
		"active-session", "idle-session", "ended-session", "unknown-session",
		"active", "idle", "ended", "unknown",
		"file://epoch/one", "file://epoch/two",
	} {
		require.Contains(t, text.String(), want)
	}

	var jsonOut bytes.Buffer
	code, err = handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Output: &jsonOut}, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	page := decodeSessionPage(t, jsonOut.String())
	require.Len(t, page.Items, 4)
	require.Equal(t, map[string]sessionapi.ActivityState{
		"active-session":  sessionapi.StateActive,
		"idle-session":    sessionapi.StateIdle,
		"ended-session":   sessionapi.StateEnded,
		"unknown-session": sessionapi.StateUnknown,
	}, sessionStates(page))

	// Both formats must show the same identities.
	for session := range sessionStates(page) {
		require.Contains(t, text.String(), session)
	}
}

func TestSessionListFilters(t *testing.T) {
	t.Parallel()

	dbPath := seedSessionReadStore(t)
	list := func(in handlers.SessionListInput) sessionapi.Page {
		t.Helper()
		var out bytes.Buffer
		in.DBPath = dbPath
		in.Output = &out
		code, err := handlers.SessionList(t.Context(), in, types.OutputJSON)
		require.NoError(t, err)
		require.Equal(t, 0, code)
		return decodeSessionPage(t, out.String())
	}

	byHarness := list(handlers.SessionListInput{Harness: "opencode"})
	require.Len(t, byHarness.Items, 1)
	require.Equal(t, "ended-session", byHarness.Items[0].NativeSessionID)

	byState := list(handlers.SessionListInput{State: string(sessionapi.StateUnknown)})
	require.Len(t, byState.Items, 1)
	require.Equal(t, "unknown-session", byState.Items[0].NativeSessionID)

	byEpoch := list(handlers.SessionListInput{Epoch: "file://epoch/one"})
	require.Len(t, byEpoch.Items, 1)
	require.Equal(t, "active-session", byEpoch.Items[0].NativeSessionID)

	firstPage := list(handlers.SessionListInput{Limit: 1})
	require.Len(t, firstPage.Items, 1)
	require.NotEmpty(t, firstPage.Next)
	secondPage := list(handlers.SessionListInput{Limit: 1, Cursor: firstPage.Next})
	require.Len(t, secondPage.Items, 1)
	require.NotEqual(t, firstPage.Items[0].NativeSessionID, secondPage.Items[0].NativeSessionID)
}

func TestSessionListRejectsInvalidFilterBeforeOpeningTheStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   handlers.SessionListInput
	}{
		{name: "unknown state", in: handlers.SessionListInput{State: "bogus"}},
		{name: "limit too large", in: handlers.SessionListInput{Limit: 101}},
		{name: "negative limit", in: handlers.SessionListInput{Limit: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dbPath := filepath.Join(t.TempDir(), "never-created.db")
			tc.in.DBPath = dbPath
			code, err := handlers.SessionList(t.Context(), tc.in, types.OutputText)
			require.Error(t, err)
			require.Equal(t, 1, code)
			require.Equal(t, pasterrors.ExitCode(err), code)
			_, statErr := os.Stat(dbPath)
			require.True(t, os.IsNotExist(statErr), "invalid input must not create the store: %v", statErr)
		})
	}
}

func TestSessionShowTextAndJSON(t *testing.T) {
	t.Parallel()

	dbPath := seedSessionReadStore(t)

	var text bytes.Buffer
	code, err := handlers.SessionShow(t.Context(), handlers.SessionShowInput{
		DBPath: dbPath, Harness: "claude-code", Session: "active-session", Output: &text,
	}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	for _, want := range []string{
		"Harness:    claude-code",
		"Session:    active-session",
		"State:      active",
		"Epoch:      file://epoch/one",
		"Role:       implementer",
		"Last event: -",
	} {
		require.Contains(t, text.String(), want)
	}

	var jsonOut bytes.Buffer
	code, err = handlers.SessionShow(t.Context(), handlers.SessionShowInput{
		DBPath: dbPath, Harness: "claude-code", Session: "active-session", Output: &jsonOut,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	var detail sessionapi.Detail
	require.NoError(t, json.Unmarshal([]byte(jsonOut.String()), &detail))
	require.Equal(t, "active-session", detail.Session.NativeSessionID)
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
	require.NotNil(t, detail.Session.Epoch)
	require.Equal(t, "file://epoch/one", detail.Session.Epoch.ID)
}

func TestSessionShowMissingSessionExitsNonZero(t *testing.T) {
	t.Parallel()

	dbPath := seedSessionReadStore(t)
	var out bytes.Buffer
	code, err := handlers.SessionShow(t.Context(), handlers.SessionShowInput{
		DBPath: dbPath, Harness: "claude-code", Session: "missing-session", Output: &out,
	}, types.OutputText)
	require.Error(t, err)
	require.NotEqual(t, 0, code)
	require.Equal(t, 3, code, "a missing session is a workflow (not-found) error")
	require.Equal(t, pasterrors.ExitCode(err), code)
	require.Contains(t, err.Error(), "No recorded session")
	require.Empty(t, out.String(), "a failed show must not print a partial detail view")
}

func TestSessionShowRequiresBothArguments(t *testing.T) {
	t.Parallel()

	code, err := handlers.SessionShow(t.Context(), handlers.SessionShowInput{Output: &bytes.Buffer{}}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 1, code)
	require.Equal(t, pasterrors.ExitCode(err), code)
}
