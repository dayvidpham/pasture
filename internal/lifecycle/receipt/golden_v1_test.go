package receipt_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// goldenV1InterpretedPayload is a FROZEN committed interpreted.v1 evidence
// payload from before the metamodel producer (M5). Its provenance is pinned by
// goldenV1InterpretedSHA256: interpreted.v1 is a read-only legacy kind, so this
// byte sequence must decode unchanged forever, with no in-place migration.
const goldenV1InterpretedPayload = `{"semantic":1,"identities":[{"kind":1,"value":"session-golden-pre-m5"}],"unresolved_facts":[],"contract":"claude-code/claude-code@2.1.210"}`

// goldenV1InterpretedSHA256 pins the golden fixture's provenance. A change to the
// golden bytes (a migration or accidental rewrite of committed v1 evidence)
// fails this pin.
const goldenV1InterpretedSHA256 = "2038445fa70b46043ef21e0105f3ee1c0fd2bc4f6ebe749de8ecf24170e46563"

// TestGoldenV1InterpretedDecodesUnchangedAfterM5 proves a pre-M5 committed
// interpreted.v1 record still decodes correctly after M5 and carries NO metamodel
// coordinate (Metamodel() reports false), so the read surface discloses "metamodel
// unresolved (pre-M5)" rather than inventing one. The SHA-256 pin guards against
// any silent migration of committed v1 evidence.
func TestGoldenV1InterpretedDecodesUnchangedAfterM5(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte(goldenV1InterpretedPayload))
	if got := hex.EncodeToString(sum[:]); got != goldenV1InterpretedSHA256 {
		t.Fatalf("golden v1 payload provenance drifted: sha256 = %s, pinned %s", got, goldenV1InterpretedSHA256)
	}

	decoded, err := receipt.DecodeInterpreted(model.InterpretationID(7), model.OccurrenceID(5), []byte(goldenV1InterpretedPayload))
	if err != nil {
		t.Fatalf("committed interpreted.v1 golden record failed to decode after M5: %v", err)
	}
	if decoded.JournalID() != 7 || decoded.OccurrenceID.JournalID() != 5 {
		t.Fatalf("decoded journal identities = (%d, %d), want (7, 5)", decoded.JournalID(), decoded.OccurrenceID.JournalID())
	}
	if decoded.Semantic() != runtime.SemanticObservation {
		t.Fatalf("decoded semantic = %v, want observation", decoded.Semantic())
	}
	if decoded.Contract().String() != "claude-code/claude-code@2.1.210" {
		t.Fatalf("decoded contract = %q, want claude-code/claude-code@2.1.210", decoded.Contract())
	}
	identities := decoded.Identities()
	if len(identities) != 1 || identities[0].Kind != runtime.IdentitySession || identities[0].Value != "session-golden-pre-m5" {
		t.Fatalf("decoded identities = %#v, want the single golden session identity", identities)
	}
	if manifest, ok := decoded.Metamodel(); ok {
		t.Fatalf("committed interpreted.v1 record reported a metamodel coordinate %#v, want none (pre-M5)", manifest)
	}

	// The v2 decoder must reject the v1 payload: v2 requires the metamodel member,
	// so the two kinds never cross-decode.
	if _, err := receipt.DecodeInterpretedV2(model.InterpretationID(7), model.OccurrenceID(5), []byte(goldenV1InterpretedPayload)); err == nil {
		t.Fatal("interpreted.v2 decoder accepted an interpreted.v1 golden payload")
	}
}
