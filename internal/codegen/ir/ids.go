package ir

import (
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

// AllHarnessIDs is a caller-owned enumeration of the enabled portable target
// set. Compiler invariants use their own canonical array, so caller mutation of
// this convenience slice cannot weaken exhaustiveness.
var AllHarnessIDs = append([]HarnessID(nil), canonicalHarnessIDs[:]...)

// EnabledHarnessIDs returns a fresh copy of the canonical enabled target set.
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

// RuntimeContractID identifies one reviewed, version-bounded runtime profile.
type RuntimeContractID string

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
