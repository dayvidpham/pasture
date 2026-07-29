package lifecycle

// These tests are package-internal on purpose.
//
// Two of the verifier's guarantees are unreachable from outside the package,
// and that is by design rather than by omission:
//
//   - Identity is opaque and constructor-owned, so an external caller cannot
//     present one that claims to be constructed while carrying a value the
//     constructor would have rejected. The verifier re-checks anyway; these
//     tests forge exactly that value to prove the re-check is real and not
//     decorative.
//
//   - No pinned profile declares two correlation fields of one kind, so the
//     value tiebreak in the identity ordering cannot be reached through any
//     contract that exists today. It is still required for determinism the
//     moment one does, so it is tested directly here.
//
// Testing them through the public surface is not possible; leaving them
// untested would mean shipping two branches nobody has ever executed.

import (
	"slices"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forgedIdentity builds an Identity that claims to have been constructed while
// carrying a value NewIdentity would have refused. Only code inside this
// package can do this — which is the point.
func forgedIdentity(kind runtime.NativeIdentityKind, nativeName, value string) Identity {
	return Identity{kind: kind, nativeName: nativeName, value: value, constructed: true}
}

func TestVerifierDoesNotTrustAConstructedIdentityFlag(t *testing.T) {
	t.Parallel()

	binding, err := BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventSessionStart)
	require.NoError(t, err)

	declared := binding.DeclaredIdentities()
	require.NotEmpty(t, declared)
	field := declared[0]

	cases := []struct {
		name        string
		value       string
		mustMention string
	}{
		{"empty value", "", "empty value"},
		{"oversized value", strings.Repeat("x", identityValueMaxBytes+1), "over the"},
		{"padded value", " s-1 ", "whitespace"},
		{"control character", "s-\x00-1", "control character"},
		{"invalid utf-8", string([]byte{0xff, 0xfe}), "UTF-8"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := binding.NewEvent(NewDigest([]byte("payload")), []Identity{
				forgedIdentity(field.Kind(), field.NativeName(), testCase.value),
			})
			require.Error(t, err, "the verifier must re-check the value rather than trust the constructed flag")
			assert.Contains(t, err.Error(), testCase.mustMention)
		})
	}
}

func TestVerifierAcceptsAForgedIdentityThatIsActuallyWellFormed(t *testing.T) {
	t.Parallel()

	binding, err := BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventSessionStart)
	require.NoError(t, err)
	field := binding.DeclaredIdentities()[0]

	event, err := binding.NewEvent(NewDigest([]byte("payload")), []Identity{
		forgedIdentity(field.Kind(), field.NativeName(), "s-1"),
	})
	require.NoError(t, err, "the re-check must reject only genuinely malformed values, not every forged one")
	assert.Equal(t, "s-1", event.Semantics().Identities()[0].Value)
}

// TestIdentityOrderingIsTotalIncludingTheValueTiebreak covers the branch no
// pinned profile can reach yet: two correlation values of one kind.
//
// Without the tiebreak the sort is not total, so two frontends that extracted
// the same pair in different orders would derive different canonical keys for
// one occurrence — and it would be recorded twice.
func TestIdentityOrderingIsTotalIncludingTheValueTiebreak(t *testing.T) {
	t.Parallel()

	t.Run("kind is the primary key", func(t *testing.T) {
		t.Parallel()
		assert.Negative(t, compareSemanticIdentities(
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "z"},
			SemanticIdentity{Kind: runtime.IdentityToolCall, Value: "a"},
		), "a lower kind sorts first regardless of its value")
	})

	t.Run("value breaks ties within one kind", func(t *testing.T) {
		t.Parallel()
		assert.Negative(t, compareSemanticIdentities(
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "a"},
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "b"},
		))
		assert.Positive(t, compareSemanticIdentities(
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "b"},
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "a"},
		))
		assert.Zero(t, compareSemanticIdentities(
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "a"},
			SemanticIdentity{Kind: runtime.IdentitySession, Value: "a"},
		))
	})

	t.Run("same-kind duplicates are retained in sorted order, never collapsed", func(t *testing.T) {
		t.Parallel()

		scrambled := []SemanticIdentity{
			{Kind: runtime.IdentityToolCall, Value: "call-2"},
			{Kind: runtime.IdentitySession, Value: "s-2"},
			{Kind: runtime.IdentityToolCall, Value: "call-1"},
			{Kind: runtime.IdentitySession, Value: "s-1"},
		}
		slices.SortFunc(scrambled, compareSemanticIdentities)

		assert.Equal(t, []SemanticIdentity{
			{Kind: runtime.IdentitySession, Value: "s-1"},
			{Kind: runtime.IdentitySession, Value: "s-2"},
			{Kind: runtime.IdentityToolCall, Value: "call-1"},
			{Kind: runtime.IdentityToolCall, Value: "call-2"},
		}, scrambled, "dropping either value of a repeated kind would silently lose correlation")
	})
}
