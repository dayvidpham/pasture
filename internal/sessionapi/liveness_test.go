package sessionapi_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/sessionapi"
)

// watermark sets the durable prompt/end/resume evidence on an existing state
// row. Times and journal ids are supplied independently so a test can build a
// superseded end (resume newer than end) or an end that is still in force.
func (f fixture) watermark(t *testing.T, session string, prompt, hostEnd, resume int64, hostEndJournal, resumeJournal int64) {
	t.Helper()
	_, err := f.db.Exec(`
		UPDATE pasture_session_state
		SET last_prompt_at = ?, host_end_at = ?, host_end_journal_id = ?,
		    last_resume_at = ?, last_resume_journal_id = ?
		WHERE harness = 'claude-code' AND session = ?`,
		prompt, hostEnd, hostEndJournal, resume, resumeJournal, session)
	require.NoError(t, err)
}

// TestLivenessStatesAndBasis walks the ratified contract's decision table. Each
// case asserts the state, the required basis, prompt exposure, and whether an
// end marker may exist.
func TestLivenessStatesAndBasis(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	minute := int64(time.Minute)
	hour := int64(time.Hour)

	// Recent prompt and no binding: prompt liveness does not require a mapping.
	f.stateWithPrompt(t, "unbound-recent", now-10*minute, now-1*minute, now-2*minute, "UserPromptSubmit", 1)

	// Exact boundary rows.
	f.stateWithPrompt(t, "exact-active", now-60*minute, now-60*minute, now-30*minute, "UserPromptSubmit", 2)
	f.stateWithPrompt(t, "just-idle", now-60*minute, now-60*minute, now-30*minute-int64(time.Nanosecond), "UserPromptSubmit", 3)
	f.stateWithPrompt(t, "exact-idle", now-25*hour, now-25*hour, now-24*hour, "UserPromptSubmit", 4)
	f.stateWithPrompt(t, "inactivity-ended", now-26*hour, now-26*hour, now-24*hour-int64(time.Nanosecond), "UserPromptSubmit", 5)

	// Bound but prompt-less: a binding is not a prompt.
	f.stateWithPrompt(t, "bound-noprompt", now-5*minute, now-1*minute, 0, "PreToolUse", 6)
	f.binding(t, "binding-bound-noprompt", "bound-noprompt", now-5*minute, sql.NullInt64{}, "")

	// Manual unbind is an explicit end independent of any prompt.
	f.stateWithPrompt(t, "manual-unbind", now-60*minute, now-2*minute, now-1*minute, "PreToolUse", 7)
	f.binding(t, "binding-manual-unbind", "manual-unbind", now-10*minute,
		sql.NullInt64{Int64: now - 2*minute, Valid: true}, "unbind")

	// Host end still in force.
	f.stateWithPrompt(t, "host-ended", now-60*minute, now-20*minute, now-40*minute, "SessionEnd", 8)
	f.watermark(t, "host-ended", now-40*minute, now-20*minute, 0, 8, 0)
	f.binding(t, "binding-host-ended", "host-ended", now-60*minute,
		sql.NullInt64{Int64: now - 20*minute, Valid: true}, "session_end")

	// Host end superseded by a newer resume: the prompt decides the state.
	f.stateWithPrompt(t, "resumed-recent", now-60*minute, now-15*minute, now-1*minute, "SessionStart", 9)
	f.watermark(t, "resumed-recent", now-1*minute, now-40*minute, now-15*minute, 8, 9)
	f.binding(t, "binding-resumed-recent", "resumed-recent", now-60*minute,
		sql.NullInt64{Int64: now - 45*minute, Valid: true}, "session_end")

	// Resume does not refresh the prompt: an old prompt stays inactivity-ended.
	f.stateWithPrompt(t, "resumed-old", now-60*minute, now-15*minute, now-25*hour, "SessionStart", 10)
	f.watermark(t, "resumed-old", now-25*hour, now-40*minute, now-15*minute, 8, 10)
	f.binding(t, "binding-resumed-old", "resumed-old", now-60*minute,
		sql.NullInt64{Int64: now - 45*minute, Valid: true}, "session_end")

	// A closed historical interval followed by a later open interval does not
	// end the session.
	f.stateWithPrompt(t, "rebound", now-60*minute, now-10*minute, now-5*minute, "UserPromptSubmit", 11)
	f.binding(t, "binding-rebound-old", "rebound", now-60*minute,
		sql.NullInt64{Int64: now - 50*minute, Valid: true}, "session_end")
	f.binding(t, "binding-rebound-open", "rebound", now-10*minute, sql.NullInt64{}, "")
	f.stateWithPrompt(t, "unbind-rebind", now-60*minute, now-minute, now-minute, "UserPromptSubmit", 18)
	f.binding(t, "binding-unbind-rebind-old", "unbind-rebind", now-60*minute,
		sql.NullInt64{Int64: now - 20*minute, Valid: true}, "unbind")
	f.binding(t, "binding-unbind-rebind-new", "unbind-rebind", now-10*minute, sql.NullInt64{}, "")

	// Constructed cache states exercise independent binding and host-end facts.
	f.stateWithPrompt(t, "end-unbound", now-60*minute, now-20*minute, now-5*minute, "SessionEnd", 12)
	f.watermark(t, "end-unbound", now-5*minute, now-20*minute, 0, 12, 0)
	f.stateWithPrompt(t, "end-inferred", now-60*minute, now-20*minute, now-5*minute, "SessionEnd", 13)
	f.watermark(t, "end-inferred", now-5*minute, now-20*minute, 0, 13, 0)
	f.inferredBinding(t, "inferred-end", "end-inferred", f.epoch, now-60*minute, sql.NullInt64{}, "", "observed")
	f.stateWithPrompt(t, "end-rebound", now-60*minute, now-5*minute, now-5*minute, "UserPromptSubmit", 14)
	f.watermark(t, "end-rebound", now-5*minute, now-20*minute, 0, 14, 0)
	f.binding(t, "binding-end-rebound", "end-rebound", now-10*minute, sql.NullInt64{}, "")
	f.stateWithPrompt(t, "resumed-idle", now-60*minute, now-5*minute, now-45*minute, "SessionStart", 15)
	f.watermark(t, "resumed-idle", now-45*minute, now-20*minute, now-5*minute, 14, 15)
	f.stateWithPrompt(t, "resumed-noprompt", now-60*minute, now-5*minute, 0, "SessionStart", 16)
	f.watermark(t, "resumed-noprompt", 0, now-20*minute, now-5*minute, 14, 16)
	f.stateWithPrompt(t, "resumed-unbind", now-60*minute, now-5*minute, now-minute, "SessionStart", 17)
	f.watermark(t, "resumed-unbind", now-minute, now-30*minute, now-5*minute, 14, 17)
	f.binding(t, "binding-resumed-unbind", "resumed-unbind", now-60*minute,
		sql.NullInt64{Int64: now - 10*minute, Valid: true}, "unbind")

	expected := map[string]struct {
		state sessionapi.ActivityState
		basis sessionapi.StateBasis
		end   bool
	}{
		"unbound-recent":   {sessionapi.StateActive, sessionapi.BasisPrompt, false},
		"exact-active":     {sessionapi.StateActive, sessionapi.BasisPrompt, false},
		"just-idle":        {sessionapi.StateIdle, sessionapi.BasisPrompt, false},
		"exact-idle":       {sessionapi.StateIdle, sessionapi.BasisPrompt, false},
		"inactivity-ended": {sessionapi.StateEnded, sessionapi.BasisInactivity, false},
		"bound-noprompt":   {sessionapi.StateUnknown, sessionapi.BasisNone, false},
		"manual-unbind":    {sessionapi.StateEnded, sessionapi.BasisExplicitEnd, true},
		"host-ended":       {sessionapi.StateEnded, sessionapi.BasisExplicitEnd, true},
		"resumed-recent":   {sessionapi.StateActive, sessionapi.BasisPrompt, false},
		"resumed-old":      {sessionapi.StateEnded, sessionapi.BasisInactivity, false},
		"rebound":          {sessionapi.StateActive, sessionapi.BasisPrompt, false},
		"unbind-rebind":    {sessionapi.StateActive, sessionapi.BasisPrompt, false},
		"end-unbound":      {sessionapi.StateEnded, sessionapi.BasisExplicitEnd, true},
		"end-inferred":     {sessionapi.StateEnded, sessionapi.BasisExplicitEnd, true},
		"end-rebound":      {sessionapi.StateEnded, sessionapi.BasisExplicitEnd, true},
		"resumed-idle":     {sessionapi.StateIdle, sessionapi.BasisPrompt, false},
		"resumed-noprompt": {sessionapi.StateUnknown, sessionapi.BasisNone, false},
		"resumed-unbind":   {sessionapi.StateEnded, sessionapi.BasisExplicitEnd, true},
	}

	service := f.service()
	ctx := context.Background()
	for session, want := range expected {
		detail, err := service.Get(ctx, "claude-code", session)
		require.NoError(t, err, session)
		require.Equal(t, want.state, detail.Session.Activity.State, session)
		require.Equal(t, want.basis, detail.Session.Activity.StateBasis, session)
		if want.end {
			require.NotNil(t, detail.Session.End, session)
			endAt, reason := now-20*minute, "session_end"
			switch session {
			case "manual-unbind":
				endAt, reason = now-2*minute, "unbind"
			case "resumed-unbind":
				endAt, reason = now-10*minute, "unbind"
			}
			require.Equal(t, reason, detail.Session.End.EndReason, session)
			require.Equal(t, time.Unix(0, endAt).UTC(), detail.Session.End.EndedAt, session)
		} else {
			require.Nil(t, detail.Session.End, "%s must not carry an invented end marker", session)
		}
	}

	// The broad activity record survives independently of prompt recency.
	detail, err := service.Get(ctx, "claude-code", "unbound-recent")
	require.NoError(t, err)
	require.NotNil(t, detail.Session.Activity.LastSeen)
	require.NotNil(t, detail.Session.Activity.LastPromptAt)
	require.Equal(t, time.Unix(0, now-2*minute).UTC(), *detail.Session.Activity.LastPromptAt)
}

