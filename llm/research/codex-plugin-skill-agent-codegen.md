---
title: "Codex plugin, skill, agent, and multi-harness delivery — Deep research"
date: "2026-07-13"
depth: "deep-dive"
request: "aura-plugins-ydhf9"
canonical_urd: "aura-plugins-iyd7j"
---

# Codex plugin, skill, agent, and multi-harness delivery

## Research status

This document is evidence for `FOLLOWUP_URD-2` and later proposals. It is not a
second requirements document or a recovery specification. The pre-reset version
mixed verified product contracts with proposal mechanisms and grew a detached
runner, executable-provider, memfd/APFS, active-generation, and exhaustive-matrix
design. Those mechanisms were removed after the user requested a structural
reset; the full audit remains in Beads Proposals 8–13.

Verified facts, current code seams, and explicit unknowns are retained below.

## Executive findings

1. Codex plugins natively carry skills and hooks, but current plugin manifests do
   not register custom agents. Codex custom agents are standalone TOML files in a
   project or user agent directory.
2. A truthful Pasture Codex distribution therefore has three independent units:
   a skills plugin, five standalone agent TOMLs, and a separately selectable hook
   plugin. The skills plugin may carry agent TOMLs as inert installer assets.
3. Pasture already has the right outer codegen seam (`TargetHarness`) and semantic
   sources (`RoleSpec`, `CommandSpec`), but `GeneratedFile` lacks component,
   package, destination, and ownership metadata. A generated delivery catalog is
   the missing bridge to the CLI and Home Manager.
4. Aura Home Manager already installs Claude Code and OpenCode skills and agents.
   Its immediate gaps are Codex, hooks, a clearer target/component matrix, and
   whole-directory skill delivery. The current module links only `SKILL.md`, so
   sibling metadata/assets can be omitted.
5. Pasture already reads `~/.config/pasture/config.yaml`, but only for two daemon
   audit fields and with no writer. Persistent installer choice is not currently
   modeled. Desired selection belongs in the versioned YAML; ownership, digests,
   pending work, and observations belong under XDG state.
6. Peasant kickstart is a useful interaction reference: Cobra owns discovery and
   mutation, Bubble Tea owns typed in-memory answers, and the final page precedes
   asynchronous apply. Its current persistence implementation must not be copied:
   it rebuilds from defaults, ignores path overrides and save errors, and writes
   non-atomically.
7. Direct artifact placement and third-party plugin activation have different
   failure models. Home Manager direct links should remain declarative. Companion
   direct writes need a bounded ownership/pending record. Claude/Codex plugin
   activation should use supported synchronous CLIs and explicit handling of an
   ambiguous result, not a universal transaction engine.

## Current official Codex contracts

Primary sources consulted:

