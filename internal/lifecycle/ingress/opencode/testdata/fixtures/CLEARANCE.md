# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

## Harness and pinned version

OpenCode 1.18.29, verified with `opencode --version` immediately before the
session on 2026-09-05: `1.18.29`. Admission is a floor: this version and every
later release; the contract records 1.18.29.

## Capture

Captured in one live session on 2026-09-05, into
`~/.local/share/pasture-captures/opencode`, with `PASTURE_CAPTURE_DIR`
set. Paths in this record are spelled with `~` for the capturing user's home
directory; the fixture bytes themselves carry the `/home/user` placeholder that
`home-path-v1` writes. The sessions were interactive terminal sessions of the real host binary on the
user's machine, run at the user's direction by the team's operators; the user
did not type the prompts. Nothing captured reached any remote before the
user's acceptance recorded below.

Build kit: one `pasture` binary, sha256
`0b9a6fbde02493ba7325e772e78703adf2dc11533c882854ac0f7defa9ce5441`, built with
`CGO_ENABLED=0` from an archive copy of head 47d1c94 with the two version
roots moved to the versions above (artifact/aggregate_types.go, the three
`Contract.Version` literals under internal/lifecycle/ingress/internal/hostcontract)
and `make generate` run, so every transport carried the new version.
Environment in the host's shell: `PASTURE_BIN` = that binary,
`PASTURE_CAPTURE_DIR` = the directory below (absolute, outside the repository,
pre-existing), `PASTURE_DB_PATH` = `~/.local/share/pasture-captures/scratch/pasture.db`
(a scratch database; the live store was not touched). On this scratch
database every hook printed one fail-open fault line ("cannot resolve
Pasture's persisted system identity") after the capture was written; the host
proceeded and the capture is unaffected.

OpenCode transport from the kit, in the project directory
`~/.local/share/pasture-captures/project-opencode/.opencode`:
`plugins/pasture-lifecycle.ts` sha256
`bfd1f25bfcef8d8f3f5b6b784816a15f1a238cc42ac3d7edd2f00a7d3ea0a879` (its
default export is the `{ id, server }` object the host's plugin loader reads
first). On first start the host installed its own plugin package into that
`.opencode` directory (package.json, package-lock.json, .gitignore,
node_modules); the plugin file was unchanged. One line per event:
- session.created: `opencode_session_created_1_18_29.1.json` — trigger: the first prompt, "Say hello in one word." (fired 1 s after Enter) — 2026-09-05T12:31:50Z — raw sha256:1c77be2e9dab5cee9b410d117d5bced8c287285960efac671dcdf6e841468bac (660 bytes) — committed sha256:71c8de3aadd8019b7e4123076625a0be6e3faaadd56a23c2a79c28a58f7ab591 (654 bytes)
- tool.execute.before: `opencode_tool_execute_before_1_18_29.1.json` — trigger: the second prompt, "Run ls -la in the shell and describe the output." (fired 3 s after Enter; no permission prompt) — 2026-09-05T12:32:28Z — raw sha256:a454a9af11e0d38cf9ce2bf9279398364743c4c87ca5054f1e10b675942cc98c (150 bytes) — committed sha256:4ac8bef2356d19aa2972e61d1f6e50fe1bf3a3ebf187382f6591a5630d548053 (150 bytes)

Record addendum, written on 2026-09-05 after the acceptance below, and
correcting nothing that was accepted: the plugin the kit carried and the plugin
this repository ships are NOT byte-identical. The committed
`.opencode/plugins/pasture-lifecycle.ts` is sha256
`900e45e7d91bd390ea2474eebb8b82c9de755f7eb393e88bfc01791791b5ebb6`. It differs
from the kit's `bfd1f25bfcef8d8f3f5b6b784816a15f1a238cc42ac3d7edd2f00a7d3ea0a879`
by exactly one line: the METADATA event table spells
`installation.update-available` where the kit spelled
`installation.update_available`. That correction landed after the kit was built,
because the host's own schema spells the type with a hyphen. Neither captured
event is that row: the captures are session.created and tool.execute.before, so
the one differing line took no part in them and the captures stand. A reader who
hashes the shipped plugin gets the first digest above.

Both files carry one sessionID. No other file was produced; none was
discarded. Observed and recorded elsewhere, not a clearance matter: the hook's
standard error was drawn inside the host's terminal screen; the capture was
unaffected.

Record addendum, written on 2026-10-01, superseding the transport identity
above but correcting no accepted capture: the OpenCode transport has been
regenerated for the 2.0.20 plugin API. The committed
`.opencode/plugins/pasture-lifecycle.ts` is now sha256
`2a8c05e333a59661813c5e66789187f480ecd71b1d6a7b83d2150e629ede28f6`
(its default export is the `Plugin.define({ id, setup })` object the 2.0.20
host's plugin loader reads, registering location-scoped hooks through the
setup context). The two captures above ran through the retired 1.18.29 plugin
shape and remain valid for the 1.18.29 contract rows only; they are not
2.0.20 captures, and no 2.0.20 capture has been cleared into this directory.
A reader who hashes the shipped plugin gets the digest above.

## Inventory

```
opencode_session_created_1_18_29.1.json
  .event.id                                                    identifier 
  .event.type                                                  identifier 
  .event.properties.sessionID                                  identifier 
  .event.properties.info.id                                    identifier 
  .event.properties.info.slug                                  identifier 
  .event.properties.info.version                               identifier 
  .event.properties.info.projectID                             identifier 
  .event.properties.info.directory                             path       
  .event.properties.info.path                                  identifier 
  .event.properties.info.title                                 free-text    FREE TEXT: substitute with free-text-v1
  .event.properties.info.agent                                 identifier 
  .event.properties.info.model.id                              identifier 
  .event.properties.info.model.providerID                      identifier 
  .event.properties.info.model.variant                         identifier 
  .event.properties.info.cost                                  number     
  .event.properties.info.tokens.input                          number     
  .event.properties.info.tokens.output                         number     
  .event.properties.info.tokens.reasoning                      number     
  .event.properties.info.tokens.cache.read                     number     
  .event.properties.info.tokens.cache.write                    number     
  .event.properties.info.time.created                          number     
  .event.properties.info.time.updated                          number     
opencode_tool_execute_before_1_18_29.1.json
  .input.tool                                                  identifier 
  .input.sessionID                                             identifier 
  .input.callID                                                identifier 
  .output.args.command                                         free-text    FREE TEXT: substitute with free-text-v1
2 payloads inventoried in ~/.local/share/pasture-captures/opencode
```

## Rules applied, in order

Per fixture, the value-only rules applied in the order applied, as listed in
the provenance sidecar: `home-path-v1`, then `free-text-v1` where the
inventory flagged free text. Structure, keys, types and nulls are unchanged.

`home-path-v1` rewrites every spelling of the capturing user's home directory
to the `user` placeholder: the absolute path `/home/<user>`, the relative
spelling `home/<user>/`, and the directory slug a host derives from a path
(`-home-<user>-`), which is how the earlier committed corpus carries it. It
applies wherever any spelling occurs, including inside free text.
`free-text-v1` replaces each free-text string the inventory flagged by `x`
placeholder text of the same raw length. Keys, nesting, types and nulls are
unchanged; after both rules the committed bytes contain no occurrence of the
user name (asserted by the clearing run). Rules applied per fixture, in order:

- session_created_1_18_29.json: home-path-v1 (the `directory` and the relative `path` spellings), free-text-v1 (.event.properties.info.title)
- tool_execute_before_1_18_29.json: free-text-v1 (.output.args.command); no home path occurs in it

## Secret scan

`TestNoCommittedTestdataCarriesASecretShape` (internal/lifecycle/ingress/secretscan_test.go)
run over the whole module with these fixtures and their sidecars in place:
PASS, zero hits, 2026-09-05. Reach control on the same run: an Anthropic
API-key shape planted into a copy of one new fixture in this directory turned
the scan RED naming that file and the byte offset; the copy was discarded.
`TestSecretScanIsRedOnEachPlantedShape` (all nine shapes): PASS.

## Refused classes

No fixture carries a tool response (tool.execute.before precedes the tool),
so none is above 4096 bytes. No payload is an environment dump. Both free-text
fields were substituted. The model id, provider id, session slug and project
id are identifiers and are kept. No payload was unclearable.

## Fixtures

- `session_created_1_18_29.json` — session.created — sha256:71c8de3aadd8019b7e4123076625a0be6e3faaadd56a23c2a79c28a58f7ab591 (654 bytes)
- `tool_execute_before_1_18_29.json` — tool.execute.before — sha256:4ac8bef2356d19aa2972e61d1f6e50fe1bf3a3ebf187382f6591a5630d548053 (150 bytes)

## User acceptance

Accepted by the user on 2026-09-05, for the whole batch (Claude 8 fixtures and
3 controls, Codex 2, OpenCode 2), after the clearance evidence above was
presented. The user was asked three questions:

1. "Your acceptance wording, verbatim, for the batch (or per harness if you
   want to hold one back)."
