package handlers_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/sessionapi/sessionapitest"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/types"
)

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

	dbPath := sessionapitest.SeedStore(t)

	var text bytes.Buffer
	code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &text}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	for _, want := range []string{
		"STATE", "HARNESS", "SESSION", "EPOCH", "LAST EVENT", "LAST SEEN",
		sessionapitest.ActiveSession, sessionapitest.IdleSession, sessionapitest.EndedSession, sessionapitest.UnknownSession,
		"active", "idle", "ended", "unknown",
		sessionapitest.EpochOne, sessionapitest.EpochTwo,
	} {
		require.Contains(t, text.String(), want)
	}

	var jsonOut bytes.Buffer
	code, err = handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &jsonOut}, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	page := decodeSessionPage(t, jsonOut.String())
	require.Len(t, page.Items, 4)
	require.Equal(t, map[string]sessionapi.ActivityState{
		sessionapitest.ActiveSession:  sessionapi.StateActive,
		sessionapitest.IdleSession:    sessionapi.StateIdle,
		sessionapitest.EndedSession:   sessionapi.StateEnded,
		sessionapitest.UnknownSession: sessionapi.StateUnknown,
	}, sessionStates(page))

	// Both formats must show the same identities.
	for session := range sessionStates(page) {
		require.Contains(t, text.String(), session)
	}
}

func TestSessionListFilters(t *testing.T) {
	t.Parallel()

	dbPath := sessionapitest.SeedStore(t)
	list := func(in handlers.SessionListInput) sessionapi.Page {
		t.Helper()
		var out bytes.Buffer
		in.DBPath = dbPath
		if in.Limit == 0 {
			in.Limit = 50
		}
		in.Output = &out
		code, err := handlers.SessionList(t.Context(), in, types.OutputJSON)
		require.NoError(t, err)
		require.Equal(t, 0, code)
		return decodeSessionPage(t, out.String())
	}

	byHarness := list(handlers.SessionListInput{Harness: sessionapitest.HarnessOpenCode})
	require.Len(t, byHarness.Items, 1)
	require.Equal(t, sessionapitest.EndedSession, byHarness.Items[0].NativeSessionID)

	byState := list(handlers.SessionListInput{State: string(sessionapi.StateUnknown)})
	require.Len(t, byState.Items, 1)
	require.Equal(t, sessionapitest.UnknownSession, byState.Items[0].NativeSessionID)

	byEpoch := list(handlers.SessionListInput{Epoch: sessionapitest.EpochOne})
	require.Len(t, byEpoch.Items, 1)
	require.Equal(t, sessionapitest.ActiveSession, byEpoch.Items[0].NativeSessionID)

	firstPage := list(handlers.SessionListInput{Limit: 1})
	require.Len(t, firstPage.Items, 1)
	require.NotEmpty(t, firstPage.Next)
	secondPage := list(handlers.SessionListInput{Limit: 1, Cursor: firstPage.Next})
	require.Len(t, secondPage.Items, 1)
	require.NotEqual(t, firstPage.Items[0].NativeSessionID, secondPage.Items[0].NativeSessionID)
}