// TestLivenessFilterParity pins the SQL state filter to the Go view: a filter
// for a state returns exactly the sessions the view labels with that state, and
// the epoch active count agrees. The fixture deliberately avoids a binding on
// the active row to prove the filter no longer requires one.
func TestLivenessFilterParity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	minute := int64(time.Minute)
	hour := int64(time.Hour)

	f.stateWithPrompt(t, "unbound-active", now-10*minute, now-1*minute, now-1*minute, "UserPromptSubmit", 1)
	f.stateWithPrompt(t, "idle", now-60*minute, now-60*minute, now-45*minute, "UserPromptSubmit", 2)
	f.stateWithPrompt(t, "inactive", now-60*minute, now-60*minute, now-25*hour, "UserPromptSubmit", 3)
	f.stateWithPrompt(t, "unknown", now-10*minute, now-1*minute, 0, "PreToolUse", 4)
	f.stateWithPrompt(t, "unbound-manual", now-10*minute, now-1*minute, now-1*minute, "PreToolUse", 5)
	f.binding(t, "binding-unbound-manual", "unbound-manual", now-5*minute,
		sql.NullInt64{Int64: now - minute, Valid: true}, "unbind")
	// An epoch-bound active session keeps the epoch active count meaningful.
	f.binding(t, "binding-epoch-active", "unbound-active", now-10*minute, sql.NullInt64{}, "")

	service := f.service()
	ctx := context.Background()

	all, err := service.List(ctx, sessionapi.ListOptions{Limit: 100})
	require.NoError(t, err)
	byState := map[sessionapi.ActivityState]map[string]bool{}
	for _, item := range all.Items {
		if byState[item.Activity.State] == nil {
			byState[item.Activity.State] = map[string]bool{}
		}
		byState[item.Activity.State][item.NativeSessionID] = true
	}

	for _, state := range []sessionapi.ActivityState{
		sessionapi.StateActive, sessionapi.StateIdle, sessionapi.StateEnded, sessionapi.StateUnknown,
	} {
		page, err := service.List(ctx, sessionapi.ListOptions{State: state, Limit: 100})
		require.NoError(t, err)
		want := byState[state]
		require.Len(t, page.Items, len(want), "filter %s", state)
		for _, item := range page.Items {
			require.True(t, want[item.NativeSessionID],
				"filter %s returned %s, which the view labels %s", state, item.NativeSessionID, item.Activity.State)
		}
	}

	epochs, err := service.Epochs(ctx)
	require.NoError(t, err)
	require.Len(t, epochs.Items, 1)
	require.Equal(t, 1, epochs.Items[0].ActiveCount,
		"the epoch active count must equal the active sessions the page reports")
}

