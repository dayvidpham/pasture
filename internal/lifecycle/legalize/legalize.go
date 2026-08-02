// Package legalize classifies verified lifecycle events into pure semantic
// terminals. It neither exercises authority nor performs durable writes.
package legalize

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Legalized is the immutable consultation terminal for a gate event.
type Legalized struct {
	constructed bool
}

func newLegalized() Legalized { return Legalized{constructed: true} }

// IsValid reports whether the value was constructed by the legalization stage.
func (l Legalized) IsValid() bool { return l.constructed }

// AuthorityExercised reports whether legalization exercised authority. M1
// legalization is statically non-authoritative.
func (l Legalized) AuthorityExercised() bool { return false }

// MarshalJSON returns the canonical consultation legalization evidence.
func (l Legalized) MarshalJSON() ([]byte, error) {
	if !l.IsValid() {
		return nil, fmt.Errorf("marshal lifecycle legalization: the Legalized value was not constructed by legalize.Event; no consultation evidence was emitted; pass the gate result returned by legalize.Event")
	}
	return []byte(`{"authority":"not-exercised"}`), nil
}

// ConsultationLegalized implements waist.ConsultationLegalized.
func (Legalized) ConsultationLegalized() {}

// NoAuthority is the immutable terminal for an explicit human response, which
// this stage observes without acquiring or exercising human authority.
type NoAuthority struct {
	constructed bool
}

func newNoAuthority() NoAuthority { return NoAuthority{constructed: true} }

// IsValid reports whether the value was constructed by the legalization stage.
func (n NoAuthority) IsValid() bool { return n.constructed }

// Result is one constructor-validated legalization terminal discriminated by
// the runtime event semantic.
type Result struct {
	semantic    runtime.EventSemantic
	legalized   Legalized
	noAuthority NoAuthority
	constructed bool
}

// Semantic returns the runtime semantic that discriminates this result.
func (r Result) Semantic() runtime.EventSemantic {
	if !r.IsValid() {
		return 0
	}
	return r.semantic
}

// Legalized returns the gate terminal when this is a gate-consultation result.
func (r Result) Legalized() (Legalized, bool) {
	if !r.IsValid() || r.semantic != runtime.SemanticGateConsultation || !r.legalized.IsValid() {
		return Legalized{}, false
	}
	return r.legalized, true
}

// NoAuthority returns the typed terminal when this is an explicit human
// response result.
func (r Result) NoAuthority() (NoAuthority, bool) {
	if !r.IsValid() || r.semantic != runtime.SemanticExplicitHumanResponse || !r.noAuthority.IsValid() {
		return NoAuthority{}, false
	}
	return r.noAuthority, true
}

// IsValid reports whether the result contains exactly the terminal allowed by
// its runtime semantic.
func (r Result) IsValid() bool {
	if !r.constructed || !r.semantic.IsValid() {
		return false
	}
	switch r.semantic {
	case runtime.SemanticObservation:
		return !r.legalized.IsValid() && !r.noAuthority.IsValid()
	case runtime.SemanticGateConsultation:
		return r.legalized.IsValid() && !r.noAuthority.IsValid()
	case runtime.SemanticExplicitHumanResponse:
		return !r.legalized.IsValid() && r.noAuthority.IsValid()
	default:
		return false
	}
}

// Event is the sole production entry point for lifecycle legalization.
func Event(event waist.L2) (Result, error) {
	if !event.IsValid() || !event.Semantics().IsValid() {
		return Result{}, fmt.Errorf("legalize lifecycle event: the waist L2 value is invalid because it was not constructed by EventBinding.NewEvent; no legalization terminal was produced; build and verify the native event before calling legalize.Event")
	}
	semantic := event.Semantics().Semantic()
	switch semantic {
	case runtime.SemanticObservation:
		return Result{semantic: semantic, constructed: true}, nil
	case runtime.SemanticGateConsultation:
		return Result{semantic: semantic, legalized: newLegalized(), constructed: true}, nil
	case runtime.SemanticExplicitHumanResponse:
		return Result{semantic: semantic, noAuthority: newNoAuthority(), constructed: true}, nil
	default:
		return Result{}, fmt.Errorf("legalize lifecycle event: runtime semantic %d is unknown or impossible at the legalization boundary; no terminal was produced; update the reviewed runtime semantic contract and legalization switch together before retrying", uint8(semantic))
	}
}

var _ waist.ConsultationLegalized = Legalized{}
