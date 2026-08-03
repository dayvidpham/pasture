package ir

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HarnessID identifies a native host family. Harness names are metadata; the
// selected version-bounded RuntimeContractID determines actual compatibility.
type HarnessID string

const (
	HarnessClaudeCode HarnessID = "claude-code"
	HarnessOpenCode   HarnessID = "opencode"
	HarnessCodex      HarnessID = "codex"
)

var canonicalHarnessIDs = [...]HarnessID{HarnessClaudeCode, HarnessOpenCode, HarnessCodex}

// EnabledHarnessIDs returns a fresh defensive copy of the canonical enabled
// target set. This is the only enumeration accessor for HarnessID: an earlier
// revision additionally exported a package-level AllHarnessIDs slice
// initialized once at load time, which callers could mutate to corrupt every
// later reader. There is exactly one construction spelling for this concept.
func EnabledHarnessIDs() []HarnessID {
	return append([]HarnessID(nil), canonicalHarnessIDs[:]...)
}

func (h HarnessID) IsValid() bool {
	switch h {
	case HarnessClaudeCode, HarnessOpenCode, HarnessCodex:
		return true
	default:
		return false
	}
}

// RuntimeContractID identifies one reviewed, version-bounded runtime profile
// bound to exactly one enabled harness. It is opaque and constructor-owned:
// the only way to produce a non-zero value is NewRuntimeContractID (or a
// successful JSON decode through the same validation), so a RuntimeContractID
// value in hand always names a real, enabled harness — decision and target
// code can compare Harness() directly instead of re-deriving it from string
// prefixes.
type RuntimeContractID struct {
	harness HarnessID
	value   string
}

// NewRuntimeContractID validates name and constructs a RuntimeContractID
// bound to harness. name may already carry the "<harness>/" prefix (so
// values round-tripped from String() can be re-supplied); a bare suffix is
// prefixed automatically.
func NewRuntimeContractID(harness HarnessID, name string) (RuntimeContractID, error) {
	if !harness.IsValid() {
		return RuntimeContractID{}, documentError(
			fmt.Sprintf("runtime contract harness %q is unknown", harness),
			"contracts bind one enabled harness", "use a known HarnessID", nil,
		)
	}
	if !utf8.ValidString(name) {
		return RuntimeContractID{}, documentError(
			fmt.Sprintf("runtime contract name for harness %q is not valid UTF-8", harness),
			"contract identities must survive exact JSON and target rendering",
			"supply a valid UTF-8 name", nil,
		)
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
		return RuntimeContractID{}, documentError(
			fmt.Sprintf("runtime contract name for harness %q is empty or padded", harness),
			"contract identities require one exact spelling",
			"supply a non-empty name without surrounding whitespace", nil,
		)
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return RuntimeContractID{}, documentError(
				fmt.Sprintf("runtime contract name for harness %q contains whitespace or control character U+%04X", harness, r),
				"whitespace and control characters are unsafe in portable identities and make two spellings compare unequal in confusing ways",
				"remove whitespace and control characters", nil,
			)
		}
	}
	prefix := string(harness) + "/"
	canonical := name
	if !strings.HasPrefix(canonical, prefix) {
		canonical = prefix + canonical
	}
	return parseRuntimeContractID(canonical)
}

