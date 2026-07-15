package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// EffectSet is an immutable canonical set of portable effect identities.
type EffectSet struct {
	ids         []EffectID
	constructed bool
}

// NewEffectSet validates, sorts, and deduplicates effect identities.
func NewEffectSet(ids ...EffectID) (EffectSet, error) {
	canonical := make([]EffectID, len(ids))
	copy(canonical, ids)
	for index, id := range canonical {
		if !id.IsValid() {
			return EffectSet{}, diagnostic(
				fmt.Sprintf("effect at index %d is zero or invalid", index),
				"an EffectSet can contain only constructor-validated EffectID values",
				"NewEffectSet", "effect validation",
				"the operation/effect descriptor cannot be constructed",
				"construct every identity with NewEffectID before building the set", nil,
			)
		}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].value < canonical[j].value })
	deduped := canonical[:0]
	for _, id := range canonical {
		if len(deduped) == 0 || deduped[len(deduped)-1].value != id.value {
			deduped = append(deduped, id)
		}
	}
	owned := make([]EffectID, len(deduped))
	copy(owned, deduped)
	return EffectSet{ids: owned, constructed: true}, nil
}

// IDs returns a defensive copy in canonical lexical order.
func (s EffectSet) IDs() []EffectID {
	out := make([]EffectID, len(s.ids))
	copy(out, s.ids)
	return out
}

func (s EffectSet) IsValid() bool {
	if !s.constructed {
		return false
	}
	for index, id := range s.ids {
		if !id.IsValid() || (index > 0 && s.ids[index-1].value >= id.value) {
			return false
		}
	}
	return true
}

// Equal reports canonical set equality.
func (s EffectSet) Equal(other EffectSet) bool {
	if !s.IsValid() || !other.IsValid() || len(s.ids) != len(other.ids) {
		return false
	}
	for index := range s.ids {
		if s.ids[index].value != other.ids[index].value {
			return false
		}
	}
	return true
}

// ContainsAll reports whether s contains every effect in required. It is useful
// for policy checks, but it is deliberately not descriptor compatibility:
// silently adding effects changes operation semantics.
func (s EffectSet) ContainsAll(required EffectSet) bool {
	if !s.IsValid() || !required.IsValid() {
		return false
	}
	available := make(map[string]struct{}, len(s.ids))
	for _, id := range s.ids {
		available[id.value] = struct{}{}
	}
	for _, id := range required.ids {
		if _, ok := available[id.value]; !ok {
			return false
		}
	}
	return true
}

// CompatibleWith requires exact canonical effect equality. A runtime binding
// with missing or additional effects does not implement the same descriptor.
func (s EffectSet) CompatibleWith(required EffectSet) bool { return s.Equal(required) }

// Compatible is an explicit alias for CompatibleWith.
func (s EffectSet) Compatible(required EffectSet) bool { return s.CompatibleWith(required) }

func (s EffectSet) MarshalJSON() ([]byte, error) {
	if !s.IsValid() {
		return nil, diagnostic(
			"EffectSet is not canonical", "zero or forged sets cannot define a stable wire identity",
			"EffectSet.MarshalJSON", "effect codec", "the descriptor cannot be serialized",
			"construct the set with NewEffectSet", nil,
		)
	}
	values := make([]string, len(s.ids))
	for index, id := range s.ids {
		values[index] = id.value
	}
	return json.Marshal(values)
}

func (s *EffectSet) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var values []string
	if err := decoder.Decode(&values); err != nil {
		return diagnostic(
			"effect-set JSON is malformed", "the canonical codec is one JSON array of namespaced effect IDs",
			"EffectSet.UnmarshalJSON", "effect codec", "the effect contract cannot be reconstructed",
			"supply one JSON string array", err,
		)
	}
	ids := make([]EffectID, 0, len(values))
	for index, value := range values {
		id, err := NewEffectID(value)
		if err != nil {
			return diagnostic(
				fmt.Sprintf("effect-set entry %d is invalid", index), "every wire identity must pass EffectID construction",
				"EffectSet.UnmarshalJSON", "effect codec", "the effect contract cannot be reconstructed",
				"replace the entry with a valid namespaced identity", err,
			)
		}
		ids = append(ids, id)
	}
	canonical, err := NewEffectSet(ids...)
	if err != nil {
		return err
	}
	canonicalBytes, _ := canonical.MarshalJSON()
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, data); err != nil || !bytes.Equal(compact.Bytes(), canonicalBytes) {
		return diagnostic(
			"effect-set JSON is not in canonical sorted unique order",
			"accepting multiple encodings would make descriptor equality and digests unstable",
			"EffectSet.UnmarshalJSON", "effect codec", "the effect contract is rejected",
			"sort IDs lexically, remove duplicates, and encode without alternate spellings", nil,
		)
	}
	*s = canonical
	return nil
}
