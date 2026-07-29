package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// These exercise the lowering pass against fakes rather than a database.
//
// The pass's own job is selection and ordering — which operation, and in what
// sequence relative to the first write. Both are properties of this file, not
// of storage. The transactional and deduplication guarantees the fakes stand in
// for belong to the production recorder and are proved where that recorder
// lives, against a real store.
//
// Every test below is written so that removing the behaviour it targets makes
// it fail. A test that would still pass without the branch it names is not
// testing that branch.

// ─── Fakes ───────────────────────────────────────────────────────────────────

// replayRecorder is a map-backed [lifecycle.ObservationRecorder]. It keeps the
// first record it sees under a replay key and reports every later record with
// that key as a replay, leaving the original untouched — the same contract the
// production recorder owes, reduced to a map.
type replayRecorder struct {
	mu     sync.Mutex
	stored map[string]lifecycle.ObservationRecord
	order  []string
}

func newReplayRecorder() *replayRecorder {
	return &replayRecorder{stored: make(map[string]lifecycle.ObservationRecord)}
}

func (r *replayRecorder) RecordObservation(
	_ context.Context,
	observation lifecycle.ObservationRecord,
) (lifecycle.RecordOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.stored[observation.ReplayKey]; found {
		return lifecycle.RecordReplayed, nil
	}
	r.stored[observation.ReplayKey] = observation
	r.order = append(r.order, observation.ReplayKey)
	return lifecycle.RecordCreated, nil
}

// records returns everything stored, in the order it was first written.
func (r *replayRecorder) records() []lifecycle.ObservationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]lifecycle.ObservationRecord, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.stored[key])
	}
	return out
}

// answeringRecorder returns a fixed answer regardless of input. It exists to
// cover the recorder contract violations the pass must not pass on to its
// caller as success.
type answeringRecorder struct {
	outcome lifecycle.RecordOutcome
	err     error
	calls   int
}

func (r *answeringRecorder) RecordObservation(
	_ context.Context,
	_ lifecycle.ObservationRecord,
) (lifecycle.RecordOutcome, error) {
	r.calls++
	return r.outcome, r.err
}

// fixedResolver answers with one identity, or fails. It counts calls so a test
// can prove a refusal happened BEFORE attribution was attempted.
type fixedResolver struct {
	actor provenance.AgentID
	err   error
	calls int
}

func (r *fixedResolver) ResolveHookActor(
	_ context.Context,
	_ lifecycle.Origin,
) (provenance.AgentID, error) {
	r.calls++
	return r.actor, r.err
}

// ─── Fixtures ────────────────────────────────────────────────────────────────

// observedAt is deliberately NOT in UTC, so a test can tell the difference
// between the pass normalising the recorded time and the pass passing whatever
// the clock happened to return straight through.
var observedAt = time.Date(2026, 7, 29, 12, 0, 0, 0, time.FixedZone("test-offset", 9*60*60))

func frozenClock() time.Time { return observedAt }

// testActor is a well-formed attribution. Its exact value is irrelevant; that
// it is non-zero is not.
func testActor(t *testing.T) provenance.AgentID {
	t.Helper()
	actor, err := provenance.ParseAgentID("pasture--3f8a1c74-6b21-4f0d-9a5e-2c7d8e1b4a60")
	require.NoError(t, err)
	return actor
}

// workingDeps are collaborators that all succeed.
func workingDeps(t *testing.T) (lifecycle.Deps, *replayRecorder, *fixedResolver) {
	t.Helper()
	recorder := newReplayRecorder()
	resolver := &fixedResolver{actor: testActor(t)}
	return lifecycle.Deps{
		Recorder: recorder,
		Actors:   resolver,
		Clock:    frozenClock,
	}, recorder, resolver
}

// observedOccurrence builds a verified, non-awaited observation carrying one
// session correlation value, over the exact payload bytes given.
func observedOccurrence(t *testing.T, session, payload string) lifecycle.Event {
	t.Helper()
	binding := observationBinding(t)
	event, err := binding.NewEvent(
		lifecycle.NewDigest([]byte(payload)),
		[]lifecycle.Identity{
			identity(t, runtime.IdentitySession, declaredField(t, binding, runtime.IdentitySession), session),
		},
	)
	require.NoError(t, err)
	require.Equal(t, runtime.NonBlocking, event.Semantics().Blocking(),
		"this fixture is the non-awaited observation; if the pinned contract changed, the tests below no longer mean what they say")
	return event
}

