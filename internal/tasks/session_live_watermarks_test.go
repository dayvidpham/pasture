package tasks_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

type liveWatermarkTail struct {
	Event   string `yaml:"event"`
	Source  string `yaml:"source"`
	JID     int64  `yaml:"jid"`
	At      int64  `yaml:"at"`
	Invalid bool   `yaml:"invalid"`
}

type liveFoldClock struct{ at int64 }

func (c *liveFoldClock) Now() time.Time { return time.Unix(c.at, 0).UTC() }

func TestLiveSessionEndResumePreservesManualAndLaterBindingHistory(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch, err := tracker.Create("file://live-watermarks", "epoch", "binding history", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	clock := &liveFoldClock{at: 100}
	store := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), clock)
	bind := func(at int64) tasks.SessionBindingResult {
		clock.at = at
		out, err := store.Bind(t.Context(), "claude-code", "watermark", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
		require.NoError(t, err)
		return out
	}
	tail := func(event, source string, jid, at int64) {
		_, err := tasks.RecordLifecycleSessionTail(t.Context(), tracker, watermarkTail(t, liveWatermarkTail{Event: event, Source: source, JID: jid, At: at}))
		require.NoError(t, err)
	}
	openCount := func(want int) {
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_session_epoch_binding WHERE session='watermark' AND unbound_at IS NULL`).Scan(&count))
		require.Equal(t, want, count)
	}
	first := bind(100)
	tail("SessionEnd", "", 10, 200)
	openCount(0)
	tail("SessionStart", "resume", 20, 300)
	openCount(0)
	second := bind(400)
	// A delayed end predating the known resume cannot close the new mapping.
	tail("SessionEnd", "", 15, 250)
	openCount(1)
	// Even an unsuperseded end may not close a mapping bound after its instant.
	tail("SessionEnd", "", 30, 350)
	openCount(1)
	tail("SessionStart", "resume", 40, 500)
	clock.at = 600
	_, err = store.Unbind(t.Context(), "claude-code", "watermark", second.BindingID)
	require.NoError(t, err)
	tail("SessionStart", "resume", 50, 700)
	openCount(0)
	var reason string
	require.NoError(t, db.QueryRow(`SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id=?`, first.BindingID).Scan(&reason))
	require.Equal(t, "session_end", reason)
	require.NoError(t, db.QueryRow(`SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id=?`, second.BindingID).Scan(&reason))
	require.Equal(t, "unbind", reason, "resume never changes the manual decision")
	third := bind(800)
	require.NotEqual(t, first.BindingID, third.BindingID)
	require.NotEqual(t, second.BindingID, third.BindingID)
	openCount(1)
}

func TestEveryLiveDirtyWriteAdvancesThePublicationGeneration(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	for range 2 {
		require.NoError(t, tasks.MarkSessionStateDirty(t.Context(), tracker))
	}
	var revision int64
	require.NoError(t, db.QueryRow(`SELECT revision FROM pasture_session_api_state`).Scan(&revision))
	require.Equal(t, int64(2), revision, "even a repeated dirty marker fences a concurrent replay")
	in := watermarkTail(t, liveWatermarkTail{Event: "UserPromptSubmit", JID: 10, At: 100})
	in.StateWriter = failingSessionStateWriter{}
	out, err := tasks.RecordLifecycleSessionTail(t.Context(), tracker, in)
	require.NoError(t, err)
	require.True(t, out.State.Dirty)
	require.NoError(t, db.QueryRow(`SELECT revision FROM pasture_session_api_state`).Scan(&revision))
	require.Equal(t, int64(3), revision, "the isolated tail failure advances the generation in its transaction")
}

func watermarkTail(t *testing.T, tail liveWatermarkTail) tasks.SessionTailInput {
	t.Helper()
	event, err := ingress.EventByNativeName(registration.ClaudeCode__2_1_261(), tail.Event)
	require.NoError(t, err)
	in := tailInput([]model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "watermark"}}, model.OccurrenceID(tail.JID))
	in.ActorClaim = ""
	in.EventKind = event.Kind
	in.RecordedAt = time.Unix(tail.At, 0).UTC()
	in.NativeBody = []byte(fmt.Sprintf(`{"hook_event_name":%q,"session_id":"watermark","source":%q}`, tail.Event, tail.Source))
	in.Capture = model.CaptureValid
	if tail.Invalid {
		in.Capture = model.CaptureUnsupportedSchema
	}
	return in
}

func TestLiveSessionWatermarksAreIndependentAndIdempotent(t *testing.T) {
	t.Parallel()
	const corpus testutil.FixtureName = "session_live_watermarks"
	var cases []struct {
		Name     string              `yaml:"name"`
		Tails    []liveWatermarkTail `yaml:"tails"`
		Broad    int64               `yaml:"broad"`
		Prompt   int64               `yaml:"prompt"`
		PromptAt int64               `yaml:"prompt_at"`
		End      int64               `yaml:"end"`
		EndAt    int64               `yaml:"end_at"`
		Resume   int64               `yaml:"resume"`
		ResumeAt int64               `yaml:"resume_at"`
		Revision int64               `yaml:"revision"`
	}
	testutil.LoadFixtures(t, corpus, &cases)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			tracker, db, _ := claimStore(t)
			for _, tail := range tc.Tails {
				_, err := tasks.RecordLifecycleSessionTail(t.Context(), tracker, watermarkTail(t, tail))
				require.NoError(t, err)
			}
			var broad, prompt, promptAt, end, endAt, resume, resumeAt, revision, cursor int64
			require.NoError(t, db.QueryRow(`SELECT last_journal_id,last_prompt_journal_id,last_prompt_at,host_end_journal_id,host_end_at,last_resume_journal_id,last_resume_at FROM pasture_session_state WHERE session='watermark'`).Scan(&broad, &prompt, &promptAt, &end, &endAt, &resume, &resumeAt))
			require.Equal(t, tc.Broad, broad)
			require.Equal(t, tc.Prompt, prompt)
			require.Equal(t, tc.PromptAt*int64(time.Second), promptAt)
			require.Equal(t, tc.End, end)
			require.Equal(t, tc.EndAt*int64(time.Second), endAt)
			require.Equal(t, tc.Resume, resume)
			require.Equal(t, tc.ResumeAt*int64(time.Second), resumeAt)
			require.NoError(t, db.QueryRow(`SELECT revision,completed_through_jid FROM pasture_session_api_state`).Scan(&revision, &cursor))
			require.Equal(t, tc.Revision, revision)
			require.Zero(t, cursor, "hot tails cannot certify a scan across receipt/tail gaps")
		})
	}
}
