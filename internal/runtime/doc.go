// Package runtime defines Pasture's opaque, version-bounded runtime contracts:
// the only authority allowed to turn a protocol semantic operation, effect, or
// typed capability into native runtime behavior for a specific harness at a
// specific host version.
//
// A RuntimeContract binds exactly one enabled harness (see ir.HarnessID) and a
// VersionConstraint. It classifies every used orchestration operation,
// task/process/Git/filesystem effect, and typed capability exactly once as one
// of effects.RuntimeClass's four members — native, parent-mediated,
// semantic-instruction, or unsupported — and exposes three separate,
// opaque-descriptor lookup families:
//
//   - LookupOperationBinding accepts an ir.OperationDescriptor[In, Out];
//   - LookupEffectBinding accepts an ir.EffectDescriptor[In, Out];
//   - LookupCapabilityBinding accepts an ir.Capability[In, Out].
//
// Every lookup returns (binding, error). Missing, wrong-type, unbound,
// out-of-range, or unsupported descriptors fail actionably; none collapses
// into a nil or zero binding. Named string IDs remain descriptor metadata
// only: an assignable untyped literal is never a lookup operand, because the
// operand is always a constructor-validated typed descriptor.
//
// External packages contribute a version-bounded binding for an opaque
// capability descriptor with BindCapability, without editing this package.
// NewRuntimeContract validates every contribution's codecs, semantics, effect
// set, and capability-version intersection, requires each requested capability
// exactly once, and rejects missing, duplicate, or conflicting contributions.
//
// # Scope boundary (delivered-surface divergence)
//
// Issue #40 also describes a landing-composition path — the versioned
// `pasture task integration publish-repository` runtime binding — that threads
// a MutationContinuation through #43's pure canonicalization/publication
// preparation and #46's guarded push, and a #49 epoch interaction-mode
// lowering. Those paths consume task-package APIs (a `task command
// canonicalize` entry point, a side-effect-free publication preparation, and a
// `CommitRepositoryPublication` commit) that the ordered #43 base plus #49
// policy files do not yet export in internal/tasks. This package therefore
// delivers the buildable core: the version-bounded contract, the three lookup
// families, external capability composition, and the initial pinned point
// contracts. The composite landing binding and the interaction-mode lowering
// are deferred until their task-side dependencies land, and are tracked on the
// slice rather than stubbed with a fabricated task surface here.
package runtime
