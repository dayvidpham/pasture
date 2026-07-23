package provadapter

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
)

// actors.go activates and addresses the reserved pasture-system actor namespace
// over the real Provenance actor-namespace registry (contract §7). Provenance
// stores only the generic claim (claimant, fixed-UUID range, codec) and the fixed
// manifest entries; the pasture-system ordinal->identity meaning lives HERE, in
// the consumer, exactly as §7 intends.
//
// Manifest v1: the pasture-system namespace claims fixed-UUID ordinals 0..1023
// under the default big-endian ordinal codec. Only ordinal zero is materialized —
// pasture-system/default, an all-zero fixed UUID, kind software_agent — the
// canonical default Pasture system actor. Ordinals 1..1023 stay reserved and
// unmaterialized so future fixed system identities cannot collide with an
// ordinary UUIDv7 actor (a UUIDv7 has version/variant bits set and can never land
// in [0, 1023]).

const (
	// PastureSystemNamespace is the reserved namespace claimed at activation.
	PastureSystemNamespace = "pasture-system"
	// PastureSystemDefaultName is manifest-v1 ordinal zero's registered name.
	PastureSystemDefaultName = "pasture-system/default"
	// PastureSystemReservedOrdinals is the count of reserved ordinals (0..1023).
	PastureSystemReservedOrdinals = 1024
	// pastureSystemMaxOrdinal is the inclusive top of the reserved range.
	pastureSystemMaxOrdinal = PastureSystemReservedOrdinals - 1
	pastureSystemVersion    = "1"
	pastureSystemSource     = "pasture"
)

// PastureSystemRange is the inclusive [0, 1023] fixed-UUID range the pasture-system
// namespace claims: Min is the all-zero UUID (ordinal 0) and Max is ordinal 1023
// as a 16-byte big-endian value.
var PastureSystemRange = provenance.UUIDRange{
	Min: [16]byte{},
	Max: provenance.BigEndianUUID(pastureSystemMaxOrdinal),
}

// PastureSystemClaim is the exact ActorNamespaceClaim activation registers. Its
// fields are fixed, so an exact re-activation is idempotent and any drift in a
// stored claim of the same namespace is detectable field-by-field.
func PastureSystemClaim() provenance.ActorNamespaceClaim {
	return provenance.ActorNamespaceClaim{
		Namespace:  PastureSystemNamespace,
		ClaimantID: PastureSystemNamespace,
		Range:      PastureSystemRange,
		Codec:      provenance.OrdinalV1CodecName,
	}
}

// OrdinalActorID returns the fixed pasture-system ActorID for a reserved ordinal
// in [0, 1023]. The UUID is the ordinal rendered big-endian into 16 bytes with no
// version/variant bits, so ordinal zero is the all-zero (uuid.Nil) UUID. An
// ordinal at or beyond 1024 is rejected: it lies outside the claimed range.
func OrdinalActorID(ordinal uint64) (provenance.ActorID, error) {
	if ordinal > pastureSystemMaxOrdinal {
		return provenance.ActorID{}, fmt.Errorf(
			"provadapter: pasture-system ordinal %d is out of the reserved range [0, %d] — "+
				"what: the requested ordinal exceeds the manifest reservation; why: fixed system actors "+
				"must stay inside the claimed [0, %d] range so they cannot collide with a neighbouring "+
				"namespace or an ordinary UUIDv7 actor; where: internal/provadapter OrdinalActorID; "+
				"when: before addressing a fixed system actor; impact: no ActorID is produced; fix: request "+
				"an ordinal in [0, %d]",
			ordinal, pastureSystemMaxOrdinal, pastureSystemMaxOrdinal, pastureSystemMaxOrdinal)
	}
	fixed, err := provenance.OrdinalUUID(PastureSystemRange, ordinal)
	if err != nil {
		return provenance.ActorID{}, fmt.Errorf(
			"provadapter: encode pasture-system ordinal %d to its fixed UUID: %w", ordinal, err)
	}
	return provenance.ActorID{Namespace: PastureSystemNamespace, UUID: uuid.UUID(fixed)}, nil
}

