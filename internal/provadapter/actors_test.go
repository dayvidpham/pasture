package provadapter

import (
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
)

// TestOrdinalActorID_Goldens pins the fixed big-endian ordinal-UUID wire form for
// the reservation boundary ordinals 0, 1, and 1023, and rejects 1024.
func TestOrdinalActorID_Goldens(t *testing.T) {
	cases := []struct {
		ordinal  uint64
		wantUUID string
	}{
		{0, "00000000-0000-0000-0000-000000000000"},
		{1, "00000000-0000-0000-0000-000000000001"},
		{1023, "00000000-0000-0000-0000-0000000003ff"},
	}
	for _, tc := range cases {
		id, err := OrdinalActorID(tc.ordinal)
		if err != nil {
			t.Fatalf("OrdinalActorID(%d): %v", tc.ordinal, err)
		}
		if id.Namespace != PastureSystemNamespace {
			t.Fatalf("ordinal %d namespace = %q, want %q", tc.ordinal, id.Namespace, PastureSystemNamespace)
		}
		if id.UUID.String() != tc.wantUUID {
			t.Fatalf("ordinal %d uuid = %q, want %q", tc.ordinal, id.UUID.String(), tc.wantUUID)
		}
	}
	if _, err := OrdinalActorID(1024); err == nil {
		t.Fatalf("expected ordinal 1024 to be rejected (out of reserved range)")
	}
}

// TestPastureSystemDefault confirms ordinal zero is the all-zero-UUID default.
func TestPastureSystemDefault(t *testing.T) {
	def := PastureSystemDefaultActorID()
	if def.UUID != uuid.Nil {
		t.Fatalf("default UUID = %q, want the nil UUID", def.UUID.String())
	}
	zero, err := OrdinalActorID(0)
	if err != nil {
		t.Fatalf("OrdinalActorID(0): %v", err)
	}
	if zero != def {
		t.Fatalf("ordinal zero %+v != default %+v", zero, def)
	}
}

// TestValidateActorID proves the zero ActorID rejects while a namespaced all-zero
// UUID (the default identity) is valid.
func TestValidateActorID(t *testing.T) {
	if err := ValidateActorID(provenance.ActorID{}); err == nil {
		t.Fatalf("expected zero ActorID to be rejected")
	}
	if err := ValidateActorID(PastureSystemDefaultActorID()); err != nil {
		t.Fatalf("namespaced nil-UUID actor should be valid: %v", err)
	}
}

func openMemoryJournal(t *testing.T) (provenance.Tracker, provenance.JournalAPI) {
	t.Helper()
	tr, err := provenance.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr, tr.Journal()
}

// TestActivate_Fresh activates against a real (in-memory) Provenance store from
// empty and registers the pasture-system namespace claim (the fixed ordinal-zero
// actor seed is deferred; see the DELIVERED-SURFACE GAP note on ActivatePastureSystem).
func TestActivate_Fresh(t *testing.T) {
	_, j := openMemoryJournal(t)

	res, err := ActivatePastureSystem(j)
	if err != nil {
		t.Fatalf("ActivatePastureSystem: %v", err)
	}
	if !res.Fresh {
		t.Fatalf("expected Fresh=true on first activation")
	}
	if res.DefaultActorID != PastureSystemDefaultActorID() {
		t.Fatalf("default actor drift: %+v", res.DefaultActorID)
	}
	// DELIVERED-SURFACE GAP: the fixed ordinal-zero actor row cannot be seeded
	// against Provenance main@7b3451a (fixed_actor_manifest_entries.actor_id FK to
	// agents(id) + no fixed-id agent creation), so activation reserves the claim
	// only and reports the seed as deferred.
	if res.DefaultActorSeeded {
		t.Fatalf("DefaultActorSeeded should be false against the delivered surface")
	}

	claims, err := j.NamespaceClaims()
	if err != nil {
		t.Fatalf("NamespaceClaims: %v", err)
	}
	var found *provenance.ActorNamespaceClaim
	for i := range claims {
		if claims[i].Namespace == PastureSystemNamespace {
			found = &claims[i]
		}
	}
	if found == nil {
		t.Fatalf("pasture-system claim not registered")
	}
	if !found.Equal(PastureSystemClaim()) {
		t.Fatalf("registered claim %+v != manifest %+v", *found, PastureSystemClaim())
	}
}

// TestActivate_ExactRepeatInert proves a second exact activation is inert
// (Fresh=false) and does not error — idempotent re-activation.
func TestActivate_ExactRepeatInert(t *testing.T) {
	_, j := openMemoryJournal(t)

	if _, err := ActivatePastureSystem(j); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	res, err := ActivatePastureSystem(j)
	if err != nil {
		t.Fatalf("second activation should be inert: %v", err)
	}
	if res.Fresh {
		t.Fatalf("expected Fresh=false on exact re-activation")
	}
}

// TestActivate_DriftRejected proves a stored pasture-system claim that differs
// from the manifest aborts activation.
func TestActivate_DriftRejected(t *testing.T) {
	_, j := openMemoryJournal(t)

	drift := provenance.ActorNamespaceClaim{
		Namespace:  PastureSystemNamespace,
		ClaimantID: PastureSystemNamespace,
		Range:      provenance.UUIDRange{Min: [16]byte{}, Max: provenance.BigEndianUUID(10)},
		Codec:      provenance.OrdinalV1CodecName,
	}
	if err := j.RegisterNamespaceClaim(drift); err != nil {
		t.Fatalf("seed drifted claim: %v", err)
	}
	if _, err := ActivatePastureSystem(j); err == nil {
		t.Fatalf("expected drift rejection when the stored claim differs from the manifest")
	}
}

// TestActivate_NilJournal rejects a nil journal.
func TestActivate_NilJournal(t *testing.T) {
	if _, err := ActivatePastureSystem(nil); err == nil {
		t.Fatalf("expected nil-journal rejection")
	}
}