2. "Keep that as home-path-v1 as documented (recommended), or mint a
   home-path-v2 name for it."
3. "Tilde spelling for the directory paths in the drafts: yes (recommended) or
   keep the full paths."

The user answered, verbatim:

```
1. seems fine
2. keep home-path-v1
3. sure, use tilde
```

Nothing in this directory reaches a remote before this section is filled. This
file is the clearance authority a fixture's provenance names by path: a fixture
may name this file only after this section holds the acceptance, so that a
reader who follows the path finds the grant recorded and never a blank form.

## Pull request

Appended by the integrator in the landing commit: the pull request URL.

## Current source revision transport — 2026-09-07

For this source revision, the generated `.opencode/plugins/pasture-lifecycle.ts`
artifact has SHA-256
`c0f4b2ab8956fcf6ae8c15bd58e1f952e939b4cd4bddeee325ca77a55c20aaf3`.
This is not the transport used for the historical captures above.

The capture-kit transport remains
`bfd1f25bfcef8d8f3f5b6b784816a15f1a238cc42ac3d7edd2f00a7d3ea0a879`.
The previous repository transport, recorded on September 5, remains
`900e45e7d91bd390ea2474eebb8b82c9de755f7eb393e88bfc01791791b5ebb6`.
The September 5 description of exactly one differing METADATA line applies
only to that previous repository transport compared with the capture kit. It
does not describe the transport in this source revision.

