package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestHookLifecycleRetainsVersionSourceOnValidAndRefusedCapture(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("../lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json")
	require.NoError(t, err)
	for _, source := range []model.HostVersionSource{"", model.HostVersionCallerSupplied, model.HostVersionExecutableQuery} {
		for _, valid := range []bool{true, false} {
			name := string(source) + "/refused"
			if valid {
				name = string(source) + "/valid"
			}
			t.Run(name, func(t *testing.T) {
				database := filepath.Join(t.TempDir(), "pasture.db")
				tracker, err := tasks.OpenTaskTracker(database)
				require.NoError(t, err)
				_, err = tracker.Create("file://handler-version-source", "bootstrap", "initialize system identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
				require.NoError(t, err)
				require.NoError(t, tracker.Close())
				raw := fixture
				if !valid {
					raw = []byte(`{"hook_event_name":"SessionStart","session_id":42}`)
				}
				outcome, err := handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
					DBPath:            database,
					Harness:           ir.HarnessClaudeCode,
					Event:             "SessionStart",
					HostVersion:       "2.1.300",
					HostVersionSource: source,
					Input:             bytes.NewReader(raw),
					Clock:             fixedLifecycleClock{},
					Operations:        fixedLifecycleOperations{id: "version-source"},
				})
				if valid {
					require.NoError(t, err)
					require.Equal(t, hostexit.ExitContinue, outcome.Exit)
					require.Empty(t, outcome.Stdout)
					require.Empty(t, outcome.Stderr)
				} else {
					require.ErrorIs(t, err, handlers.ErrLifecycleDeliveryRefused)
					require.Equal(t, hostexit.Outcome{}, outcome, "refusal carries no committed decision Outcome")
				}
				tracker, err = tasks.OpenTaskTracker(database)
				require.NoError(t, err)
				defer tracker.Close()
				evidence := queryOneEvidence(t, tracker, "pasture.lifecycle.occurrence.v1")
				var payload struct {
					Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
					Capture  model.CaptureDisposition    `json:"capture"`
				}
				require.NoError(t, json.Unmarshal(evidence.Payload, &payload))
				require.Equal(t, source, payload.Envelope.HostVersionSource, "caller source must reach both write branches")
				require.Equal(t, "2.1.300", payload.Envelope.HostVersion)
				if valid {
					require.Equal(t, model.CaptureValid, payload.Capture)
				} else {
					require.Equal(t, model.CaptureUnsupportedSchema, payload.Capture)
				}
				require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
				reader, err := tasks.NewLifecycleReader(tracker)
				require.NoError(t, err)
				size, err := model.NewPageSize(10)
				require.NoError(t, err)
				page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
				require.NoError(t, err)
				require.Len(t, page.Items, 1)
				require.Equal(t, source, page.Items[0].Occurrence.Envelope.HostVersionSource)
				body, err := reader.Payload(context.Background(), digest.FromBytes(raw))
				require.NoError(t, err)
				require.Equal(t, raw, body)
			})
		}
	}
}

func TestHookLifecycleRejectsUnknownVersionSourceBeforeOpeningStore(t *testing.T) {
	t.Parallel()
	database := filepath.Join(t.TempDir(), "unopened", "pasture.db")
	input := &readTrackingLifecycleInput{}
	outcome, err := handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:            database,
		Harness:           ir.HarnessClaudeCode,
		Event:             "SessionStart",
		HostVersion:       "2.1.300",
		HostVersionSource: "running-process-attested",
		Input:             input,
		Clock:             fixedLifecycleClock{},
		Operations:        fixedLifecycleOperations{id: "invalid-version-source"},
	})
	require.ErrorContains(t, err, "host version source is not a declared provenance value")
	require.ErrorIs(t, err, handlers.ErrLifecycleBeforeDurableWrite)
	require.Equal(t, hostexit.Outcome{}, outcome)
	require.Zero(t, input.reads)
	_, err = os.Stat(filepath.Dir(database))
	require.ErrorIs(t, err, os.ErrNotExist)
}
