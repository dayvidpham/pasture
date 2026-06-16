# DESIGN INVESTIGATION REPORT — OpenCode + Codex Compilation Targets for Pasture Codegen

**Scope:** Add OpenCode and Codex harness targets to pasture's Go codegen pipeline (`internal/codegen/` + `tools/codegen/main.go`), plus the plugin/marketplace/distribution files so users of those harnesses can install pasture skills.
**Date:** 2026-06-15 · **Author:** Lead architect · **Status:** Investigation (no code written)
**Revision:** Final. Disputed source claims re-verified this session (see §0 and §9).

---

## 0. Corrections applied since draft (read first)

This revision fixes four factual defects in the draft, each re-checked against source this session:

1. **OpenCode skill-validator schema — DOWNGRADED from "verified."** The current source at `~/codebases/opencode/packages/opencode/src/skill/index.ts:30-50,86-99` defines the skill schema as exactly `z.object({ name: z.string(), description: z.string(), location, content })`. There is **no `name` regex, no ≤64 char bound, and no 1–1024 char `description` bound** in current source. The draft's "verified-in-source regex/length" claim was wrong; those specifics do not exist in the cited file. Furthermore, `SkillNameMismatchError` is **declared but never thrown** anywhere in the OpenCode source tree (`grep` confirms the only occurrence is the `NamedError.create` declaration). The loader keys a skill purely by its frontmatter `name` (`index.ts:98`). **Net effect on the thesis:** the "skill artifact needs no per-harness fork" conclusion is *more* supported than the draft argued (the validator is *more* permissive than claimed), but the basis is now correctly stated as "the live schema accepts arbitrary `name`/`description` strings and ignores all other frontmatter keys," not a regex spec. The pasture name-equals-dirname convention is still good hygiene but is **not** currently enforced by OpenCode.

2. **Skill count corrected: 29 generated, not 27.** The pipeline generates **5 role skills + 24 command skills = 29** marker-bounded `SKILL.md` files. Verified two ways: `commandSkillDirs` in `tools/codegen/main.go:72-98` has **24 entries** (counted), and `grep -rl "BEGIN GENERATED" pasture/skills/` returns **29**. The draft's "22 commands" was the **stale "Python-not-ported" figure** echoed by an out-of-date in-code comment (`main.go:75` literally says "22 skills") and by `pasture/CLAUDE.md` (which project memory explicitly flags as stale). Slice-1 fixture/route sizing must use **29 / 24**.

3. **Renderer signatures corrected.** The draft claimed `renderSkill`/`renderSubSkill`/`renderAgent` "already accept an explicit output path." **They do not.** Verified actual signatures:
   - `renderSkill(roleId protocol.RoleId, figuresDir string)` — `skills.go:408`
   - `renderSubSkill(commandId, figuresDir string)` — `skills.go:494`
   - `renderAgent(roleId protocol.RoleId, figuresDir string)` — `agents.go:74`
   The output-path parameter lives on the **`Generate*` wrappers**, not the renderers:
   - `GenerateSkill(roleId, skillPath, figuresDir, opts)` — `skills.go:591`
   - `GenerateSubSkill(commandId, skillPath, figuresDir, opts)` — `skills.go:686`
   - `GenerateAgent(roleId, agentPath, figuresDir, opts)` — `agents.go:163`
   The architectural conclusion is unchanged (the renderers are still reusable; only template selection needs threading), but §2.5/§3 now name the right functions.

4. **Byte-identical regression guard — DOWNGRADED to "content-fragment guard."** The existing `testdata/skills.yaml` uses `must_contain` / `must_contain_headers` checks ("contains-expected-sections strategy (not exact string matching)" — verbatim from the fixture header), **not** byte-for-byte assertions. So the Slice-1 "byte-identical default output" guarantee is **not** provided by today's harness and must be **built** if wanted (see §7 Slice 1 and OPEN ITEM O-8).

---

## 1. Executive Summary

