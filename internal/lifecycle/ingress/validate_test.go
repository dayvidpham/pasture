package ingress_test

import (
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/lineage"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// harnessParser is one per-harness ingress parser with its manifest. The set
// is the three ingress packages this tree ships; a fourth harness adds a row
// here and a row in the hook command's dispatch registry, and the count below
// keeps the two from drifting apart in silence.
type harnessParser struct {
	manifest registration.Manifest
	parse    func(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) (model.CaptureDisposition, []model.NativeBinding, []byte, digest.Digest)
}

func harnessParsers() []harnessParser {
	return []harnessParser{
		{registration.ClaudeCode2_1_210(), func(raw []byte, event registration.Event, version string, envelope model.OccurrenceEnvelopeRef) (model.CaptureDisposition, []model.NativeBinding, []byte, digest.Digest) {
			capture := claude.Parse(raw, event, version, envelope)
			return capture.Disposition, capture.Delivery.Bindings, capture.Delivery.Body, capture.Digest
		}},
		{registration.Codex0_146_0(), func(raw []byte, event registration.Event, version string, envelope model.OccurrenceEnvelopeRef) (model.CaptureDisposition, []model.NativeBinding, []byte, digest.Digest) {
			capture := codex.Parse(raw, event, version, envelope)
			return capture.Disposition, capture.Delivery.Bindings, capture.Delivery.Body, capture.Digest
		}},
		{registration.OpenCode1_18_10(), func(raw []byte, event registration.Event, version string, envelope model.OccurrenceEnvelopeRef) (model.CaptureDisposition, []model.NativeBinding, []byte, digest.Digest) {
			capture := opencode.Parse(raw, event, version, envelope)
			return capture.Disposition, capture.Delivery.Bindings, capture.Delivery.Body, capture.Digest
		}},
	}
}

// TestTheSameMalformedInputIsRefusedIdenticallyOnEveryHarness feeds one
// malformed payload of each shared class to every harness parser and requires
// the same disposition from all of them, with no bindings and the exact bytes
// retained. Before the shared dispatch, the struct-decoding parsers accepted
// invalid UTF-8 and a repeated member as valid input.
func TestTheSameMalformedInputIsRefusedIdenticallyOnEveryHarness(t *testing.T) {
	t.Parallel()
	parsers := harnessParsers()
	require.Len(t, parsers, 3, "three harnesses ship an ingress parser; a fourth adds a row here")

	cases := []struct {
		name string
		raw  []byte
		want model.CaptureDisposition
	}{
		{"not well-formed JSON", []byte(`{"session_id":`), model.CaptureMalformed},
		{"a JSON array instead of an object", []byte(`[]`), model.CaptureMalformed},
		{"a JSON string instead of an object", []byte(`"hello"`), model.CaptureMalformed},
		{"a trailing value after the object", []byte(`{}{}`), model.CaptureMalformed},
		{"a member name that is not a string", []byte(`{1:2}`), model.CaptureMalformed},
		{"invalid UTF-8", []byte{'{', '"', 's', '"', ':', '"', 0xff, 0xfe, '"', '}'}, model.CaptureInvalidUTF8},
		{"a lone invalid byte", []byte{0xff}, model.CaptureInvalidUTF8},
		{"a repeated member", []byte(`{"session_id":"a","session_id":"b"}`), model.CaptureDuplicateField},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, parser := range parsers {
				event := parser.manifest.Events[0]
				disposition, bindings, body, sum := parser.parse(tc.raw, event, parser.manifest.Version, model.OccurrenceEnvelopeRef{})
				assert.Equal(t, tc.want, disposition, "%s refused %q with %v; every harness must refuse the same malformed input with the same disposition", parser.manifest.Harness, tc.name, disposition)
				assert.Empty(t, bindings, "%s bound identities from a refused payload", parser.manifest.Harness)
				assert.Equal(t, tc.raw, body, "%s did not retain the exact refused bytes", parser.manifest.Harness)
				assert.Equal(t, digest.FromBytes(tc.raw), sum, "%s took the digest over other bytes than the ones received", parser.manifest.Harness)
			}
		})
	}
}

