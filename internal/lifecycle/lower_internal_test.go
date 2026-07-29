package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/runtime"
)

// The lowering pass refuses on two independent grounds: an occurrence the host
// waits on, and an occurrence whose meaning it has no reviewed handling for.
//
// Under every currently pinned contract those two sets coincide — every
// decision request and every relayed human answer is also awaited — so the
// second refusal cannot be reached through the public API. That coincidence is
// a property of today's tables, not of the design, and a branch no test can
// reach is a branch nobody has run. This file forges the value the public API
// cannot yet produce, so the refusal is exercised rather than assumed.
//
// It is white-box for exactly that reason, and for no other.

// forgedNonAwaitedDecisionRequest builds what a future pinned contract could
// legitimately declare: an event asking Pasture for a decision that the host
// does NOT block on.
//
// It starts from a real verified event so every other field is what the
// verifier would have produced, and changes only the blocking mode.
func forgedNonAwaitedDecisionRequest(t *testing.T) Event {
	t.Helper()

	binding, err := BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)

	session, err := NewIdentity(runtime.IdentitySession, "session_id", "sess-01")
	require.NoError(t, err)
	toolCall, err := NewIdentity(runtime.IdentityToolCall, "tool_use_id", "call-01")
	require.NoError(t, err)

	event, err := binding.NewEvent(NewDigest([]byte(`{"forged":true}`)), []Identity{session, toolCall})
	require.NoError(t, err)
	require.Equal(t, runtime.SemanticGateConsultation, event.semantics.semantic)

	event.semantics.blocking = runtime.NonBlocking
	return event
}

// refusingRecorder fails the test if it is ever asked to write. It proves
// "wrote nothing" directly rather than by inspecting state afterwards.
type refusingRecorder struct{ t *testing.T }

func (r refusingRecorder) RecordObservation(context.Context, ObservationRecord) (RecordOutcome, error) {
	r.t.Helper()
	r.t.Fatal("a refused occurrence must never reach the recorder")
	return 0, nil
}

// refusingActorResolver fails the test if attribution is ever attempted.
type refusingActorResolver struct{ t *testing.T }

func (r refusingActorResolver) ResolveHookActor(context.Context, Origin) (provenance.AgentID, error) {
	r.t.Helper()
	r.t.Fatal("a refused occurrence must never reach attribution")
	return provenance.AgentID{}, nil
}

// TestLowerRefusesANonAwaitedDecisionRequest proves the pass refuses on
// MEANING, not merely on whether the host is waiting.
//
// Without the semantic dispatch, this event slips past the awaited-reply check
// and is recorded as though it were an observation — storing a decision request
// as evidence that something happened, which is a claim nobody made.
func TestLowerRefusesANonAwaitedDecisionRequest(t *testing.T) {
	t.Parallel()

	deps := Deps{
		Recorder: refusingRecorder{t: t},
		Actors:   refusingActorResolver{t: t},
		Clock:    func() time.Time { return time.Unix(0, 0).UTC() },
	}

	outcome, err := Lower(context.Background(), deps, forgedNonAwaitedDecisionRequest(t))

	require.Error(t, err)
	assert.Equal(t, Outcome{}, outcome)
	assert.Contains(t, err.Error(), runtime.SemanticGateConsultation.String(),
		"the refusal must name the meaning it could not handle")
}

// TestLowerRefusesANonAwaitedRelayedAnswer is the same argument for the other
// unhandled meaning. The two are refused for different reasons and say
// different things, so both wordings need a test that reaches them.
func TestLowerRefusesANonAwaitedRelayedAnswer(t *testing.T) {
	t.Parallel()

	event := forgedNonAwaitedDecisionRequest(t)
	event.semantics.semantic = runtime.SemanticExplicitHumanResponse

	deps := Deps{
		Recorder: refusingRecorder{t: t},
		Actors:   refusingActorResolver{t: t},
		Clock:    func() time.Time { return time.Unix(0, 0).UTC() },
	}

	outcome, err := Lower(context.Background(), deps, event)

	require.Error(t, err)
	assert.Equal(t, Outcome{}, outcome)
	assert.Contains(t, err.Error(), runtime.SemanticExplicitHumanResponse.String())
}
