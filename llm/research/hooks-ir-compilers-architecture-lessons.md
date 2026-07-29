# Harness Lifecycle Hooks: Architecture Lessons from Compilers

Internal design research. Captures why the current generated-adapter implementation
drifted, and which compiler-engineering patterns solve the same problem
maintainably.

Related records: request `aura-plugins-s43qq`, UAT `aura-plugins-sj1sc`,
drifted leaf `aura-plugins-jqvw7`, reusable contracts `aura-plugins-5tab4`.

---

## 1. The problem

Pasture must integrate with several agent harnesses (Claude Code, Codex,
OpenCode, potentially more). Each emits its own lifecycle events with its own
names, payload shapes, blocking rules, mutation rules, ordering, and failure
semantics.

Pasture must turn those occurrences into typed protocol operations and durable
Provenance effects, without letting host quirks leak into workflow semantics and
without reimplementing the same rules once per harness.

This is structurally identical to a multi-source-language, multi-target compiler.

---

## 2. The hourglass (narrow waist)

Without a canonical middle, integration cost is `N x M` — every harness times
every operation family. With one, it is `N + M`.

```text
Claude events ──┐                            ┌── protocol operations
Codex events  ──┼──> canonical lifecycle IR ─┼── Provenance effects
OpenCode      ──┘         (narrow waist)     └── future backends
```

Used by LLVM, GCC, and Roslyn.

**The waist must be semantic and narrow, never a union of frontend features.**
The historical counterexample is UNCOL (1958): a "universal" IR attempting to be
a superset of all source languages. It fails because every new frontend widens
the core, and every backend must then handle concepts it has no meaning for.

This is the same reason the "harness-superset IR" option was rejected during URE.

---

## 3. Terminology: frontend vs adapter vs trampoline vs driver

These were conflated in the current implementation, which is the root of the
drift.

| Term | Meaning | Where it runs | Contains semantics? |
|---|---|---|---|
| **Frontend** | Source language → IR | Inside the compiler | Yes — parsing and lowering to IR |
| **Trampoline / thunk** | Minimal dispatch shim | Boundary | No |
| **Driver** | Orchestrates pipeline stages | Inside the compiler | Policy only, not language semantics |
| **Adapter / binding / stub** | Cross-boundary glue, often generated | Foreign side of a boundary | No — marshalling only |
| **Target description** | Declarative table describing a target | Build time | Data, not logic |

A frontend is *part of the compiler*: it is written in the compiler's language,
linked into it, and produces IR. `gcc` parsing C is a frontend. The fact that it
parses text does not make "arbitrary text" the compiler's API.

An adapter lives on the *other* side of a process, language, or ABI boundary. It
exists only because the host cannot call the compiler's functions directly.

The current `pasture-lifecycle.py` is all four at once: trampoline, frontend,
driver, and part of the backend — emitted as generated source in a foreign
language. That fusion is the defect, not the existence of generated code.

---

## 4. Is generated code normal in compilers?

Generated code is pervasive. Generated *semantics* is not.

Legitimate, commonplace generation:

- **Parser generators** — yacc/bison, ANTLR, LALRPOP. The frontend's parser is
  generated from a grammar. Generated frontends are completely standard.
- **Target descriptions** — LLVM TableGen `.td` files generate register info,
  instruction matchers, and emitters.
- **Instruction selection tables** — BURG/iburg tree tiling generates matchers
  from pattern descriptions.
- **Binding/stub generators** — SWIG, bindgen, cgo, protoc/gRPC stubs,
  wasm-bindgen. These generate shims *in another language* to cross a boundary.

The invariant across all of them: **generated code is mechanical marshalling or
dispatch. Business rules live in hand-written, single-implementation code.**

A gRPC stub serializes and calls. It never decides whether a request is
authorized.

### The closest analogue: Language Server Protocol

LSP is the best match for Pasture's situation:

```text
VS Code extension  ─┐
Neovim client      ─┼── LSP (JSON-RPC) ──> language server (real compiler)
Emacs client       ─┘
```

- The per-editor extension is thin, sometimes generated, and often trivial.
- All semantics live in the server.
- Editors that put logic in the extension end up reimplementing it N times and
  drifting.

That is precisely the failure mode of duplicating event/operation authorization
into Python, TypeScript, and shell.

### The hourglass API pattern

