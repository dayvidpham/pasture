package handlers

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

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