// PastureSystemDefaultActorID is manifest-v1 ordinal zero: the all-zero-UUID
// pasture-system/default identity. It is the default committing actor for
// system-authored operations (e.g. legacy-baseline migration).
func PastureSystemDefaultActorID() provenance.ActorID {
	return provenance.ActorID{Namespace: PastureSystemNamespace, UUID: uuid.Nil}
}

// PastureSystemDefaultEntry is the fixed-actor manifest entry activation seeds for
// ordinal zero: pasture-system/default, kind software_agent.
func PastureSystemDefaultEntry() provenance.FixedActorEntry {
	return provenance.FixedActorEntry{
		ActorID:   PastureSystemDefaultActorID(),
		Namespace: PastureSystemNamespace,
		ActorKind: provenance.AgentKindSoftware,
		Name:      PastureSystemDefaultName,
	}
}

// PastureSystemDefaultRegistration is the complete atomic activation request for
// manifest-v1 ordinal zero. The manifest and software-agent names intentionally
// match so every public actor lookup returns the same durable identity.
func PastureSystemDefaultRegistration() provenance.FixedSoftwareAgentRegistration {
	return provenance.FixedSoftwareAgentRegistration{
		Claim:     PastureSystemClaim(),
		Entry:     PastureSystemDefaultEntry(),
		AgentName: PastureSystemDefaultName,
		Version:   pastureSystemVersion,
		Source:    pastureSystemSource,
	}
}

// ValidateActorID rejects a structurally invalid ActorID: the zero value (empty
// namespace) is never a usable identity, while a namespaced all-zero UUID (the
// pasture-system/default identity) is valid. This is the boundary check the
// adapter applies before handing an actor to a store call.
func ValidateActorID(id provenance.ActorID) error {
	if id.Namespace == "" {
		return fmt.Errorf(
			"provadapter: actor id %q is invalid — what: the ActorID has an empty namespace (the zero "+
				"value); why: an actor identity is namespace-scoped and a namespaceless actor cannot be "+
				"addressed or attributed; where: internal/provadapter ValidateActorID; when: before a store "+
				"call; impact: no operation runs under an unidentifiable actor; fix: supply a namespaced "+
				"ActorID (a namespaced all-zero UUID such as pasture-system/default is valid)",
			id.String())
	}
	return nil
}

// ActivationResult identifies the exact default actor installed by activation.
// Success means the namespace claim, software-agent rows, and manifest entry all
// exist atomically; callers do not need a race-prone fresh-versus-retry flag.
type ActivationResult struct {
	DefaultActorID provenance.ActorID
}

// ActivatePastureSystem atomically claims the pasture-system namespace, reserves
// fixed-UUID ordinals 0..1023, and installs only ordinal zero as the fixed
// pasture-system/default software agent. Exact retries are inert, claim-only
// persisted state is repaired, and any claim, actor, or manifest conflict aborts
// without partial writes. Ordinals 1..1023 remain unmaterialized.
func ActivatePastureSystem(tr provenance.Tracker) (ActivationResult, error) {
	if tr == nil {
		return ActivationResult{}, errors.New(
			"provadapter: cannot activate pasture-system — what: the Provenance Tracker is nil; " +
				"why: activation atomically registers the namespace, software agent, and manifest entry; " +
				"where: internal/provadapter ActivatePastureSystem; when: at startup; impact: no reservation " +
				"or default actor is created; fix: pass an open Provenance tracker")
	}

	agent, err := tr.RegisterFixedSoftwareAgent(PastureSystemDefaultRegistration())
	if err != nil {
		return ActivationResult{}, fmt.Errorf(
			"provadapter: atomically activate pasture-system default actor: %w", err)
	}
	if agent.ID != PastureSystemDefaultActorID() {
		return ActivationResult{}, fmt.Errorf(
			"provadapter: activate pasture-system returned actor %q instead of %q — what: the atomic registration returned an unexpected identity; why: the fixed actor response diverged from the manifest request; where: internal/provadapter ActivatePastureSystem; when: after activation commit; impact: Pasture will not bind journal operations to an ambiguous actor; fix: verify the pinned Provenance fixed-agent contract",
			agent.ID.String(), PastureSystemDefaultActorID().String())
	}
	return ActivationResult{DefaultActorID: agent.ID}, nil
}
