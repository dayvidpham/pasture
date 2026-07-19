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

// ActivationResult reports how ActivatePastureSystem resolved. Fresh is true when
// the namespace claim was newly registered; false when an exact existing claim
// made activation inert. DefaultActorID is always the pasture-system/default
// identity on success. DefaultActorSeeded reports whether the manifest-v1
// ordinal-zero fixed actor row was seeded — see the DELIVERED-SURFACE GAP note on
// ActivatePastureSystem for why it is currently always false against main@7b3451a.
type ActivationResult struct {
	Fresh              bool
	DefaultActorSeeded bool
	DefaultActorID     provenance.ActorID
}

// ActivatePastureSystem registers the pasture-system namespace claim and reserves
// fixed-UUID ordinals 0..1023 over a real Provenance actor-namespace registry,
// idempotently. Fresh absence registers the claim. An exact existing claim is
// inert (Fresh=false). A stored claim of the same namespace that differs in any
// field (owner/range/codec drift) aborts with an actionable error and writes
// nothing further. Ordinals 1..1023 are never materialized.
//
// DELIVERED-SURFACE GAP (pasture#14 acceptance vs Provenance main@7b3451a).
// Issue #14 additionally requires seeding manifest-v1 ordinal zero as a fixed
// pasture-system/default software_agent. The delivered Provenance schema declares
// fixed_actor_manifest_entries.actor_id as a FOREIGN KEY to agents(id), but the
// released public surface exposes no way to create an agent with a caller-supplied
// fixed ActorID: RegisterSoftwareAgent always mints a fresh UUIDv7, so the
// all-zero-UUID default actor row cannot be created, and RegisterFixedActorEntry
// for it fails a FOREIGN KEY constraint. The fixed-actor SEED is therefore
// deferred (DefaultActorSeeded=false) pending a Provenance surface that can
// register a fixed-id system agent (or a manifest seam that creates the agent row
// atomically). PastureSystemDefaultEntry() below is the exact entry to register
// once that surface exists; the claim + range reservation this function performs
// is the maximum a #14 adapter can commit against the released API today.
func ActivatePastureSystem(j provenance.JournalAPI) (ActivationResult, error) {
	if j == nil {
		return ActivationResult{}, errors.New(
			"provadapter: cannot activate pasture-system — what: the Provenance JournalAPI is nil; " +
				"why: activation registers a namespace claim through the journal registry; " +
				"where: internal/provadapter ActivatePastureSystem; when: at startup; impact: no reservation " +
				"is made; fix: pass Tracker.Journal() from an open Provenance tracker")
	}
	want := PastureSystemClaim()

	existing, err := j.NamespaceClaims()
	if err != nil {
		return ActivationResult{}, fmt.Errorf(
			"provadapter: activate pasture-system: read existing namespace claims: %w", err)
	}
	var current *provenance.ActorNamespaceClaim
	for i := range existing {
		if existing[i].Namespace == PastureSystemNamespace {
			current = &existing[i]
			break
		}
	}

	fresh := false
	if current == nil {
		if err := j.RegisterNamespaceClaim(want); err != nil {
			return ActivationResult{}, fmt.Errorf(
				"provadapter: activate pasture-system: register namespace claim: %w", err)
		}
		fresh = true
	} else if !current.Equal(want) {
		return ActivationResult{}, fmt.Errorf(
			"provadapter: activate pasture-system: a %q namespace claim already exists but differs from the "+
				"manifest — what: stored claim %+v does not equal the expected %+v; why: the reserved system "+
				"namespace must have exactly the manifest-v1 owner, [0, %d] range, and %q codec, and a drifted "+
				"claim signals corruption or a foreign claimant; where: internal/provadapter ActivatePastureSystem; "+
				"when: activation drift check; impact: nothing further is written; fix: reconcile the stored claim "+
				"with the manifest or investigate the conflicting claimant",
			PastureSystemNamespace, *current, want, pastureSystemMaxOrdinal, provenance.OrdinalV1CodecName)
	}

	// The manifest-v1 ordinal-zero fixed-actor SEED is deferred: see the
	// DELIVERED-SURFACE GAP note above. Ordinals 1..1023 remain unmaterialized.
	return ActivationResult{Fresh: fresh, DefaultActorSeeded: false, DefaultActorID: PastureSystemDefaultActorID()}, nil
}
