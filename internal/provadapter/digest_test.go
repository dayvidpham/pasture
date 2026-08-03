package provadapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

func mustDigest(t *testing.T, command []byte) ir.CanonicalCommandDigest {
	t.Helper()
	d, err := ir.DigestCanonicalCommand(command)
	if err != nil {
		t.Fatalf("DigestCanonicalCommand: %v", err)
	}
	return d
}

// TestCommandDigestBytes_BytePreserving proves the converted bytes are exactly the
// 32 SHA-256 bytes the ir digest carries (no re-hash, no re-canonicalization): the
// output equals a direct hex-decode of the digest's canonical text, and equals a
// direct sha256.Sum256 of the same command bytes ir digested.
func TestCommandDigestBytes_BytePreserving(t *testing.T) {
	command := []byte(`{"verb":"close","task":"aura-plugins--x","reason":"done"}`)
	d := mustDigest(t, command)

	got, err := CommandDigestBytes(d)
	if err != nil {
		t.Fatalf("CommandDigestBytes: %v", err)
	}
	if len(got) != sha256.Size {
		t.Fatalf("digest length = %d, want %d", len(got), sha256.Size)
	}

	// Byte-identical to the hex embedded in the digest's own textual form.
	wantHex := d.String()[len("sha256:"):]
	wantBytes, _ := hex.DecodeString(wantHex)
	if !bytes.Equal(got, wantBytes) {
		t.Fatalf("bytes diverge from digest text: got %x want %x", got, wantBytes)
	}

	// ir.DigestCanonicalCommand hashes the raw command bytes as-is (no
	// canonicalization), so the adapter output matches a direct SHA-256 of the same
	// bytes — the byte-preservation the #14 contract requires.
	direct := sha256.Sum256(command)
	if !bytes.Equal(got, direct[:]) {
		t.Fatalf("bytes diverge from direct SHA-256: got %x want %x", got, direct[:])
	}
}

// TestCommandDigestBytes_CrossProcessEquality proves the same command yields
// byte-identical digest bytes across independent digest computations, which is
// what makes a pinned-OperationID replay match across processes.
func TestCommandDigestBytes_CrossProcessEquality(t *testing.T) {
	command := []byte(`{"verb":"start","task":"aura-plugins--y"}`)
	a, err := CommandDigestBytes(mustDigest(t, command))
	if err != nil {
		t.Fatalf("CommandDigestBytes a: %v", err)
	}
	b, err := CommandDigestBytes(mustDigest(t, append([]byte(nil), command...)))
	if err != nil {
		t.Fatalf("CommandDigestBytes b: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("equal commands produced diverging digest bytes: %x vs %x", a, b)
	}
}

// TestCommandDigestBytes_ChangeDetected proves any change to the command bytes
// changes the digest bytes, so a reused OperationID presenting a changed command
// surfaces as a §11 conflict rather than a silent match.
func TestCommandDigestBytes_ChangeDetected(t *testing.T) {
	base, err := CommandDigestBytes(mustDigest(t, []byte(`{"verb":"close","task":"t1","reason":"a"}`)))
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	changed, err := CommandDigestBytes(mustDigest(t, []byte(`{"verb":"close","task":"t1","reason":"b"}`)))
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if bytes.Equal(base, changed) {
		t.Fatalf("changed command produced identical digest bytes")
	}
}

// TestCommandDigestBytes_RejectsZero proves an empty/zero digest is rejected.
func TestCommandDigestBytes_RejectsZero(t *testing.T) {
	if _, err := CommandDigestBytes(ir.CanonicalCommandDigest{}); err == nil {
		t.Fatalf("expected rejection of the zero CanonicalCommandDigest")
	}
}
