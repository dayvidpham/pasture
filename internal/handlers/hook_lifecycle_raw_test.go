package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
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
}

type rawOutcomeClock struct{}

func (rawOutcomeClock) Now() time.Time { return time.Now() }

type rawOutcomeOperation struct{}

func (rawOutcomeOperation) NewOperationID() (string, error) {
	return "pasture.raw.outcome-proof", nil
}

func TestRawPreviewAndCommitPreserveFullSuppliedOutcome(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_261.json")
	require.NoError(t, err)
	decision, err := backend.NewDecision(backend.DecisionRequireHuman, backend.ReasonRoleForbidsAction)
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
		Decision:      &decision,
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
	require.Equal(t, 2, document.ExitStatus)
	require.Empty(t, document.Continuation)
	require.Equal(t, decision.Reason().Message(), document.Stderr)
	_, err = os.Stat(previewDB)
	require.ErrorIs(t, err, os.ErrNotExist)
	previewBytes := preview.Preview()
	previewBytes[0] = 'X'
	require.Equal(t, byte('{'), preview.Preview()[0], "preview bytes must be owned")

	// This is a real receipt/encoding proof with a supplied constructor-valid
	// verdict, not a Reader-derived role denial. RequireHuman normalizes to
	// Deny because the evidenced Claude event has no supported Ask channel.
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
	require.Equal(t, hostexit.ExitBlock, outcome.Exit)
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
	require.Equal(t, backend.DecisionDeny, stored.Decision.Kind())
	require.Equal(t, backend.ReasonRoleForbidsAction, stored.Decision.Reason())
}