// TestValidateRefusesInTheOrderTheClaudeParserEstablished pins the order of the
// shared refusals on inputs that fail more than one of them, so the
// disposition a payload receives cannot change when the checks are reordered.
func TestValidateRefusesInTheOrderTheClaudeParserEstablished(t *testing.T) {
	t.Parallel()
	// Invalid UTF-8 inside a repeated member: the encoding check runs first.
	both := []byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', ',', '"', 'a', '"', ':', '1', '}'}
	assert.Equal(t, model.CaptureInvalidUTF8, ingress.Validate(both).Disposition)
	// A repeated member inside an unterminated object: the repeat is found
	// before the missing terminator.
	assert.Equal(t, model.CaptureDuplicateField, ingress.Validate([]byte(`{"a":1,"a":2`)).Disposition)
	// A valid object keeps its members, and the body is a copy.
	raw := []byte(`{"a":1,"b":"two"}`)
	validation := ingress.Validate(raw)
	require.Equal(t, model.CaptureValid, validation.Disposition)
	assert.Len(t, validation.Members, 2)
	raw[0] = '!'
	assert.Equal(t, byte('{'), validation.Body[0], "the validation owns a defensive copy of the bytes")
	assert.Nil(t, ingress.Validate([]byte(`[]`)).Members, "a refused payload exposes no members")
}

// TestEventByNativeNameRefusesAnUnknownNameNamingHarnessAndName checks the
// validating reverse lookup on every manifest: a declared name resolves to its
// event; an undeclared name, including a case variant of a declared one, is
// refused with the harness and the name in the refusal.
func TestEventByNativeNameRefusesAnUnknownNameNamingHarnessAndName(t *testing.T) {
	t.Parallel()
	for _, parser := range harnessParsers() {
		manifest := parser.manifest
		t.Run(string(manifest.Harness), func(t *testing.T) {
			t.Parallel()
			require.NotEmpty(t, manifest.Events)
			for _, declared := range manifest.Events {
				event, err := ingress.EventByNativeName(manifest, declared.NativeName)
				require.NoError(t, err)
				assert.Equal(t, declared.Kind, event.Kind)
			}
			for _, unknown := range []string{"NoSuchEvent", "", " " + manifest.Events[0].NativeName, upperFirst(manifest.Events[0].NativeName)} {
				if unknown == manifest.Events[0].NativeName {
					continue
				}
				event, err := ingress.EventByNativeName(manifest, unknown)
				require.Error(t, err, "%q must be refused", unknown)
				assert.Zero(t, event.Kind)
				assert.Contains(t, err.Error(), "declares no native event named "+quote(unknown))
				assert.Contains(t, err.Error(), "the "+string(manifest.Harness)+" registration")
				assert.Contains(t, err.Error(), "nothing was read or recorded")
			}
		})
	}
}

func quote(s string) string { return `"` + s + `"` }

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first >= 'a' && first <= 'z' {
		return string(first-'a'+'A') + s[1:]
	}
	if first >= 'A' && first <= 'Z' {
		return string(first-'A'+'a') + s[1:]
	}
	return s
}

// noIdentityRow is one pinned profile row labelled IdentityPolicyNone, bound
// to its L1.
type noIdentityRow struct {
	harness    ir.HarnessID
	nativeName string
	mapping    runtime.LifecycleEventMapping
	l1         waist.L1
	contract   ir.RuntimeContractID
}

func collectNoIdentityRows[E comparable](t *testing.T, contract runtime.LifecycleContract[E], into *[]noIdentityRow, declared *int) {
	t.Helper()
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		switch mapping.IdentityPolicy() {
		case runtime.IdentityPolicyDeclared:
			require.NotEmpty(t, mapping.Identities(), "%s/%s is labelled declared and declares nothing", contract.Harness(), mapping.NativeName())
			*declared++
		case runtime.IdentityPolicyNone:
			require.Empty(t, mapping.Identities(), "%s/%s is labelled none and declares identities", contract.Harness(), mapping.NativeName())
			l1, err := waist.BindEvent(contract, event)
			require.NoError(t, err)
			*into = append(*into, noIdentityRow{harness: contract.Harness(), nativeName: mapping.NativeName(), mapping: mapping, l1: l1, contract: contract.ID()})
		default:
			t.Fatalf("%s/%s has identity policy %d, which is not a declared label", contract.Harness(), mapping.NativeName(), uint8(mapping.IdentityPolicy()))
		}
	}
}

