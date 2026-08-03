package provadapter

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// digest.go converts a #43-produced canonical command digest into the raw
// command-digest bytes Provenance stores and compares (OperationInput.CommandDigest
// and the LookupCommitted lookup key are both []byte).
//
// Byte-preservation is the whole contract: pasture#14 must NOT re-canonicalize the
// command, must NOT hash anything itself, and must NOT expose a "caller digest"
// flag. The digest is produced ONCE by #43 over its canonical command bytes
// (ir.DigestCanonicalCommand) and #14 hands the SAME 32 SHA-256 bytes to both the
// committed-lookup and the Apply path, so a change to any typed command field
// yields a different digest (and thus a §11 conflict on reuse), and the same
// command produces byte-identical digests across processes.
//
// ir.CanonicalCommandDigest keeps its 32-byte sum unexported and exposes it only
// as its canonical text form "sha256:<64 lowercase hex>". Decoding that hex is an
// exact, lossless recovery of the original sum bytes — it introduces no second
// hashing and no re-canonicalization — so the returned bytes are byte-identical to
// the digest #43 computed.

// commandDigestHexPrefix is the algorithm tag on ir.CanonicalCommandDigest's
// textual form. Provenance stores the raw digest bytes without the tag.
const commandDigestHexPrefix = "sha256:"

// CommandDigestBytes returns the raw Provenance command-digest bytes for a
// #43-produced ir.CanonicalCommandDigest, byte-for-byte preserving the digest #43
// computed. The result is the exact input to Journal.Apply's OperationInput.CommandDigest
// and to the LookupCommitted lookup key. A zero/invalid digest is rejected: an
// empty digest must never stand in for a missing resolved command.
func CommandDigestBytes(d ir.CanonicalCommandDigest) ([]byte, error) {
	if !d.IsValid() {
		return nil, fmt.Errorf(
			"provadapter: cannot convert command digest — what: the ir.CanonicalCommandDigest is the " +
				"zero value; why: an empty digest cannot stand in for a resolved command and would collide " +
				"with any other empty digest under Provenance's replay short-circuit; where: internal/provadapter " +
				"command-digest conversion; when: before LookupCommitted/Apply; impact: no operation is keyed on " +
				"an empty digest; fix: produce the digest with ir.DigestCanonicalCommand over the canonical " +
				"command bytes before converting")
	}
	text := d.String()
	raw, ok := strings.CutPrefix(text, commandDigestHexPrefix)
	if !ok {
		// ir.CanonicalCommandDigest.String() always emits the sha256: prefix; a
		// missing prefix means the type's textual contract changed under us.
		return nil, fmt.Errorf(
			"provadapter: cannot convert command digest — what: %q has no %q algorithm prefix; "+
				"why: byte-preservation relies on ir.CanonicalCommandDigest's stable sha256:<hex> textual form; "+
				"where: internal/provadapter command-digest conversion; when: decoding the digest text; "+
				"impact: the raw digest bytes cannot be recovered without re-hashing (forbidden by #14); "+
				"fix: this indicates an incompatible ir.CanonicalCommandDigest change — reconcile the adapter "+
				"with the ir digest textual contract",
			text, commandDigestHexPrefix)
	}
	sum, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf(
			"provadapter: cannot convert command digest — what: the digest hex %q is malformed; "+
				"why: %w; where: internal/provadapter command-digest conversion; when: decoding the digest text; "+
				"impact: the raw digest bytes cannot be recovered; fix: recompute the digest with "+
				"ir.DigestCanonicalCommand",
			raw, err)
	}
	return sum, nil
}
