# `pkg/protocol` — Pasture's public protocol types

This package is the **public, importable surface** of Pasture: the shared protocol types
(`TaskTracker`, `PhaseId`, `AuditEvent`, `SessionEntry`, `AgentCategories`, `ContextKind`, …)
that pasture's own binaries and any external consumer build on.

## Stability: `v0.x` — pre-1.0, no external API guarantees yet

Pasture is **unversioned** (0 git tags). **There is no semver guarantee on this Go API today.**

- **External consumers:** pin a commit pseudo-version — `go get github.com/dayvidpham/pasture@<commit>`.
  Do **not** assume the surface is stable between commits.
- **Stabilization trigger:** when a real external consumer actually imports this package (the
  anticipated first ones are an orchestrator **UI app**, or **peasant**'s analytics / taxonomy —
  `SessionEntry` is already aligned with peasant's schema), the consumed surface will be frozen and
  `v1.0.0` cut.

### Stability tiers

| Tier | Types | Depend on it? |
|------|-------|---------------|
| **Stable** | `TaskTracker`, `PhaseId`, `AuditEvent` (heavily used internally) | yes, but still pre-1.0 |
| **Experimental** | `SessionEntry` (aligned with peasant; will move), newer ACP types | expect changes |

## Internal consumers — this is the anti-drift contract

The pasture binaries (`pastured`, `pasture`, `pasture-release`) all import this
package as their **single source of truth**. **Import these types directly
(not via `internal/types` aliases) and never hardcode a signal/query string.**

## Full policy

See [`pasture/docs/VERSIONING.md`](../../docs/VERSIONING.md) for the complete three-channel
consumption + versioning policy (Claude Code plugin, external Go, inter-tool) and the R5 status.
