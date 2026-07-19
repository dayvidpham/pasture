package tasks

// decision_ledger_test.go round-trips the #43 decision-ledger BASE types: descriptor
// construction and validation, catalog registration/conflict detection, draft/token
// gating, stored-encoding canonicalization, typed decode, CoverageDigest text form, and
// JSON serializability of the stored entry/snapshot types. No policy is exercised — that
// is #49's — only the declarative, serializable base contract. It is an in-package test so
// it can exercise the package-private draft→encoding bridge #49's append path uses.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
)

// sampleDecision is a stand-in decision value type for the base-type round-trip tests.
type sampleDecision struct {
	Choice string `json:"choice"`
	Count  int    `json:"count"`
}

func sampleSchema() DecisionSchemaDigest {
	return DecisionSchemaDigest(sha256.Sum256([]byte("sampleDecision/v1")))
}

var errChoiceEmpty = errors.New("sampleDecision.Choice is empty")

func newSampleDescriptor(t *testing.T, kind DecisionKindID) DecisionDescriptor[sampleDecision] {
	t.Helper()
	d, err := NewDecisionDescriptor[sampleDecision](
		kind, "json/v1", sampleSchema(),
		func(v sampleDecision) error {
			if v.Choice == "" {
				return errChoiceEmpty
			}
			return nil
		},
		func(v sampleDecision) (CanonicalDecisionPayload, error) {
			b, err := json.Marshal(v)
			return CanonicalDecisionPayload(b), err
		},
		func(p CanonicalDecisionPayload) (sampleDecision, error) {
			var v sampleDecision
			err := json.Unmarshal(p, &v)
			return v, err
		},
	)
	if err != nil {
		t.Fatalf("NewDecisionDescriptor(%q): %v", kind, err)
	}
	return d
}