Stefanus Du Toit's "hourglass interface": a narrow stable waist (often a C ABI)
with generated language bindings above and below. Same rule — the bindings are
dumb; the waist carries meaning.

---

## 5. So are adapters necessary here?

Yes, but only in their minimal forms, and only because of real boundaries.

| Piece | Necessary? | Why | Should contain logic? |
|---|---|---|---|
| Native registration (`hooks.json`, `opencode.json`) | Yes | Each host has its own config format | No — pure declaration |
| Shell trampoline (Claude, Codex) | Yes | Hosts execute a command, not a Go function | No — `exec` the binary |
| OpenCode TS plugin | Yes | OpenCode's extension surface is in-process JS | Minimal — forward and return |
| Native payload parsing | Yes | Something must read the host's wire format | **Yes — but in Go, as a frontend** |
| Operation selection | Yes | — | **Yes — but in the middle-end, once** |

The last two rows are what must move. They are frontend and middle-end
responsibilities that were pushed across the process boundary into generated
foreign-language code.

Target shape:

```text
Claude:   generated hooks.json + ~3-line shell trampoline
Codex:    generated hooks.json + ~3-line shell trampoline
OpenCode: generated opencode.json + thin TS plugin (in-process API, unavoidable)
```

All three remain generated, because they are mechanical projections of the pinned
contract table. None contains semantics. Python disappears as an accidental
dependency.

---

## 6. Frontends emit IR, never target operations

The current failure has a precise name: **syntax-directed code generation**,
where the parser emits target code directly. Abandoned decades ago because every
frontend then duplicates target knowledge and no analysis pass has anywhere to
live.

```text
Broken:   native event ──> (adapter selects operation) ──> backend op

Correct:  native event ──> frontend ──> IR ──> semantic pass ──> lowering ──> op
```

The current design is worse than syntax-directed: the operation is selected by an
*environment variable supplied by the caller*. That is equivalent to requiring the
user to tell the parser which machine instruction to emit.

---

## 7. Progressive lowering (multi-level IR)

MLIR is the state of the art, and this problem is genuinely multi-level:

```text
Level 1: harness dialect     PreToolUse, tool.execute.before
Level 2: lifecycle dialect   gate-consultation, blocking, mutation=input
Level 3: protocol dialect    StartReview, RecordPlanUAT, Land
Level 4: effects             journal operations, tasks, assignments
```

Each lowering step is small, separately testable, and independently reviewable.
GCC does a coarser version (`GENERIC` → `GIMPLE` → `RTL`). The nanopass framework
(Sarkar/Waddell/Dybvig) is the extreme form: many tiny passes, each with an
explicitly declared IR variant.

Payoff: "which native event may cause a user decision" becomes **one pass over
Level 2**, tested once — instead of a rule duplicated per harness.

Note: `internal/runtime.LifecycleEventMapping` already encodes the Level-1 →
Level-2 axes (semantic, blocking, mutation, order, reconciliation, failure,
stop-loop, native identity). It is a genuine target-description table and should
be retained. What is missing is materializing and transporting the IR value.

Do not conflate this with `internal/codegen/ir.SemanticOperation`, which is the
authoring/orchestration IR for generated documents. Different level, different
purpose.

---

## 8. Verifier and legalization

LLVM has `Verifier`; MLIR has per-op verifiers plus a `ConversionTarget`
declaring which operations are legal for a target, with partial/full conversion
and explicit failure.

```text
lower(IR, target) ->
    ok(target operations)
  | error("OpenCode 1.17.18 cannot express follow-up; reconstruct the assignment")
```

Legalization failure must be a first-class, actionable result. The current
adapters silently no-op when unconfigured, which is exactly the outcome this
pattern exists to prevent.

`AntigravityLifecycleContract()` already models this correctly at contract level.

---

## 9. Serialization is a boundary artifact, not the semantic model

LLVM has in-memory IR, textual `.ll`, and bitcode:

- the **in-memory typed structure** is the API;
- the **text form** exists mainly for testing and debugging;
- the **binary form** is a versioned compatibility boundary.

Nobody treats `.ll` parsing as how frontends talk to the middle-end.

For Pasture: JSON is acceptable as (a) an internal process-boundary encoding if a
daemon split is ever introduced, and (b) a strict versioned escape-hatch decoder.
It is not the semantic model.

