package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// Record is the constructor-owned interpreted lifecycle evidence.
//
// The record deliberately keeps the waist's semantic vocabulary rather than
// the occurrence model's native-binding vocabulary.  That preserves the
// boundary between interpretation and raw occurrence ingestion.
type Record struct {
	semantic    runtime.EventSemantic
	identities  []waist.SemanticIdentity
	unresolved  []waist.UnresolvedFact
	contract    ir.RuntimeContractID
	constructed bool
}

// NewInterpreted constructs an interpreted record from a verified L2 value.
func NewInterpreted(l2 waist.L2, contract ir.RuntimeContractID) (Record, error) {
	const where = "Constructing an interpreted lifecycle record (internal/lifecycle/receipt/interpreted.go in receipt.NewInterpreted)."
	if !l2.IsValid() || !l2.Semantics().IsValid() || !l2.Origin().IsValid() {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			"The interpreted lifecycle record source is invalid.",
			"Only a constructor-built waist L2 carries a verified semantic arm, origin, and bounded correlation values.",
			where,
			"No interpreted evidence was constructed.",
			"Build L2 through waist.BindEvent followed by EventBinding.NewEvent before creating the interpreted record.",
			nil,
		)
	}
	if !contract.IsValid() {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			"The interpreted lifecycle record has an invalid runtime contract.",
			"Durable interpretation must identify the version-bounded runtime contract that produced the L2 value.",
			where,
			"No interpreted evidence was constructed.",
			"Pass the constructor-produced contract ID from the same pinned lifecycle profile as the L2 value.",
			nil,
		)
	}
	originContract := l2.Origin().Contract()
	if originContract != contract {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			fmt.Sprintf("The interpreted lifecycle record contract %q does not match the L2 origin contract %q.", contract, originContract),
			"The redundant contract argument must agree with the contract pinned into L2; otherwise evidence could be attributed to a different runtime profile.",
			where,
			"No interpreted evidence was constructed.",
			"Pass l2.Origin().Contract() unchanged as the record contract.",
			nil,
		)
	}

	semantics := l2.Semantics()
	return Record{
		semantic:    semantics.Semantic(),
		identities:  append([]waist.SemanticIdentity(nil), semantics.Identities()...),
		unresolved:  append([]waist.UnresolvedFact(nil), semantics.UnresolvedFacts()...),
		contract:    contract,
		constructed: true,
	}, nil
}

// Semantic returns the interpreted lifecycle arm.
func (r Record) Semantic() runtime.EventSemantic { return r.semantic }

// Identities returns a defensive copy of the interpreted waist identities.
func (r Record) Identities() []waist.SemanticIdentity {
	return append([]waist.SemanticIdentity(nil), r.identities...)
}

// UnresolvedFacts returns a defensive copy of the known correlation gaps.
func (r Record) UnresolvedFacts() []waist.UnresolvedFact {
	return append([]waist.UnresolvedFact(nil), r.unresolved...)
}

// Contract returns the pinned runtime contract that produced the record.
func (r Record) Contract() ir.RuntimeContractID { return r.contract }

// IsValid reports whether the record was constructed by NewInterpreted.
func (r Record) IsValid() bool { return r.constructed }

// Effect converts the interpreted record into its durable evidence effect.
func (r Record) Effect() provenance.Effect {
	if !r.IsValid() {
		return provenance.Effect{}
	}
	payload, err := canonicalInterpretedPayload(r)
	if err != nil {
		return provenance.Effect{}
	}
	digest := sha256.Sum256(payload)
	return provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    interpretedSlot,
		EvidenceKind:  interpretedKind,
		ContentDigest: append([]byte(nil), digest[:]...),
		Payload:       append(json.RawMessage(nil), payload...),
	}
}

type interpretedPayload struct {
	Semantic        runtime.EventSemantic   `json:"semantic"`
	Identities      []interpretedIdentity   `json:"identities"`
	UnresolvedFacts []interpretedUnresolved `json:"unresolved_facts"`
	Contract        ir.RuntimeContractID    `json:"contract"`
}

type interpretedIdentity struct {
	Kind  runtime.NativeIdentityKind `json:"kind"`
	Value string                     `json:"value"`
}

type interpretedUnresolved struct {
	Reason waist.UnresolvedReason `json:"reason"`
}

func canonicalInterpretedPayload(r Record) ([]byte, error) {
	identities := make([]interpretedIdentity, len(r.identities))
	for index, identity := range r.identities {
		identities[index] = interpretedIdentity{Kind: identity.Kind, Value: identity.Value}
	}
	unresolved := make([]interpretedUnresolved, len(r.unresolved))
	for index, fact := range r.unresolved {
		unresolved[index] = interpretedUnresolved{Reason: fact.Reason}
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(interpretedPayload{
		Semantic:        r.semantic,
		Identities:      identities,
		UnresolvedFacts: unresolved,
		Contract:        r.contract,
	}); err != nil {
		return nil, fmt.Errorf("encode interpreted lifecycle evidence: %w", err)
	}
	return append([]byte(nil), bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})...), nil
}
