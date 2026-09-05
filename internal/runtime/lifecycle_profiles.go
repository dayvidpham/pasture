package runtime

import "github.com/dayvidpham/pasture/internal/codegen/ir"

// This file holds only the helpers that every pinned lifecycle profile uses:
// the identity-list builder, the whole-profile validation the generator calls,
// and the name-keyed failure lookup the hook command calls. It declares no
// harness row. Each harness has its own file, lifecycle_profiles_claude.go,
// lifecycle_profiles_codex.go and lifecycle_profiles_opencode.go, so that a
// change to one harness never has to touch the file another harness is being
// edited in. A row added here would put two harnesses back into one file; the
// placement test in profiles_test.go refuses a harness-named declaration
// here and a foreign or shared declaration in a harness file.

func identities(base []NativeIdentityField, extra ...NativeIdentityField) []NativeIdentityField {
	result := make([]NativeIdentityField, 0, len(base)+len(extra))
	result = append(result, base...)
	result = append(result, extra...)
	return result
}

// IdentityPolicy is a TABLE LABEL over a lifecycle row's declared identities.
// It has NO waist effect: it decides nothing about how an event lands. An
// IdentityPolicyNone row declares no identities, lands as an occurrence with
// zero identities and no correlation key, is ordered by ingress sequence
// only, and never produces an unresolved fact. The label is DERIVED from the
// identities column rather than stored beside it, so it cannot disagree with
// the column it describes.
type IdentityPolicy uint8

const (
	IdentityPolicyInvalid IdentityPolicy = iota
	// IdentityPolicyNone marks a row that declares no correlation identity.
	IdentityPolicyNone
	// IdentityPolicyDeclared marks a row that declares at least one.
	IdentityPolicyDeclared
)

func (p IdentityPolicy) IsValid() bool { return p == IdentityPolicyNone || p == IdentityPolicyDeclared }

func (p IdentityPolicy) String() string {
	switch p {
	case IdentityPolicyNone:
		return "none"
	case IdentityPolicyDeclared:
		return "declared"
	default:
		return ""
	}
}

// IdentityPolicy labels this row by its declared identities. See IdentityPolicy.
func (m LifecycleEventMapping) IdentityPolicy() IdentityPolicy {
	if len(m.identities) == 0 {
		return IdentityPolicyNone
	}
	return IdentityPolicyDeclared
}

// Pi intentionally has no lifecycle contract constructor. Its extension and RPC
// research informed the semantic split, but no Pi adapter is shipped.

// ValidatePinnedLifecycleProfiles rebuilds every pinned lifecycle profile and
// returns the first row that fails contract validation, WITHOUT panicking.
//
// The three profile constructors panic on an invalid row, which is right for a
// program that is already running: an invalid contract must never reach code
// generation. A generator, though, has to report the offending row before it
// writes anything, so it calls this instead and prints the six-part diagnostic.
func ValidatePinnedLifecycleProfiles() error {
	if _, err := newLifecycleContract(
		ClaudeCode2_1_210(), ClaudeLifecycleEvents(), claudeLifecycleMappings(),
	); err != nil {
		return err
	}
	if _, err := newLifecycleContract(
		Codex0_146_0(), CodexLifecycleEvents(), codexLifecycleMappings(),
	); err != nil {
		return err
	}
	if _, err := newLifecycleContract(
		OpenCode1_18_10(), OpenCodeLifecycleEvents(), openCodeLifecycleMappings(),
	); err != nil {
		return err
	}
	return nil
}

// LifecycleFailurePolicy is the declared failure behaviour of one native event:
// the mode the host contract says applies, the citation for a blocking exit
// code, and the event class the host reads the answer as.
type LifecycleFailurePolicy struct {
	// Mode is the EFFECTIVE failure mode of the row, after the failure-evidence
	// rule. Every behaviour obeys this one.
	Mode FailureMode
	// DeclaredMode is the mode the host contract DECLARES for the row, before
	// the evidence rule demotes an uncited blocking row to report-and-continue.
	// It equals Mode on every row the rule does not demote.
	//
	// It is carried so that an EXPLANATION can tell an operator the truth about
	// the declaration. A blocking row with no citation and a row that is
	// declared report-and-continue are indistinguishable once the demotion has
	// happened, and they need opposite advice: the first becomes blockable when
	// somebody supplies the citation, the second can never block. Read this
	// field only to explain the row; never to choose an exit status.
	DeclaredMode FailureMode
	Evidence     FailureEvidence
	// Semantic is the declared class of the event. It is carried here because
	// the bytes a host reads as "proceed" depend on the class as well as on the
	// harness: a Codex gate is refused unless the continuation says continue,
	// while a Codex observation contributes no directives. The zero value means
	// the event is not declared by this build, and the caller must not guess a
	// class from it.
	Semantic EventSemantic
}

// Declared reports whether a row of this build's registration declares the
// event this policy describes. Every declared row carries a valid Semantic —
// a lifecycle contract refuses a mapping without one, in
// LifecycleEventMapping.validate — and the one producer of an undeclared
// policy, the hook command's fallback for a coordinate this build cannot name,
// leaves it zero. It exists so a diagnostic or a durable record can say
// "undeclared" instead of calling the mode such a coordinate is treated as a
// declaration.
func (p LifecycleFailurePolicy) Declared() bool { return p.Semantic.IsValid() }

// LookupLifecycleFailure returns the declared failure behaviour of one native
// event, by the harness and the exact native NAME.
//
// The typed lifecycle contracts have no string lookup on purpose, so a caller
// cannot invent an event. This function is the ONE narrow exception, and it
// exists because a real host really does hand the hook a harness and an event
// NAME on the command line. It answers only this one question, it cannot reach
// identities, payload fields or ordering, and it returns false for a name no
// pinned profile declares, so the caller must decide what an unknown event
// means rather than receive a guess.
func LookupLifecycleFailure(harness ir.HarnessID, nativeName string) (LifecycleFailurePolicy, bool) {
	switch harness {
	case ir.HarnessClaudeCode:
		return lookupLifecycleFailure(ClaudeCode2_1_210Lifecycle(), ClaudeLifecycleEvents(), nativeName)
	case ir.HarnessCodex:
		return lookupLifecycleFailure(Codex0_146_0Lifecycle(), CodexLifecycleEvents(), nativeName)
	case ir.HarnessOpenCode:
		return lookupLifecycleFailure(OpenCode1_18_10Lifecycle(), OpenCodeLifecycleEvents(), nativeName)
	default:
		return LifecycleFailurePolicy{}, false
	}
}

func lookupLifecycleFailure[E comparable](
	contract LifecycleContract[E],
	events []E,
	nativeName string,
) (LifecycleFailurePolicy, bool) {
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		if err != nil {
			continue
		}
		if mapping.NativeName() == nativeName {
			return LifecycleFailurePolicy{
				Mode:         mapping.Failure(),
				DeclaredMode: mapping.DeclaredFailure(),
				Evidence:     mapping.Evidence(),
				Semantic:     mapping.Semantic(),
			}, true
		}
	}
	return LifecycleFailurePolicy{}, false
}
