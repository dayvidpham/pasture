// Package middleend derives provider-independent lifecycle effects and host
// responses from verified lifecycle events.
package middleend

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// Derivation is the constructor-owned result of deriving a verified event.
// Its effects are PREPARED, non-durable evidence: one interpreted.v2-shaped
// prepared effect and its optional consultation, carrying verified L2
// semantic arms, the producer contract and manifest, but no journal refs. The
// common receipt boundary finalizes them to occurrence.v2, interpreted.v3
// and a rebound consultation. The prepared interpreter coordinate pins the
// compiled interpretation entry that executed this derivation; the finalizer
// ensures the same compiled artifact (never a store-latest substitute), and
// production tests prove the committed ref resolves to that exact body.
type Derivation struct {
	effects     []provenance.Effect
	response    backend.HostResponse
	interpreter definitions.Document
	constructed bool
}

// Derive returns the canonical effects and optional host response for event.
// The metamodel coordinate is stamped onto the interpreted.v2 evidence, binding
// this interpretation to a versioned, journaled interpretation vocabulary. The
// coordinate is compile-time data (metamodel.Active()), so Derive stays pure.
func Derive(event waist.L2, manifest model.LifecycleMetamodelManifest) (Derivation, error) {
	result, err := legalize.Event(event)
	if err != nil {
		return Derivation{}, err
	}
	interpreted, err := receipt.NewInterpreted(event, event.Origin().Contract(), manifest)
	if err != nil {
		return Derivation{}, err
	}
	interpreter, err := definitions.NewInterpreter()
	if err != nil {
		return Derivation{}, err
	}
	derivation := Derivation{
		effects:     []provenance.Effect{interpreted.Effect()},
		interpreter: interpreter,
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

// PreparedInterpreter returns the compiled interpreter document coordinate
// pinned at derivation time: the artifact entry that executed NewEvent,
// Derive and NewInterpreted for this value. Production tests prove the
// finalized envelope resolves to this exact body, so the committed semantic
// envelope identifies the implementation that actually ran.
func (d Derivation) PreparedInterpreter() definitions.Document {
	if !d.IsValid() {
		return definitions.Document{}
	}
	return d.interpreter
}

// IsValid reports whether this value was constructed by Derive.
func (d Derivation) IsValid() bool {
	return d.constructed && len(d.effects) > 0
}
