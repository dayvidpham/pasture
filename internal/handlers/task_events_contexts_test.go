package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

// eventJSONShape mirrors the formatter's audit-event wire shape.
type eventJSONShape struct {
	Id        int64          `json:"id"`
	EpochId   string         `json:"epochId"`
	Phase     string         `json:"phase"`
	Role      string         `json:"role"`
	EventType string         `json:"eventType"`
	Payload   map[string]any `json:"payload"`
}

// contextJSONShape mirrors the formatter's context-edge wire shape.
type contextJSONShape struct {
	Kind      string `json:"kind"`
	ContextId string `json:"contextId"`
}

// seedAuditEvent records one event and attaches the given contexts, returning
// the new event ID. It opens and closes its own tracker so the handler under
// test owns its own handle.
func seedAuditEvent(t *testing.T, path string, event protocol.AuditEvent, contexts map[protocol.ContextKind]string) int64 {
	t.Helper()
	tr, err := tasks.OpenTaskTrackerWithOptions(path, tasks.WithSkipMigrations())
	require.NoError(t, err)
	defer func() { require.NoError(t, tr.Close()) }()

	ctx := context.Background()
	eventID, err := tr.RecordEventReturningId(ctx, event)
	require.NoError(t, err)
	for kind, id := range contexts {
		require.NoError(t, tr.AttachContext(ctx, eventID, kind, id))
	}
	return eventID
}

func decodeEvents(t *testing.T, body string) []eventJSONShape {
	t.Helper()
	var got []eventJSONShape
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	return got
}

func decodeContexts(t *testing.T, body string) []contextJSONShape {
	t.Helper()
	var got []contextJSONShape
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	return got
}

