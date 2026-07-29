package lifecycle

import (
	"strconv"
	"strings"
)

// Two strings are derived from the waist, and both must be injective: distinct
// inputs must produce distinct output. Neither may reserve a separator
// character, because half of what they encode — correlation values — comes out
// of a host payload whose content Pasture does not control. Any byte a
// separator scheme forbids is a byte some host is free to send, and rejecting
// it at the boundary would fail a legitimate occurrence to protect an encoding
// choice.
//
// Length prefixing has no forbidden byte. Each field is written as its decimal
// RAW BYTE length, a colon, then the bytes themselves:
//
//	field     := decimal-length ":" raw-bytes
//	replayKey := field(contract) field(nativeEventName) field(hex(digest))
//	canonical := field(semantic) field(blocking) field(count)
//	             [ field(kind) field(value) ]...     // sorted by (Kind, Value)
//
// The decoder is never written, but injectivity is what matters, and it holds
// for the usual reason: a reader could recover every field boundary from the
// string alone, so two different field sequences cannot render identically.
// Concretely, "ab" then "c" encodes as `2:ab1:c` while "a" then "bc" encodes as
// `1:a2:bc` — a separator scheme would render both as `ab,c` versus `a,bc` only
// while no value contains a comma.
//
// Lengths count raw bytes, not runes, so a multi-byte value cannot make the
// prefix disagree with the bytes that follow it.

// encodeField writes one length-prefixed field.
func encodeField(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}

// CanonicalKey renders the target-agnostic content of these semantics as one
// injective string: meaning, blocking mode, and every correlation value in
// sorted order.
//
// Unlike [Semantics.EquivalentTo] it DOES encode correlation values, so equal
// canonical keys imply equivalence but not the reverse. The identity count is
// encoded explicitly so a key can be read as a self-describing sequence rather
// than inferred from where the string happens to end.
//
// A zero Semantics has no canonical key and renders as the empty string, which
// no constructed value can produce.
func (s Semantics) CanonicalKey() string {
	if !s.constructed {
		return ""
	}
	var key strings.Builder
	key.WriteString(encodeField(s.semantic.String()))
	key.WriteString(encodeField(s.blocking.String()))
	key.WriteString(encodeField(strconv.Itoa(len(s.identities))))
	for _, identity := range s.identities {
		key.WriteString(encodeField(identity.Kind.String()))
		key.WriteString(encodeField(identity.Value))
	}
	return key.String()
}

// ReplayKey renders the coordinates of this occurrence as one injective string:
// which pinned contract described it, which native event fired, and the digest
// of the exact bytes the host sent.
//
// It is derived only from what the host actually delivered. No clock, no
// sequence number, and no Pasture-side state participates, so a second delivery
// of identical bytes always produces the same key and can be recognised as a
// replay rather than recorded twice. Conversely two occurrences whose payloads
// differ by even one byte always produce different keys and cannot collapse.
//
// A zero Origin has no replay key and renders as the empty string, which no
// constructed value can produce.
func (o Origin) ReplayKey() string {
	if !o.constructed {
		return ""
	}
	var key strings.Builder
	key.WriteString(encodeField(o.contract.String()))
	key.WriteString(encodeField(string(o.nativeName)))
	key.WriteString(encodeField(o.digest.String()))
	return key.String()
}
