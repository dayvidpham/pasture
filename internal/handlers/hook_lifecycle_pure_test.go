package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/gatepolicy"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// This file is the store-free arm of the hybrid lifecycle boundary. It drives
// the production admission and preview functions directly, with no store
// opener, no database path, and no receipt service. The durable half stays on
// real SQLite: migrations, WAL, ledger replay, claims, concurrency/deadlines,
// and the daemon lane are not faked here.
//
// The functions under test are unexported package seams, not test-only
// exports. prepareLifecycleDelivery is the shared admission step for both the
// direct and served paths, and rawDryRunPreview is the same L1→L2 derivation
// tail the committing raw path runs. Neither takes a store handle, and these
// tests prove the refusals and the rendered preview do not need one.

const pureClaudeFixture = "../lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_261.json"

func readPureFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(pureClaudeFixture)
	require.NoError(t, err)
	return raw
}

// admissionReader records whether the bounded payload read was reached. A
// refusal that claims "the input was not read" must not touch it.
type admissionReader struct {
	reads int
	err   error
}

func (r *admissionReader) Read([]byte) (int, error) {
	r.reads++
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.EOF
}

type admissionCase struct {
	name       string
	admission  lifecycleAdmission
	wantErr    string
	wantReads  int
	wantNoRead bool
}

func TestPrepareLifecycleDeliveryRefusesBeforeAnyStore(t *testing.T) {
	t.Parallel()

	valid := lifecycleAdmission{
		Harness:     ir.HarnessClaudeCode,
		Event:       "PreToolUse",
		HostVersion: registration.ClaudeCode__2_1_261().Version,
		Input:       bytes.NewReader(readPureFixture(t)),
	}

	cases := []admissionCase{
		{
			name: "missing host version",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessClaudeCode,
				Event:       "PreToolUse",
				HostVersion: " ",
				Input:       &admissionReader{},
			},
			wantErr:    "observed host version is missing",
			wantNoRead: true,
		},
		{
			name: "invalid host version source",
			admission: lifecycleAdmission{
				Harness:           ir.HarnessClaudeCode,
				Event:             "PreToolUse",
				HostVersion:       registration.ClaudeCode__2_1_261().Version,
				HostVersionSource: model.HostVersionSource("not-a-declared-source"),
				Input:             &admissionReader{},
			},
			wantErr:    "host version source is not a declared provenance value",
			wantNoRead: true,
		},
		{
			name: "unknown event",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessClaudeCode,
				Event:       "NotARegisteredEvent",
				HostVersion: registration.ClaudeCode__2_1_261().Version,
				Input:       &admissionReader{},
			},
			wantErr:    `declares no native event named "NotARegisteredEvent"`,
			wantNoRead: true,
		},
		{
			name: "withheld Claude event",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessClaudeCode,
				Event:       "Elicitation",
				HostVersion: registration.ClaudeCode__2_1_261().Version,
				Input:       &admissionReader{},
			},
			wantErr:    `event "Elicitation" is withheld`,
			wantNoRead: true,
		},
		{
			name: "withheld OpenCode event",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessOpenCode,
				Event:       "session.updated",
				HostVersion: registration.OpenCode__1_18_29().Version,
				Input:       &admissionReader{},
			},
			wantErr:    `event "session.updated" is withheld`,
			wantNoRead: true,
		},
		{
			name: "payload read failure",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessClaudeCode,
				Event:       "PreToolUse",
				HostVersion: registration.ClaudeCode__2_1_261().Version,
				Input:       &admissionReader{err: errors.New("stdin broke")},
			},
			wantErr:   "native payload could not be read",
			wantReads: 1,
		},
		{
			name: "empty payload",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessClaudeCode,
				Event:       "PreToolUse",
				HostVersion: registration.ClaudeCode__2_1_261().Version,
				Input:       bytes.NewReader(nil),
			},
			wantErr:   "standard input carried zero bytes",
			wantReads: 1,
		},
		{
			name: "oversized payload",
			admission: lifecycleAdmission{
				Harness:     ir.HarnessClaudeCode,
				Event:       "PreToolUse",
				HostVersion: registration.ClaudeCode__2_1_261().Version,
				Input:       bytes.NewReader(bytes.Repeat([]byte("x"), model.MaxNativePayloadBytes+1)),
			},
			wantErr:   "exceeds",
			wantReads: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// There is no store path in lifecycleAdmission. The empty temp
			// directory is a witness that nothing was created beside it.
			dir := t.TempDir()
			_, err := prepareLifecycleDelivery(tc.admission)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)

			var structured *pasterrors.StructuredError
			require.ErrorAs(t, err, &structured, "admission refusals are typed validation errors")
			require.Equal(t, pasterrors.CategoryValidation, structured.Category)

			if reader, ok := tc.admission.Input.(*admissionReader); ok {
				if tc.wantNoRead {
					require.Zero(t, reader.reads, "refusal must happen before the payload is read")
				} else {
					require.Equal(t, tc.wantReads, reader.reads)
				}
			}
			entries, readErr := os.ReadDir(dir)
			require.NoError(t, readErr)
			require.Empty(t, entries)
		})
	}

	prepared, err := prepareLifecycleDelivery(valid)
	require.NoError(t, err)
	require.Equal(t, "Claude", prepared.dispatch.name)
	require.Equal(t, "PreToolUse", prepared.event.NativeName)
	require.Equal(t, model.CaptureValid, prepared.capture.disposition, "the real fixture must classify valid")
	require.NotEmpty(t, prepared.capture.delivery.Bindings)
	require.Equal(t, prepared.capture.delivery.Event, prepared.event.Kind)
}