// awaitedOccurrence builds a verified occurrence whose host waits for a reply.
func awaitedOccurrence(t *testing.T) lifecycle.Event {
	t.Helper()
	binding := gateBinding(t)
	event, err := binding.NewEvent(
		lifecycle.NewDigest([]byte(`{"awaited":true}`)),
		[]lifecycle.Identity{
			identity(t, runtime.IdentitySession, declaredField(t, binding, runtime.IdentitySession), "sess-01"),
			identity(t, runtime.IdentityToolCall, declaredField(t, binding, runtime.IdentityToolCall), "call-01"),
		},
	)
	require.NoError(t, err)
	require.NotEqual(t, runtime.NonBlocking, event.Semantics().Blocking(),
		"this fixture is the awaited occurrence; if the pinned contract changed, the refusal test below proves nothing")
	return event
}

func requireStructured(t *testing.T, err error) *pasterrors.StructuredError {
	t.Helper()
	var structured *pasterrors.StructuredError
	require.Truef(t, errors.As(err, &structured),
		"a refusal must be actionable, not a bare error; got %#v", err)
	return structured
}

// ─── Mis-wiring: every collaborator is required, none is substituted ──────────

func TestLowerRefusesEveryMissingCollaboratorWithoutWriting(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	var noContext context.Context

	cases := []struct {
		name string
		ctx  context.Context
		// mangle removes exactly one collaborator from otherwise working deps.
		mangle func(*lifecycle.Deps)
		wantIn string
	}{
		{
			name:   "no context",
			ctx:    noContext,
			mangle: func(*lifecycle.Deps) {},
			wantIn: "without an execution context",
		},
		{
			name:   "no recorder",
			ctx:    context.Background(),
			mangle: func(d *lifecycle.Deps) { d.Recorder = nil },
			wantIn: "nowhere to record",
		},
		{
			name:   "no resolver",
			ctx:    context.Background(),
			mangle: func(d *lifecycle.Deps) { d.Actors = nil },
			wantIn: "no way to credit",
		},
		{
			// The pass must NOT reach for the system clock here. If it did,
			// this case would succeed and record a time nobody chose.
			name:   "no clock",
			ctx:    context.Background(),
			mangle: func(d *lifecycle.Deps) { d.Clock = nil },
			wantIn: "without a clock",
		},
		{
			name:   "already cancelled",
			ctx:    cancelled,
			mangle: func(*lifecycle.Deps) {},
			wantIn: "already been cancelled",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			deps, recorder, resolver := workingDeps(t)
			testCase.mangle(&deps)

			outcome, err := lifecycle.Lower(testCase.ctx, deps, observedOccurrence(t, "sess-01", `{"a":1}`))

			structured := requireStructured(t, err)
			assert.Contains(t, structured.What, testCase.wantIn,
				"the refusal must name the collaborator that is missing, not just report incomplete wiring")
			assert.Contains(t, structured.Impact, "Nothing was recorded")
			assert.Equal(t, lifecycle.Outcome{}, outcome)
			assert.Empty(t, recorder.records(), "a mis-wiring refusal must write nothing")
			assert.Zero(t, resolver.calls, "a mis-wiring refusal must not reach attribution")
		})
	}
}

func TestLowerRefusesAnUncheckedEventWithoutWriting(t *testing.T) {
	t.Parallel()

	deps, recorder, resolver := workingDeps(t)

	outcome, err := lifecycle.Lower(context.Background(), deps, lifecycle.Event{})

	structured := requireStructured(t, err)
	assert.Contains(t, structured.What, "unchecked")
	assert.Equal(t, lifecycle.Outcome{}, outcome)
	assert.Empty(t, recorder.records())
	assert.Zero(t, resolver.calls)
}

// ─── Legalization: an awaited occurrence is refused, loudly and cleanly ───────

// TestLowerRefusesAnAwaitedOccurrenceWithoutWriting is the behaviour the design
// exists to produce. Accepting the occurrence would record something true and
// then leave the host blocked on a reply that never arrives, which is worse
// than refusing: it looks like it worked.
func TestLowerRefusesAnAwaitedOccurrenceWithoutWriting(t *testing.T) {
	t.Parallel()

	deps, recorder, resolver := workingDeps(t)

	outcome, err := lifecycle.Lower(context.Background(), deps, awaitedOccurrence(t))

	structured := requireStructured(t, err)
	assert.Contains(t, structured.What, "waits for a reply",
		"the refusal must name the missing capability so an operator knows what to do instead")
	assert.Contains(t, structured.Impact, "No partial record")
	assert.Equal(t, lifecycle.Outcome{}, outcome)
	assert.Empty(t, recorder.records(), "an awaited occurrence must not be recorded")
	assert.Zero(t, resolver.calls, "the refusal must precede attribution, not follow it")
}