// parseRuntimeContractID is the single parser and validator shared by
// NewRuntimeContractID and RuntimeContractID's JSON decoder: constructing and
// parsing a contract go through exactly one set of invariants, so a value
// NewRuntimeContractID accepts can never be a value its own decoder rejects.
func parseRuntimeContractID(value string) (RuntimeContractID, error) {
	if !utf8.ValidString(value) {
		return RuntimeContractID{}, documentError(
			"runtime contract is not valid UTF-8",
			"contract identities must survive exact JSON and target rendering",
			"supply a valid UTF-8 contract", nil,
		)
	}
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return RuntimeContractID{}, documentError(
			"runtime contract is empty or padded",
			"contract identities require one exact spelling",
			"supply a non-empty contract without surrounding whitespace", nil,
		)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return RuntimeContractID{}, documentError(
				fmt.Sprintf("runtime contract %q contains whitespace or control character U+%04X", value, r),
				"whitespace and control characters are unsafe in portable identities and make two spellings compare unequal in confusing ways",
				"remove whitespace and control characters", nil,
			)
		}
	}
	for _, harness := range canonicalHarnessIDs {
		prefix := string(harness) + "/"
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(value, prefix)
		if suffix == "" {
			return RuntimeContractID{}, documentError(
				fmt.Sprintf("runtime contract %q has an empty suffix after harness %q", value, harness),
				"a contract must name one specific version-bound profile, not just its harness",
				"supply a non-empty suffix after the harness prefix", nil,
			)
		}
		return RuntimeContractID{harness: harness, value: value}, nil
	}
	return RuntimeContractID{}, documentError(
		fmt.Sprintf("runtime contract %q has no enabled harness prefix", value),
		"literal and target selection must be exhaustive by harness",
		"construct the contract with NewRuntimeContractID for an enabled harness", nil,
	)
}

// Harness returns the exact enabled harness this contract is bound to. It is
// always derived at construction, never re-parsed from an unrelated caller
// value, so it cannot disagree with the contract's own String().
func (c RuntimeContractID) Harness() HarnessID { return c.harness }

func (c RuntimeContractID) String() string { return c.value }

func (c RuntimeContractID) IsValid() bool {
	_, err := parseRuntimeContractID(c.value)
	return err == nil && c.harness.IsValid() && strings.HasPrefix(c.value, string(c.harness)+"/")
}

func (c RuntimeContractID) MarshalJSON() ([]byte, error) {
	if !c.IsValid() {
		return nil, documentError("runtime contract is zero or invalid", "only a constructor-produced contract has stable wire identity", "construct it with NewRuntimeContractID", nil)
	}
	return json.Marshal(c.value)
}

func (c *RuntimeContractID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return documentError("runtime contract JSON is not a string", "a contract uses one exact JSON string", "encode the contract as one JSON string", err)
	}
	parsed, err := parseRuntimeContractID(value)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// DecisionPurpose identifies why a user interaction is required.
type DecisionPurpose string

// UserDecisionRequestID is minted by the parent for one decision request.
type UserDecisionRequestID string

// OptionID is the stable identity of one presented decision option.
type OptionID string

// SchemaID identifies a versioned input or result codec.
type SchemaID string

// SkillID is an opaque validated portable skill identity.
type SkillID struct{ value string }

// SemanticOperationID is protocol meaning, not a runtime function name or a
// durable-store idempotency identity.
type SemanticOperationID struct{ value string }

// EffectID is the opaque portable identity of a declared effect.
type EffectID struct{ value string }

// WorktreeRef identifies assignment-local workspace context.
type WorktreeRef struct{ value string }

// EvidenceRef identifies portable evidence retained across continuation.
type EvidenceRef struct{ value string }

// DecisionRef identifies a portable prior decision.
type DecisionRef struct{ value string }

// WorkItemRef identifies portable outstanding work.
type WorkItemRef struct{ value string }

// ReviewRoundRef identifies a portable review-round result.
type ReviewRoundRef struct{ value string }

func NewSkillID(value string) (SkillID, error) {
	value, err := validateOpaqueID("skill identity", value, true)
	return SkillID{value: value}, err
}

func NewSemanticOperationID(value string) (SemanticOperationID, error) {
	value, err := validateOpaqueID("semantic operation identity", value, true)
	return SemanticOperationID{value: value}, err
}

func NewEffectID(value string) (EffectID, error) {
	value, err := validateOpaqueID("effect identity", value, true)
	return EffectID{value: value}, err
}

