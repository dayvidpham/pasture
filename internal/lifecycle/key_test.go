package lifecycle_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateEvent builds the two-identity blocking gate with caller-chosen
// correlation values, so an encoding test can steer exactly the bytes that end
// up adjacent in the key.
func gateEvent(t *testing.T, payload, session, call string) lifecycle.Event {
	t.Helper()
	event, err := gateBinding(t).NewEvent(lifecycle.NewDigest([]byte(payload)), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, session),
		identity(t, runtime.IdentityToolCall, toolCallField, call),
	})
	require.NoError(t, err)
	return event
}

// naiveCanonical renders exactly the fields CanonicalKey renders, with no
// length prefix and no separator at all. It is the encoding this package
// deliberately does NOT use, written out so a test can prove a case is really
// adversarial rather than asserting it in a comment.
func naiveCanonical(semantics lifecycle.Semantics) string {
	out := semantics.Semantic().String() + semantics.Blocking().String() + itoa(len(semantics.Identities()))
	for _, correlation := range semantics.Identities() {
		out += correlation.Kind.String() + correlation.Value
	}
	return out
}

// separatorCanonical renders the same fields joined by one reserved byte: the
// other encoding this package deliberately does not use.
func separatorCanonical(semantics lifecycle.Semantics, separator string) string {
	fields := []string{semantics.Semantic().String(), semantics.Blocking().String(), itoa(len(semantics.Identities()))}
	for _, correlation := range semantics.Identities() {
		fields = append(fields, correlation.Kind.String(), correlation.Value)
	}
	return strings.Join(fields, separator)
}

// TestCanonicalKeyCannotCollideUnderNaiveConcatenation attacks the boundary
// between adjacent encoded fields.
//
// The classic collision — "ab" then "c" against "a" then "bc" — does not
// reproduce verbatim here, because the kind label is encoded between the two
// values and happens to act as a de-facto separator. That is an accident of the
// current enum spellings, not a property: a correlation value is host payload
// content, so it may CONTAIN the kind label. Each pair below shifts the field
// boundary by moving the label across it, and the precondition proves a
// separator-free encoder really would render the two identically.
func TestCanonicalKeyCannotCollideUnderNaiveConcatenation(t *testing.T) {
	t.Parallel()

	// The tool-call kind renders as this label, and nothing stops a host from
	// sending it as a value.
	const label = "tool-call"

	cases := []struct {
		name         string
		leftSession  string
		leftCall     string
		rightSession string
		rightCall    string
	}{
		{
			name:        "the kind label moves left across the boundary",
			leftSession: "a", leftCall: label + "b",
			rightSession: "a" + label, rightCall: "b",
		},
		{
			name:        "the kind label moves right across the boundary",
			leftSession: "x" + label, leftCall: label,
			rightSession: "x", rightCall: label + label,
		},
		{
			name:        "the boundary shifts by the whole label",
			leftSession: "p" + label + "q", leftCall: "r",
			rightSession: "p", rightCall: "q" + label + "r",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			left := gateEvent(t, "payload", testCase.leftSession, testCase.leftCall)
			right := gateEvent(t, "payload", testCase.rightSession, testCase.rightCall)

			require.Equal(t, naiveCanonical(left.Semantics()), naiveCanonical(right.Semantics()),
				"this case is only adversarial if a separator-free encoder really would collide on it")

			assert.NotEqual(t, left.Semantics().CanonicalKey(), right.Semantics().CanonicalKey(),
				"distinct correlation must produce distinct keys, whatever bytes the host chose to send")
		})
	}
}

// TestCanonicalKeyCannotCollideUnderASeparatorScheme attacks the other
// candidate encoding: joining fields with a reserved byte.
//
// Reserving a byte is not open to Pasture. Correlation values arrive inside a
// host payload whose content Pasture does not control, so every reserved byte
// is a byte some host is free to send, and enforcing the reservation would mean
// refusing a legitimate occurrence in order to protect an encoding choice.
// Each pair below is two distinct occurrences that a scheme using the named
// separator would render as one string.
func TestCanonicalKeyCannotCollideUnderASeparatorScheme(t *testing.T) {
	t.Parallel()

	const label = "tool-call"

	// Left puts the separator inside the session value; right puts the same
	// bytes after the boundary. Joined by that separator the two are identical.
	cases := []struct {
		name      string
		separator string
	}{
		{"comma", ","},
		{"colon", ":"},
		{"pipe", "|"},
		{"slash", "/"},
		{"space", " "},
		{"multi-byte separator", "::"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			separator := testCase.separator
			left := gateEvent(t, "payload", "a"+separator+label, "b")
			right := gateEvent(t, "payload", "a", label+separator+"b")

			require.Equal(t,
				separatorCanonical(left.Semantics(), separator),
				separatorCanonical(right.Semantics(), separator),
				"this case is only adversarial if a scheme using this separator really would collide on it")

			assert.NotEqual(t, left.Semantics().CanonicalKey(), right.Semantics().CanonicalKey(),
				"length prefixing has no reserved byte, so no host payload content can force a collision")
		})
	}
}