func TestSessionListRejectsInvalidInputBeforeOpeningTheStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   handlers.SessionListInput
	}{
		{name: "unknown state", in: handlers.SessionListInput{State: "bogus", Limit: 50}},
		{name: "zero limit", in: handlers.SessionListInput{Limit: 0}},
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

// A concurrent writer can mark the cache dirty after the command's refresh and
// before the projection read. A trigger that re-marks the marker on every update
// reproduces that window deterministically: the refresh still succeeds, and the
// read observes dirty=1.
func TestSessionListReportsAStaleCacheAsStorageExit(t *testing.T) {
	t.Parallel()

	dbPath := sessionapitest.SeedStore(t)
	db, err := dbconn.OpenSharedDBWithProfile(dbPath, timeouts.TestProfile())
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `
		CREATE TRIGGER keep_session_cache_dirty
		AFTER UPDATE ON pasture_session_api_state
		WHEN NEW.singleton_id = 0
		BEGIN
			UPDATE pasture_session_api_state SET dirty = 1 WHERE singleton_id = 0;
		END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	var out bytes.Buffer
	code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{
		DBPath: dbPath, Limit: 50, Output: &out,
	}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 5, code, "a stale cache is a storage error")
	require.Equal(t, pasterrors.ExitCode(err), code)
	require.Contains(t, err.Error(), "marked stale")
	require.Empty(t, out.String())
}

func TestSessionListRejectsInvalidCursor(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{
		DBPath: sessionapitest.SeedStore(t),
		Limit:  50,
		Cursor: "not-a-page-cursor",
		Output: &out,
	}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 1, code, "a malformed cursor is a validation error")
	require.Equal(t, pasterrors.ExitCode(err), code)
	require.Contains(t, err.Error(), "cursor")
	require.Empty(t, out.String())
}

func TestSessionReadsRefuseToCreateAMissingStore(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "missing", "pasture.db")
	_, statErr := os.Stat(dbPath)
	require.True(t, os.IsNotExist(statErr))

	for _, run := range []struct {
		name string
		call func() (int, error)
	}{
		{
			name: "list",
			call: func() (int, error) {
				return handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &bytes.Buffer{}}, types.OutputText)
			},
		},
		{
			name: "show",
			call: func() (int, error) {
				return handlers.SessionShow(t.Context(), handlers.SessionShowInput{DBPath: dbPath, Harness: "claude-code", Session: "any", Output: &bytes.Buffer{}}, types.OutputText)
			},
		},
	} {
		t.Run(run.name, func(t *testing.T) {
			code, err := run.call()
			require.Error(t, err)
			require.Equal(t, 2, code, "an absent store is a connection error")
			require.Equal(t, pasterrors.ExitCode(err), code)
			require.Contains(t, err.Error(), "No pasture database exists")
			var structured *pasterrors.StructuredError
			require.ErrorAs(t, err, &structured)
			require.Contains(t, structured.Fix, "pasture task")
			_, statErr := os.Stat(dbPath)
			require.True(t, os.IsNotExist(statErr), "a read must not create %q: %v", dbPath, statErr)
		})
	}
}

func TestSessionReadsRejectUnopenableStorePaths(t *testing.T) {
	t.Parallel()

	t.Run("path is a directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dir, Limit: 50, Output: &bytes.Buffer{}}, types.OutputText)
		require.Error(t, err)
		require.Equal(t, 2, code)
		require.Contains(t, err.Error(), "is a directory")
	})

	t.Run("parent is a regular file", func(t *testing.T) {
		t.Parallel()
		parent := filepath.Join(t.TempDir(), "not-a-directory")
		require.NoError(t, os.WriteFile(parent, []byte("x"), 0o600))
		dbPath := filepath.Join(parent, "pasture.db")
		code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &bytes.Buffer{}}, types.OutputText)
		require.Error(t, err)
		require.Equal(t, 2, code)
		require.Contains(t, err.Error(), "Couldn't inspect the pasture database path")
	})

	t.Run("file is unreadable", func(t *testing.T) {
		t.Parallel()
		if os.Geteuid() == 0 {
			t.Skip("root ignores file permission bits")
		}
		dbPath := filepath.Join(t.TempDir(), "pasture.db")
		require.NoError(t, os.WriteFile(dbPath, []byte("not a database"), 0o600))
		require.NoError(t, os.Chmod(dbPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(dbPath, 0o600) })

		code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &bytes.Buffer{}}, types.OutputText)
		require.Error(t, err)
		require.Equal(t, 2, code)
		require.Contains(t, err.Error(), "not readable")
	})
}

func TestSessionReadsOnAnUnwritableStoreExitConnection(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root ignores file permission bits")
	}
	dbPath := sessionapitest.SeedStore(t)
	require.NoError(t, os.Chmod(dbPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o600) })

	code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &bytes.Buffer{}}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 2, code, "the tracker's structured store-open failure is a connection error")
	require.Equal(t, pasterrors.ExitCode(err), code)
	require.Contains(t, err.Error(), "Couldn't open")
}

// The cache refresh is a write. A trigger that aborts its DELETE makes the
// failure deterministic after the cache has been populated once, so this pins
// sessionCacheError's exact category and message rather than accepting either
// of two exit codes.
func TestSessionListReportsACacheRefreshFailureAsStorageExit(t *testing.T) {
	t.Parallel()

	dbPath := sessionapitest.SeedStore(t)

	// Warm the cache: the rebuild's DELETE only fires a row trigger when the
	// disposable table already holds rows.
	var warm bytes.Buffer
	code, err := handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &warm}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	db, err := dbconn.OpenSharedDBWithProfile(dbPath, timeouts.TestProfile())
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `
		CREATE TRIGGER deny_session_rebuild
		BEFORE DELETE ON pasture_session_state
		BEGIN
			SELECT RAISE(ABORT, 'denied');
		END`)
	require.NoError(t, err)
	// Mark the cache dirty so the next command rebuilds it and meets the trigger.
	_, err = db.ExecContext(t.Context(), `
		UPDATE pasture_session_api_state SET dirty = 1 WHERE singleton_id = 0`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	var out bytes.Buffer
	code, err = handlers.SessionList(t.Context(), handlers.SessionListInput{DBPath: dbPath, Limit: 50, Output: &out}, types.OutputText)
	require.Error(t, err)
	require.Equal(t, 5, code, "a failed cache refresh is exactly a storage error")
	require.Equal(t, pasterrors.ExitCode(err), code)
	require.Contains(t, err.Error(), "Couldn't refresh the session cache")
	require.Empty(t, out.String())
}

func TestSessionShowRendersHistoricalBindingIntervals(t *testing.T) {
	t.Parallel()

	dbPath := sessionapitest.SeedStore(t)

	var text bytes.Buffer
	code, err := handlers.SessionShow(t.Context(), handlers.SessionShowInput{
		DBPath: dbPath, Harness: sessionapitest.HarnessClaudeCode, Session: sessionapitest.ActiveSession, Output: &text,
	}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	for _, want := range []string{
		"Harness:    claude-code",
		"Session:    active-session",
		"State:      active",
		"Epoch:      file://epoch/one",
		"Role:       implementer",
		"Source:     explicit",
		"Confidence: verified",
		"Last event: -",
		"Bindings:   2",
	} {
		require.Contains(t, text.String(), want)
	}
	// The older, closed interval must be rendered with its provenance and end.
	require.Contains(t, text.String(), "1. bound ")
	require.Contains(t, text.String(), "endReason=unbind")
	require.Contains(t, text.String(), "source=explicit")
	require.Contains(t, text.String(), "confidence=verified")
	require.Contains(t, text.String(), "epoch=file://epoch/two")
	require.Contains(t, text.String(), "2. bound ")
	require.Contains(t, text.String(), "open")

	var jsonOut bytes.Buffer
	code, err = handlers.SessionShow(t.Context(), handlers.SessionShowInput{
		DBPath: dbPath, Harness: sessionapitest.HarnessClaudeCode, Session: sessionapitest.ActiveSession, Output: &jsonOut,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	var detail sessionapi.Detail
	require.NoError(t, json.Unmarshal([]byte(jsonOut.String()), &detail))
	require.Equal(t, sessionapitest.ActiveSession, detail.Session.NativeSessionID)
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
	require.NotNil(t, detail.Session.Epoch)
	require.Equal(t, sessionapitest.EpochOne, detail.Session.Epoch.ID)
	require.Len(t, detail.Session.Bindings, 2)
	require.Equal(t, sessionapi.BindingSourceExplicit, detail.Session.Bindings[0].Source)
	require.NotNil(t, detail.Session.Bindings[0].UnboundAt)
	require.NotNil(t, detail.Session.Bindings[0].Epoch)
	require.Equal(t, sessionapitest.EpochTwo, detail.Session.Bindings[0].Epoch.ID)
	require.Nil(t, detail.Session.Bindings[1].UnboundAt, "the current interval is open")
}

func TestSessionShowMissingSessionExitsNonZero(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	code, err := handlers.SessionShow(t.Context(), handlers.SessionShowInput{
		DBPath: sessionapitest.SeedStore(t), Harness: sessionapitest.HarnessClaudeCode, Session: "missing-session", Output: &out,
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