func NewWorktreeRef(value string) (WorktreeRef, error) {
	value, err := validateOpaqueID("worktree reference", value, false)
	return WorktreeRef{value: value}, err
}

func NewEvidenceRef(value string) (EvidenceRef, error) {
	value, err := validateOpaqueID("evidence reference", value, false)
	return EvidenceRef{value: value}, err
}

func NewDecisionRef(value string) (DecisionRef, error) {
	value, err := validateOpaqueID("decision reference", value, false)
	return DecisionRef{value: value}, err
}

func NewWorkItemRef(value string) (WorkItemRef, error) {
	value, err := validateOpaqueID("work-item reference", value, false)
	return WorkItemRef{value: value}, err
}

func NewReviewRoundRef(value string) (ReviewRoundRef, error) {
	value, err := validateOpaqueID("review-round reference", value, false)
	return ReviewRoundRef{value: value}, err
}

func validateOpaqueID(domain, value string, requireNamespace bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", diagnostic(
			fmt.Sprintf("%s is not valid UTF-8", domain),
			"portable identifiers must survive exact JSON and target rendering",
			domain+" constructor", "semantic validation",
			"the descriptor or reference cannot be constructed",
			"use a valid UTF-8 identifier", nil,
		)
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", diagnostic(
			fmt.Sprintf("%s is empty or has surrounding whitespace", domain),
			"portable identifiers require one exact non-empty spelling",
			domain+" constructor", "semantic validation",
			"identity comparison would be ambiguous",
			"supply a non-empty identifier without surrounding whitespace", nil,
		)
	}
	if requireNamespace && !strings.ContainsAny(value, ".:") {
		return "", diagnostic(
			fmt.Sprintf("%s %q is not namespaced", domain, value),
			"semantic and effect identities must not collide across contributors",
			domain+" constructor", "semantic validation",
			"the descriptor cannot enter the portable registry",
			"use a package-style identity with a dot or colon namespace separator", nil,
		)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", diagnostic(
				fmt.Sprintf("%s contains control character U+%04X", domain, r),
				"control characters are unsafe in portable identities",
				domain+" constructor", "semantic validation",
				"the value cannot be represented safely",
				"remove control characters", nil,
			)
		}
	}
	return value, nil
}

func validateNamedID(domain, value string) error {
	_, err := validateOpaqueID(domain, value, false)
	return err
}

func (id SkillID) String() string             { return id.value }
func (id SemanticOperationID) String() string { return id.value }
func (id EffectID) String() string            { return id.value }
func (r WorktreeRef) String() string          { return r.value }
func (r EvidenceRef) String() string          { return r.value }
func (r DecisionRef) String() string          { return r.value }
func (r WorkItemRef) String() string          { return r.value }
func (r ReviewRoundRef) String() string       { return r.value }

func (id SkillID) IsValid() bool {
	_, err := validateOpaqueID("skill identity", id.value, true)
	return err == nil
}
func (id SemanticOperationID) IsValid() bool {
	_, err := validateOpaqueID("semantic operation identity", id.value, true)
	return err == nil
}
func (id EffectID) IsValid() bool {
	_, err := validateOpaqueID("effect identity", id.value, true)
	return err == nil
}
func (r WorktreeRef) IsValid() bool {
	_, err := validateOpaqueID("worktree reference", r.value, false)
	return err == nil
}
func (r EvidenceRef) IsValid() bool {
	_, err := validateOpaqueID("evidence reference", r.value, false)
	return err == nil
}
func (r DecisionRef) IsValid() bool {
	_, err := validateOpaqueID("decision reference", r.value, false)
	return err == nil
}
func (r WorkItemRef) IsValid() bool {
	_, err := validateOpaqueID("work-item reference", r.value, false)
	return err == nil
}
func (r ReviewRoundRef) IsValid() bool {
	_, err := validateOpaqueID("review-round reference", r.value, false)
	return err == nil
}