- **Feasible, and the skill content is highly portable.** Pasture's generated `skills/<name>/SKILL.md` files use `name` + `description` YAML frontmatter plus a markdown body. The **current** OpenCode skill loader (`skill/index.ts`) accepts any `name`/`description` strings and ignores all other frontmatter keys; it does not enforce a name regex, length bounds, or name-equals-dirname (the mismatch error type is declared but unthrown). Codex's skill format is the same `name`/`description` + body shape per third-party evidence (**but see the Codex evidence caveat below**). Pasture frontmatter (e.g. `name: worker`) therefore needs **no per-harness fork** for the OpenCode skill tier, and almost certainly none for Codex.
- **The work splits into two cleanly-separable tiers.** Tier A (small, interface-first): a `Target` abstraction at the orchestration layer so existing renderers emit skills into harness-specific output trees. Tier B (greenfield, larger): per-harness **packaging manifests** (OpenCode `opencode.json` + optional `.mjs` plugin + agent files; Codex `.codex-plugin/plugin.json`) — none have a generator today (even Claude's `plugin.json`/`marketplace.json` are hand-maintained, verified: zero codegen references).
- **The divergent parts are agent definitions and packaging, not skills.** Claude agent frontmatter (`tools`/`model`/`thinking`) does not map cleanly to OpenCode agent frontmatter (`mode`/`model`/`permission`), and Codex's per-agent-file concept is **unconfirmed** (conflicting evidence). Each target needs its own agent-emission strategy, or a no-op.
- **Biggest risk: Codex manifest/agent schemas are reverse-engineered, not authoritative.** Every Codex format here is from local third-party repos (ponytail, oh-my-codex, claude-mem); **all `developers.openai.com/codex/*` doc fetches returned 403/404 this investigation**, and the dossier contradicts itself on whether Codex has agent files. These MUST be empirically confirmed against a real Codex install before any Codex manifest is generated. Flagged throughout §5, §8, §9 and gated as a hard prerequisite in §7 Slice 3.
- **Recommended shape:** Slice 1 = `Target` interface + OpenCode/Codex SKILL.md output (reuse renderers, gate behind `-targets` flag, default Claude-only — content-fragment-guarded against regression). Slice 2 = OpenCode agent-file template (new frontmatter dialect, with the two mapping tables in §6.3/§6.4 pinned first). Slice 3 = packaging manifests, owned by `pasture-release`, **not** codegen, and **blocked** on empirical schema confirmation.

---

## 2. Pasture Codegen Today + the Extension Point

### 2.1 Pipeline (verified)

Single entry point: `go generate ./internal/codegen/...` → `go run ../../tools/codegen`. `tools/codegen/main.go` walks up to `go.mod` for the module root (`moduleRoot()`, `main.go:37-57`), accepts an `--output` override flag (`main.go:101`), then runs five stages (`main.go:120-175`):

1. `GenerateSchemaToFile(root/schema.xml)` — full overwrite.
2. `GenerateSkill` for each role in `roleSkillDirs` (**5 roles**) → `root/skills/<dir>/SKILL.md` — marker-bounded.
3. `GenerateSubSkill` for each command in `commandSkillDirs` (**24 commands**) → `root/skills/<dir>/SKILL.md` — marker-bounded.
4. `GenerateAgent` for each `RoleSpec` with non-empty `Tools` → `root/agents/<roleId>.md` — full overwrite.
5. `ValidateGlobalIds()` — ID-uniqueness gate.

Total generated SKILL.md = **29** (5 + 24), confirmed by `grep -rl "BEGIN GENERATED" pasture/skills/`.

### 2.2 Data model (source of truth)

All protocol facts are typed Go values, target-agnostic:
- `internal/codegen/specs_data.go` — `RoleSpecs`, `CommandSpecs`, `PhaseSpecs`, `ConstraintSpecs`, `HandoffSpecs`, etc.
- `internal/codegen/specs_data_body*.go` — `SkillBodySpecs` (prose bodies).
- `internal/codegen/specs_data_fragments.go` — `SharedFragmentSpecs` (define-once, reference-by-ID).
- `internal/codegen/context.go` — `roleConstraints`/`phaseConstraints` maps, `GetRoleContext()`.
- `internal/codegen/specs.go` — struct defs. **`RoleSpec` carries `Model string`, `Thinking string`, `Tools []string`** (`specs.go:243-245`) — the exact fields a per-harness agent mapper consumes.

### 2.3 The Claude-specific coupling points (verified)

- **Templates** (embedded via `internal/codegen/embed.go`, `//go:embed templates`):
  - `templates/skill.go.tmpl` — role frontmatter emits `name`/`description` + an optional `skills:` list (`skill.go.tmpl:1-7`).
  - `templates/skill_sub.go.tmpl` — sub-skill `name`/`description` only.
  - `templates/agent_definition.go.tmpl` — emits **Claude-only** `tools`/`model`/`thinking` (`agent_definition.go.tmpl:4-7`).
- **Marker constants** (`internal/codegen/markers.go:15,18`): `<!-- BEGIN/END GENERATED FROM pasture schema -->`. These are HTML comments — **harness-neutral**; markdown comments are preserved by OpenCode/Codex, so `markers.go` needs **no change**.
- **Output paths**: hardcoded `filepath.Join` literals in `main.go` (stages 1–4).
- **`pasture:` namespace**: built in `skills.go` `subSkillsForRole` for the role-frontmatter `skills:` list.

### 2.4 Generated-vs-hand-authored boundary

- **Generated (marker-bounded):** 29 skill dirs (5 roles + 24 commands). `ReplaceMarkerRegion(content, rendered, dropPrefix=true)` owns frontmatter + H1 + generated body; preserves hand-authored prose after END.
- **Fully generated (no markers, full overwrite):** `agents/*.md`, `schema.xml`.
- **Fully hand-authored (no generator):** `skills/protocol/` (12-phase reference docs) and `skills/install-cli/` (binary installer). The pipeline **leaves these in place** — it neither generates nor copies them (verified: `main.go` only generates marker/overwrite outputs; these dirs have no markers and are never touched).
- **Hand-maintained, NOT generated (verified: zero codegen references):** `.claude-plugin/plugin.json` and the parent `aura-protocol/.claude-plugin/marketplace.json`. **There is no codegen pathway for any harness's plugin manifest today.**

### 2.5 The clean seam

The **`Generate*` wrappers** (`GenerateSkill` `skills.go:591`, `GenerateSubSkill` `skills.go:686`, `GenerateAgent` `agents.go:163`) already accept an explicit output path + `figuresDir` — they are path-agnostic and reusable. The underlying **renderers** (`renderSkill` `skills.go:408`, `renderSubSkill` `skills.go:494`, `renderAgent` `agents.go:74`) take only `(id, figuresDir)` and do **not** accept an output path; output paths are the wrappers' job.

What is *not* parameterized anywhere is **template selection**: each renderer calls `mustParseTemplateFS` (`skills.go:129`) with a hardcoded template name. The minimal refactor is therefore:
- (a) loop `main.go`'s stage-2/3/4 path literals over a `[]Target`, and
- (b) thread a template-name (or `*template.Template`) argument through `renderSkill`/`renderSubSkill` (and `renderAgent` for the agent tier), defaulting to today's value so existing call sites are untouched.

`ValidateGlobalIds` stays unchanged (target-neutral). This is the *only* renderer-layer change for the skill tier.

---

## 3. Target Abstraction — Proposed Go Shape

The two cross-referenced precedents diverge in philosophy:

- **vercel-labs/skills** (`~/codebases/vercel-labs--skills/src/agents.ts`): *no content transformation.* One `SKILL.md` is the universal format; each harness is just **path routing** (`AgentConfig{skillsDir, globalSkillsDir, detectInstalled}`). OpenCode and Codex are "universal" agents reading `.agents/skills/`. Lesson: **harness ≠ template; harness = output path** for the skill tier.
- **ponytail** (`~/codebases/ponytail`): *thin per-harness adapters*, one dir each, each with a small manifest/dialect (Codex `.codex-plugin/plugin.json`; OpenCode `opencode.json` + `.opencode/command/*.md` + `.mjs`). A `check-rule-copies.js` enforces byte-identity of shared prose. Lesson: **packaging files are per-harness and small, but real.**

Synthesized into pasture's Go pipeline:

```go
// internal/codegen/targets.go (new)

// HarnessName is the strongly-typed target identifier.
type HarnessName string
const (
    HarnessClaude   HarnessName = "claude"
    HarnessOpenCode HarnessName = "opencode"
    HarnessCodex    HarnessName = "codex"
)

// Target carries everything that varies per harness. Skill tier = path routing
// (vercel-labs lesson); agent + packaging tiers diverge by dialect (ponytail lesson).
type Target struct {
    Name HarnessName

    // SkillPath returns the output path for a skill given its dir name.
    // All three currently resolve to skills/<dir>/SKILL.md if the shared-tree
    // route is chosen (see OPEN ITEM O-5).
    SkillPath func(root, dir string) string

    // SkillTemplate names the embedded template for the skill frontmatter
    // dialect. Given the live OpenCode schema ignores unknown keys (§9 V-1),
    // all three CAN share skill.go.tmpl; per-target variants only if a
    // harness is later found to reject extra keys.
    SkillTemplate    string
    SubSkillTemplate string

    // AgentStrategy decides how (or whether) per-role agent files are emitted.
    AgentStrategy AgentStrategy

    // CopyVerbatim lists hand-authored skill dirs (protocol, install-cli).
    // NOTE: only meaningful if non-Claude outputs live in a SEPARATE tree.
    // If the shared-tree route is chosen (recommended), this is unused — see
    // OPEN ITEM O-10. Do not add the field until O-5 is resolved in favor of
    // separate trees.
    CopyVerbatim []string
}

// AgentStrategy is the divergent axis.
//   Claude   -> agents/<role>.md (tools/model/thinking)
//   OpenCode -> .opencode/agent/<role>.md (mode/model/permission)
//   Codex    -> none, OR .codex/agents/<role>.toml (UNCONFIRMED — §5/§9 V-4)
type AgentStrategy interface {
    Emit(root string, roles map[protocol.RoleId]RoleSpec, figuresDir string, opts GenerateOptions) ([]string, error)
}
```

Registry + flag wiring in `tools/codegen/main.go`:

```go
var allTargets = map[codegen.HarnessName]codegen.Target{
    codegen.HarnessClaude:   codegen.ClaudeTarget,
    codegen.HarnessOpenCode: codegen.OpenCodeTarget,
    codegen.HarnessCodex:    codegen.CodexTarget,
}

// -targets=claude (default) preserves current behavior exactly.
targetsFlag := flag.String("targets", "claude", "comma-separated: claude,opencode,codex")
```

**Why a static registry, not config files:** exactly three targets, all known at compile time, all needing Go logic (agent mapping, path math). A static typed registry matches pasture's existing `internal/acp/adapter.go` precedent (compile-time `adapters` map). Do **not** build a plugin system for hypothetical future harnesses (over-engineering, against project standards).

**On `CopyVerbatim`:** flagged as conditional. The recommended shared-tree route (§4.1/O-5) makes it dead weight; only the separate-tree route needs it. Per project standards ("don't add abstractions for unresolved hypotheticals"), **do not add this field until O-5 is decided in favor of separate trees.** It is shown above only to document the contingency.

---

## 4. OpenCode Target

### 4.1 Files to emit

| Path | Generated? | Format |
|---|---|---|
| `skills/<name>/SKILL.md` | **Reuse existing** `GenerateSkill`/`GenerateSubSkill` | YAML frontmatter (`name`, `description`) + markdown. Already valid against the live loader. |
| `opencode.json` (repo root or `.opencode/opencode.json`) | New (Tier B) — **schema UNPINNED, see O-2** | At minimum a JSON config that adds the skills dir to discovery. Two candidate shapes seen in evidence; not reconciled. |
| `.opencode/agent/<role>.md` | New template (Slice 2) | Frontmatter: `description` (required), `mode` (primary\|subagent\|all), `model`, `permission{...}`, `temperature`, `color`. **No `tools` field.** |
| `.opencode/command/<cmd>.md` (optional) | New template | `---\ndescription: <CommandSpec.Description>\n---` + prompt body; `$ARGUMENTS` placeholder. Filename derived from command (strip `pasture:`, `:`→`-`). |
| `.opencode/plugins/pasture.mjs` (optional) | **Hand-authored, NOT generated** | ESM plugin. See O-7/O-9 — questionable value for pasture. |

**Authoritative discovery facts (verified from OpenCode source `skill/index.ts:21-26` + `discovery.ts`):** the loader scans, for external dirs `.claude` and `.agents`, the pattern `skills/**/SKILL.md`; for OpenCode dirs the pattern `{skill,skills}/**/SKILL.md`; plus a general `**/SKILL.md` scan. Skills are keyed by frontmatter `name` (`index.ts:98`), with a duplicate-name warning (`index.ts:89-94`) but no hard rejection. **Pointing OpenCode at pasture's existing `skills/` tree is therefore a documented discovery route — no skill relocation needed.** The exact `opencode.json` key used to register an arbitrary extra path (`skills.paths` vs another key) is **NOT pinned** from the read I performed — see OPEN ITEM O-2; for MVP the `.agents/skills/` or `.claude/skills/` symlink/copy route avoids the config entirely.

### 4.2 Generator design

- **Skills:** zero new code beyond `Target` path wiring. The live OpenCode loader accepts pasture's bare `name: worker` frontmatter and ignores extra keys (`Info.pick({name,description})` discards everything else, `index.ts:86`). The role-file `skills:` list and `pasture:` prefix are Claude conventions; for OpenCode output they may be **dropped** (cleaner) or **left in** (harmlessly ignored). See O-4.
- **Agents:** new `internal/codegen/opencode_agent.go` with an `openCodeAgentStrategy` + `templates/opencode_agent.go.tmpl`. Mapping inputs come from `RoleSpec.Model`/`Tools`. The two non-trivial mappings (`Tools`→`permission`, `Model`→provider-id) are specified concretely in §6.3 and §6.4 below — **these must be pinned before writing the template.**
  - `mode`: derived per role (see §6.2 table). Recommend hardcoding the role→mode map in the strategy rather than adding `RoleSpec.OpenCodeMode` (keeps `specs_data.go` harness-neutral). See O-6.
- **Body reuse:** OpenCode agent `prompt` supports `{file:./skills/<role>/SKILL.md}` interpolation — recommended over re-rendering the body, to avoid duplication. **NEEDS-VERIFICATION:** the exact interpolation token (`{file:...}`) is from dossier evidence, not re-read from source this session (O-2 family).

### 4.3 Distribution (how a user installs)

1. **vercel-labs `skills` CLI (lowest effort, recommended primary):** `npx skills add dayvidpham/pasture -a opencode`. Works **today** with zero new files because pasture's `skills/` tree + valid `name`/`description` frontmatter satisfy OpenCode discovery. Highest-ROI path.
2. **Commit `opencode.json` + `.opencode/agent/*.md`** so a cloning/symlinking user gets skills + agents auto-discovered (gated on O-2 schema pinning).
3. **npm plugin** (only if `.mjs` injection is wanted — see O-7/O-9): publish a package, user adds the name to `opencode.json` `plugin[]`.

---

## 5. Codex Target

⚠️ **Evidence quality flag (unchanged severity).** Every Codex format below is from **local third-party repos** (ponytail `.codex-plugin/plugin.json`; oh-my-codex `src/config/codex-hooks.ts`, `src/agents/native-config.ts`, `docs/codex-native-hooks.md`; claude-mem). **All official `developers.openai.com/codex/*` fetches failed (403/404) this investigation.** The dossier sub-agents **disagree** on whether Codex has agent files at all: one cites OpenAI docs ("skills + AGENTS.md, no per-agent file"); another cites oh-my-codex's native `~/.codex/agents/<name>.toml`. These may be **two different products** (Codex CLI skills model vs. the oh-my-codex third-party native-agent extension). **OPEN ITEM O-3 / §9 V-4 is a hard gate before Slice 3.**

### 5.1 Files to emit (per the reverse-engineered ponytail model — UNCONFIRMED)

| Path | Generated? | Format |
|---|---|---|
| `skills/<name>/SKILL.md` | **Reuse existing** | Same `name`/`description` frontmatter. Per evidence, Codex scans `.agents/skills/`, `$CODEX_HOME/skills/` (`~/.codex/skills/`), `/etc/codex/skills`. **NEEDS-VERIFICATION.** |
| `.codex-plugin/plugin.json` | New (Tier B) — **schema UNCONFIRMED** | JSON: `name`, `version`, `description`, `author{name}`, `keywords[]`, `skills:"./skills/"`, `interface{...}`. Mirrors `.claude-plugin/plugin.json` + an `interface` block. Reverse-engineered from ponytail only. |
| `AGENTS.md` | pasture already has one (hand-authored) | Plain markdown, no frontmatter. Per evidence, Codex loads it root-down as project guidance. |
| `.codex/hooks.json` (optional) | install-time, **gitignored** | `{"hooks":{"SessionStart":[...]}}`; requires `[features] codex_hooks = true` in `~/.codex/config.toml`. **NEEDS-VERIFICATION.** |
| `.codex/agents/<role>.toml` | **UNCONFIRMED** — only if the oh-my-codex native-agent model is the target | TOML: `name`, `description`, `model`, `model_reasoning_effort`, `developer_instructions = """<body>"""`. **Reserved names include `worker`, which collides with a pasture role** — unresolved, see O-3. |

### 5.2 Generator design

- **Skills:** identical reuse to OpenCode (subject to confirming Codex's discovery roots).
- **`.codex-plugin/plugin.json`:** simplest new generator — no template; `encoding/json` `MarshalIndent` of a struct sourced from a new `PluginMetadata`/`CodexInterfaceSpec`. **Version-drift hazard:** if `.codex-plugin/plugin.json` and `.claude-plugin/plugin.json` are separate files, `pasture-release` must bump both (§7 Slice 3).
- **Codex agents:** if confirmed, new `templates/codex_agent.go.tmpl` + a strategy; `developer_instructions` = rendered agent body. If "skills + AGENTS.md only" is correct, the Codex `AgentStrategy` is a **no-op** (`codexNoAgents{}`) and the 5 `agents/*.md` have **no Codex counterpart**. **Do not build either path until O-3 resolves.**

### 5.3 Distribution

1. **vercel-labs `skills` CLI:** `npx skills add dayvidpham/pasture -a codex` → `~/.codex/skills/` (per evidence). Likely works today with zero new files. **Primary route** — and the one that does **not** depend on the unconfirmed manifest schema.
2. **`.codex-plugin/plugin.json`** committed → Codex plugin-ecosystem discovery (mirrors ponytail) — **blocked on O-3**.
3. **install-cli extension:** `pasture install-cli --target codex` writes `.codex/hooks.json`, patches `~/.codex/config.toml`, copies skills — only if native hooks/agents are wanted; **blocked on O-3**.

---

## 6. Translation Mapping Tables

### 6.1 Skill files (`SKILL.md`)

| Claude field | OpenCode | Codex | Notes |
|---|---|---|---|
| `name` | `name` (any string; keyed on this; dup-name warns, no hard reject) | `name` | **Clean.** No regex/length enforcement in current OpenCode source (V-1). |
| `description` | `description` (any string) | `description` | **Clean.** No length bound in current source (V-1). |
| `skills:` (role list) | ignored (extra key dropped at parse) | presumed ignored | Claude-only role→sub-skill linkage. Drop or leave. |
| `pasture:` prefix | bare dir name | bare dir name | Strip prefix recommended (O-4). |
| marker block | preserved (HTML comment) | preserved | **No change.** |
| body markdown | verbatim | verbatim | **Clean — the portable bulk.** |

### 6.2 Agent definitions

| Claude (`agents/<role>.md`) | OpenCode (`.opencode/agent/<role>.md`) | Codex |
|---|---|---|
| `name` | filename = id | **No agent-file concept** (or `name` in TOML — UNCONFIRMED) |
| `description` | `description` (required) | `description` (TOML, if native agents) |
| `tools: [...]` | `permission{...}` — **mapping in §6.3** | `developer_instructions` prose / none |
| `model: opus` | `model: <provider/id>` — **mapping in §6.4** | `model: <id>` + `model_reasoning_effort` — UNCONFIRMED |
| `thinking: medium` | `temperature`/`steps` (no direct equiv) | `model_reasoning_effort` (rough analog) |
| (implicit) | `mode: primary\|subagent` — derived per role | — |
| body | `{file:./skills/<role>/SKILL.md}` (token NEEDS-VERIFICATION) | `developer_instructions` triple-quoted body / `AGENTS.md` |

**Role → OpenCode mode (proposed, hardcode in strategy):** `epoch`, `supervisor`, `architect` → `primary`; `worker`, `reviewer` → `subagent`. This derives from pasture's existing topology (epoch/supervisor/architect orchestrate; worker/reviewer are spawned). Confirm with the user (O-6).

### 6.3 Tools → OpenCode permission mapping (NEEDS DECISION — was hand-waved in draft)

Pasture's tool vocabulary (verified in `agents/*.md` and `agent_definition.go.tmpl`): **`Read, Glob, Grep, Bash, Skill, Edit, Write, SendMessage`** (8 names). OpenCode's permission model is `{read, edit, bash}` plus pattern maps — it is **not** a 1:1 surjection of tool names. There is **no canonical mapping in any source**; the draft left this as "needs synthesis." Proposed concrete rule (OPEN ITEM O-PERM — must be confirmed against OpenCode permission docs before coding):

| Pasture tool | OpenCode permission effect |
|---|---|
| `Read`, `Glob`, `Grep` | `permission.read: allow` (read-family) |
| `Edit`, `Write` | `permission.edit: allow` |
| `Bash` | `permission.bash: allow` (or `ask` for non-allowlisted patterns) |
| `Skill` | no OpenCode permission analog → **no entry** (skills gated separately via `Permission.evaluate("skill", …)`, seen at `index.ts:226`) |
| `SendMessage` | no analog (Claude-team-only) → **no entry / dropped** |
| **tool absent from a role's list** | corresponding permission set to `deny` (least-privilege default) |

**Open sub-decisions:** (a) is "absence ⇒ `deny`" or "absence ⇒ `ask`"? (b) does OpenCode accept per-key `allow|ask|deny` strings or a nested pattern map? Both must be checked against OpenCode's permission schema (not read this session). Marked **OPEN ITEM O-PERM**.

### 6.4 Model-id mapping (NEEDS PINNED VALUES — was a placeholder in draft)

`RoleSpec.Model` holds bare `opus`/`sonnet`. OpenCode requires a provider-qualified id; Codex requires its own. **No verified mapping values exist in pasture or the dossier** (the `anthropic/claude-opus-4-20250514`-style ids floated in the dossier are guessed/dated and must NOT be hardcoded blindly — a wrong id silently yields a non-functional agent). Recommended structure: a generator-local lookup table (keeps `specs_data.go` harness-neutral, O-6), populated with **source-checked** values:

| `RoleSpec.Model` | OpenCode `model` | Codex `model` |
|---|---|---|
| `opus` | `anthropic/<current-opus-id>` — **OPEN ITEM O-MODEL** | `<current-codex-default>` — UNCONFIRMED |
| `sonnet` | `anthropic/<current-sonnet-id>` — **OPEN ITEM O-MODEL** | `<…>` — UNCONFIRMED |

The exact ids must be pinned from an authoritative source at build time and given a single home (the lookup table) so they cannot rot silently. Until then, treat O-MODEL as blocking for Slice 2's agent emission (skills tier is unaffected). The Anthropic model-id reference (the `claude-api` skill) should be consulted to pin the OpenCode side before coding.

### 6.5 Commands / packaging

| Claude | OpenCode | Codex |
|---|---|---|
| sub-skill `SKILL.md` | `.opencode/command/<cmd>.md` (optional) OR same SKILL.md | same SKILL.md (no command file) |
| `.claude-plugin/plugin.json` (hand-made) | `opencode.json` (new, schema UNPINNED — O-2) | `.codex-plugin/plugin.json` (new, schema UNCONFIRMED — O-3) |
| `marketplace.json` entry (hand-made) | no confirmed first-party marketplace (npm / `ocx` / `skills` CLI) — O-2 | Codex plugin ecosystem via `.codex-plugin/` — O-3 |
| hooks: `hooks/hooks.json` | `.opencode/plugins/*.mjs` (JS hooks) | `.codex/hooks.json` (+ `config.toml` flag) — UNCONFIRMED |

---

## 7. Implementation Plan (vertical slices for a future epoch)

**Slice 1 — Target abstraction + multi-harness SKILL.md output (interface-first foundation).**
- *Files:* `internal/codegen/targets.go` (new — `HarnessName`, `Target`, `AgentStrategy` iface, `ClaudeTarget`/`OpenCodeTarget`/`CodexTarget` registry with **no-op** agent strategies for non-Claude); thread an optional template-name arg through `renderSkill`/`renderSubSkill` (`skills.go:408,494`); `tools/codegen/main.go` (loop stages 2–4 over selected targets; add `-targets` flag, default `claude`; also fix the stale "22 skills" comment at `main.go:75`).
- *Behavior contract:* default invocation produces output **identical to today** under the existing content-fragment fixtures. **NOTE:** today's `testdata/*.yaml` use `must_contain`/`must_contain_headers` (not byte-for-byte). If a true byte-identical guard is wanted, it must be **added** in this slice (O-8); otherwise state the guarantee as "content-fragment-stable."
- *Scope sizing:* **29 SKILL.md (5 roles + 24 commands)** — not 27/22.
- *Tests:* extend `testdata/skills.yaml` cases; add a target-routing test asserting Claude default unchanged and OpenCode/Codex skill paths/frontmatter.
- *Empirical gate (do first):* re-confirm V-1 (OpenCode ignores unknown keys) — already verified in source this session; spot-check Codex equivalently once O-3 lands. Determines whether skill templates stay shared (they should).

**Slice 2 — OpenCode agent dialect.**
- *Files:* `internal/codegen/opencode_agent.go` (`openCodeAgentStrategy` + the §6.3 `Tools`→`permission` rule + §6.4 `Model`→provider-id lookup + §6.2 role→mode map); `templates/opencode_agent.go.tmpl`; wire into `OpenCodeTarget.AgentStrategy`.
- *Hard prerequisites:* **O-PERM** (permission mapping confirmed) and **O-MODEL** (model ids pinned). Do not start the template until both are resolved.

**Slice 3 — Packaging manifests (owned by `pasture-release`, NOT codegen).**
- *Files:* `internal/release/*` — generate/version-sync `opencode.json` and `.codex-plugin/plugin.json`; extend `registry sync-versions` to bump all manifests together; new `CodexInterfaceSpec`/`PluginMetadata` source; update `marketplace.json` flow.
- *Hard prerequisite (BLOCKER):* **O-2** (OpenCode manifest schema pinned) and **O-3** (Codex manifest schema confirmed against a real install). Manifests are versioned distribution artifacts, not schema-derived skill content; folding them into `go generate` creates a version-drift trap.

**Slice 4 (optional, deferred) — Hand-authored runtime glue + install paths.**
- *Files:* `.opencode/plugins/pasture.mjs` (hand-authored ESM); `.opencode/command/*.md` template if commands wanted; extend `skills/install-cli/SKILL.md` to document `npx skills add … -a opencode|codex` and any `config.toml` patching; optionally extend `nix/hm-module.nix` to install into `~/.config/opencode/skills/` and `~/.codex/skills/`.
- *Caveat (O-9):* pasture has **no per-session mode/phase state**, so a system-prompt-injection `.mjs` has no obvious dynamic injection target. Recommend dropping `.mjs` from MVP — see O-7/O-9.

**Slicing principle:** Slices 1–2 are pure codegen (testable, reversible, no external-schema risk once O-PERM/O-MODEL are pinned). Slice 3 isolates the **schema-unconfirmed manifest risk** behind the release tool and is hard-gated. Slice 4 isolates **hand-authored JS** that cannot be generated.

---

## 8. Open Questions / Decisions for the User

- **(O-1, RESOLVED this session) Unknown-frontmatter-key tolerance — OpenCode IGNORES extra keys.** Confirmed in source: `Info.pick({name,description}).safeParse(md.data)` (`index.ts:86`) discards all other frontmatter. **One shared SKILL.md serves OpenCode with zero frontmatter divergence.** Codex side still needs the equivalent spot-check (folded into O-3).
- **(O-2, FLAGGED — schema unpinned) OpenCode `opencode.json` / packaging shape.** The discovery *roots* are verified from source (§4.1), but the exact `opencode.json` key to register an extra skills path, the agent-block shape (inline JSON vs separate `.opencode/agent/*.md`), and whether any first-party marketplace exists were **not** pinned from source this session. *Default recommendation: ship via `npx skills add` + the `.agents/skills` or `.claude/skills` discovery root, and defer `opencode.json` generation until the schema is read from OpenCode's config source/docs.*
- **(O-3, BLOCKER — conflicting/403-blocked evidence) Codex agent + plugin model.** Native agent files (oh-my-codex `~/.codex/agents/*.toml`) vs strictly skills + `AGENTS.md`? All official Codex fetches failed; manifest/TOML/hook formats are reverse-engineered. **Confirm against a real Codex install before Slice 3.** Also resolve the `worker` reserved-name collision (rename pasture's Codex agent, or rely on no-agent-files model).
- **(O-4) Drop or keep `pasture:` namespace for OpenCode/Codex?** Both invoke by bare dir name. Dropping is cleaner; keeping is harmless (ignored). If dropped, `subSkillsForRole`/role-file `skills:` need a target-aware branch.
- **(O-5) Output location + commit/gitignore policy.** Shared `skills/` tree (recommended — discovered via roots/manifest paths) vs per-harness top-level dirs (`.opencode/`, `.codex/`). Drives `Target.SkillPath` and whether `CopyVerbatim` exists at all. *Recommendation: shared `skills/` tree; commit `opencode.json`/`.codex-plugin/plugin.json` once their schemas are pinned; gitignore install-time `.codex/hooks.json`.*
- **(O-6) `RoleSpec` extension vs generator-local lookup** for mode/model/reasoning. *Recommendation: generator-local lookup tables (keep `specs_data.go` harness-neutral).*
- **(O-7 / O-9) `.mjs` system-prompt injection in scope?** It is the only non-generatable artifact, adds a JS runtime + dep surface, **and** pasture has no per-session mode/phase state to inject. *Recommendation: defer/drop `.mjs`. User must accept that without it, OpenCode/Codex integration provides skills + agents discovery only — NO automatic phase/role context injection.*
- **(O-8) Build a true byte-identical regression guard?** Today's fixtures are content-fragment only. Decide whether Slice 1 adds golden-file byte comparison or keeps the looser guarantee.
- **(O-PERM) Pin the `Tools`→OpenCode-permission mapping** (§6.3), including the absence-default (`deny` vs `ask`) and the schema shape. Blocks Slice 2.
- **(O-MODEL) Pin the `opus`/`sonnet`→provider-id values** (§6.4) from an authoritative source (use the `claude-api` reference for the Anthropic side). Blocks Slice 2 agent emission.

---

## 9. Confidence & Verification Needed

**Verified in source this session (high confidence):**
- **V-1.** OpenCode skill schema is `{name, description, location, content}` with **no regex / no length bounds**; `SkillNameMismatchError` is **declared but never thrown**; skills keyed by frontmatter `name` with a soft duplicate warning. (`~/codebases/opencode/packages/opencode/src/skill/index.ts:30-50,86-99,226`)
- **V-2.** OpenCode discovery roots/patterns: `.claude`/`.agents` → `skills/**/SKILL.md`; OpenCode dirs → `{skill,skills}/**/SKILL.md`. (`index.ts:21-26`)
- **V-3.** Pasture generates **29** SKILL.md (5 roles + **24** commands); the "22" figure is stale. (`tools/codegen/main.go:72-98`; `grep -rl "BEGIN GENERATED" pasture/skills/` = 29)
- **V-5.** Renderer signatures take `(id, figuresDir)` only; output paths live on `Generate*` wrappers. (`skills.go:408,494,591,686`; `agents.go:74,163`)
- **V-6.** `RoleSpec` carries `Model`/`Thinking`/`Tools`. (`specs.go:243-245`)
- **V-7.** Pasture tool vocabulary = `Read, Glob, Grep, Bash, Skill, Edit, Write, SendMessage`.
- **V-8.** No codegen pathway exists for any plugin manifest today (Claude `plugin.json`/`marketplace.json` hand-maintained).
- **V-9.** Existing fixtures assert content fragments (`must_contain`), not byte-identical output. (`testdata/skills.yaml` header + body)

**Must be confirmed before building (blocking):**
- **V-4 (BLOCKER, Slice 3).** Authoritative Codex schemas — does Codex have native agent files; exact `.codex-plugin/plugin.json`, `.codex/agents/*.toml`, `.codex/hooks.json`, and skill-discovery roots — confirmed against a **real Codex install**, not ponytail/oh-my-codex reverse-engineering. All `developers.openai.com/codex/*` fetches returned 403/404.
- **O-2 (Slice 3).** Real `opencode.json` schema (the key that registers extra skills paths; agent-block shape) and whether any first-party OpenCode marketplace/packaging format exists.
- **O-PERM (Slice 2).** Concrete, total `Tools`→`{read,edit,bash}` permission mapping incl. absence-default and schema shape, checked against OpenCode's permission docs.
- **O-MODEL (Slice 2).** Exact current provider-qualified model ids for `opus`/`sonnet` (OpenCode) and the Codex model values, pinned in one lookup table.
- **Body-interpolation token.** Confirm OpenCode's `{file:...}` prompt-interpolation syntax from source/docs before relying on it (§4.2).

**Risk-mitigated by default recommendation:** shipping via `npx skills add` + existing `skills/` discovery roots makes **Slice 1 + distribution viable with zero dependence on the unconfirmed manifest schemas** (O-2/V-4). Manifests, agent dialects, and `.mjs` are all isolated into later, individually-gated slices.

---

**Files cited (verified this session unless marked):** `pasture/tools/codegen/main.go:37-185` (incl. `commandSkillDirs` 72-98, stale "22" comment 75), `pasture/internal/codegen/skills.go:129,408,494,591,686`, `pasture/internal/codegen/agents.go:74,163`, `pasture/internal/codegen/specs.go:243-245`, `pasture/internal/codegen/markers.go:15,18`, `pasture/internal/codegen/embed.go`, `pasture/internal/codegen/templates/skill.go.tmpl:1-7`, `pasture/internal/codegen/templates/agent_definition.go.tmpl:1-9`, `pasture/internal/codegen/testdata/skills.yaml`, `~/codebases/opencode/packages/opencode/src/skill/index.ts:21-26,30-50,86-99,226`. Dossier precedents NOT re-verified this session: `~/codebases/vercel-labs--skills/src/agents.ts`, `~/codebases/ponytail/.codex-plugin/plugin.json`, `~/codebases/Yeachan-Heo--oh-my-codex/src/agents/native-config.ts` (Codex evidence — see V-4 blocker).