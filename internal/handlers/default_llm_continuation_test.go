package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

func TestImplicitAndExplicitGenericClaimMismatch(t *testing.T) {
	for _, route := range []string{"direct", "served"} {
		t.Run(route, func(t *testing.T) {
			path := gateStore(t)
			raw := gateFixture(t, gateClaudeSessionStartFixture)
			session := GateSessionIdentity(t, raw, "session_id")
			prepareClaimDiagnosticOwners(t, path)
			tracker, err := tasks.OpenTaskTracker(path)
			require.NoError(t, err)
			_, err = tasks.RecordLifecycleSessionClaim(t.Context(), tracker, ir.HarnessClaudeCode, sessionStartEventKind(ir.HarnessClaudeCode), []model.NativeBinding{{Kind: model.BindingSession, Value: session}}, "stored-actor", gateClock{})
			require.NoError(t, err)
			require.NoError(t, tracker.Close())
			var diagnostics bytes.Buffer
			var run func(bool) (hostexit.Outcome, error)
			if route == "direct" {
				run = func(implicit bool) (hostexit.Outcome, error) {
					in := gateInput(t, path, "SessionStart", raw)
					in.ActorClaim = tasks.ActorClaim(provadapter.DefaultLLMActorID().String())
					in.ImplicitClaim, in.Diagnostics = implicit, &diagnostics
					return HookLifecycleNative(t.Context(), in)
				}
			} else {
				tr, lane := servedFixture(t, path)
				t.Cleanup(func() { require.NoError(t, tr.Close()) })
				server, err := NewHookLifecycleServer(HookLifecycleServerConfig{Tracker: tr, Lane: lane, Clock: gateClock{}, Operations: &gateOperations{}, Profile: timeouts.TestProfile(), Diagnostics: &diagnostics})
				require.NoError(t, err)
				run = func(implicit bool) (hostexit.Outcome, error) {
					in := gateServeInput(t, raw, "SessionStart")
					in.ActorClaim, in.ImplicitClaim = tasks.ActorClaim(provadapter.DefaultLLMActorID().String()), implicit
					out, err := server.Serve(t.Context(), in)
					return out.Outcome, err
				}
			}
			implicit, err := run(true)
			require.NoError(t, err)
			require.Empty(t, diagnostics.String())
			explicit, err := run(false)
			require.NoError(t, err)
			require.Equal(t, implicit, explicit)
			require.Contains(t, diagnostics.String(), "stored-actor")
			require.Contains(t, diagnostics.String(), provadapter.DefaultLLMActorID().String())
			check, err := tasks.OpenTaskTracker(path)
			require.NoError(t, err)
			defer check.Close()
			claim, err := tasks.RecordLifecycleSessionClaim(t.Context(), check, ir.HarnessClaudeCode, sessionStartEventKind(ir.HarnessClaudeCode), []model.NativeBinding{{Kind: model.BindingSession, Value: session}}, tasks.ActorClaim(provadapter.DefaultLLMActorID().String()), gateClock{})
			require.NoError(t, err)
			require.Equal(t, "stored-actor", claim.StoredActor)
		})
	}
}

func TestDefaultLLMNativeDirectAndServedContinuation(t *testing.T) {
	for _, route := range []string{"direct", "served"} {
		t.Run(route, func(t *testing.T) {
			path := gateStore(t)
			raw := gateFixture(t, gateClaudeGateFixture)
			actor := tasks.ActorClaim(provadapter.DefaultLLMActorID().String())
			operations := &gateOperations{}
			var serve func() (hostexit.Outcome, error)
			if route == "direct" {
				serve = func() (hostexit.Outcome, error) {
					in := gateInput(t, path, "PreToolUse", raw)
					in.ActorClaim = actor
					in.Operations = operations
					return HookLifecycleNative(context.Background(), in)
				}
			} else {
				tracker, lane := servedFixture(t, path)
				t.Cleanup(func() { require.NoError(t, tracker.Close()) })
				server := servedServer(t, tracker, lane, tasks.NewGateReader)
				serve = func() (hostexit.Outcome, error) {
					in := gateServeInput(t, raw, "PreToolUse")
					in.ActorClaim = actor
					out, err := server.Serve(context.Background(), in)
					return out.Outcome, err
				}
			}
			for i, reason := range []backend.DecisionReason{backend.ReasonUnboundSession, backend.ReasonAttributionOnlySession, backend.ReasonAttributionOnlySession} {
				if i == 2 {
					// A later explicit claim cannot replace the first fallback claim.
					actor = tasks.ActorClaim("other/0193f1c0-0000-7000-8000-0000000000fe")
				}
				out, err := serve()
				require.NoError(t, err)
				require.Equal(t, hostexit.ExitContinue, out.Exit)
				require.Empty(t, out.Stdout)
				require.Empty(t, out.Stderr)
				rows := gateQueryEvidence(t, path, []provenance.EvidenceKind{receipt.CurrentConsultationEvidenceKind()})
				var stored struct {
					Decision backend.Decision `json:"decision"`
				}
				require.NoError(t, json.Unmarshal(rows[len(rows)-1].Payload, &stored))
				require.Equal(t, reason, stored.Decision.Reason())
			}
		})
	}
}

func TestDefaultLLMRecordOnlyCodexDirectAndServed(t *testing.T) {
	raw, err := os.ReadFile("../lifecycle/ingress/codex/testdata/fixtures/pre_tool_use_0_153_0.json")
	require.NoError(t, err)
	for _, route := range []string{"direct", "served"} {
		t.Run(route, func(t *testing.T) {
			path := gateStore(t)
			actor := tasks.ActorClaim(provadapter.DefaultLLMActorID().String())
			operations := &gateOperations{}
			var serve func() (hostexit.Outcome, error)
			if route == "direct" {
				serve = func() (hostexit.Outcome, error) {
					in := gateInput(t, path, "PreToolUse", raw)
					in.Harness, in.HostVersion, in.ActorClaim, in.Operations = ir.HarnessCodex, registration.Codex__0_153_0().Version, actor, operations
					return HookLifecycleNative(context.Background(), in)
				}
			} else {
				tracker, lane := servedFixture(t, path)
				t.Cleanup(func() { require.NoError(t, tracker.Close()) })
				server := servedServer(t, tracker, lane, tasks.NewGateReader)
				serve = func() (hostexit.Outcome, error) {
					in := gateServeInput(t, raw, "PreToolUse")
					in.Harness, in.HostVersion, in.ActorClaim = ir.HarnessCodex, registration.Codex__0_153_0().Version, actor
					out, err := server.Serve(context.Background(), in)
					return out.Outcome, err
				}
			}
			for _, reason := range []backend.DecisionReason{backend.ReasonUnboundSession, backend.ReasonAttributionOnlySession} {
				out, err := serve()
				require.NoError(t, err)
				require.Equal(t, hostexit.ExitContinue, out.Exit)
				require.Equal(t, `{"continue":true}`, string(out.Stdout))
				require.Empty(t, out.Stderr)
				rows := gateQueryEvidence(t, path, []provenance.EvidenceKind{receipt.CurrentConsultationEvidenceKind()})
				var stored struct {
					Decision backend.Decision `json:"decision"`
				}
				require.NoError(t, json.Unmarshal(rows[len(rows)-1].Payload, &stored))
				require.Equal(t, reason, stored.Decision.Reason())
			}
		})
	}
}
