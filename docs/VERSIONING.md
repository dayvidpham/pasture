# Pasture Versioning & Consumption Policy

> **Status: `v0.x` (pre-1.0).** Pasture is now **released**: latest tag `v0.0.4`,
> a plugin manifest (`.claude-plugin/plugin.json`), a `pasture` entry in the
> `aura-plugins` marketplace, and an automated **tag-on-merge** release flow are
> all in place. The R5 plugin-distribution residual (`76qby`) is closed. This
> document specifies the versioning **policy** per consumption channel; the
> operational **release recipe** lives in
> [CONTRIBUTING.md](../CONTRIBUTING.md#releasing). Authored by audit
> `aura-plugins-h2zd9` (elicit `aura-plugins-bb2om`, URD `aura-plugins-oy9s7`).

Pasture is consumed through **three channels**. The Claude Code **plugin** channel is the
primary, imminent driver of versioning; the **inter-tool** channel is the real *current*
consumer; the **external Go** channel is deferred-but-plausible.

| # | Channel | Consumer | Status | Versioning need |
|---|---------|----------|--------|-----------------|
| ① | **Claude Code plugin** | skills + agent definitions, installed via Claude Code | content + release tooling ready; **no manifest, 0 tags** | **semver tags + plugin manifest — needed now** |
| ② | **External Go import** | `pkg/protocol` imported by another module | no importer yet; **peasant** (ex-`agent-data-leverage`) is a plausible future consumer (analytics tool / taxonomy) | `v0.x` no-guarantees; pin a commit; stabilization trigger |
| ③ | **Inter-tool** | the pasture binaries (`pastured`, `pasture-msg`, `pasture`, `pasture-release`) | **real, current** — 54 internal imports | shared contract to prevent drift |

---

## ① Claude Code plugin channel (skills + agent definitions) — PRIMARY

Pasture generates `skills/*/SKILL.md` and `agents/*.md` that are consumed as a **Claude Code
plugin**. This is the channel that needs versioning *now*.

- **Current state: shipped.** The content exists (`pasture/skills/`, `pasture/agents/`), the
  release machinery exists (`pasture-release`: `SemVer`, `GenerateChangelog`,
  `GitLatestVersionTag`, `GitCommitsSince`), and the channel is now live: pasture has its own
  `.claude-plugin/plugin.json`, a `pasture` entry in `aura-plugins/.claude-plugin/marketplace.json`,
  semver tags (latest `v0.0.4`), and an automated tag-on-merge release flow
  (see [CONTRIBUTING.md](../CONTRIBUTING.md#releasing)).
- **Distribution mechanism:** pasture is published as a **separate-repo plugin within the
  `aura-plugins` Claude Code marketplace** — it does **not** host its own marketplace. The glue
  mirrors the existing `agentfilter` plugin entry:
  1. The **pasture repo** carries its own `.claude-plugin/plugin.json` (manifest: `name: pasture`,
     `version`, `repository`, `keywords`).
  2. **`aura-plugins/.claude-plugin/marketplace.json`** gains a `pasture` entry under `plugins`
     with `"source": { "source": "github", "repo": "dayvidpham/pasture" }` and a `version` matching
     pasture's latest tag (exactly how `agentfilter` is wired: `"version": "0.4.0", "source":
     {"source":"github","repo":"dayvidpham/agentfilter"}`).
  3. pasture cuts semver git **tags** (`pasture-release`); the marketplace entry's `version` tracks
     the tag.
- **Versioning strategy:** semantic versioning, cut by `pasture-release`; a **git tag is the
  unit of release**.
  - **MAJOR** — a breaking change to a skill/agent *contract* (renamed/removed skill, changed
    required inputs, changed phase IDs a consumer relies on).
  - **MINOR** — additive (a new skill/agent, a new behavior block, a new optional field).
  - **PATCH** — wording/fix that doesn't change the contract.
- **Example:** renaming the `worker-slices` phase ID, or removing the `impl-review` skill, is a
  **MAJOR** bump; adding a new `aura:research` skill is **MINOR**; fixing a typo in the supervisor
  SKILL.md body is **PATCH**.

## ② External Go import channel (`pkg/protocol`) — DEFERRED (`v0.x`)

`github.com/dayvidpham/pasture/pkg/protocol` is the public Go surface (`TaskTracker`, `PhaseId`,
`AuditEvent`, `SessionEntry`, …). **No external module imports it today.**

- **Plausible future consumer:** `peasant` (formerly `agent-data-leverage`) — *might* base its
  **analytics tool** on it, or use it to **inform its taxonomy** (the "analytics convergence"
  direction). `SessionEntry` is already *aligned* with peasant's schema (`session_entry.go:5`),
  so it is the most likely first externally-consumed type.
- **Policy (pre-1.0):** **no semver guarantees** on the Go API. External consumers **pin a commit
  pseudo-version** (`go get github.com/dayvidpham/pasture@<commit>`), not a tag.
- **Stabilization trigger:** when a real external consumer (peasant's analytics, or an orchestrator
  UI) *actually imports* `pkg/protocol`, freeze the consumed surface and **cut `v1.0.0`**, after
  which channel ② follows the same semver rules as ①.
- **Example:** today, a consumer wanting `SessionEntry` pins
  `github.com/dayvidpham/pasture@abc1234`; there is no promise that `abc1234`'s `SessionEntry`
  matches next week's. Post-`v1.0`, `SessionEntry` changes follow semver.

## ③ Inter-tool channel (the pasture binaries) — REAL CURRENT consumer

`pastured` (Temporal worker daemon), `pasture-msg` (control CLI), `pasture` (task/audit CLI), and
`pasture-release` all import `pkg/protocol`. This is the **real, present** consumption — and its
purpose is **drift-prevention**.

- **The contract:** the shared single-source-of-truth is **`pkg/protocol`** (types: `PhaseId`,
  `AuditEvent`, `TaskTracker`, `VoteType`, …) **plus `internal/temporal/constants.go`** (signal /
  query names: `SignalSubmitVote`, `SignalAdvancePhase`, `QueryCurrentState`, `QueryFullState`, …).
- **Why it matters:** `pastured` *registers* the workflow's signals/queries; `pasture-msg` *sends*
  them. If either hardcoded a name or redefined a type, they would **drift** and silently fail to
  communicate. Both referencing the same constants/types makes drift a compile error, not a runtime
  surprise.
- **Convention:** in-tree binaries **import `pkg/protocol` directly** (never via `internal/types`
  aliases — see `pasture/CLAUDE.md`) and **never hardcode** a signal/query string — always use the
  `internal/temporal/constants.go` constant.
- **Example:** adding a new query, a contributor adds `QuerySliceProgressState = "slice_progress_state"`
  to `constants.go` once; both `pastured`'s query handler and `pasture-msg`'s query command
  reference that constant, so they cannot disagree on the wire name.

---

## The 7 spec axes (cross-channel summary)

| Axis | Policy |
|------|--------|
| **versioning-strategy** | SemVer, cut by `pasture-release`; a git tag is the release unit (channel ①). Pre-1.0 today (0 tags). |
| **semver-guarantees** | Pre-1.0: **none** for external consumers (①/②). Internal (③): the binaries live in one Go module and move in lockstep — no inter-binary version skew possible. |
| **import-path-convention** | External: `github.com/dayvidpham/pasture/pkg/protocol`. Internal: import `pkg/protocol` directly, not `internal/types` aliases; never hardcode signal/query names (use `internal/temporal/constants.go`). |
| **deprecation-policy** | **Pre-1.0 (now): anything goes** — any symbol/skill/command/type may change or be removed in *any* release, with **no deprecation window** required (still note notable removals in CHANGELOG). Post-1.0: **one MINOR-version window** — deprecate in `vX.Y`, remove no earlier than `vX.(Y+1)`; deprecated items get a `DEPRECATED.md` entry + an in-skill banner (see breaking-change-communication). |
| **module-version-pinning** | External consumers pin a **commit pseudo-version** until `v1.0` tags exist; then pin a tag. |
| **stability-tiers** | **Stable** (heavily-used, safe to depend on): `TaskTracker`, `PhaseId`, `AuditEvent`. **Experimental** (may move): `SessionEntry` (aligned with peasant; will change as that alignment firms up), newer ACP types. |
| **breaking-change-communication** | **Three mechanisms:** (1) **CHANGELOG** generated by `pasture-release` (`GenerateChangelog`) from Conventional Commits, with migration notes on MAJOR; (2) a standing **`DEPRECATED.md`** ledger listing each deprecated item → its scheduled removal version (mirrors the Python package's `DEPRECATED.md`); (3) an **in-skill deprecation banner** emitted into the generated `SKILL.md` header by the codegen when a command/skill is deprecated, so the deprecation surfaces at the point of use. Note: removing a skill = deregistering its `cmd-*` command (the codegen regenerates from registrations) → a MAJOR bump. |

---

## R5 verdict (`jbnx3` R5 — "Shared Go Library")

**`R5 status: DONE`.** The shared library exists and is internally consumed
(channel ③), and the plugin-distribution versioning has now landed: a plugin
manifest, a marketplace entry, and semver tags (latest `v0.0.4`) cut by
`pasture-release` via the tag-on-merge flow (channel ①). External Go consumption
(②) remains deferred by design and never blocked R5.

### Residuals appendix

| bd task | summary | status |
|---|---|---|
| `aura-plugins-76qby` | Add pasture's `.claude-plugin/plugin.json`, register a `pasture` github-source entry in `aura-plugins/.claude-plugin/marketplace.json` (mirroring `agentfilter`), and cut the first `pasture-release` semver tag — unblocks the Claude Code plugin channel + R5 | **DONE** (closed) |
