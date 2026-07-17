// Package effects defines Pasture's typed process, Git, and filesystem
// workflow effect algebra: opaque, constructor-validated operands and closed
// effect sums that make operational semantics explicit instead of hiding them
// in shell-fragment strings.
//
// The package models effects; it does not grant permission. Typing an effect
// never bypasses harness, user-sandbox, escalation, or hook policy — execution
// of any effect still obeys those constraints. See RuntimeClass for how each
// effect is classified for a runtime contract.
//
// Import direction is strictly one-way: a consumer such as the authoritative
// task package imports effects and immediately hands a verified proof (see
// VerifiedGuardedPush) to its protected commit. effects never imports the task
// package; TestEffectsImportsNoTaskPackage enforces that edge.
package effects

import "github.com/dayvidpham/pasture/internal/codegen/ir"

// effectError builds the shared actionable diagnostic used across the effect
// algebra. It reuses ir.Diagnostic so effect and IR errors present the same
// what/why/where/phase/impact/fix/cause shape to callers.
func effectError(what, why, where, phase, impact, fix string, cause error) error {
	return &ir.Diagnostic{
		What:   what,
		Why:    why,
		Where:  where,
		Phase:  phase,
		Impact: impact,
		Fix:    fix,
		Cause:  cause,
	}
}

// RuntimeClass is the closed classification a runtime contract assigns to every
// modeled effect. It is deliberately exhaustive: every effect this package can
// construct answers Classify with exactly one of these, so a lowerer can never
// encounter an effect it has no explicit plan for.
type RuntimeClass string

const (
	// RuntimeClassNative is executed directly by the host harness runtime.
	RuntimeClassNative RuntimeClass = "native"
	// RuntimeClassParentMediated is executed by the parent orchestrator on the
	// effect's behalf (for example a guarded landing push).
	RuntimeClassParentMediated RuntimeClass = "parent-mediated"
	// RuntimeClassSemanticInstruction is lowered to a semantic instruction the
	// agent must carry out (for example repository-policy commit guidance).
	RuntimeClassSemanticInstruction RuntimeClass = "semantic-instruction"
	// RuntimeClassUnsupported names a construct that has no modeled semantics
	// and must become a dedicated operation before it can be lowered. It never
	// renders through an opaque shell string.
	RuntimeClassUnsupported RuntimeClass = "unsupported"
)

func (c RuntimeClass) IsValid() bool {
	switch c {
	case RuntimeClassNative, RuntimeClassParentMediated, RuntimeClassSemanticInstruction, RuntimeClassUnsupported:
		return true
	default:
		return false
	}
}

// Effect is the closed super-sum of every modeled workflow effect. Each variant
// is an opaque constructor-owned value; the marker method keeps the sum closed
// so exhaustive lowering and classification cannot silently miss a variant.
type Effect interface {
	// Classify reports the runtime class a contract must honor for this effect.
	Classify() RuntimeClass
	// isEffect keeps the sum closed to this package's constructed variants.
	isEffect()
}
