// Package middleend derives provider-independent lifecycle effects and host
// responses from verified lifecycle events.
package middleend

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// Derivation is the constructor-owned result of deriving a verified event.
type Derivation struct {
	effects     []provenance.Effect
	response    backend.HostResponse
	constructed bool
}

// Derive returns the canonical effects and optional host response for event.
// The codebook coordinate is stamped onto the interpreted.v2 evidence, binding
// this interpretation to a versioned, journaled interpretation vocabulary. The
// coordinate is compile-time data (codebook.Active()), so Derive stays pure.
func Derive(event waist.L2, book model.CodebookCoordinate) (Derivation, error) {
	result, err := legalize.Event(event)
	if err != nil {
		return Derivation{}, err
	}
	interpreted, err := receipt.NewInterpreted(event, event.Origin().Contract(), book)
	if err != nil {
		return Derivation{}, err
	}
	derivation := Derivation{
		effects:     []provenance.Effect{interpreted.Effect()},
		constructed: true,
	}
	switch result.Semantic() {
	case runtime.SemanticObservation:
		return derivation, nil
	case runtime.SemanticGateConsultation:
		legalized, ok := result.Legalized()
		if !ok {
			return Derivation{}, fmt.Errorf("derive lifecycle gate: legalize.Event returned no valid Legalized terminal for a gate consultation; no derivation was produced; keep the runtime semantic and legalization result synchronized")
		}
		consultation, response, err := backend.BuildConsultation(interpreted, legalized)
		if err != nil {
			return Derivation{}, err
		}
		derivation.effects = append(derivation.effects, consultation.Effect())
		derivation.response = response
		return derivation, nil
	case runtime.SemanticExplicitHumanResponse:
		noAuthority, ok := result.NoAuthority()
		if !ok || !noAuthority.IsValid() {
			return Derivation{}, fmt.Errorf("derive explicit human response: legalize.Event returned no valid NoAuthority terminal; no derivation was produced because this boundary cannot exercise human authority; preserve the typed NoAuthority result from legalize.Event")
		}
		return derivation, nil
	default:
		return Derivation{}, fmt.Errorf("derive lifecycle event: legalize.Event returned unsupported semantic %d; no derivation was produced; extend the reviewed legalization and middle-end contracts together before retrying", result.Semantic())
	}
}

// Effects returns a defensive copy of the effects in canonical order.
func (d Derivation) Effects() []provenance.Effect {
	if !d.IsValid() {
		return nil
	}
	effects := make([]provenance.Effect, len(d.effects))
	for index, effect := range d.effects {
		effects[index] = effect
		effects[index].ContentDigest = append([]byte(nil), effect.ContentDigest...)
		effects[index].Payload = append([]byte(nil), effect.Payload...)
	}
	return effects
}

// Response returns the optional constructor-built host response.
func (d Derivation) Response() backend.HostResponse {
	if !d.IsValid() {
		return backend.HostResponse{}
	}
	return d.response
}

// IsValid reports whether this value was constructed by Derive.
func (d Derivation) IsValid() bool {
	return d.constructed && len(d.effects) > 0
}