// TestNewDecisionDescriptorValidates proves the constructor rejects malformed inputs.
func TestNewDecisionDescriptorValidates(t *testing.T) {
	t.Parallel()
	schema := sampleSchema()
	okEnc := func(sampleDecision) (CanonicalDecisionPayload, error) { return nil, nil }
	okDec := func(CanonicalDecisionPayload) (sampleDecision, error) { return sampleDecision{}, nil }
	okVal := func(sampleDecision) error { return nil }

	cases := []struct {
		name     string
		kind     DecisionKindID
		codec    DecisionCodecID
		schema   DecisionSchemaDigest
		validate func(sampleDecision) error
		encode   func(sampleDecision) (CanonicalDecisionPayload, error)
		decode   func(CanonicalDecisionPayload) (sampleDecision, error)
	}{
		{"empty-kind", "", "json/v1", schema, okVal, okEnc, okDec},
		{"empty-codec", "k", "", schema, okVal, okEnc, okDec},
		{"zero-schema", "k", "json/v1", DecisionSchemaDigest{}, okVal, okEnc, okDec},
		{"nil-validate", "k", "json/v1", schema, nil, okEnc, okDec},
		{"nil-encode", "k", "json/v1", schema, okVal, nil, okDec},
		{"nil-decode", "k", "json/v1", schema, okVal, okEnc, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewDecisionDescriptor[sampleDecision](tc.kind, tc.codec, tc.schema, tc.validate, tc.encode, tc.decode); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

// TestCatalogRegistersAndRejectsDuplicates proves catalog construction manifests entries
// and rejects a duplicate kind.
func TestCatalogRegistersAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	a := newSampleDescriptor(t, "decision.a")
	b := newSampleDescriptor(t, "decision.b")

	cat, err := NewDecisionCatalog(BindDecision(a), BindDecision(b))
	if err != nil {
		t.Fatalf("NewDecisionCatalog: %v", err)
	}
	man := cat.Manifest()
	if len(man) != 2 || man[0].Kind != "decision.a" || man[1].Kind != "decision.b" {
		t.Fatalf("manifest = %+v, want [a b] in order", man)
	}

	dup := newSampleDescriptor(t, "decision.a")
	if _, err := NewDecisionCatalog(BindDecision(a), BindDecision(dup)); err == nil {
		t.Fatalf("expected duplicate-kind rejection")
	}
}

// TestDraftTokenGating proves a draft is accepted only by a catalog that registers its
// exact descriptor, and rejected by one registering a different descriptor for the kind.
func TestDraftTokenGating(t *testing.T) {
	t.Parallel()
	registered := newSampleDescriptor(t, "decision.k")
	cat, err := NewDecisionCatalog(BindDecision(registered))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	draft, err := registered.Draft(sampleDecision{Choice: "accept", Count: 3})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if err := cat.ValidateDraft(draft); err != nil {
		t.Fatalf("ValidateDraft of registered draft: %v", err)
	}

	// A different descriptor for the same kind has a different token: its draft is rejected.
	impostor := newSampleDescriptor(t, "decision.k")
	badDraft, err := impostor.Draft(sampleDecision{Choice: "accept"})
	if err != nil {
		t.Fatalf("impostor Draft: %v", err)
	}
	if err := cat.ValidateDraft(badDraft); err == nil {
		t.Fatalf("expected rejection of a draft from an unregistered descriptor token")
	}

	// A draft validation failure surfaces before drafting.
	if _, err := registered.Draft(sampleDecision{Choice: ""}); err == nil {
		t.Fatalf("expected draft validation failure for empty choice")
	}
}

// TestDecodeDecisionRoundTrip proves a stored encoding decodes back to the original value
// through the catalog-registered descriptor, and that non-canonical/unknown inputs fail.
func TestDecodeDecisionRoundTrip(t *testing.T) {
	t.Parallel()
	desc := newSampleDescriptor(t, "decision.rt")
	cat, err := NewDecisionCatalog(BindDecision(desc))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	want := sampleDecision{Choice: "revise", Count: 9}
	draft, err := desc.Draft(want)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	enc := draft.encoding()

	if err := cat.ValidateStored(enc); err != nil {
		t.Fatalf("ValidateStored: %v", err)
	}
	got, err := DecodeDecision(cat, desc, enc)
	if err != nil {
		t.Fatalf("DecodeDecision: %v", err)
	}
	if got != want {
		t.Fatalf("decoded = %+v, want %+v", got, want)
	}

	// A non-canonical payload (extra whitespace) is rejected by ValidateStored.
	noncanon := enc
	noncanon.Payload = CanonicalDecisionPayload(append([]byte(" "), enc.Payload...))
	if err := cat.ValidateStored(noncanon); err == nil {
		t.Fatalf("expected non-canonical payload rejection")
	}

	// An unregistered kind is rejected.
	unknown := enc
	unknown.Kind = "decision.unknown"
	if _, err := DecodeDecision(cat, desc, unknown); err == nil {
		t.Fatalf("expected unknown-kind rejection")
	}
}

// TestCoverageDigestTextRoundTrip proves the sha256:<hex> text form round-trips through
// MarshalText/ParseCoverageDigest and JSON.
func TestCoverageDigestTextRoundTrip(t *testing.T) {
	t.Parallel()
	d := CoverageDigest(sha256.Sum256([]byte("coverage")))
	text, err := d.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if !bytes.HasPrefix(text, []byte("sha256:")) {
		t.Fatalf("text %q missing sha256: prefix", text)
	}
	back, err := ParseCoverageDigest(text)
	if err != nil {
		t.Fatalf("ParseCoverageDigest: %v", err)
	}
	if back != d {
		t.Fatalf("round-trip digest mismatch")
	}

	// JSON round-trip through the text marshaler.
	blob, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var jd CoverageDigest
	if err := json.Unmarshal(blob, &jd); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if jd != d {
		t.Fatalf("json round-trip mismatch")
	}

	for _, bad := range []string{"", "nope", "sha256:xyz", "sha256:abcd"} {
		if _, err := ParseCoverageDigest([]byte(bad)); err == nil {
			t.Errorf("ParseCoverageDigest(%q) accepted a malformed digest", bad)
		}
	}
}

// TestLedgerEntryAndSnapshotJSONRoundTrip proves the stored ledger entry and Plan-UAT
// snapshot are serializable and round-trip through JSON with all fields preserved.
func TestLedgerEntryAndSnapshotJSONRoundTrip(t *testing.T) {
	t.Parallel()
	desc := newSampleDescriptor(t, "decision.ledger")
	draft, err := desc.Draft(sampleDecision{Choice: "accept", Count: 1})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	dec := draft.encoding()

	entry := DecisionLedgerEntry{
		ID:    "entry-1",
		Epoch: "epoch-root-1",
		Actor: DecisionAttribution{
			Decider:      provenance.ActorID{Namespace: "user", UUID: uuid.Must(uuid.NewV7())},
			DeciderKind:  provenance.AgentKindHuman,
			Recorder:     provenance.ActorID{Namespace: "agent", UUID: uuid.Must(uuid.NewV7())},
			RecorderKind: provenance.AgentKindSoftware,
		},
		Decision: dec,
	}
	blob, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var back DecisionLedgerEntry
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	if back.ID != entry.ID || back.Epoch != entry.Epoch || back.Actor != entry.Actor {
		t.Fatalf("entry round-trip mismatch: %+v vs %+v", back, entry)
	}
	if back.Decision.Kind != dec.Kind || !bytes.Equal(back.Decision.Payload, dec.Payload) {
		t.Fatalf("decision payload not preserved across JSON round-trip")
	}

	snap := PlanUATSnapshot{
		ID:            "plan-uat-1",
		UATTaskID:     provenance.TaskID{Namespace: "aura-plugins", UUID: uuid.Must(uuid.NewV7())},
		Proposal:      "doc-rev-1",
		DecisionEntry: "entry-1",
		InputLedger:   "doc-rev-0",
		OutputLedger:  "doc-rev-2",
	}
	sblob, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var sback PlanUATSnapshot
	if err := json.Unmarshal(sblob, &sback); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if sback != snap {
		t.Fatalf("snapshot round-trip mismatch: %+v vs %+v", sback, snap)
	}
}
