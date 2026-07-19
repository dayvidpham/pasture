// Package provadapter is Pasture's thin low-level adapter over the released
// Provenance global-journal surface (github.com/dayvidpham/provenance at
// dayvidpham/provenance#4/#5/#6, module pinned to main@7b3451a). It owns exactly
// the boundary conversions and system-actor activation that pasture#14 places
// between the portable protocol identity domain (pkg/protocol/portable, produced
// by the in-memory compiler internal/codegen/ir) and Provenance's durable
// identity, authority, and command-digest domains.
//
// Scope boundary (pasture#14). This package deliberately does NOT define any
// pasture.* material event, effect-to-command mapping, task-closure policy, task
// backend semantics, timeline projection, or public CLI vocabulary — those are
// pasture#43, the exclusive consumer of this adapter. Everything here is a pure,
// side-effect-free conversion or a single-purpose registry activation against a
// real Provenance store; there is no split audit-store write path.
//
// The four capabilities delivered here, each independently testable against a
// real Provenance store (OpenMemory / a temp-file tracker):
//
//   - refs.go: validated, round-tripping conversions between the portable
//     AssignmentRef/TaskRef/RoleID/MutationRef/AgentRef domain and Provenance's
//     AssignmentID/TaskID/AssignmentSlotID/OperationID/ActorID domains, rejecting
//     wrong or empty domains without making the dependency-free portable package
//     depend on Provenance.
//   - authority.go: derivation of a non-aliasing Provenance OperationAuthorityID
//     from a portable bootstrap-agent or assignment authority, preserving
//     authority kind/slot/task so two authorities of the SAME actor over
//     different assignment kinds/slots/tasks can never alias to one key; plus the
//     retained-initiating-identity -> historical ActorID resolution used for
//     committed replay/lookup (never re-resolving whichever assignment is current).
//   - digest.go: byte-preserving conversion of a #43-produced
//     ir.CanonicalCommandDigest to the raw Provenance command-digest bytes handed
//     to LookupCommitted and Apply, with no re-canonicalization and no caller
//     digest flag.
//   - actors.go: activation of the reserved pasture-system actor namespace
//     (ordinals 0..1023, manifest-v1 ordinal zero seeded as pasture-system/default
//     / software_agent) over a real Provenance actor-namespace registry, with the
//     fixed big-endian ordinal-UUID wire encoding.
//   - facade.go: the thin Apply/LookupCommitted facade over the single Provenance
//     journal write path (no split audit store), routing every call through the
//     ref/authority/digest conversions above and surfacing the closed
//     Absent/Committed/Conflict outcome with the underlying typed provenance error
//     round-tripped for errors.Is/As.
//   - migrate.go: the finite serial legacy-baseline migration coordinator over
//     MigrateLegacyBaseline — deterministic (RecordedAt, LegacyRowID) ordering,
//     read-only source byte-hash integrity before and after, whole-batch
//     stop-on-first-failure with typed errors round-tripped, and idempotent
//     re-run counts surfaced.
//   - legacyaudit.go: the separately-named, read-only 'pasture legacy-audit event
//     list' API over NON-TASK legacy rows, preserving raw actor text, contexts, and
//     source identity verbatim in deterministic order — the surface #43's CLI wires
//     (no CLI wiring here).
package provadapter
