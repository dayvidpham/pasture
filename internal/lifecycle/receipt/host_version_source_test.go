package receipt_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestReceiptVersionSourceRoundTripsAndLegacyRemainsUnspecified(t *testing.T) {
	t.Parallel()
	for _, source := range []model.HostVersionSource{"", model.HostVersionCallerSupplied, model.HostVersionExecutableQuery} {
		t.Run(string(source), func(t *testing.T) {
			database := filepath.Join(t.TempDir(), "pasture.db")
			tracker, err := tasks.OpenTaskTracker(database)
			require.NoError(t, err)
			defer tracker.Close()
			_, err = tracker.Create("file://version-source", "bootstrap", "initialize system identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
			require.NoError(t, err)
			service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
			require.NoError(t, err)
			event, err := ingress.EventByNativeName(registration.ClaudeCode2_1_261(), "SessionStart")
			require.NoError(t, err)
			raw, err := os.ReadFile("../ingress/claude/testdata/fixtures/session_start_2_1_261.json")
			require.NoError(t, err)
			delivery := claude.Parse(raw, event, "2.1.300", model.OccurrenceEnvelopeRef{}).Delivery
			delivery.Envelope.HostVersionSource = source
			intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
			require.Nil(t, refusal)
			warrant, refusal := gate.Legalize(intent)
			require.Nil(t, refusal)
			committed, err := service.Receive(context.Background(), warrant, delivery)
			require.NoError(t, err)
			before := versionSourceEvidence(t, tracker.Journal())
			require.Len(t, before.Rows, 1)
			var payload struct {
				Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
			}
			require.NoError(t, json.Unmarshal(before.Rows[0].Payload, &payload))
			require.Equal(t, source, payload.Envelope.HostVersionSource)
			if source == "" {
				require.NotContains(t, string(before.Rows[0].Payload), "hostVersionSource")
			}
			require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
			reader, err := tasks.NewLifecycleReader(tracker)
			require.NoError(t, err)
			size, err := model.NewPageSize(10)
			require.NoError(t, err)
			page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, committed.OccurrenceID, page.Items[0].Occurrence.OccurrenceID)
			require.Equal(t, source, page.Items[0].Occurrence.Envelope.HostVersionSource)
			require.Equal(t, "2.1.300", page.Items[0].Occurrence.Envelope.HostVersion)
			body, err := reader.Payload(context.Background(), digest.FromBytes(raw))
			require.NoError(t, err)
			require.Equal(t, raw, body)
			after := versionSourceEvidence(t, tracker.Journal())
			require.Equal(t, before.Rows, after.Rows, "projection replay must not rewrite legacy bytes, digests or journal identities")
			require.NoError(t, tracker.Journal().VerifyIntegrity())
		})
	}
}

func TestReceiptRejectsUnknownVersionSourceBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	database := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(database)
	require.NoError(t, err)
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, gateTestClock{}, &gateTestOperations{}, timeouts.TestProfile())
	require.NoError(t, err)
	event, err := ingress.EventByNativeName(registration.ClaudeCode2_1_261(), "SessionStart")
	require.NoError(t, err)
	delivery := claude.Parse([]byte(`{"hook_event_name":"SessionStart","session_id":"session"}`), event, "2.1.300", model.OccurrenceEnvelopeRef{}).Delivery
	delivery.Envelope.HostVersionSource = "running-process-attested"
	intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	require.Nil(t, refusal)
	warrant, refusal := gate.Legalize(intent)
	require.Nil(t, refusal)
	_, err = service.Receive(context.Background(), warrant, delivery)
	require.ErrorContains(t, err, "host version source is not a declared provenance value")
	require.Empty(t, versionSourceEvidence(t, tracker.Journal()).Rows)
	db, err := sql.Open("sqlite", database)
	require.NoError(t, err)
	defer db.Close()
	var blobs int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM lifecycle_payload_blobs").Scan(&blobs))
	require.Zero(t, blobs, "source validation must precede the blob write, not only the journal append")
}

func versionSourceEvidence(t *testing.T, journal provenance.Journal) provenance.EvidencePage {
	t.Helper()
	page, err := journal.Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1"},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	return page
}