func TestRawDryRunPreviewRendersFromThePreparedDeliveryWithoutAStore(t *testing.T) {
	t.Parallel()

	admission := lifecycleAdmission{
		Harness:     ir.HarnessClaudeCode,
		Event:       "PreToolUse",
		HostVersion: registration.ClaudeCode__2_1_261().Version,
		Input:       bytes.NewReader(readPureFixture(t)),
	}
	prepared, err := prepareLifecycleDelivery(admission)
	require.NoError(t, err)

	preview, err := rawDryRunPreview(
		prepared.dispatch,
		prepared.event,
		admission.HostVersion,
		RawSchemaClaudeCode2_1_261,
		prepared.capture.delivery,
	)
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(preview, []byte("\n")))

	var document rawDryRunView
	require.NoError(t, json.Unmarshal(preview, &document))
	require.True(t, document.DryRun)
	require.Equal(t, "Claude", document.Harness)
	require.Equal(t, "PreToolUse", document.Event)
	require.Equal(t, admission.HostVersion, document.HostVersion)
	require.Equal(t, string(RawSchemaClaudeCode2_1_261), document.SchemaVersion)
	require.Equal(t, prepared.capture.delivery.Contract.String(), document.Contract)
	require.NotEmpty(t, document.Effects, "the preview must expose the derived evidence the commit would bind")
	for _, effect := range document.Effects {
		require.NotEmpty(t, effect.Sort)
		require.NotEmpty(t, effect.ResultSlot)
		require.NotEmpty(t, effect.EvidenceKind)
		require.NotEmpty(t, effect.ContentDigest)
		require.NotEmpty(t, effect.Payload)
	}
	require.Equal(t, 0, document.ExitStatus, "the unevaluated default is a proceed")
	require.Empty(t, document.Continuation)
	require.Empty(t, document.Stderr)
}

// TestPreparedAdmissionFeedsGateAndEncoderWithoutAStore is the composition
// check for the pure middle end: the admitted delivery's event class and
// mapping feed gatepolicy.Decide, and the registry encoder turns the resulting
// decision into host bytes. None of the three stages takes a store handle.
func TestPreparedAdmissionFeedsGateAndEncoderWithoutAStore(t *testing.T) {
	t.Parallel()

	admission := lifecycleAdmission{
		Harness:     ir.HarnessClaudeCode,
		Event:       "PreToolUse",
		HostVersion: registration.ClaudeCode__2_1_261().Version,
		Input:       bytes.NewReader(readPureFixture(t)),
	}
	prepared, err := prepareLifecycleDelivery(admission)
	require.NoError(t, err)

	mapping, err := prepared.dispatch.mapping(prepared.event.NativeName)
	require.NoError(t, err)
	require.True(t, mapping.IsValid())

	action, ok := gateauthority.ClassForEvent(ir.HarnessClaudeCode, prepared.event.NativeName)
	require.True(t, ok, "every registered event must have an action class")
	require.True(t, action.IsValid())

	result, err := gatepolicy.Decide(gatepolicy.Input{
		Event:      prepared.event.Kind,
		Action:     action,
		Semantic:   mapping.Semantic(),
		StopLoop:   mapping.StopLoop(),
		Capability: mapping.Response(),
		Claim:      gateauthority.SessionClaim{Bound: false},
		Authority:  gateauthority.ActorAuthority{},
	})
	require.NoError(t, err)
	decision, ok := result.Decision()
	require.True(t, ok, "an unbound session must produce a decision, not a refusal")
	require.Equal(t, backend.DecisionProceed, decision.Kind())
	require.Equal(t, backend.ReasonUnboundSession, decision.Reason())

	response, err := backend.NewHostResponse(decision)
	require.NoError(t, err)
	outcome, err := prepared.dispatch.encode(mapping, response)
	require.NoError(t, err)
	require.Equal(t, hostexit.ExitContinue, outcome.Exit)
	require.Empty(t, outcome.Stdout, "a Claude proceed carries no bytes")
	require.Empty(t, outcome.Stderr)
}

// TestPrepareLifecycleDeliveryHonoursTheActivationOverride proves the override
// input is the same admission path, not a second dispatch table: an event the
// committed catalog enables becomes withheld when the supplied catalog says so,
// and the payload is then not read.
func TestPrepareLifecycleDeliveryHonoursTheActivationOverride(t *testing.T) {
	t.Parallel()

	entries, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	overridden := make([]activation.Entry, 0, len(entries))
	found := false
	for _, entry := range entries {
		if entry.Event == registration.EventPreToolUse {
			entry.State = activation.Withheld
			entry.Reason = activation.WithheldMissingFixture
			entry.CaptureProof = 0
			entry.ProductionProof = 0
			entry.Clearance = ""
			require.True(t, entry.IsValid(), "the override must be a valid withheld catalog entry")
			found = true
		}
		overridden = append(overridden, entry)
	}
	require.True(t, found, "the committed catalog must carry PreToolUse")

	reader := &admissionReader{}
	_, err = prepareLifecycleDelivery(lifecycleAdmission{
		Harness:     ir.HarnessClaudeCode,
		Event:       "PreToolUse",
		HostVersion: registration.ClaudeCode__2_1_261().Version,
		Activations: overridden,
		Input:       reader,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "withheld")
	require.Zero(t, reader.reads, "the supplied catalog must be consulted before the payload")
}