// TestTaskEvents_EpochFilter exercises the --epoch-id path against a real
// context_edges-backed store (the post-v4 schema).
func TestTaskEvents_EpochFilter(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	epochID := "acme--01968a3c-0000-7000-8000-000000000001"
	seededID := seedAuditEvent(t, path, protocol.AuditEvent{
		EpochId:   epochID,
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "supervisor",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"from": "p8", "to": "p9"},
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{protocol.ContextEpoch: epochID})

	var out bytes.Buffer
	code, err := handlers.TaskEvents(&out, handlers.TaskEventsInput{
		DBPath:  path,
		EpochId: epochID,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)

	events := decodeEvents(t, out.String())
	require.Len(t, events, 1)
	require.Equal(t, epochID, events[0].EpochId)
	require.Equal(t, "worker-slices", events[0].Phase)
	require.Equal(t, "PhaseTransition", events[0].EventType)
	// The event ID must survive the query so `task contexts <id>` is reachable.
	require.Equal(t, seededID, events[0].Id)
}

// TestTaskEvents_CombinedEpochAndContextFilter is the regression for a
// combined --context-kind/--context-id + --epoch-id query: an event carrying
// both an EpochContext and a GitContext edge must appear under the combined
// filter, and an event carrying only the GitContext edge must not.
func TestTaskEvents_CombinedEpochAndContextFilter(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	const (
		epochID = "acme--01968a3c-0000-7000-8000-000000000010"
		sha     = "deadbeefcafebabe1234567890abcdef12345678"
	)

	// In-epoch event with both edges.
	seedAuditEvent(t, path, protocol.AuditEvent{
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "worker",
		EventType: protocol.EventSliceStarted,
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{
		protocol.ContextEpoch: epochID,
		protocol.ContextGit:   sha,
	})
	// Git-only event: same commit, no epoch edge.
	seedAuditEvent(t, path, protocol.AuditEvent{
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "worker",
		EventType: protocol.EventSliceCompleted,
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{protocol.ContextGit: sha})

	kind := protocol.ContextGit
	var out bytes.Buffer
	code, err := handlers.TaskEvents(&out, handlers.TaskEventsInput{
		DBPath:      path,
		ContextKind: &kind,
		ContextId:   sha,
		EpochId:     epochID,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)

	events := decodeEvents(t, out.String())
	require.Len(t, events, 1, "only the epoch member survives the combined filter")
	require.Equal(t, "SliceStarted", events[0].EventType)
}

// TestTaskEvents_AgentFilterMatchesLiveAndLegacyNames proves --agent matches
// the recorded attribution for both a live well-known agent name and a plain
// legacy role name, on both the epoch and context query paths.
func TestTaskEvents_AgentFilterMatchesLiveAndLegacyNames(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	const (
		epochLive   = "acme--01968a3c-0000-7000-8000-000000000020"
		epochLegacy = "acme--01968a3c-0000-7000-8000-000000000021"
		liveName    = "pasture/automaton/check-constraints"
		legacyName  = "worker"
	)

	seedAuditEvent(t, path, protocol.AuditEvent{
		Phase:     protocol.PhaseCodeReview,
		Role:      liveName,
		EventType: protocol.EventConstraintChecked,
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{protocol.ContextEpoch: epochLive})
	seedAuditEvent(t, path, protocol.AuditEvent{
		Phase:     protocol.PhaseWorkerSlices,
		Role:      legacyName,
		EventType: protocol.EventSliceStarted,
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{protocol.ContextEpoch: epochLegacy})

	cases := []struct {
		name    string
		epochID string
		agent   string
	}{
		{"live name on epoch path", epochLive, liveName},
		{"legacy name on epoch path", epochLegacy, legacyName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			code, err := handlers.TaskEvents(&out, handlers.TaskEventsInput{
				DBPath:  path,
				EpochId: tc.epochID,
				Agent:   tc.agent,
			}, types.OutputJSON)
			require.NoError(t, err)
			require.Zero(t, code)
			events := decodeEvents(t, out.String())
			require.Len(t, events, 1)
			require.Equal(t, tc.agent, events[0].Role)
		})
	}

	// The same attribution filter works on the epoch-context path.
	kind := protocol.ContextEpoch
	var out bytes.Buffer
	code, err := handlers.TaskEvents(&out, handlers.TaskEventsInput{
		DBPath:      path,
		ContextKind: &kind,
		ContextId:   epochLive,
		Agent:       liveName,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	events := decodeEvents(t, out.String())
	require.Len(t, events, 1)
	require.Equal(t, liveName, events[0].Role)
}

// TestTaskEvents_ContextFilter exercises the --context-kind/--context-id path
// and proves post-fetch filters (--phase, --type) narrow the result.
func TestTaskEvents_ContextFilter(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	sha := "deadbeefcafebabe1234567890abcdef12345678"
	seedAuditEvent(t, path, protocol.AuditEvent{
		Phase:     protocol.PhaseCodeReview,
		Role:      "reviewer",
		EventType: protocol.EventVoteRecorded,
		Payload:   map[string]any{"vote": "accept"},
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{protocol.ContextGit: sha})

	kind := protocol.ContextGit
	var out bytes.Buffer
	code, err := handlers.TaskEvents(&out, handlers.TaskEventsInput{
		DBPath:      path,
		ContextKind: &kind,
		ContextId:   sha,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	events := decodeEvents(t, out.String())
	require.Len(t, events, 1)
	require.Equal(t, "VoteRecorded", events[0].EventType)

	// A non-matching --phase filter empties the result.
	phase := protocol.PhaseLanding
	out.Reset()
	code, err = handlers.TaskEvents(&out, handlers.TaskEventsInput{
		DBPath:      path,
		ContextKind: &kind,
		ContextId:   sha,
		Phase:       &phase,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	require.Empty(t, decodeEvents(t, out.String()))

	// A matching --type filter keeps it.
	eventType := protocol.EventVoteRecorded
	out.Reset()
	code, err = handlers.TaskEvents(&out, handlers.TaskEventsInput{
		DBPath:      path,
		ContextKind: &kind,
		ContextId:   sha,
		EventType:   &eventType,
	}, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	require.Len(t, decodeEvents(t, out.String()), 1)
}

// TestTaskContexts_ListsAttachedEdges exercises `task contexts EVENT-ID`.
func TestTaskContexts_ListsAttachedEdges(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	epochID := "acme--01968a3c-0000-7000-8000-000000000002"
	sha := "deadbeefcafebabe1234567890abcdef12345678"
	eventID := seedAuditEvent(t, path, protocol.AuditEvent{
		EpochId:   epochID,
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "worker",
		EventType: protocol.EventSliceStarted,
		Timestamp: time.Now().UTC(),
	}, map[protocol.ContextKind]string{
		protocol.ContextEpoch: epochID,
		protocol.ContextGit:   sha,
	})

	var out bytes.Buffer
	code, err := handlers.TaskContexts(&out, path, strconv.FormatInt(eventID, 10), types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)

	contexts := decodeContexts(t, out.String())
	require.Len(t, contexts, 2)
	got := map[string]string{}
	for _, c := range contexts {
		got[c.Kind] = c.ContextId
	}
	require.Equal(t, epochID, got["EpochContext"])
	require.Equal(t, sha, got["GitContext"])
}

// TestTaskEvents_ValidationErrors proves the handler refuses unbounded or
// ambiguous queries with actionable validation errors.
func TestTaskEvents_ValidationErrors(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	kind := protocol.ContextGit

	cases := []struct {
		name string
		in   handlers.TaskEventsInput
	}{
		{name: "no top-level filter", in: handlers.TaskEventsInput{DBPath: path}},
		{name: "context kind without id", in: handlers.TaskEventsInput{DBPath: path, ContextKind: &kind}},
		{name: "context id without kind", in: handlers.TaskEventsInput{DBPath: path, ContextId: "abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			code, err := handlers.TaskEvents(&out, tc.in, types.OutputJSON)
			require.Equal(t, 1, code)
			var se *pasterrors.StructuredError
			require.ErrorAs(t, err, &se)
			require.Equal(t, pasterrors.CategoryValidation, se.Category)
			require.NotEmpty(t, se.Fix)
			require.Empty(t, out.String())
		})
	}
}

// TestTaskContexts_RejectsBadEventIDs covers the positional event-ID parser.
func TestTaskContexts_RejectsBadEventIDs(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	for _, raw := range []string{"", "abc", "0", "-3"} {
		t.Run("id="+raw, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			code, err := handlers.TaskContexts(&out, path, raw, types.OutputJSON)
			require.Equal(t, 1, code)
			var se *pasterrors.StructuredError
			require.ErrorAs(t, err, &se)
			require.Equal(t, pasterrors.CategoryValidation, se.Category)
			require.NotEmpty(t, se.Fix)
			require.Empty(t, out.String())
		})
	}
}

// TestParseFlags_RejectsUnknownValues covers the CLI-facing flag parsers the
// events verb relies on.
func TestParseFlags_RejectsUnknownValues(t *testing.T) {
	t.Parallel()

	if _, err := handlers.ParseContextKindFlag("NotAKind"); err == nil {
		t.Fatal("ParseContextKindFlag accepted an unknown kind")
	}
	if _, err := handlers.ParseEventTypeFlag("NotAType"); err == nil {
		t.Fatal("ParseEventTypeFlag accepted an unknown type")
	}
	if _, err := handlers.ParseSinceFlag("not-a-time"); err == nil {
		t.Fatal("ParseSinceFlag accepted an unparseable timestamp")
	}
	if _, err := handlers.ParseSinceFlag(""); err == nil {
		t.Fatal("ParseSinceFlag accepted an empty timestamp")
	}
}