WebAssembly is the deliberate counterexample: it made the serialized form the
stable public contract. That is an expensive versioning commitment, justified only
when third parties must produce the format directly.

---

## 10. Escape hatches are modeled explicitly

Every serious compiler has one, named and contained:

- LLVM: inline `asm`, target intrinsics
- Rust: `unsafe`
- MLIR: `unrealized_conversion_cast`
- Pasture: raw JSON ingestion

Rules: the escape hatch produces the same IR, passes the same verifier, and is
visibly marked so review can find it. It is never the default path.

---

## 11. Testing discipline

This is where the architecture earns its cost back:

- **Round-trip** — `print(parse(x)) == x` for the IR form.
- **Golden IR per frontend** — each harness event lowers to expected IR. Fast,
  no host or daemon required.
- **Differential equivalence** — Claude `PreToolUse` and OpenCode
  `tool.execute.before` must lower to the *same* Level-2 IR. This test is
  impossible in the current design, which is itself a strong smell.
- **Per-pass tests** — LLVM's lit/FileCheck runs one pass on one IR file. An
  end-to-end test is valuable but is not a substitute.
- **Verifier invariants** at every pass boundary.

---

## 12. Current vs target, mapped

```text
Current:
  native event
    -> generated Python/TS translator (parses JSON, validates, correlates)
    -> PASTURE_ADAPTER_OPERATION selected externally by the caller
    -> hidden JSON envelope
    -> pasture __adapter invoke
    -> operation DTO
    -> EpochService

Target:
  native event
    -> generated registration + thin trampoline
    -> typed Pasture entry point
    -> per-harness frontend (Go)      : native payload -> Level 1
    -> lowering pass (Go)             : Level 1 -> Level 2 lifecycle IR
    -> legalization/authorization pass : Level 2 -> Level 3 protocol operation
    -> engine + Provenance             : Level 3 -> Level 4 effects
```

Retained assets: pinned lifecycle contracts, `EpochService`, Provenance, the
operation DTO set (as the backend instruction set), and the native registration
plumbing.

Replaced: generated Python translators, `PASTURE_ADAPTER_*` environment binding,
and caller-selected operations as the primary path.

---

## 13. Open design decisions

1. ~~Does "no pure JSON ingestion" forbid a Go frontend from parsing native host
   payloads?~~ **RESOLVED (user):** No. A per-harness Go frontend parsing that
   harness's native payload is legitimate frontend work, exactly as `gcc` parsing
   C does not make arbitrary text its API. What is forbidden is raw JSON being
   Pasture's *semantic API*. Consequence: trampolines stay trivial and forward
   the native payload unmodified; all parsing, validation, and correlation live
   in Go frontends.
2. **Where does epoch/assignment/actor context come from** for events that need
   it? Observations may not need any; gates and human decisions do.
3. **What is the honest first semantic** for a native observation — a recorded
   Provenance activity/event, rather than an epoch lifecycle transition.
4. **Does the daemon split happen now or later?** If the CLI hosts the engine
   in-process initially, IR serialization can be deferred until measured.

---

## 14. Config fencing and generated-file ownership — DROPPED

**Status: DROPPED by user decision.** Retained only as analysis of the ownership
surface, not as planned work. Do not implement from this section.

Original requirement (superseded): Pasture must never clobber a user's existing
harness config.
Generated regions must be fenced like `skillgen`, and the fence must carry the
producing Pasture **version and commit hash**.

### 14.1 Ownership is not uniform

| File | Owner | Overwrite risk |
|---|---|---|
| `hooks/hooks.json` (Claude plugin bundle) | Pasture plugin | None — whole file is ours |
| `hooks/scripts/*`, `.opencode/plugins/*.ts` | Pasture | None — whole file is ours |
| `opencode.json` | **User** | **High** — currently written wholesale |
| `.codex/codex.toml` | **User** | **High** |
| `.codex/hooks.json` | **User** | **High** |
| `~/.claude/settings.json` (if ever targeted) | **User** | High |

Whole-file ownership is fine and needs only a `DO NOT EDIT` banner plus
provenance. Shared user documents need real fencing.

### 14.2 Comment fencing does not transfer to JSON

`skillgen` fences Markdown with `<!-- BEGIN GENERATED FROM pasture schema -->`.
That mechanism depends on comments.

