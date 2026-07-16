package ir_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonInputClass is one class of "not a well-formed single duplicate-free
// JSON value" input, used to prove every duplicate-member-rejecting call
// site classifies its failure accurately instead of mislabeling every
// rejectDuplicateJSONMembers error as "duplicate member".
type jsonInputClass struct {
	name          string
	data          []byte
	wantDuplicate bool
}

func jsonInputClasses() []jsonInputClass {
	return []jsonInputClass{
		{name: "genuine duplicate member", data: []byte(`{"a":1,"a":2}`), wantDuplicate: true},
		{name: "empty input", data: []byte(``), wantDuplicate: false},
		{name: "truncated input", data: []byte(`{"a":1`), wantDuplicate: false},
		{name: "syntax error", data: []byte(`not json at all`), wantDuplicate: false},
	}
}

// TestNewResolvedMutationRequestClassifiesJSONFailures covers finding
// kggu8's required duplicate-member negative test for mutation
// canonicalization (NewResolvedMutationRequest -> continuation.go's
// canonicalJSON), and finding 1m8pd's requirement that canonicalJSON not
// mislabel empty/truncated/malformed input as a duplicate member.
func TestNewResolvedMutationRequestClassifiesJSONFailures(t *testing.T) {
	t.Parallel()

	operationID, err := ir.NewSemanticOperationID("pasture.test.classify-operation/v1")
	require.NoError(t, err)

	for _, class := range jsonInputClasses() {
		class := class
		t.Run(class.name, func(t *testing.T) {
			t.Parallel()
			request, err := ir.NewResolvedMutationRequest(operationID, "pasture.test.classify-request/v1", class.data)
			require.Error(t, err, "%s must be rejected", class.name)
			assert.False(t, request.IsValid())
			assertJSONFailureClassified(t, err, class)
		})
	}
}

// TestJSONCodecDecodeClassifiesJSONFailures covers finding kggu8's required
// duplicate-member negative test for typed result capture (JSONCodec.Decode,
// descriptor.go), and finding 1m8pd's requirement that it not mislabel
// empty/truncated/malformed input as a duplicate member.
func TestJSONCodecDecodeClassifiesJSONFailures(t *testing.T) {
	t.Parallel()

	type input struct {
		Name string `json:"name"`
	}
	codec, err := ir.NewJSONCodec[input]("pasture.test.classify-codec/v1", nil)
	require.NoError(t, err)

	for _, class := range jsonInputClasses() {
		class := class
		t.Run(class.name, func(t *testing.T) {
			t.Parallel()
			// rejectDuplicateJSONMembers runs before typed decoding, so it
			// classifies these fixtures the same way regardless of whether
			// their keys match input's own "name" field.
			_, err := codec.Decode(class.data)
			require.Error(t, err, "%s must be rejected", class.name)
			assertJSONFailureClassified(t, err, class)
		})
	}
}

// TestDecodeReportedResultClassifiesJSONFailures covers finding 1m8pd's
// required negatives for the report-decoding entry point: empty, truncated,
// and syntactically malformed input must each surface an "empty, truncated,
// or malformed" diagnostic, not "duplicate member". The genuine-duplicate
// case (which must keep the "duplicate member" message) is already covered
// exhaustively by TestReportedUserDecisionRejectsDuplicateJSONMembersAtEvery
// Level; this test only adds the three previously-unproven negative classes.
func TestDecodeReportedResultClassifiesJSONFailures(t *testing.T) {
	t.Parallel()

	request := decisionRequest(t, ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "Explain", DefinitionShown: "def", CommandShown: "cmd"}})

	for _, class := range jsonInputClasses() {
		if class.wantDuplicate {
			continue // covered by TestReportedUserDecisionRejectsDuplicateJSONMembersAtEveryLevel
		}
		class := class
		t.Run(class.name, func(t *testing.T) {
			t.Parallel()
			_, canonical, err := request.DecodeReportedResult(bytes.NewReader(class.data), 64<<10)
			require.Error(t, err, "%s must be rejected", class.name)
			assert.False(t, canonical.IsValid())
			assertJSONFailureClassified(t, err, class)
		})
	}
}

// assertJSONFailureClassified asserts err's message accurately names its
// failure class: "duplicate member" only for a genuine duplicate, and
// "empty, truncated, or malformed" (never "duplicate") for everything else.
func assertJSONFailureClassified(t testing.TB, err error, class jsonInputClass) {
	t.Helper()
	message := err.Error()
	if class.wantDuplicate {
		assert.True(t, strings.Contains(message, "duplicate"), "%s: expected a duplicate-member diagnostic, got: %s", class.name, message)
		return
	}
	assert.False(t, strings.Contains(message, "duplicate"), "%s: must not be misdiagnosed as a duplicate member, got: %s", class.name, message)
	assert.True(t,
		strings.Contains(message, "empty") || strings.Contains(message, "truncated") || strings.Contains(message, "malformed"),
		"%s: expected an empty/truncated/malformed diagnostic, got: %s", class.name, message,
	)
}