- [Building Codex plugins](https://learn.chatgpt.com/docs/build-plugins)
- [Using Codex plugins](https://learn.chatgpt.com/docs/plugins)
- [Building Codex skills](https://learn.chatgpt.com/docs/build-skills)
- [Codex custom agents](https://developers.openai.com/codex/codex-manual.md#custom-agents)
- [Codex lifecycle hooks](https://learn.chatgpt.com/docs/hooks)
- [Codex configuration reference](https://developers.openai.com/codex/codex-manual.md#configuration-reference)
- [Codex 0.144.1 plugin manifest parser](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/core-plugins/src/manifest.rs)
- [Codex 0.144.1 native agent loader](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/core/src/config/agent_roles.rs)
- [Codex 0.144.1 skill loader](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/core-skills/src/loader.rs)

Local probes used `codex-cli 0.144.1`, isolated homes, `$skill-creator`, and
`$plugin-creator`.

### Plugin bundle

The required manifest is `.codex-plugin/plugin.json`. Supported plugin surfaces
include skills, hooks, apps/app templates, MCP servers, assets, and interface
metadata. Neither the public manifest contract nor the pinned runtime parser has
a native custom-agent component.

The truthful Pasture shape is:

```text
plugins/pasture/
  .codex-plugin/plugin.json
  skills/<skill>/SKILL.md
  skills/<skill>/agents/openai.yaml
  assets/agent-bundle/manifest.json
  assets/agent-bundle/pasture_*.toml

plugins/pasture-hooks/
  .codex-plugin/plugin.json
  hooks/hooks.json
  <hook assets>
```

The primary plugin activates skills. Its bundled TOMLs are data until an explicit
installer or Home Manager action places them in the agent loader's tree. Keeping
hooks in a second plugin is the simplest way to make “skills without hooks” true.

### Skills and UI metadata

Each skill is a directory with `SKILL.md`. The selected creator contract uses
frontmatter `name` and `description`; target UI metadata lives at
`agents/openai.yaml` and includes a curated display name, a 25–64 character short
description, and a default prompt that explicitly mentions `$skill-name`.

Pasture's existing descriptions cannot all be copied directly into that length
contract. Metadata therefore needs a typed exhaustive registry or projection, not
best-effort truncation. Whole skill directories must survive packaging because a
skill may have references, scripts, figures, assets, and UI metadata.

### Native custom agents

Codex loads custom roles from:

```text
.codex/agents/<role>.toml
~/.codex/agents/<role>.toml
```

Required fields are `name`, `description`, and `developer_instructions`.
Optional model, reasoning, sandbox, MCP, and skill configuration can inherit from
the live session. A custom role can override a built-in name, so Pasture must use
names such as `pasture_worker`, not `worker`.

An isolated probe placed an agent TOML inside a plugin and added an experimental
manifest field. Codex cached the file but did not register it. The plugin skill
was registered, demonstrating that plugin skills and native agent loaders are
independent surfaces.

### Marketplace and plugin lifecycle

Repository marketplaces use `.agents/plugins/marketplace.json`; personal
marketplaces use `~/.agents/plugins/marketplace.json`. Aura is the aggregate
marketplace owner. A Pasture entry can use a `git-subdir` source pinned by an
immutable commit and path.

Pinned probe commands include:

```text
codex plugin marketplace add <source>
codex plugin marketplace upgrade [marketplace]
codex plugin add pasture@<marketplace>
codex plugin list --json
codex plugin remove pasture
```

The observed runtime copied plugins into a private versioned cache and enabled
them through Codex configuration. Writing that cache directly is not a supported
installation API. A fresh thread is required to rebuild the initial skill/agent
catalog after some changes.

## Other harness contracts

Primary sources:

- [Claude Code plugin discovery and installation](https://code.claude.com/docs/en/discover-plugins)
- [Claude Code plugin reference and CLI](https://code.claude.com/docs/en/plugins-reference)
- [Claude Code marketplaces](https://code.claude.com/docs/en/plugin-marketplaces)
- [OpenCode plugins](https://opencode.ai/docs/plugins/)

Claude Code supports noninteractive plugin marketplace/install/uninstall commands
and copies installed plugins into a private cache. Pasture should invoke the
supported CLI and observe the public plugin list, never write cache internals.
Claude Code plugin skills are namespaced by plugin. The current monolithic
Pasture Claude plugin includes skills, agents, and hooks; a selective installer
must treat an active copy as a legacy conflict until a separately ratified
migration exists.

OpenCode discovers global JavaScript/TypeScript plugins under
`~/.config/opencode/plugins/`. Its target-native plugin API covers the relevant
session/system, compaction, and pre-tool behavior; copying Claude hook JSON would
not be correct. OpenCode skill and agent trees are already generated at their
native paths.

## Current Pasture model and seams

Relevant files in the Pasture worktree:

- `internal/codegen/harness.go` defines `HarnessName`, `TargetHarness`,
  `GeneratedFile{Path, Content}`, the registry, and `EmitHarness`.
- The current registry contains Claude Code and OpenCode. Codex belongs here.
- `internal/codegen/specs.go` defines `RoleSpec`, `CommandSpec`, and related
  canonical semantics.
- logical skill mappings are split across private role/command/support maps;
  installation must not infer component or role ownership from filenames.
- OpenCode recursively copies protocol/install support directories, proving that
  a delivery artifact can be a tree rather than one `SKILL.md`.
- `hooks/hooks.json` references files through `${CLAUDE_PLUGIN_ROOT}`, proving
  that a hook is a package with support assets, not a standalone JSON fragment.

The missing generated bridge needs to express:

```text
canonical semantic owner
  → rendered content artifact
  → package member and package-relative placement
  → selectable harness/component unit
  → direct or external activation strategy
```

This can be added adjacent to codegen without polluting `RoleSpec` or
`CommandSpec` with installer policy.

### Runtime dialect depth

Pasture source prose contains Claude-specific operations such as skill
invocation, initial spawn, follow-up dispatch, messaging, waiting, user input,
and model/tool names. A Codex target that only changes frontmatter and paths can
install successfully while remaining operationally wrong.

A typed runtime seam should distinguish literal text from runtime references and
render references using harness, surface, owner, and lifecycle context. It must
visit every active generated text owner. A global post-render string replacement
cannot model self-invocation, root orchestration versus leaf work, follow-up to a
persistent agent, or harness-specific user-input fallback.

## Current Pasture configuration

`internal/config/config.go` currently defines:

```go
type PasturedConfig struct {
    AuditTrail  types.AuditTrailBackend
    AuditDBPath string
}
```

`DefaultConfigPath()` returns `~/.config/pasture/config.yaml`. The Viper resolver
implements CLI > environment > YAML > defaults for those fields. It is read-only;
there is no unified config loader/writer, installer selection, schema version,
or atomic update API. `ProvenanceConfig` exists but its nested resolver is noted
as unwired.

Consequences:

- persistent installer choice is absent from the current data model;
- desired selection can be added without changing canonical protocol specs;
- keeping existing root audit keys avoids an unnecessary migration;
- a strict versioned root config can add `provenance` and `install` sections;
- applied ownership must not reuse `pasture.db`, which is workflow/audit data.

Recommended state boundary for proposal evaluation:

```text
${XDG_CONFIG_HOME:-~/.config}/pasture/config.yaml  # desired human intent
${XDG_STATE_HOME:-~/.local/state}/pasture/install # ownership/recovery evidence
```

## Current Aura Home Manager module

`nix/hm-module.nix` already:

- sources Pasture-generated Claude Code skills/agents;
- sources generated OpenCode skills/agents;
- installs them through `home.file`;
- has no Codex or hook target options;
- links only each `SKILL.md` for skills;
- uses filename-prefix logic for some role selection;
- sources Pasture rather than deprecated Aura agent/skill trees.

The user question “why wouldn't Home Manager install the skills too?” exposed a
proposal error, not a live-module absence: it already does. The new work should
preserve that behavior, install whole directories, and add a clear
`targets.<harness>.components` matrix.

Direct links should remain Home Manager links. Sharing a catalog and desired
selection semantics does not require Home Manager and the companion CLI to share
one mutation engine. Plugin cells are different: Codex and Claude plugins require
supported CLI activation and public postcondition observation. A narrow
Home Manager activation adapter can handle only those cells.

The Aura flake currently exports Linux packages. Any Home Manager or external
plugin platform claim must match actual package and conformance evidence.

## Peasant kickstart comparison

Read-only exploration used `/home/minttea/dev/peasant-labs/peasant/develop`.

Useful boundaries:

- the Cobra command performs path/environment resolution, discovery, and service
  injection;
- Bubble Tea receives DTOs and injected functions rather than importing the
  ingest implementation;
- pages preserve answers across back-navigation and conditional flow;
- discovery progress is TTY-only;
- a summary is rendered before apply;
- desired YAML is distinct from credentials, data, and PID/runtime state.

Do not copy these current persistence defects:

- wizard save rebuilds a fresh default config and can drop non-wizard values;
- it ignores the command's custom config path;
- it writes directly rather than temp+fsync+rename;
- save and external-settings errors can be discarded while the UI reports
  success;
- YAML parsing is not strict and unknown keys may be ignored;
- reset conflates desired config, credentials, data, and runtime state.

Pasture should use the interaction grammar but implement strict typed load,
semantic preservation, exact path routing, atomic publication, and explicit
save/apply errors.

## Structural audit of Proposals 7–13

Proposal 7 passed all three review axes for the narrower Codex-agent subtree.
Proposal 8 grafted the 3×3 matrix, Home Manager, and TUI onto that design. Later
reviews repeatedly found gaps at new boundaries:

```text
semantic content → package membership → placement → activation
desired selection → owner → direct or external convergence
external command → observation → ambiguous result
platform claim → actual executable/package fixture
production catalog → independent test oracle
```

Because each proposal declared earlier proposals normative, local fixes retained
all earlier mechanisms. One external-command concern cascaded through detached
runner, start gate, executable copy, partial-copy oracle, memfd/APFS, helper
identity, provider algebra, and active Home Manager capability. Same-user tools
were simultaneously trusted to own configuration and modeled as hostile during
exec, so the threat model could not converge.

The structural correction is to:

- keep requirements free of review-invented mechanisms;
- state a cooperative same-user threat boundary;
- give direct files, Home Manager links, and external CLIs separate lifecycles;
- persist desired selection before treating installation as a declarative
  convergence problem;
- make ambiguous third-party outcomes explicit/manual;
- make untested cells unavailable instead of solving hypothetical platforms;
- test nine cell contracts and meaningful state boundaries, not products of
  every orthogonal enum.

## Evidence-backed capability map

| Harness/component | Loader/activation evidence | Initial strategy |
|---|---|---|
| Claude skills | current generated tree and Aura links | direct whole directories |
| Claude agents | current generated agents and Aura links | direct files |
| Claude hooks | current plugin package and official plugin CLI | separate external plugin |
| OpenCode skills | current generated `.opencode/skill` tree | direct whole directories |
| OpenCode agents | current generated `.opencode/agent` tree | direct files |
| OpenCode hooks | official global plugin directory and target-native API | direct plugin tree/file |
| Codex skills | official/current plugin loader and isolated probe | external skills plugin |
| Codex agents | current TOML loader and isolated probe | direct TOMLs |
| Codex hooks | official/current plugin hook surface | separate external plugin |

The table is a research input. The generated catalog and a hand-authored test
fixture must independently freeze exact destinations, packages, versions, and
platform availability before implementation calls a cell supported.

## Explicit unknowns and required implementation probes

1. Pin the release-time Claude Code, OpenCode, and Codex versions/checksums and
   freeze exact machine-readable observe/add/remove argv fixtures.
2. Prove each target loader follows the proposed direct tree/file placements,
   including whole-directory symlinks under Home Manager.
3. Prove the final Codex hook manifest against both current repo validation and a
   real isolated CLI because creator/runtime hook schemas have drifted before.
4. Decide at Plan UAT whether Darwin external plugin cells, mixed ownership,
   per-role companion selection, automatic ambiguous recovery, legacy adoption,
   and project export are deferred.
5. Record any new Go TUI/YAML dependency and exact version before lockfile
   mutation.

Unsupported or unverified results must produce visible capability reasons and
must not fall back to cache writes, hidden hooks, or unreviewed platform claims.