// TestLivenessWindowValidation refuses every nonsensical pair at the service
// boundary rather than producing a silently wrong state.
func TestLivenessWindowValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		active time.Duration
		idle   time.Duration
	}{
		{"zero active", 0, time.Hour},
		{"negative active", -time.Minute, time.Hour},
		{"zero idle", time.Minute, 0},
		{"negative idle", time.Minute, -time.Hour},
		{"equal windows", time.Hour, time.Hour},
		{"inverted windows", 2 * time.Hour, time.Hour},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &sessionapi.Service{DB: f.db, Now: func() time.Time { return f.now },
				ActiveWindow: testCase.active, IdleWindow: testCase.idle}
			_, err := service.List(ctx, sessionapi.ListOptions{})
			require.True(t, errors.Is(err, sessionapi.ErrInvalidWindow), "List: %v", err)
			_, err = service.Get(ctx, "claude-code", "unbound-recent")
			require.True(t, errors.Is(err, sessionapi.ErrInvalidWindow), "Get: %v", err)
			_, err = service.Epochs(ctx)
			require.True(t, errors.Is(err, sessionapi.ErrInvalidWindow), "Epochs: %v", err)
			_, err = service.Inspect(ctx)
			require.True(t, errors.Is(err, sessionapi.ErrInvalidWindow), "Inspect: %v", err)
		})
	}
}

// TestInspectUsesOneClock proves inspect labels epochs and sessions with the
// same injected instant, even when the clock advances between calls.
func TestInspectUsesOneClock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := f.now.UnixNano()
	// A prompt sits exactly on the active cutoff. If epoch counts read a later
	// clock than the session page, the count and the labels diverge.
	f.stateWithPrompt(t, "cutoff", now-int64(30*time.Minute), now-int64(30*time.Minute),
		now-int64(30*time.Minute), "UserPromptSubmit", 1)
	f.binding(t, "binding-cutoff", "cutoff", now-int64(30*time.Minute), sql.NullInt64{}, "")

	service := f.service()
	var calls int
	service.Now = func() time.Time {
		calls++
		if calls == 1 {
			return f.now
		}
		// A second read would move the clock past the cutoff, so a reader that
		// called the clock twice would label the same prompt idle.
		return f.now.Add(time.Minute)
	}
	inspect, err := service.Inspect(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, calls, "inspect must read the response clock exactly once")
	require.Equal(t, f.now, inspect.AsOf)
	require.Equal(t, sessionapi.StateActive, inspect.Sessions[0].Activity.State)
	require.Equal(t, 1, inspect.Epochs[0].ActiveCount)
}