// TestCanonicalKeyEncodingIsPinned fixes the exact wire form. It exists so a
// change to the encoding is a deliberate act with a visible diff, not an
// accident that silently invalidates every previously recorded key.
func TestCanonicalKeyEncodingIsPinned(t *testing.T) {
	t.Parallel()

	event := gateEvent(t, "payload", "ab", "c")

	// field := decimal-byte-length ":" raw-bytes
	// canonical := field(semantic) field(blocking) field(count)
	//              [ field(kind) field(value) ]...   sorted by (Kind, Value)
	want := strings.Join([]string{
		"17:gate-consultation",
		"8:blocking",
		"1:2",
		"7:session", "2:ab",
		"9:tool-call", "1:c",
	}, "")

	assert.Equal(t, want, event.Semantics().CanonicalKey())
}

func TestCanonicalKeyLengthsCountRawBytesNotRunes(t *testing.T) {
	t.Parallel()

	// Five runes, six UTF-8 bytes. A rune-counted prefix would disagree with
	// the bytes that follow it and make the key ambiguous.
	multibyte := "héllo"
	event := gateEvent(t, "payload", multibyte, "c")

	assert.Contains(t, event.Semantics().CanonicalKey(),
		"6:"+multibyte,
		"the length prefix counts raw bytes")
	assert.Len(t, multibyte, 6)
}

func TestCanonicalKeyIsStableAcrossExtractionOrder(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	digest := lifecycle.NewDigest([]byte("payload"))

	forward, err := binding.NewEvent(digest, []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	reverse, err := binding.NewEvent(digest, []lifecycle.Identity{
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	assert.Equal(t, forward.Semantics().CanonicalKey(), reverse.Semantics().CanonicalKey(),
		"two frontends reading one payload in different orders must agree")
}

// ---------------------------------------------------------------------------
// Replay key
// ---------------------------------------------------------------------------

func TestReplayKeyIsDerivedOnlyFromWhatTheHostSent(t *testing.T) {
	t.Parallel()

	first := gateEvent(t, `{"payload":1}`, "s-1", "call-1")
	repeat := gateEvent(t, `{"payload":1}`, "s-1", "call-1")
	different := gateEvent(t, `{"payload":2}`, "s-1", "call-1")

	assert.Equal(t, first.Origin().ReplayKey(), repeat.Origin().ReplayKey(),
		"an identical delivery must be recognisable as a replay rather than recorded twice")
	assert.NotEqual(t, first.Origin().ReplayKey(), different.Origin().ReplayKey(),
		"payloads differing by one byte must not collapse into one record")
}

func TestReplayKeyDistinguishesEventsAndHostsWithIdenticalPayloads(t *testing.T) {
	t.Parallel()

	payload := lifecycle.NewDigest([]byte("identical bytes"))

	gate, err := gateBinding(t).NewEvent(payload, []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	observation, err := observationBinding(t).NewEvent(payload, []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	foreign := foreignGateBinding(t)
	foreignEvent, err := foreign.NewEvent(payload, []lifecycle.Identity{
		identity(t, runtime.IdentitySession, declaredField(t, foreign, runtime.IdentitySession), "s-1"),
		identity(t, runtime.IdentityToolCall, declaredField(t, foreign, runtime.IdentityToolCall), "call-1"),
	})
	require.NoError(t, err)

	keys := map[string]string{
		"gate":        gate.Origin().ReplayKey(),
		"observation": observation.Origin().ReplayKey(),
		"foreign":     foreignEvent.Origin().ReplayKey(),
	}
	assert.NotEqual(t, keys["gate"], keys["observation"],
		"two different native events must not share a replay key even when the bytes match")
	assert.NotEqual(t, keys["gate"], keys["foreign"],
		"the same-spelled event on two hosts must not collide; the contract is part of the key")
}

func TestReplayKeyEncodingIsPinned(t *testing.T) {
	t.Parallel()

	event := gateEvent(t, "payload", "s-1", "call-1")
	origin := event.Origin()

	// replayKey := field(contract) field(nativeEventName) field(hex(digest))
	want := strings.Join([]string{
		encodeFieldForTest(origin.Contract().String()),
		encodeFieldForTest(string(origin.NativeEventName())),
		encodeFieldForTest(origin.PayloadDigest().String()),
	}, "")

	assert.Equal(t, want, origin.ReplayKey())
	assert.Contains(t, origin.ReplayKey(), "64:"+origin.PayloadDigest().String(),
		"the digest is encoded as its 64-character lowercase hex form")
}

// TestZeroValuesRenderNoKey keeps an unconstructed value from ever being
// mistaken for a real one: the empty string is not producible by any
// constructed value, because every real key starts with a non-empty field.
func TestZeroValuesRenderNoKey(t *testing.T) {
	t.Parallel()

	assert.Empty(t, lifecycle.Semantics{}.CanonicalKey())
	assert.Empty(t, lifecycle.Origin{}.ReplayKey())

	event := gateEvent(t, "payload", "s-1", "call-1")
	assert.NotEmpty(t, event.Semantics().CanonicalKey())
	assert.NotEmpty(t, event.Origin().ReplayKey())
}

// encodeFieldForTest mirrors the production encoder. It is written out
// independently, rather than exported from the package, so the test fails if
// the production encoding changes shape rather than silently agreeing with it.
func encodeFieldForTest(value string) string {
	return itoa(len(value)) + ":" + value
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