// TestRefusalsNameCapabilitiesRatherThanInternalPlans guards the wording of
// every refusal this pass authors. An operator reading one of these has a
// misregistered hook and needs to know what capability is missing; an internal
// milestone or task reference tells them nothing they can act on.
func TestRefusalsNameCapabilitiesRatherThanInternalPlans(t *testing.T) {
	t.Parallel()

	deps, _, _ := workingDeps(t)
	_, err := lifecycle.Lower(context.Background(), deps, awaitedOccurrence(t))
	structured := requireStructured(t, err)

	rendered := structured.What + structured.Why + structured.Impact + structured.Fix
	for _, leaked := range []string{"M5", "milestone", "SLICE", "PROPOSAL", "aura-plugins-"} {
		assert.NotContains(t, rendered, leaked,
			"a refusal must describe the missing capability, not the plan that will deliver it")
	}
	assert.NotEmpty(t, structured.Fix, "a refusal that suggests nothing is not actionable")
}

// ─── The observation path ────────────────────────────────────────────────────

func TestLowerRecordsAnObservationOnceAndPassesOnOnlyTheWaist(t *testing.T) {
	t.Parallel()

	deps, recorder, resolver := workingDeps(t)
	event := observedOccurrence(t, "sess-01", `{"session_id":"sess-01"}`)

	outcome, err := lifecycle.Lower(context.Background(), deps, event)
	require.NoError(t, err)

	assert.Equal(t, lifecycle.Outcome{Kind: lifecycle.OutcomeRecorded, Record: lifecycle.RecordCreated}, outcome)
	assert.Equal(t, 1, resolver.calls)

	records := recorder.records()
	require.Len(t, records, 1)
	written := records[0]

	assert.Equal(t, testActor(t), written.Actor, "the record must carry the resolved attribution")
	assert.Equal(t, event.Origin(), written.Observed)
	assert.Equal(t, event.Semantics(), written.Semantics)
	assert.Equal(t, event.Origin().ReplayKey(), written.ReplayKey,
		"deduplication must key on what the host sent, not on anything derived here")

	// The clock's answer is normalised rather than stored as handed over, so
	// two hosts in two zones do not produce records that only a reader with a
	// timezone table can compare.
	assert.Equal(t, observedAt.UTC(), written.ObservedAt)
	assert.Equal(t, time.UTC, written.ObservedAt.Location())
}

// TestLowerReportsARepeatDeliveryAsAReplay is the property that makes hooks
// safe to fire more than once — and they do: retries, crash recovery, and a
// user re-running a session all deliver the same occurrence again.
//
// The third case is the half that makes the first two mean something. A pass
// that keyed on anything less specific than the payload would report every
// occurrence as a replay and pass the first two assertions while silently
// discarding real events.
func TestLowerReportsARepeatDeliveryAsAReplay(t *testing.T) {
	t.Parallel()

	deps, recorder, _ := workingDeps(t)
	const payload = `{"session_id":"sess-01","hook_event_name":"observed"}`

	first, err := lifecycle.Lower(context.Background(), deps, observedOccurrence(t, "sess-01", payload))
	require.NoError(t, err)
	assert.Equal(t, lifecycle.RecordCreated, first.Record)

	// Rebuilt from the same bytes rather than reused, so the replay key is
	// shown to come from the occurrence and not from an in-memory value.
	for range 3 {
		replay, err := lifecycle.Lower(context.Background(), deps, observedOccurrence(t, "sess-01", payload))
		require.NoError(t, err)
		assert.Equal(t, lifecycle.Outcome{Kind: lifecycle.OutcomeRecorded, Record: lifecycle.RecordReplayed}, replay)
	}
	require.Len(t, recorder.records(), 1, "repeat deliveries must not accumulate records")

	// Two occurrences that share every waist value but differ in the bytes the
	// host sent are genuinely different occurrences — the same session doing
	// the same thing twice. Keying on anything derived from the waist alone
	// would collapse them and silently discard the second.
	sameSession, err := lifecycle.Lower(context.Background(), deps,
		observedOccurrence(t, "sess-01", `{"session_id":"sess-01","hook_event_name":"observed","seq":2}`))
	require.NoError(t, err)
	assert.Equal(t, lifecycle.RecordCreated, sameSession.Record)

	// And a different session is different again.
	otherSession, err := lifecycle.Lower(context.Background(), deps,
		observedOccurrence(t, "sess-02", `{"session_id":"sess-02","hook_event_name":"observed"}`))
	require.NoError(t, err)
	assert.Equal(t, lifecycle.RecordCreated, otherSession.Record)

	assert.Len(t, recorder.records(), 3, "distinct occurrences must not be collapsed into one record")
}

// ─── Attribution precedes the write ──────────────────────────────────────────