| Format | Comments? | Fence mechanism |
|---|---|---|
| Markdown, Python, shell, TypeScript | Yes | Existing textual markers |
| TOML (`codex.toml`) | Yes | Textual markers work |
| JSON (`hooks.json`, `opencode.json`) | **No** | Requires **structural** fencing |

So JSON targets need ownership expressed *in the document model*, not in text.

### 14.3 Inline marker keys are rejected

**User determination:** Codex and OpenCode will not tolerate an unknown
`x-pasture` key in their config schemas. Inline structural tagging is therefore
unavailable, and no provenance may be written into host-owned documents.

Provenance must live in Pasture-owned files only.

### 14.4 Corrected design: minimal footprint + value-signature ownership

Inspecting the actual generated output shows the shared-config footprint is far
smaller than a full document, and is already self-identifying.

`opencode.json` — the only genuine host-owned config Pasture writes wholesale:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": ["./.opencode/plugins/pasture-lifecycle.ts"],
  "skills": { "paths": [".opencode/skill"] }
}
```

The entire footprint is **two array appends**, and both values are
Pasture-owned paths. That yields ownership identification without any marker key:

> An entry is ours if its value is a path we own.

This is host-legal by construction, because the value *is* the functional
content. Merge becomes append-if-absent; uninstall becomes remove-exact-value.

Files already correctly Pasture-owned (keep as-is, whole-file ownership plus
`DO NOT EDIT` banner and provenance):

- `.codex/codex.toml` — a Pasture manifest (`schema = "pasture.codex.manifest.v1"`),
  not Codex's user config
- `.opencode/pasture-opencode.json` — Pasture target manifest; the right place
  for version/commit provenance
- `hooks/hooks.json`, `hooks/scripts/*`, `.opencode/plugins/*.ts`

### 14.5 Preference order

The established Unix precedent for this problem is the **drop-in directory**:
`sudoers.d`, systemd drop-ins, `nginx conf.d`, `apt sources.list.d`. Each package
owns one whole file; nobody merges into a shared document.

| Rank | Approach | When |
|---|---|---|
| 1 | Host auto-discovers a directory → own one whole file | Preferred; zero merge |
| 2 | Minimal array-append into shared config, value-signature ownership, provenance in sidecar | Only if the host requires registration |
| 3 | Wholesale overwrite | Never |

**Verification required before implementing rank 2:**

- Does OpenCode auto-discover `.opencode/plugins/` and `.opencode/skill/`? If so,
  `opencode.json` need not be touched at all, eliminating the risk entirely.
- Is `.codex/hooks.json` host-owned shared config, or a Pasture plugin artifact?

**Safety rules (unchanged):**

- Target file exists but contains none of our known values → treat as unowned;
  append only, never rewrite unrelated content.
- Recorded value missing or altered → report drift; do not silently reapply.
- Uninstall removes only recorded values, never whole files we did not create.

### 14.6 Sidecar inventory is authoritative

`internal/install/inventory` already models `(type, digest, mode)` ownership and
created directories. It becomes the single source of truth for what Pasture wrote
into shared files, and carries the version/commit provenance that cannot live in
the host document.

### 14.5 Build stamping is currently missing

There is no version/commit stamping in the binary today: no `ldflags`, no
`debug.ReadBuildInfo` use, no build-info package. The commit-hash requirement
therefore needs new work.

- `debug.ReadBuildInfo()` exposes `vcs.revision` for ordinary `go build`.
- Nix builds commonly set `-trimpath` and disable VCS stamping, so the flake and
  `Makefile` must pass version/commit explicitly via `-ldflags`.
- The stamp must be reproducible, or generated-output drift checks will fail on
  every rebuild.

---

## 15. References

- LLVM: IR design, `Verifier`, TableGen, bitcode compatibility policy
- MLIR: dialects, progressive lowering, `ConversionTarget`, `Location` propagation
- GCC: `GENERIC` / `GIMPLE` / `RTL` pipeline
- Nanopass framework (Sarkar, Waddell, Dybvig)
- BURG / iburg instruction selection
- UNCOL (1958) — the universal-IR cautionary tale
- Language Server Protocol — thin clients, semantics in the server
- Stefanus Du Toit, "Hourglass Interfaces for C++ APIs"
- protoc / gRPC generated stubs — generated marshalling, never business logic
