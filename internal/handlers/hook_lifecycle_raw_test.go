package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

// TestRawSchemaVersionConstantsPinToGeneratedRegistrations pins the
// RawSchemaVersion constants to the generated per-harness registrations via
// the derived rawSchemaVersionFor, so the doc claim "the closed set is pinned
// by tests against the generated registrations" is true (review MINOR-2): a
// build cannot advertise a wire identity its own registrations do not decode.
func TestRawSchemaVersionConstantsPinToGeneratedRegistrations(t *testing.T) {
	t.Parallel()

	require.Equal(t, string(RawSchemaClaudeCode2_1_261), string(rawSchemaVersionFor(ir.HarnessClaudeCode)),
		"the Claude wire identity must equal the contract derived from the generated 2.1.261 registration")
	require.Equal(t, string(RawSchemaOpenCode1_18_29), string(rawSchemaVersionFor(ir.HarnessOpenCode)),
		"the OpenCode wire identity must equal the contract derived from the generated 1.18.29 registration")
	require.Equal(t, string(RawSchemaCodex0_153_0), string(rawSchemaVersionFor(ir.HarnessCodex)),
		"the Codex wire identity must equal the contract derived from the generated 0.153.0 registration")
	// The 2.0.20 wire identity is selected by observed host version, not by
	// the harness default above: pin it to the generated 2.0.20 registration
	// it decodes.
	contract, err := ir.NewRuntimeContractID(registration.OpenCode2_0_20().Harness, registration.OpenCode2_0_20().Version)
	require.NoError(t, err)
	require.Equal(t, string(RawSchemaOpenCode2_0_20), contract.String(),
		"the OpenCode 2.0.20 wire identity must equal the contract derived from the generated 2.0.20 registration")
}

type rawOutcomeClock struct{}

func (rawOutcomeClock) Now() time.Time { return time.Now() }

type rawOutcomeOperation struct{}

func (rawOutcomeOperation) NewOperationID() (string, error) {
	return "pasture.raw.outcome-proof", nil
}

