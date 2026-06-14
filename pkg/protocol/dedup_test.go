package protocol_test

import (
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/google/uuid"
)

// DedupKey must be deterministic: identical inputs always yield the identical
// key, so a crash-replay of the same emission collapses onto one row.
func TestDedupKey_Deterministic(t *testing.T) {
	t.Parallel()
	a := protocol.DedupKey("epoch-1", "code-review", "PhaseTransition", "3")
	b := protocol.DedupKey("epoch-1", "code-review", "PhaseTransition", "3")
	if a != b {
		t.Fatalf("DedupKey not deterministic: %q != %q", a, b)
	}
}

// Distinct epochs at the same (phase, kind, stepSeq) must produce distinct keys.
// step_seq resets per workflow, so without the epoch folded into the hashed name
// two different epochs would false-dedup against each other.
func TestDedupKey_CrossEpochDistinct(t *testing.T) {
	t.Parallel()
	a := protocol.DedupKey("epoch-A", "code-review", "PhaseTransition", "1")
	b := protocol.DedupKey("epoch-B", "code-review", "PhaseTransition", "1")
	if a == b {
		t.Fatalf("distinct epochs produced the same DedupKey %q — cross-epoch false dedup", a)
	}
}

// Each field independently affects the key — proves the name is built from all
// four components, not a subset.
func TestDedupKey_EachFieldMatters(t *testing.T) {
	t.Parallel()
	base := protocol.DedupKey("e", "p", "k", "s")
	variants := map[string]string{
		"epoch":   protocol.DedupKey("E", "p", "k", "s"),
		"phase":   protocol.DedupKey("e", "P", "k", "s"),
		"kind":    protocol.DedupKey("e", "p", "K", "s"),
		"stepSeq": protocol.DedupKey("e", "p", "k", "S"),
	}
	for field, v := range variants {
		if v == base {
			t.Errorf("changing %s did not change the key (%q) — field is not part of the derivation", field, base)
		}
	}
}

// Field order and the "/" separator are part of the contract: the components
// must not be ambiguously concatenable. "a/b" vs "ab" with a shifted boundary
// must not collide.
func TestDedupKey_SeparatorStability(t *testing.T) {
	t.Parallel()
	// If the separator were absent, ("ab","c",..) and ("a","bc",..) would collide.
	x := protocol.DedupKey("ab", "c", "kind", "1")
	y := protocol.DedupKey("a", "bc", "kind", "1")
	if x == y {
		t.Fatal("separator ambiguity: differently-bounded fields produced the same key")
	}
}

// The key must be a well-formed, version-5 (name-based SHA-1) UUID string —
// this is the storage contract both forensic tiers rely on.
func TestDedupKey_IsUUIDv5(t *testing.T) {
	t.Parallel()
	key := protocol.DedupKey("epoch-1", "handoff", "SliceStarted", "7")
	parsed, err := uuid.Parse(key)
	if err != nil {
		t.Fatalf("DedupKey returned a non-UUID string %q: %v", key, err)
	}
	if parsed.Version() != 5 {
		t.Errorf("DedupKey UUID version = %d, want 5 (name-based)", parsed.Version())
	}
}

// Pinned golden value: the namespace + name encoding must never silently change,
// or already-recorded dedup keys would stop matching new emissions. If this
// assertion fails, the derivation changed and existing forensic rows are at risk.
func TestDedupKey_PinnedGolden(t *testing.T) {
	t.Parallel()
	got := protocol.DedupKey("epoch-1", "code-review", "PhaseTransition", "3")
	const exp = "a5e53398-e14b-560f-a24b-d6895c3e16d1"
	if got != exp {
		t.Fatalf("DedupKey derivation drifted: got %q, want %q", got, exp)
	}
}
