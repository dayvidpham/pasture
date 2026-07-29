package lifecycle

import "github.com/dayvidpham/pasture/internal/runtime"

// BackendView opens the target-detail side of an [Event]: the pinned table
// describing how to speak back to the specific host it came from.
//
// It is BACKEND-ONLY. The lowering pass must never reference this symbol, nor
// call anything that does.
//
// The rule needs a name because the alternative fails quietly. If the target
// behaviour were reachable as an ordinary accessor on [Origin], keeping the
// lowering pass target-agnostic would depend on every future author declining
// to reach for a method that was right there and did exactly what they wanted —
// and the resulting host-specific branch would look like ordinary code. Go
// cannot make the restriction compiler-enforced across packages without
// ceremony that costs more than it buys. What this symbol buys instead is
// collapsing the invariant to one mechanically checkable question: does the
// lowering pass, or anything it calls, mention BackendView?
//
// The behaviour it exposes is retained from bind time rather than re-derived,
// because internal/runtime deliberately offers no native-name lookup — there is
// no way to ask "what does this host call this event" after the fact, by design.
//
// A [Backend] taken from a zero Event is itself zero; check [Backend.IsValid].
func BackendView(e Event) Backend {
	return Backend{behaviour: e.origin.behaviour, constructed: e.constructed}
}

// Backend is the target-detail view of one occurrence. It is opaque and can
// only be obtained from [BackendView].
type Backend struct {
	behaviour   runtime.LifecycleEventMapping
	constructed bool
}

// TargetBehaviour returns the immutable pinned mapping for this occurrence:
// its native wire surface, the mutation it permits, its handler ordering and
// reconciliation, its failure behaviour, and its stop-loop policy.
//
// Every one of these describes how to speak back to one specific host. None of
// them is target-agnostic, which is exactly why they live here and not in
// [Semantics].
func (b Backend) TargetBehaviour() runtime.LifecycleEventMapping { return b.behaviour }

// IsValid reports whether this view came from a constructed [Event].
func (b Backend) IsValid() bool { return b.constructed }