// TestRawPreviewShowsTheUnevaluatedDefaultAndTheCommitRunsTheGate holds the one
// difference between the raw preview and the raw commit: a preview opens no
// store, so it cannot read a claim and cannot evaluate the gate, while the
// commit consults the reader and records what the policy decided.
//
// The preview half therefore shows the UNEVALUATED default — the middle end's own
// Proceed — and the commit half records the real reason. Asserting that
// difference is the point: a preview that claimed to predict the committed
// consultation would be making a promise it cannot keep, and an operator
// comparing the two digests would be reading a defect as a fact.
func TestRawPreviewShowsTheUnevaluatedDefaultAndTheCommitRunsTheGate(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_261.json")
	require.NoError(t, err)
	previewDB := filepath.Join(t.TempDir(), "unopened", "pasture.db")
	input := HookLifecycleRawInput{
		DBPath:        previewDB,
		Harness:       ir.HarnessClaudeCode,
		Event:         "PreToolUse",
		HostVersion:   registration.ClaudeCode2_1_261().Version,
		SchemaVersion: RawSchemaClaudeCode2_1_261,
		DryRun:        true,
		Input:         bytes.NewReader(raw),
		Clock:         rawOutcomeClock{},
		Operations:    rawOutcomeOperation{},
	}

	preview, err := HookLifecycleRaw(context.Background(), input)

	require.NoError(t, err)
	_, committed := preview.Outcome()
	require.False(t, committed, "a preview must not manufacture a committed result")
	var document struct {
		DryRun       bool   `json:"dryRun"`
		ExitStatus   int    `json:"exitStatus"`
		Continuation string `json:"continuation"`
		Stderr       string `json:"stderr"`
	}
	require.NoError(t, json.Unmarshal(preview.Preview(), &document))
	require.True(t, document.DryRun)
	require.Equal(t, 0, document.ExitStatus, "the unevaluated default is a proceed, because no gate was consulted")
	require.Empty(t, document.Continuation)
	require.Empty(t, document.Stderr, "a proceed carries no refusal message")
	_, err = os.Stat(previewDB)
	require.ErrorIs(t, err, os.ErrNotExist)
	previewBytes := preview.Preview()
	previewBytes[0] = 'X'
	require.Equal(t, byte('{'), preview.Preview()[0], "preview bytes must be owned")

	// The real commit runs the real Reader over a real store, and the receipt
	// names the reason the policy gave. This is a production-path proof at the
	// handler layer: no verdict is supplied anywhere in this subject.
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tracker.Create("file://raw-outcome-proof", "bootstrap", "initialize ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
	input.DBPath = dbPath
	input.DryRun = false
	input.Input = bytes.NewReader(raw)

	result, err := HookLifecycleRaw(context.Background(), input)

	require.NoError(t, err)
	require.Nil(t, result.Preview())
	outcome, committed := result.Outcome()
	require.True(t, committed)
	require.Equal(t, hostexit.ExitContinue, outcome.Exit, "an unclaimed session fails open")
	require.Empty(t, outcome.Stdout)
	require.Equal(t, document.Stderr, outcome.Stderr)
	tracker, err = tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{receipt.CurrentConsultationEvidenceKind()},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	var stored struct {
		Decision backend.Decision `json:"decision"`
	}
	require.NoError(t, json.Unmarshal(page.Rows[0].Payload, &stored))
	require.Equal(t, backend.DecisionProceed, stored.Decision.Kind())
	require.Equal(t, backend.ReasonUnboundSession, stored.Decision.Reason(),
		"the committed consultation must name UNBOUND while the store-free preview names the default: the two are "+
			"meant to differ, and the raw help text says so")
}

func TestRawHostVersionSourcePreviewCommitAndLegacy(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json")
	require.NoError(t, err)
	dispatch, err := dispatchLifecycle(ir.HarnessClaudeCode)
	require.NoError(t, err)
	event := dispatch.manifest.Events[0]
	require.Equal(t, "SessionStart", event.NativeName)
	legacy := dispatch.rawParse(raw, event, "2.1.299")
	legacyEnvelope, err := json.Marshal(legacy.delivery.Envelope)
	require.NoError(t, err)
	// The journal stores canonical object ordering, not Go struct-field order.
	// Use its public encoding contract and still compare exact retained bytes.
	legacyDigest := sha256.Sum256(legacyEnvelope)
	canonical, err := provenance.Canonicalize(provenance.OperationInput{
		Effects: []provenance.Effect{{
			Sort:          provenance.EffectEvidence,
			ResultSlot:    "legacy-envelope",
			EvidenceKind:  "pasture.test.raw-envelope",
			ContentDigest: legacyDigest[:],
			Payload:       legacyEnvelope,
		}},
	})
	require.NoError(t, err)
	require.Len(t, canonical.NormalizedEffects(), 1)
	legacyEnvelope = canonical.NormalizedEffects()[0].Payload
	for _, source := range []model.HostVersionSource{"", model.HostVersionCallerSupplied} {
		name := string(source)
		if name == "" {
			name = "legacy unspecified"
		}
		t.Run(name, func(t *testing.T) {
			previewDB := filepath.Join(t.TempDir(), "unopened", "pasture.db")
			input := HookLifecycleRawInput{
				DBPath:            previewDB,
				Harness:           ir.HarnessClaudeCode,
				Event:             "SessionStart",
				HostVersion:       "2.1.299",
				HostVersionSource: source,
				SchemaVersion:     RawSchemaClaudeCode2_1_261,
				DryRun:            true,
				Input:             bytes.NewReader(raw),
				Clock:             rawOutcomeClock{},
				Operations:        rawOutcomeOperation{},
			}

			preview, err := HookLifecycleRaw(context.Background(), input)

			require.NoError(t, err)
			_, committed := preview.Outcome()
			require.False(t, committed)
			var previewFields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(preview.Preview(), &previewFields))
			if source == "" {
				require.NotContains(t, previewFields, "hostVersionSource")
			} else {
				require.Contains(t, previewFields, "hostVersionSource", "raw preview must retain the supplied observation source")
				var observed model.HostVersionSource
				require.NoError(t, json.Unmarshal(previewFields["hostVersionSource"], &observed))
				require.Equal(t, source, observed)
			}
			_, err = os.Stat(filepath.Dir(previewDB))
			require.ErrorIs(t, err, os.ErrNotExist)
			database := filepath.Join(t.TempDir(), "pasture.db")
			tracker, err := tasks.OpenTaskTracker(database)
			require.NoError(t, err)
			_, err = tracker.Create("file://raw-version-proof", "bootstrap", "initialize ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
			require.NoError(t, err)
			require.NoError(t, tracker.Close())
			input.DBPath = database
			input.DryRun = false
			input.Input = bytes.NewReader(raw)

			result, err := HookLifecycleRaw(context.Background(), input)

			require.NoError(t, err)
			outcome, committed := result.Outcome()
			require.True(t, committed)
			require.Equal(t, hostexit.ExitContinue, outcome.Exit)
			require.Empty(t, outcome.Stdout)
			require.Empty(t, outcome.Stderr)
			tracker, err = tasks.OpenTaskTracker(database)
			require.NoError(t, err)
			defer tracker.Close()
			page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
				Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
				Kinds:  []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1"},
				Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
			})
			require.NoError(t, err)
			require.Len(t, page.Rows, 1)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(page.Rows[0].Payload, &fields))
			var envelope model.OccurrenceEnvelopeRef
			require.NoError(t, json.Unmarshal(fields["envelope"], &envelope))
			require.Equal(t, "2.1.299", envelope.HostVersion)
			require.Equal(t, source, envelope.HostVersionSource)
			require.Equal(t, "raw", string(envelope.Origin))
			if source == "" {
				require.Equal(t, legacyEnvelope, []byte(fields["envelope"]), "zero-source raw envelope bytes must remain identical to the unstamped parser envelope")
			}
		})
	}
}