The current transport adds an 8-second bound on the child process and both
pipe drains, with termination and reaping on timeout. It also adds a closed
Proceed/Deny response parser and asynchronous diagnostic forwarding through
the Node-compatible writable completion API. These changes were not in the
capture kit. Tests in `internal/codegen/opencode_target_test.go` exercise the
generated plugin under Bun 1.3.13, including exact diagnostic bytes and a
restoration of the earlier forwarding call that reproduces its stalled write.
The forwarding change is a tested workaround in that context; Bun's internal
cause remains unestablished.

No fixture bytes, sidecar hashes, substitutions or user acceptance above have
changed. The enabled callbacks remain `session.created` and
`tool.execute.before`, and response capability remains None. This addendum
records no new capture or host-displayed Deny acceptance. It does not extend
the historical capture-isolation or acceptance evidence.

## Current transport addendum — 2026-09-08

The generated plugin and its installed payload copy now have SHA-256
`acdfaaf3148d80b674deaac8b3458dfd58e3a5103a9fcaf9c74fd207703c5a17`.
Their source is `internal/codegen/opencode_hooks.go`. This digest identifies
the current transport, not the historical capture kit or captured payloads.

Emission now joins activation and registration by event kind and selects the
runtime observation or named-callback surface. The event listener forwards
only enabled observation names. Named callbacks do not write host-owned
objects; `tool.execute.before` retains its `{ input, output: { args } }`
payload projection. The default `{ id, server }` loader, eight-second child
and pipe bound, closed response parser and diagnostic forwarding remain.

Constructed additional registrations in
`internal/codegen/opencode_target_test.go` prove generator and Bun dispatch
mechanics only. The existing two accepted payloads also pass through the
generated default loader, built CLI and durable readback. Neither proof is a
new live capture or evidence of host-displayed Deny.

The accepted fixture bytes, sidecars and acceptance text above are unchanged.
Production remains two enabled events out of 47 registered, both with response
capability None. This addendum clears no additional captured row and does not
extend the historical capture-isolation evidence.

## Invocation-version transport addendum — 2026-09-08

The generated plugin and installed `pasture-hooks.ts` payload now have SHA-256
`d73061e1b130a983ff72d423ec41c02ef6a32799fed6daf5cc55aba92172e00a`.
Their source remains `internal/codegen/opencode_hooks.go`. This is a transport
identity, not a new capture-kit or fixture identity.

The plugin no longer supplies a compiled version as an invocation observation.
For `session.created`, a nonempty string at `event.properties.info.version`
is passed unchanged through the explicit version argument for that occurrence.
Missing, empty, whitespace-only or wrong-kind optional metadata leaves version
discovery to the CLI. Later tool callbacks always leave discovery to the CLI;
they never reuse the stored creation version. Static contract metadata remains
the build baseline. The callback bodies, default loader, response parser,
diagnostic forwarding and child/pipe bounds are unchanged.

This addendum records no new live capture, fixture acceptance or host capability.
All historical fixture bytes, sidecars, pairing and acceptance records above
remain unchanged. Constructed version controls are transport tests, not evidence
of a capture from a newer host. Production remains two enabled events out of 47,
both with response capability None.