// TestLowerAttributesBeforeItWrites is why step order is stated in the pass's
// doc comment rather than left to taste: if the write came first, a failure to
// attribute would leave a record of an occurrence observed by nobody.
func TestLowerAttributesBeforeItWrites(t *testing.T) {
	t.Parallel()

	unregistered := errors.New("no automaton is registered to handle this event")
	recorder := newReplayRecorder()
	deps := lifecycle.Deps{
		Recorder: recorder,
		Actors:   &fixedResolver{err: unregistered},
		Clock:    frozenClock,
	}

	outcome, err := lifecycle.Lower(context.Background(), deps, observedOccurrence(t, "sess-01", `{"a":1}`))

	require.ErrorIs(t, err, unregistered,
		"the resolver's own diagnostic must reach the operator; it is the only text that says what to register")
	assert.Equal(t, lifecycle.Outcome{}, outcome)
	assert.Empty(t, recorder.records(), "a failed attribution must leave nothing behind")
}

// TestLowerRefusesAnEmptyAttribution covers a resolver that reports success and
// returns nothing. The resolver owes a non-zero identity, but a pass that
// trusts its collaborators is not enforcing anything: an empty attribution
// would be written as a record nobody can be answerable for.
func TestLowerRefusesAnEmptyAttribution(t *testing.T) {
	t.Parallel()

	recorder := newReplayRecorder()
	deps := lifecycle.Deps{
		Recorder: recorder,
		Actors:   &fixedResolver{}, // zero identity, no error
		Clock:    frozenClock,
	}

	outcome, err := lifecycle.Lower(context.Background(), deps, observedOccurrence(t, "sess-01", `{"a":1}`))

	structured := requireStructured(t, err)
	assert.Contains(t, structured.What, "No one could be credited")
	assert.Equal(t, lifecycle.Outcome{}, outcome)
	assert.Empty(t, recorder.records())
}

// ─── The recorder's answer is checked, not assumed ───────────────────────────

func TestLowerPassesOnTheRecordersOwnFailure(t *testing.T) {
	t.Parallel()

	unwritable := errors.New("the recorded history is not writable")
	recorder := &answeringRecorder{err: unwritable}
	deps := lifecycle.Deps{Recorder: recorder, Actors: &fixedResolver{actor: testActor(t)}, Clock: frozenClock}

	outcome, err := lifecycle.Lower(context.Background(), deps, observedOccurrence(t, "sess-01", `{"a":1}`))

	require.ErrorIs(t, err, unwritable,
		"the recorder's own diagnostic must reach the operator; only it knows whether the store was unreachable, unmigrated, or read-only")
	assert.Equal(t, lifecycle.Outcome{}, outcome)
	assert.Equal(t, 1, recorder.calls)
}

// TestLowerRefusesAnUndecidedRecordOutcome covers a recorder that reports
// success without saying what it did. Reporting that as a successful record
// would tell the caller an occurrence is stored when nothing establishes that.
func TestLowerRefusesAnUndecidedRecordOutcome(t *testing.T) {
	t.Parallel()

	recorder := &answeringRecorder{} // zero outcome, no error
	deps := lifecycle.Deps{Recorder: recorder, Actors: &fixedResolver{actor: testActor(t)}, Clock: frozenClock}

	outcome, err := lifecycle.Lower(context.Background(), deps, observedOccurrence(t, "sess-01", `{"a":1}`))

	structured := requireStructured(t, err)
	assert.Contains(t, structured.What, "not clear whether")
	assert.Equal(t, lifecycle.Outcome{}, outcome)
}

// ─── The invariant that makes this a middle-end ──────────────────────────────

// TestLowerNeedsNothingFromTheBackendView pins the capability boundary in the
// only way a test can: [lifecycle.Deps] is the pass's entire supply of outside
// knowledge, and it offers no route to the pinned per-host table. Anything that
// varies by host has to arrive through the resolver, where it is one injected
// collaborator rather than a branch.
//
// The greppable half of the invariant — that lower.go never mentions
// BackendView or a host name — is checked outside the test binary, because a
// test cannot read its own package's source without asserting on a build
// artefact.
func TestLowerNeedsNothingFromTheBackendView(t *testing.T) {
	t.Parallel()

	deps := reflect.TypeOf(lifecycle.Deps{})
	declared := make([]string, 0, deps.NumField())
	for index := range deps.NumField() {
		declared = append(declared, deps.Field(index).Name)
	}
	assert.Equal(t, []string{"Recorder", "Actors", "Clock"}, declared,
		"Deps is the pass's whole window onto the outside world; widening it is how host-specific behaviour gets back in")

	// The resolver is handed coordinates, not a host name and not a table, so
	// the pass never has to know which host it is talking about in order to
	// ask who observed something.
	resolverArgument := reflect.TypeOf((*lifecycle.ActorResolver)(nil)).Elem().Method(0).Type.In(1)
	assert.Equal(t, reflect.TypeOf(lifecycle.Origin{}), resolverArgument)
}