func TestRawUnknownHostVersionSourceRefusesBeforeIO(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json")
	require.NoError(t, err)
	for _, dryRun := range []bool{true, false} {
		name := "commit"
		if dryRun {
			name = "preview"
		}
		t.Run(name, func(t *testing.T) {
			database := filepath.Join(t.TempDir(), "unopened", "pasture.db")
			reader := bytes.NewReader(raw)
			input := HookLifecycleRawInput{
				DBPath:            database,
				Harness:           ir.HarnessClaudeCode,
				Event:             "SessionStart",
				HostVersion:       "2.1.299",
				HostVersionSource: model.HostVersionSource("unrecognized-source"),
				SchemaVersion:     RawSchemaClaudeCode2_1_261,
				DryRun:            dryRun,
				Input:             reader,
				Clock:             rawOutcomeClock{},
				Operations:        rawOutcomeOperation{},
			}

			result, err := HookLifecycleRaw(context.Background(), input)

			require.Nil(t, result)
			require.ErrorContains(t, err, "host version source")
			require.Equal(t, len(raw), reader.Len(), "unknown provenance must refuse before reading input")
			_, err = os.Stat(filepath.Dir(database))
			require.ErrorIs(t, err, os.ErrNotExist, "unknown provenance must not open or create the store")
		})
	}
}

// TestRawHatchRoutesOpenCodeByObservedVersion pins the raw hatch to the same
// version routing the native path uses: a 2.0.20 host version selects the
// 2.0.20 registration (manifest, parser and activation) and admits the
// 2.0.20 wire schema, while an older host stays on 1.18.29. Every leg below
// is refused before a byte is read, so no store exists afterwards: the
// refused reason is what distinguishes the selected row. A v2-only event
// through the v2 row reaches the all-withheld activation; the same event on
// the v1 row is unknown.
func TestRawHatchRoutesOpenCodeByObservedVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		hostVersion string
		schema      RawSchemaVersion
		event       string
		wantReason  string
	}{
		{
			name:        "2.0.20 host with 2.0.20 schema reaches the v2 activation",
			hostVersion: "2.0.20", schema: RawSchemaOpenCode2_0_20, event: "session.prompt",
			wantReason: "withheld (reason missing-fixture)",
		},
		{
			name:        "1.18.29 host with 1.18.29 schema stays on the v1 row",
			hostVersion: "1.18.29", schema: RawSchemaOpenCode1_18_29, event: "session.prompt",
			wantReason: "is not in the generated OpenCode registration",
		},
		{
			name:        "2.0.20 host with 1.18.29 schema is a mismatched pairing",
			hostVersion: "2.0.20", schema: RawSchemaOpenCode1_18_29, event: "session.prompt",
			wantReason: "does not describe",
		},
		{
			name:        "1.18.29 host with 2.0.20 schema is a mismatched pairing",
			hostVersion: "1.18.29", schema: RawSchemaOpenCode2_0_20, event: "session.created",
			wantReason: "does not describe",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := HookLifecycleRawInput{
				DBPath:        filepath.Join(t.TempDir(), "unopened", "pasture.db"),
				Harness:       ir.HarnessOpenCode,
				Event:         tc.event,
				HostVersion:   tc.hostVersion,
				SchemaVersion: tc.schema,
				Input:         bytes.NewReader([]byte(`{}`)),
				Clock:         rawOutcomeClock{},
				Operations:    rawOutcomeOperation{},
			}
			_, err := HookLifecycleRaw(context.Background(), input)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantReason)
			_, statErr := os.Stat(input.DBPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "a refused raw invocation opens no store")
		})
	}
}