// TestAnIdentityPolicyNoneEventLandsWithoutIdentitiesOrUnresolvedReason walks
// every pinned profile row labelled IdentityPolicyNone, the population being
// the contracts' own event lists, and requires: zero identities on the L2, no
// unresolved fact, no unresolved kind declared, and no lineage link between
// two such occurrences, because with no identity there is no correlation key
// to thread a chain over. The label is a table label with no waist effect,
// and the waist keeps its single unresolved reason: no arm is added for it.
func TestAnIdentityPolicyNoneEventLandsWithoutIdentitiesOrUnresolvedReason(t *testing.T) {
	t.Parallel()
	var rows []noIdentityRow
	declared := 0
	collectNoIdentityRows(t, runtime.ClaudeCode2_1_210Lifecycle(), &rows, &declared)
	collectNoIdentityRows(t, runtime.Codex0_146_0Lifecycle(), &rows, &declared)
	collectNoIdentityRows(t, runtime.OpenCode1_18_10Lifecycle(), &rows, &declared)
	require.NotEmpty(t, rows, "no pinned profile row declares zero identities, so this test would check nothing")
	require.Positive(t, declared, "no pinned profile row declares an identity, so the label has one value only")

	for _, row := range rows {
		row := row
		t.Run(string(row.harness)+"/"+row.nativeName, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, row.mapping.UnresolvedIdentities(), "row %s/%s declares no identity and must declare no unresolved kind", row.harness, row.nativeName)
			l2, err := row.l1.NewEvent(nil)
			require.NoError(t, err)
			assert.Empty(t, l2.Semantics().Identities(), "row %s/%s landed with identities although it declares none", row.harness, row.nativeName)
			assert.Empty(t, l2.Semantics().UnresolvedFacts(), "row %s/%s landed with an unresolved reason although it declares no identity; an identity-policy-none row never touches waist.UnresolvedReason", row.harness, row.nativeName)

			first := occurrenceRecord(t, 10, row.contract, l2)
			second := occurrenceRecord(t, 20, row.contract, l2)
			facts, err := lineage.DeriveLinks([]model.LifecycleRecord{first, second}, nil)
			require.NoError(t, err)
			assert.Empty(t, facts, "two %s/%s occurrences were threaded into a chain although neither carries a correlation key", row.harness, row.nativeName)
		})
	}
}

// occurrenceRecord builds a committed lifecycle record for one L2 through the
// production model constructors; the identities are exactly the L2's.
func occurrenceRecord(t *testing.T, jid int64, contract ir.RuntimeContractID, l2 waist.L2) model.LifecycleRecord {
	t.Helper()
	occurrence := model.NewOccurrenceRecord(model.OccurrenceID(jid), model.ContractEventKind(1), contract, model.OccurrenceEnvelopeRef{}, time.Unix(0, jid).UTC(), provenance.AgentID{}, nil, model.CaptureValid, model.EvidencePayloadRef{})
	interpreted, err := model.NewInterpretedRecord(model.InterpretationID(jid), model.OccurrenceID(jid), l2.Semantics().Semantic(), l2.Semantics().Identities(), l2.Semantics().UnresolvedFacts(), contract)
	require.NoError(t, err)
	record, err := model.NewLifecycleRecord(occurrence, []model.InterpretedRecord{interpreted})
	require.NoError(t, err)
	return record
}

// TestTheWaistKeepsItsSingleUnresolvedReason pins the closed set: the one
// declared reason is valid and the next value is not, so an arm cannot be
// added for identity-policy-none rows without turning this RED.
func TestTheWaistKeepsItsSingleUnresolvedReason(t *testing.T) {
	t.Parallel()
	assert.True(t, waist.UnresolvedToolCall.IsValid())
	assert.Equal(t, "tool-call-unresolved", waist.UnresolvedToolCall.String())
	assert.False(t, waist.UnresolvedReason(0).IsValid())
	assert.False(t, (waist.UnresolvedToolCall + 1).IsValid(), "a second unresolved reason was declared; an identity-policy-none row needs none, so this is a new waist arm and a design change")
	assert.Equal(t, "", (waist.UnresolvedToolCall + 1).String())
}
