# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

## Harness and pinned version

OpenCode 1.18.29, verified with `opencode --version` immediately before the
session on 2026-09-05: `1.18.29`. Admission is a floor: this version and every
later release; the contract records 1.18.29.

Second batch: OpenCode 2.0.20, verified with `opencode --version` immediately
before the sitting on 2026-10-01: `opencode v2.0.20` (the binary resolves
`~/.bun/bin/opencode` to `@opencode/cli 2.0.20`; the reference source pins tag
`v2.0.20`). Admission for this batch is the same floor applied at the new
version: the contract records 2.0.20 for the nine fixtures below. The 1.18.29
fixtures above are unchanged.

Third batch: OpenCode 2.0.21, verified with `opencode --version` immediately
before the sitting on 2026-10-02 (UTC; 2026-10-01 local): `opencode v2.0.21`.
The installed host moved 2.0.20 -> 2.0.21 after the second-batch sitting and
the user chose to capture from the installed 2.0.21 (npm registry and the
upstream tags confirm both releases; the plugin-activation and session source
files are unchanged between them). The capture records 2.0.21 plainly, a later
release than the 2.0.20 the contract records. Committing it enables nothing:
no activation row names it, and amending the recorded-version invariant to
admit a later-release capture is a separate enablement change that follows the
user's acceptance.

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
`4a42c540aad61ee466b922300c8617b3dfd90dbcbe9576a90d78cb7d81e283ae`
(its default export is the `Plugin.define({ id, setup })` object the 2.0.20
host's plugin loader reads, registering location-scoped hooks through the
setup context). The two captures above ran through the retired 1.18.29 plugin
shape and remain valid for the 1.18.29 contract rows only; they are not
2.0.20 captures, and no 2.0.20 capture has been cleared into this directory.
A reader who hashes the shipped plugin gets the digest above.

### Second batch — OpenCode 2.0.20, 2026-10-01

Captured in one live non-interactive sitting on 2026-10-01, into
`~/.local/share/pasture-captures/opencode-v2`, with `PASTURE_CAPTURE_DIR` set.
Paths in this record are spelled with `~` for the capturing user's home
directory; the fixture bytes themselves carry the `/home/user` placeholder that
`home-path-v1` writes. The sitting was a live session of the real host binary
on the user's machine, run at the user's direction by the team's supervisor
(not the user typing); the supervisor drove via non-interactive
`opencode run --standalone` once, in a throwaway project directory outside
every repository (`--standalone` was required so the plugin's child process
inherits the capture environment; the background service would not). Nothing
captured reaches any remote before the user's acceptance for this batch; that
acceptance was recorded on 2026-10-01 (see User acceptance below).

Build kit: one `pasture` binary, sha256
`3f74c750c0c7d7f77abf0f20f62877b45b95856a4e7ead7c2b3ebb005e15df24`,
built with `CGO_ENABLED=0` from an archive copy of
`integration/rp2tju-opencode-v2 @ eccf78a0e4bb72e7909954d3d1c3fb44187e71a8`
under `/tmp/opencode/kit-v2` with `make build`. Generated plugin (emitted +
asset snapshot, byte-identical) sha256
`65d58932be728b76f1bd2b3eb812de56089469d3650f70d0c9fd86e84f388fc2`
(the plain definition object the 2.0.20 host loads with no runtime import;
zero runtime imports). The kit plugin is byte-identical to the committed
`.opencode/plugins/pasture-lifecycle.ts` at clearance time, so no
kit-vs-committed divergence needs a correction paragraph for this batch.
Environment in the capture shell only: `PASTURE_BIN` = that binary,
`PASTURE_CAPTURE_DIR` = the directory above (absolute, outside every
repository, pre-existing), `PASTURE_DB_PATH` =
`~/.local/share/pasture-captures/scratch/pasture.db` (a fresh scratch
database; the live store was never touched). Project directory
`~/.local/share/pasture-captures/project-opencode-v2` holding the kit's
`.opencode/`, `README.md`, `NOTES.md`. On this scratch database every hook
printed the expected fresh-store identity-bootstrap fault line
(`lifecycle-faults.jsonl`, 20 lines beside the scratch database, one per hook
invocation); the host proceeded and the capture is unaffected. With the fixed
plugin the host logged `loading plugin ... pasture-lifecycle.ts` with no
failure line (one earlier attempt captured nothing because the plugin did not
load, which is why the no-runtime-import fix preceded this sitting).

Exact prompt (one session, agent `build`, model
`opencode-go/deepseek-v4.1-flash`): Run the shell command `ls -la`, then
create a file named capture-note.txt containing the word hello. Exit 0. The
model listed the directory and created capture-note.txt with the word hello.
No approval prompt appeared (the build agent allows edit and bash).

One line per chosen event: event, capture file, trigger, capture time (UTC
from the capture file's modification time), raw digest with its byte size, and
committed digest with its byte size. The difference between the two digests is
what the substitution changed, and the byte sizes show how much. Times are the
file modification times converted to UTC.
- session.prompt: `opencode_session_prompt_2_0_20.1.json` — trigger: the single session prompt above (steer delivery) — 2026-10-01T10:48:59Z — raw sha256:56e5a393cb8389886f5ce0805cb2bfc049479ae4cf67ec0ff167ecde1258bd31 (246 bytes) — committed sha256:f3c62d3564e443606d8ae537c04053e39661de4e4c50e29f0ebcd4ff5f92f2a8 (246 bytes)
- session.context: `opencode_session_context_2_0_20.1.json` — trigger: the model-context assembly for the same prompt (system plus tools dump) — 2026-10-01T10:48:59Z — raw sha256:f20a4a809c52e99cb1dcf6610039ba04b5e28df442cc0197362772ec33121172 (42843 bytes) — committed sha256:6a79e867dcf6a0575646d162c7438e1f470b81b3ad5ff67f70c9d28a1126148d (42837 bytes)
- session.title: `opencode_session_title_2_0_20.1.json` — trigger: the title-model assembly for the same prompt (title agent) — 2026-10-01T10:48:59Z — raw sha256:325b35641de4b239434e493f893a465b17788a50aaed40123e9b5aa6030c30ff (2562 bytes) — committed sha256:f0df594d2e4fc506d55934d84f9288c821dc2a87ad7d4cd85c2d74f447a0b4c0 (2562 bytes)
- session.model.request: `opencode_session_model_request_2_0_20.2.json` — trigger: the title-model HTTP assembly (title agent, smallest of three) — 2026-10-01T10:48:59Z — raw sha256:cea5dc5168c0cbe36654a9d3621e62d7ea08d6153a000a1e9c1d77b6af86280b (504 bytes) — committed sha256:cea5dc5168c0cbe36654a9d3621e62d7ea08d6153a000a1e9c1d77b6af86280b (504 bytes)
- session.http.request: `opencode_session_http_request_2_0_20.2.json` — trigger: the title-model HTTP request (title agent, smallest of three) — 2026-10-01T10:48:59Z — raw sha256:62bae0127b62eba1e6f85aa0676e745bd308e90daefed74f9c5f22258867581b (162 bytes) — committed sha256:62bae0127b62eba1e6f85aa0676e745bd308e90daefed74f9c5f22258867581b (162 bytes)
- session.http.response: `opencode_session_http_response_2_0_20.1.json` — trigger: the title-model HTTP response (title agent, smallest of three) — 2026-10-01T10:49:00Z — raw sha256:f0d8e7ac5bafed0dcfa624aa5e7f7bc12134776d4d4b50f9f80936e7b73d380f (176 bytes) — committed sha256:f0d8e7ac5bafed0dcfa624aa5e7f7bc12134776d4d4b50f9f80936e7b73d380f (176 bytes)
- tool.execute.before: `opencode_tool_execute_before_2_0_20.1.json` — trigger: the shell call `ls -la` before it ran (build agent) — 2026-10-01T10:49:01Z — raw sha256:4400b23088b1fb0fc43535999fc684998454b78ee6efca1e2e1dacf6507263c3 (191 bytes) — committed sha256:ccfc79264f6f5814d3d98442f4a3d0d26da251b85585becb8c5d14c09de6c5d5 (191 bytes)
- tool.execute.after: `opencode_tool_execute_after_2_0_20.1.json` — trigger: the write call creating capture-note.txt after it completed (build agent, smallest of two) — 2026-10-01T10:49:01Z — raw sha256:a0be38622eb651dc28b369ce7cb70fab49e7709f92a7b693c3133a0f625e248c (500 bytes) — committed sha256:7aed62a209ddeaedca5ea8e24a25103cdf99fc19887f1d06cd83b6bab9a6095d (497 bytes)
- permission.evaluate: `opencode_permission_evaluate_2_0_20.2.json` — trigger: the shell `ls -la` evaluation answering allow (build agent, smallest of two) — 2026-10-01T10:49:01Z — raw sha256:b88aba3c7c8660e0dc14757569bdfd54de4e4e7af33b4e86dde5c039d55e1045 (229 bytes) — committed sha256:94e2525cc2b0446d07bc0f72996defd0cc56471159b0a06b73aa0f618c426729 (229 bytes)

Captured and not used (not committed; listed so the record is complete;
smallest authentic file per event is the fixture, the rest are sizes not
chosen):
- `opencode_session_context_2_0_20.2.json` — raw sha256:45e3cbd684efb44aff1c0f1b3cc02967360d082e9557468809fa64bd02409080 (44374 bytes; 1531 bytes larger than the chosen .1)
- `opencode_session_model_request_2_0_20.1.json` — raw sha256:302110a5653bffd3e18314f2fded094736b1d3dd589a40f210daa294fc9ac65a (518 bytes)
- `opencode_session_model_request_2_0_20.3.json` — raw sha256:302110a5653bffd3e18314f2fded094736b1d3dd589a40f210daa294fc9ac65a (518 bytes)
- `opencode_session_http_request_2_0_20.1.json` — raw sha256:f028cdae9781f1ec87d7c927b2771201f6c34f9675fa624854dfb0ab9becbba4 (176 bytes)
- `opencode_session_http_request_2_0_20.3.json` — raw sha256:f028cdae9781f1ec87d7c927b2771201f6c34f9675fa624854dfb0ab9becbba4 (176 bytes)
- `opencode_session_http_response_2_0_20.2.json` — raw sha256:296632be2880eb392015db3537bf1f85be51cf4e561ee53906fe05fac0a32d87 (190 bytes)
- `opencode_session_http_response_2_0_20.3.json` — raw sha256:296632be2880eb392015db3537bf1f85be51cf4e561ee53906fe05fac0a32d87 (190 bytes)
- `opencode_tool_execute_before_2_0_20.2.json` — raw sha256:463a88f391e3bbeb5742f570ddb6b1faabe1209df55d8c2806fbfe77c1e65b2b (218 bytes)
- `opencode_tool_execute_after_2_0_20.2.json` — raw sha256:b7c434b24f504b6b7cee171426d5eeb8a0654362f1999abf1c625e4719e063ef (1079 bytes; the shell `ls -la` output carrying the directory listing)
- `opencode_permission_evaluate_2_0_20.1.json` — raw sha256:d67cae2fd0b84b794d0f6db9f923e08319873fd6c02864d4413b36fc76963bbe (508 bytes; the edit evaluation for capture-note.txt with the file patch)
- `opencode_shell_create_before_2_0_20.1.json` — raw sha256:0ea42c790b35c0b7fbb60f2136222c9de33604c666cf875a26357b0f16d21869 (29339 bytes; withheld as unclearable, see refused classes)

Not fired (a measurement, not a conclusion of unreachability; each needs a
second trigger; each gets no fixture in this slice and stays withheld):
session.created, session.compaction, session.generate, session.retry,
session.experimental.ws.handshake, session.experimental.ws.send,
session.experimental.ws.receive. In particular session.created did not fire,
so no session claim is written from this sitting and the v2 gate has no claim
to consult from these bytes.

### Third batch — OpenCode 2.0.21, 2026-10-02

Captured in one supervised live sitting on 2026-10-02 (UTC), into
`~/.local/share/pasture-captures/opencode-v2-sessioncreated`, with
`PASTURE_CAPTURE_DIR` set. Paths are spelled with `~`; the fixture bytes carry
the `/home/user` placeholder that `home-path-v1` writes. The sitting was a live
session of the real host binary on the user's machine, driven by the team's
supervisor agent at the user's direction (the user did not type the prompts),
with non-interactive `opencode run` WITHOUT `--standalone`, exactly as the user
prescribed, so the event came through the managed background service rather
than a standalone process.

Isolation: the sitting ran against an isolated managed service with its own
XDG data/state/cache/config roots under `/tmp/opencode/capture-xdg`, private
port 49375, credentials linked read-only, and the capture environment injected
through the service config `env` field (the managed spawn does not pass the
caller's `PASTURE_*` by default; the service.json env map is the designed
channel). The user's running service (port 49374) and the user's live
OpenCode database were never touched. The live pasture store was not touched
(mtime unchanged). Honest note: the dry run before the sitting was piped into
the kit binary without `PASTURE_DB_PATH`, so it appended a fault line (and the
hung first run a tool.execute.before line) to the live
`lifecycle-faults.jsonl` beside the user's store; the dry-run capture file was
deleted, the fault lines remain in that log, and no fixture derives from them.

Build kit: one `pasture` binary, sha256
`b5a31993496a6f723ebf787ed58178509351a86e5ef5317f7c66af602af7036c`, built from
`fd0f155` (origin/main). Generated plugin sha256
`65d58932be728b76f1bd2b3eb812de56089469d3650f70d0c9fd86e84f388fc2`,
byte-identical to the committed `.opencode/plugins/pasture-lifecycle.ts` at
`fd0f155`, so no kit-vs-committed divergence paragraph is needed.

Sequence: run 1 on a fresh service created a session while plugin activation
was still settling; session.prompt was captured as `.1` and session.created was
missed (the measured first-session race, tracked as a design question
separately). Run 2 on the warm service captured session.created and
session.prompt `.2`. The model call failed in isolation (provider unresolved;
an auth plugin failed to load from the isolated plugin cache); session creation
and its event precede the model call, so the captured bytes are authentic host
bytes.

- session.created: `opencode_session_created_2_0_21.1.json` — trigger: run 2 of `opencode run` against the warm isolated service — 2026-10-02T01:12:46Z — raw sha256:bb4a0361818e5177a774d3459ed970f281e8315f5097632e4310569ab1b93e44 (606 bytes) — committed sha256:7eda755b4bb9d82a44e057742f0ede1d8ff90280e607ccf900cefe9776f5cc5c (600 bytes)

Captured and not used (not committed; session.prompt already has a cleared
2.0.20 fixture, so these are recorded, not selected):
- `opencode_session_prompt_2_0_21.1.json` — raw sha256:8f6dbf1f80be3f543b3faa8d78a5c1e14a94fabc3487e018978b51125f9927ca (156 bytes; run 1)
- `opencode_session_prompt_2_0_21.2.json` — raw sha256:63e7069adfc2ac98f5a3758d80ac73fd24cd65cac073bba794e5f03995f5e01d (162 bytes; run 2)

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

### Second batch — OpenCode 2.0.20

Output of the inventory report (`PASTURE_INVENTORY_DIR` over the capture
directory, all 20 payloads; the 9 smallest per event below are the fixtures).
The report names every refused class and every unclearable reason it finds.
Only the shell payload is refused/unclearable; the two large session.context
payloads carry no refused class (their size is many small free-text fields,
no single tool-response value above 4096 bytes, no environment dump).

```
opencode_permission_evaluate_2_0_20.1.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .action                                                      identifier 
  .resources[0]                                                identifier 
  .metadata.files[0].file                                      identifier 
  .metadata.files[0].patch                                     free-text    FREE TEXT: substitute with free-text-v1
  .metadata.files[0].status                                    identifier 
  .metadata.files[0].additions                                 number     
  .metadata.files[0].deletions                                 number     
  .source.type                                                 identifier 
  .source.messageID                                            identifier 
  .source.id                                                   identifier 
  .effect                                                      identifier 
opencode_permission_evaluate_2_0_20.2.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .action                                                      identifier 
  .resources[0]                                                free-text    FREE TEXT: substitute with free-text-v1
  .source.type                                                 identifier 
  .source.messageID                                            identifier 
  .source.id                                                   identifier 
  .effect                                                      identifier 
opencode_session_context_2_0_20.1.json
  .sessionID                                                   identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .system[0].type                                              identifier 
  .system[0].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .system[1].type                                              identifier 
  .system[1].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .system[2].type                                              identifier 
  .system[2].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .system[3].type                                              identifier 
  .system[3].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .messages[0].id                                              identifier 
  .messages[0].role                                            identifier 
  .messages[0].content[0].type                                 identifier 
  .messages[0].content[0].text                                 free-text    FREE TEXT: substitute with free-text-v1
  .options.maxTokens                                           number     
  .agent                                                       identifier 
  .tools.edit.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.type                                       identifier 
  .tools.edit.input.properties.path.type                       identifier 
  .tools.edit.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.properties.oldString.type                  identifier 
  .tools.edit.input.properties.oldString.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.properties.newString.type                  identifier 
  .tools.edit.input.properties.newString.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.properties.replaceAll.type                 identifier 
  .tools.edit.input.properties.replaceAll.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.required[0]                                identifier 
  .tools.edit.input.required[1]                                identifier 
  .tools.edit.input.required[2]                                identifier 
  .tools.edit.input.additionalProperties                       bool       
  .tools.glob.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.type                                       identifier 
  .tools.glob.input.properties.pattern.type                    identifier 
  .tools.glob.input.properties.pattern.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.properties.path.type                       identifier 
  .tools.glob.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.properties.hidden.type                     identifier 
  .tools.glob.input.properties.hidden.description              free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.properties.limit.type                      identifier 
  .tools.glob.input.properties.limit.exclusiveMinimum          number     
  .tools.glob.input.properties.limit.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.required[0]                                identifier 
  .tools.glob.input.additionalProperties                       bool       
  .tools.grep.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.type                                       identifier 
  .tools.grep.input.properties.pattern.type                    identifier 
  .tools.grep.input.properties.pattern.minLength               number     
  .tools.grep.input.properties.pattern.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.path.type                       identifier 
  .tools.grep.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.include.type                    identifier 
  .tools.grep.input.properties.include.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.literal.type                    identifier 
  .tools.grep.input.properties.literal.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.caseSensitive.type              identifier 
  .tools.grep.input.properties.caseSensitive.description       free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.limit.type                      identifier 
  .tools.grep.input.properties.limit.exclusiveMinimum          number     
  .tools.grep.input.properties.limit.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.required[0]                                identifier 
  .tools.grep.input.additionalProperties                       bool       
  .tools.question.description                                  free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.type                                   identifier 
  .tools.question.input.properties.questions.type              identifier 
  .tools.question.input.properties.questions.items.type        identifier 
  .tools.question.input.properties.questions.items.properties.question.type identifier 
  .tools.question.input.properties.questions.items.properties.question.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.header.type identifier 
  .tools.question.input.properties.questions.items.properties.header.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.options.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.properties.label.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.properties.label.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.options.items.properties.description.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.properties.description.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.options.items.required[0] identifier 
  .tools.question.input.properties.questions.items.properties.options.items.required[1] identifier 
  .tools.question.input.properties.questions.items.properties.options.items.additionalProperties bool       
  .tools.question.input.properties.questions.items.properties.options.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.multiple.type identifier 
  .tools.question.input.properties.questions.items.required[0] identifier 
  .tools.question.input.properties.questions.items.required[1] identifier 
  .tools.question.input.properties.questions.items.required[2] identifier 
  .tools.question.input.properties.questions.items.additionalProperties bool       
  .tools.question.input.properties.questions.minItems          number     
  .tools.question.input.properties.questions.description       free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.required[0]                            identifier 
  .tools.question.input.additionalProperties                   bool       
  .tools.read.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.type                                       identifier 
  .tools.read.input.properties.path.type                       identifier 
  .tools.read.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.properties.offset.type                     identifier 
  .tools.read.input.properties.offset.minimum                  number     
  .tools.read.input.properties.offset.description              free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.properties.limit.type                      identifier 
  .tools.read.input.properties.limit.minimum                   number     
  .tools.read.input.properties.limit.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.required[0]                                identifier 
  .tools.read.input.additionalProperties                       bool       
  .tools.shell.description                                     free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.type                                      identifier 
  .tools.shell.input.properties.command.type                   identifier 
  .tools.shell.input.properties.command.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.properties.workdir.type                   identifier 
  .tools.shell.input.properties.workdir.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.properties.timeout.type                   identifier 
  .tools.shell.input.properties.timeout.minimum                number     
  .tools.shell.input.properties.timeout.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.properties.background.type                identifier 
  .tools.shell.input.properties.background.description         free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.required[0]                               identifier 
  .tools.shell.input.additionalProperties                      bool       
  .tools.skill.description                                     free-text    FREE TEXT: substitute with free-text-v1
  .tools.skill.input.type                                      identifier 
  .tools.skill.input.properties.id.type                        identifier 
  .tools.skill.input.properties.id.description                 free-text    FREE TEXT: substitute with free-text-v1
  .tools.skill.input.required[0]                               identifier 
  .tools.skill.input.additionalProperties                      bool       
  .tools.subagent.description                                  free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.type                                   identifier 
  .tools.subagent.input.properties.agent.type                  identifier 
  .tools.subagent.input.properties.agent.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.description.type            identifier 
  .tools.subagent.input.properties.description.description     free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.prompt.type                 identifier 
  .tools.subagent.input.properties.prompt.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.model.type                  identifier 
  .tools.subagent.input.properties.model.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.sessionID.type              identifier 
  .tools.subagent.input.properties.sessionID.pattern           identifier 
  .tools.subagent.input.properties.sessionID.description       free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.background.type             identifier 
  .tools.subagent.input.properties.background.description      free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.required[0]                            identifier 
  .tools.subagent.input.required[1]                            identifier 
  .tools.subagent.input.required[2]                            identifier 
  .tools.subagent.input.additionalProperties                   bool       
  .tools.webfetch.description                                  free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.type                                   identifier 
  .tools.webfetch.input.properties.url.type                    identifier 
  .tools.webfetch.input.properties.url.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.properties.format.type                 identifier 
  .tools.webfetch.input.properties.format.enum[0]              identifier 
  .tools.webfetch.input.properties.format.enum[1]              identifier 
  .tools.webfetch.input.properties.format.enum[2]              identifier 
  .tools.webfetch.input.properties.format.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.properties.timeout.type                identifier 
  .tools.webfetch.input.properties.timeout.exclusiveMinimum    number     
  .tools.webfetch.input.properties.timeout.maximum             number     
  .tools.webfetch.input.properties.timeout.description         free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.required[0]                            identifier 
  .tools.webfetch.input.additionalProperties                   bool       
  .tools.websearch.description                                 free-text    FREE TEXT: substitute with free-text-v1
  .tools.websearch.input.type                                  identifier 
  .tools.websearch.input.properties.query.type                 identifier 
  .tools.websearch.input.properties.query.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.websearch.input.required[0]                           identifier 
  .tools.websearch.input.additionalProperties                  bool       
  .tools.write.description                                     free-text    FREE TEXT: substitute with free-text-v1
  .tools.write.input.type                                      identifier 
  .tools.write.input.properties.path.type                      identifier 
  .tools.write.input.properties.path.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.write.input.properties.content.type                   identifier 
  .tools.write.input.properties.content.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.write.input.required[0]                               identifier 
  .tools.write.input.required[1]                               identifier 
  .tools.write.input.additionalProperties                      bool       
  .tools.execute.description                                   free-text    FREE TEXT: substitute with free-text-v1
  .tools.execute.input.type                                    identifier 
  .tools.execute.input.properties.code.type                    identifier 
  .tools.execute.input.required[0]                             identifier 
  .tools.execute.input.additionalProperties                    bool       
opencode_session_context_2_0_20.2.json
  .sessionID                                                   identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .system[0].type                                              identifier 
  .system[0].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .system[1].type                                              identifier 
  .system[1].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .system[2].type                                              identifier 
  .system[2].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .system[3].type                                              identifier 
  .system[3].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .messages[0].id                                              identifier 
  .messages[0].role                                            identifier 
  .messages[0].content[0].type                                 identifier 
  .messages[0].content[0].text                                 free-text    FREE TEXT: substitute with free-text-v1
  .messages[1].id                                              identifier 
  .messages[1].role                                            identifier 
  .messages[1].content[0].type                                 identifier 
  .messages[1].content[0].text                                 free-text    FREE TEXT: substitute with free-text-v1
  .messages[1].content[0].providerMetadata.opencode-go.reasoningField identifier 
  .messages[1].content[1].type                                 identifier 
  .messages[1].content[1].text                                 free-text    FREE TEXT: substitute with free-text-v1
  .messages[1].content[2].type                                 identifier 
  .messages[1].content[2].id                                   identifier 
  .messages[1].content[2].name                                 identifier 
  .messages[1].content[2].input.command                        free-text    FREE TEXT: substitute with free-text-v1
  .messages[1].content[2].providerExecuted                     bool       
  .messages[1].content[3].type                                 identifier 
  .messages[1].content[3].id                                   identifier 
  .messages[1].content[3].name                                 identifier 
  .messages[1].content[3].input.path                           identifier 
  .messages[1].content[3].input.content                        free-text    FREE TEXT: substitute with free-text-v1
  .messages[1].content[3].providerExecuted                     bool       
  .messages[2].role                                            identifier 
  .messages[2].content[0].type                                 identifier 
  .messages[2].content[0].id                                   identifier 
  .messages[2].content[0].name                                 identifier 
  .messages[2].content[0].result.type                          identifier 
  .messages[2].content[0].result.value                         free-text    FREE TEXT: substitute with free-text-v1
  .messages[2].content[0].providerExecuted                     bool       
  .messages[3].role                                            identifier 
  .messages[3].content[0].type                                 identifier 
  .messages[3].content[0].id                                   identifier 
  .messages[3].content[0].name                                 identifier 
  .messages[3].content[0].result.type                          identifier 
  .messages[3].content[0].result.value                         free-text    FREE TEXT: substitute with free-text-v1
  .messages[3].content[0].providerExecuted                     bool       
  .options.maxTokens                                           number     
  .agent                                                       identifier 
  .tools.edit.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.type                                       identifier 
  .tools.edit.input.properties.path.type                       identifier 
  .tools.edit.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.properties.oldString.type                  identifier 
  .tools.edit.input.properties.oldString.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.properties.newString.type                  identifier 
  .tools.edit.input.properties.newString.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.properties.replaceAll.type                 identifier 
  .tools.edit.input.properties.replaceAll.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.edit.input.required[0]                                identifier 
  .tools.edit.input.required[1]                                identifier 
  .tools.edit.input.required[2]                                identifier 
  .tools.edit.input.additionalProperties                       bool       
  .tools.glob.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.type                                       identifier 
  .tools.glob.input.properties.pattern.type                    identifier 
  .tools.glob.input.properties.pattern.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.properties.path.type                       identifier 
  .tools.glob.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.properties.hidden.type                     identifier 
  .tools.glob.input.properties.hidden.description              free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.properties.limit.type                      identifier 
  .tools.glob.input.properties.limit.exclusiveMinimum          number     
  .tools.glob.input.properties.limit.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.glob.input.required[0]                                identifier 
  .tools.glob.input.additionalProperties                       bool       
  .tools.grep.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.type                                       identifier 
  .tools.grep.input.properties.pattern.type                    identifier 
  .tools.grep.input.properties.pattern.minLength               number     
  .tools.grep.input.properties.pattern.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.path.type                       identifier 
  .tools.grep.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.include.type                    identifier 
  .tools.grep.input.properties.include.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.literal.type                    identifier 
  .tools.grep.input.properties.literal.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.caseSensitive.type              identifier 
  .tools.grep.input.properties.caseSensitive.description       free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.properties.limit.type                      identifier 
  .tools.grep.input.properties.limit.exclusiveMinimum          number     
  .tools.grep.input.properties.limit.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.grep.input.required[0]                                identifier 
  .tools.grep.input.additionalProperties                       bool       
  .tools.question.description                                  free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.type                                   identifier 
  .tools.question.input.properties.questions.type              identifier 
  .tools.question.input.properties.questions.items.type        identifier 
  .tools.question.input.properties.questions.items.properties.question.type identifier 
  .tools.question.input.properties.questions.items.properties.question.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.header.type identifier 
  .tools.question.input.properties.questions.items.properties.header.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.options.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.properties.label.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.properties.label.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.options.items.properties.description.type identifier 
  .tools.question.input.properties.questions.items.properties.options.items.properties.description.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.options.items.required[0] identifier 
  .tools.question.input.properties.questions.items.properties.options.items.required[1] identifier 
  .tools.question.input.properties.questions.items.properties.options.items.additionalProperties bool       
  .tools.question.input.properties.questions.items.properties.options.description free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.properties.questions.items.properties.multiple.type identifier 
  .tools.question.input.properties.questions.items.required[0] identifier 
  .tools.question.input.properties.questions.items.required[1] identifier 
  .tools.question.input.properties.questions.items.required[2] identifier 
  .tools.question.input.properties.questions.items.additionalProperties bool       
  .tools.question.input.properties.questions.minItems          number     
  .tools.question.input.properties.questions.description       free-text    FREE TEXT: substitute with free-text-v1
  .tools.question.input.required[0]                            identifier 
  .tools.question.input.additionalProperties                   bool       
  .tools.read.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.type                                       identifier 
  .tools.read.input.properties.path.type                       identifier 
  .tools.read.input.properties.path.description                free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.properties.offset.type                     identifier 
  .tools.read.input.properties.offset.minimum                  number     
  .tools.read.input.properties.offset.description              free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.properties.limit.type                      identifier 
  .tools.read.input.properties.limit.minimum                   number     
  .tools.read.input.properties.limit.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.read.input.required[0]                                identifier 
  .tools.read.input.additionalProperties                       bool       
  .tools.shell.description                                     free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.type                                      identifier 
  .tools.shell.input.properties.command.type                   identifier 
  .tools.shell.input.properties.command.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.properties.workdir.type                   identifier 
  .tools.shell.input.properties.workdir.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.properties.timeout.type                   identifier 
  .tools.shell.input.properties.timeout.minimum                number     
  .tools.shell.input.properties.timeout.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.properties.background.type                identifier 
  .tools.shell.input.properties.background.description         free-text    FREE TEXT: substitute with free-text-v1
  .tools.shell.input.required[0]                               identifier 
  .tools.shell.input.additionalProperties                      bool       
  .tools.skill.description                                     free-text    FREE TEXT: substitute with free-text-v1
  .tools.skill.input.type                                      identifier 
  .tools.skill.input.properties.id.type                        identifier 
  .tools.skill.input.properties.id.description                 free-text    FREE TEXT: substitute with free-text-v1
  .tools.skill.input.required[0]                               identifier 
  .tools.skill.input.additionalProperties                      bool       
  .tools.subagent.description                                  free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.type                                   identifier 
  .tools.subagent.input.properties.agent.type                  identifier 
  .tools.subagent.input.properties.agent.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.description.type            identifier 
  .tools.subagent.input.properties.description.description     free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.prompt.type                 identifier 
  .tools.subagent.input.properties.prompt.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.model.type                  identifier 
  .tools.subagent.input.properties.model.description           free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.sessionID.type              identifier 
  .tools.subagent.input.properties.sessionID.pattern           identifier 
  .tools.subagent.input.properties.sessionID.description       free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.properties.background.type             identifier 
  .tools.subagent.input.properties.background.description      free-text    FREE TEXT: substitute with free-text-v1
  .tools.subagent.input.required[0]                            identifier 
  .tools.subagent.input.required[1]                            identifier 
  .tools.subagent.input.required[2]                            identifier 
  .tools.subagent.input.additionalProperties                   bool       
  .tools.webfetch.description                                  free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.type                                   identifier 
  .tools.webfetch.input.properties.url.type                    identifier 
  .tools.webfetch.input.properties.url.description             free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.properties.format.type                 identifier 
  .tools.webfetch.input.properties.format.enum[0]              identifier 
  .tools.webfetch.input.properties.format.enum[1]              identifier 
  .tools.webfetch.input.properties.format.enum[2]              identifier 
  .tools.webfetch.input.properties.format.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.properties.timeout.type                identifier 
  .tools.webfetch.input.properties.timeout.exclusiveMinimum    number     
  .tools.webfetch.input.properties.timeout.maximum             number     
  .tools.webfetch.input.properties.timeout.description         free-text    FREE TEXT: substitute with free-text-v1
  .tools.webfetch.input.required[0]                            identifier 
  .tools.webfetch.input.additionalProperties                   bool       
  .tools.websearch.description                                 free-text    FREE TEXT: substitute with free-text-v1
  .tools.websearch.input.type                                  identifier 
  .tools.websearch.input.properties.query.type                 identifier 
  .tools.websearch.input.properties.query.description          free-text    FREE TEXT: substitute with free-text-v1
  .tools.websearch.input.required[0]                           identifier 
  .tools.websearch.input.additionalProperties                  bool       
  .tools.write.description                                     free-text    FREE TEXT: substitute with free-text-v1
  .tools.write.input.type                                      identifier 
  .tools.write.input.properties.path.type                      identifier 
  .tools.write.input.properties.path.description               free-text    FREE TEXT: substitute with free-text-v1
  .tools.write.input.properties.content.type                   identifier 
  .tools.write.input.properties.content.description            free-text    FREE TEXT: substitute with free-text-v1
  .tools.write.input.required[0]                               identifier 
  .tools.write.input.required[1]                               identifier 
  .tools.write.input.additionalProperties                      bool       
  .tools.execute.description                                   free-text    FREE TEXT: substitute with free-text-v1
  .tools.execute.input.type                                    identifier 
  .tools.execute.input.properties.code.type                    identifier 
  .tools.execute.input.required[0]                             identifier 
  .tools.execute.input.additionalProperties                    bool       
opencode_session_http_request_2_0_20.1.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
opencode_session_http_request_2_0_20.2.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
opencode_session_http_request_2_0_20.3.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
opencode_session_http_response_2_0_20.1.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
opencode_session_http_response_2_0_20.2.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
opencode_session_http_response_2_0_20.3.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
opencode_session_model_request_2_0_20.1.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
  .baseURL                                                     identifier 
  .headers.x-session-affinity                                  identifier 
  .headers.X-Session-Id                                        identifier 
  .headers.User-Agent                                          identifier 
  .headers.x-opencode-project                                  identifier 
  .headers.x-opencode-session                                  identifier 
  .headers.x-opencode-client                                   identifier 
opencode_session_model_request_2_0_20.2.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
  .baseURL                                                     identifier 
  .headers.x-session-affinity                                  identifier 
  .headers.X-Session-Id                                        identifier 
  .headers.User-Agent                                          identifier 
  .headers.x-opencode-project                                  identifier 
  .headers.x-opencode-session                                  identifier 
  .headers.x-opencode-client                                   identifier 
opencode_session_model_request_2_0_20.3.json
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .kind                                                        identifier 
  .baseURL                                                     identifier 
  .headers.x-session-affinity                                  identifier 
  .headers.X-Session-Id                                        identifier 
  .headers.User-Agent                                          identifier 
  .headers.x-opencode-project                                  identifier 
  .headers.x-opencode-session                                  identifier 
  .headers.x-opencode-client                                   identifier 
opencode_session_prompt_2_0_20.1.json
  .sessionID                                                   identifier 
  .messageID                                                   identifier 
  .prompt.text                                                 free-text    FREE TEXT: substitute with free-text-v1
  .delivery                                                    identifier 
opencode_session_title_2_0_20.1.json
  .sessionID                                                   identifier 
  .model.id                                                    identifier 
  .model.providerID                                            identifier 
  .model.variant                                               identifier 
  .system[0].type                                              identifier 
  .system[0].text                                              free-text    FREE TEXT: substitute with free-text-v1
  .messages[0].role                                            identifier 
  .messages[0].content[0].type                                 identifier 
  .messages[0].content[0].text                                 free-text    FREE TEXT: substitute with free-text-v1
  .options.textVerbosity                                       identifier 
opencode_shell_create_before_2_0_20.1.json
  .command                                                     free-text    FREE TEXT: substitute with free-text-v1
  .cwd                                                         path       
  .timeout                                                     number     
  .shell                                                       path       
  .env.COLORTERM                                               identifier 
  .env.HOME                                                    path       
  .env.INVOCATION_ID                                           identifier 
  .env.JOURNAL_STREAM                                          identifier 
  .env.LANG                                                    identifier 
  .env.LOCALE_ARCHIVE                                          path       
  .env.LOGNAME                                                 identifier 
  .env.MEMORY_PRESSURE_WATCH                                   path       
  .env.MEMORY_PRESSURE_WRITE                                   identifier 
  .env.PATH                                                    path       
  .env.PWD                                                     path       
  .env.SHELL                                                   path       
  .env.SHLVL                                                   identifier 
  .env.SYSTEMD_EXEC_PID                                        identifier 
  .env.TERM                                                    identifier 
  .env.TERM_PROGRAM                                            identifier 
  .env.TERM_PROGRAM_VERSION                                    identifier 
  .env.TMUX                                                    path       
  .env.TMUX_PANE                                               identifier 
  .env.TMUX_TMPDIR                                             path       
  .env.TZDIR                                                   path       
  .env.USER                                                    identifier 
  .env.XDG_RUNTIME_DIR                                         path       
  .env.OLDPWD                                                  path       
  .env.__NIXOS_SET_ENVIRONMENT_DONE                            identifier 
  .env.BEMENU_BACKEND                                          identifier 
  .env.BROWSER                                                 identifier 
  .env.CLUTTER_BACKEND                                         identifier 
  .env.CUDA_PATH                                               path       
  .env.CUPS_DATADIR                                            path       
  .env.DEFAULT_BROWSER                                         identifier 
  .env.EDITOR                                                  identifier 
  .env.ELECTRON_OZONE_PLATFORM_HINT                            identifier 
  .env.EXTRA_LDFLAGS                                           free-text    FREE TEXT: substitute with free-text-v1
  .env.GIO_EXTRA_MODULES                                       path       
  .env.GTK_A11Y                                                identifier 
  .env.GTK_PATH                                                path       
  .env.HOST                                                    identifier 
  .env.INFOPATH                                                path       
  .env.LD_LIBRARY_PATH                                         path       
  .env.LESSKEYIN_SYSTEM                                        path       
  .env.LIBEXEC_PATH                                            path       
  .env.MOZ_ENABLE_WAYLAND                                      identifier 
  .env.NIXOS_OZONE_WL                                          identifier 
  .env.NIXPKGS_CONFIG                                          path       
  .env.NIX_LD                                                  path       
  .env.NIX_LD_LIBRARY_PATH                                     path       
  .env.NIX_PATH                                                free-text    FREE TEXT: substitute with free-text-v1
  .env.NIX_XDG_DESKTOP_PORTAL_DIR                              path       
  .env.NO_AT_BRIDGE                                            identifier 
  .env.NVD_BACKEND                                             identifier 
  .env.PAGER                                                   identifier 
  .env.QTWEBKIT_PLUGIN_PATH                                    path       
  .env.QT_QPA_PLATFORM                                         identifier 
  .env.SDL_VIDEODRIVER                                         identifier 
  .env.SOPS_AGE_KEY_FILE                                       path       
  .env.SSH_ASKPASS                                             path       
  .env.SYSTEMD_XKB_DIRECTORY                                   path       
  .env.TERMINFO_DIRS                                           path       
  .env.XCURSOR_PATH                                            path       
  .env.XDG_CONFIG_DIRS                                         path       
  .env.XDG_DATA_DIRS                                           path       
  .env.XDG_SESSION_TYPE                                        identifier 
  .env.__GL_GSYNC_ALLOWED                                      identifier 
  .env.SSH_AUTH_SOCK                                           path       
  .env.NIX_USER_PROFILE_DIR                                    path       
  .env.NIX_PROFILES                                            path       
  .env.ZDOTDIR                                                 path       
  .env.BEADS_DOLT_SERVER_PORT                                  identifier 
  .env.BEADS_DOLT_AUTO_START                                   identifier 
  .env.__HM_SESS_VARS_SOURCED                                  identifier 
  .env.BD_BACKUP_GIT_PUSH                                      identifier 
  .env.FZF_DEFAULT_OPTS                                        free-text    FREE TEXT: substitute with free-text-v1
  .env.GTK2_RC_FILES                                           path       
  .env.GTK_DEFAULT_BROWSER                                     identifier 
  .env.GTK_THEME                                               identifier 
  .env.LOCALE_ARCHIVE_2_27                                     path       
  .env.MOZ_DBUS_REMOTE                                         identifier 
  .env.VISUAL                                                  identifier 
  .env.XCURSOR_SIZE                                            identifier 
  .env.XCURSOR_THEME                                           identifier 
  .env.XDG_CURRENT_DESKTOP                                     identifier 
  .env.__HM_ZSH_SESS_VARS_SOURCED                              identifier 
  .env.ZSH_DISABLE_COMPFIX                                     identifier 
  .env.GPG_TTY                                                 path       
  .env.VI_MODE                                                 free-text    FREE TEXT: substitute with free-text-v1
  .env.MANPAGER                                                free-text    FREE TEXT: substitute with free-text-v1
  .env.MANROFFOPT                                              identifier 
  .env.LESSOPEN                                                free-text    FREE TEXT: substitute with free-text-v1
  .env.LESS                                                    free-text    FREE TEXT: substitute with free-text-v1
  .env.BATPIPE                                                 identifier 
  .env.LS_COLORS                                               free-text    FREE TEXT: substitute with free-text-v1
  .env.MANPATH                                                 path       
  .env.DOLT_REMOTE_PASSWORD                                    free-text    FREE TEXT: substitute with free-text-v1
  .env.LD                                                      identifier 
  .env.DIRENV_WATCHES                                          free-text    FREE TEXT: substitute with free-text-v1
  .env.depsHostHost                                            identifier 
  .env.NIX_CC                                                  path       
  .env.AR                                                      identifier 
  .env.READELF                                                 identifier 
  .env.depsTargetTarget                                        identifier 
  .env.dontAddDisableDepTrack                                  identifier 
  .env.buildPhase                                              free-text    FREE TEXT: substitute with free-text-v1
  .env._PYTHON_SYSCONFIGDATA_NAME                              identifier 
  .env.NIX_STORE                                               path       
  .env.SIZE                                                    identifier 
  .env.OBJCOPY                                                 identifier 
  .env.buildInputs                                             identifier 
  .env.propagatedBuildInputs                                   identifier 
  .env.NIX_ENFORCE_NO_NATIVE                                   identifier 
  .env.NIX_CC_WRAPPER_TARGET_HOST_x86_64_unknown_linux_gnu     identifier 
  .env.AS                                                      identifier 
  .env.configureFlags                                          identifier 
  .env.PYTHONPATH                                              path       
  .env.HOST_PATH                                               path       
  .env.CXX                                                     identifier 
  .env.cmakeFlags                                              identifier 
  .env.strictDeps                                              identifier 
  .env.builder                                                 path       
  .env.PYTHONHASHSEED                                          identifier 
  .env.NIX_BINTOOLS                                            path       
  .env.propagatedNativeBuildInputs                             identifier 
  .env.NIX_BINTOOLS_WRAPPER_TARGET_HOST_x86_64_unknown_linux_gnu identifier 
  .env.shell                                                   path       
  .env.phases                                                  identifier 
  .env.RANLIB                                                  identifier 
  .env.__structuredAttrs                                       identifier 
  .env.DIRENV_DIFF                                             free-text    FREE TEXT: substitute with free-text-v1
  .env.depsHostHostPropagated                                  identifier 
  .env.OBJDUMP                                                 identifier 
  .env.STRIP                                                   identifier 
  .env.nativeBuildInputs                                       path       
  .env.IN_NIX_SHELL                                            identifier 
  .env.mesonFlags                                              identifier 
  .env.depsBuildTargetPropagated                               identifier 
  .env.NM                                                      identifier 
  .env.shellHook                                               identifier 
  .env.NIX_HARDENING_ENABLE                                    free-text    FREE TEXT: substitute with free-text-v1
  .env.CC                                                      identifier 
  .env.NIX_LDFLAGS                                             free-text    FREE TEXT: substitute with free-text-v1
  .env.DIRENV_FILE                                             path       
  .env.doCheck                                                 identifier 
  .env.SOURCE_DATE_EPOCH                                       identifier 
  .env.DETERMINISTIC_BUILD                                     identifier 
  .env.depsBuildBuild                                          identifier 
  .env.outputs                                                 identifier 
  .env.NIX_BUILD_CORES                                         identifier 
  .env.patches                                                 identifier 
  .env.system                                                  identifier 
  .env.depsTargetTargetPropagated                              identifier 
  .env.NIX_CFLAGS_COMPILE                                      free-text    FREE TEXT: substitute with free-text-v1
  .env.name                                                    identifier 
  .env.DIRENV_DIR                                              identifier 
  .env.preferLocalBuild                                        identifier 
  .env.PYTHONNOUSERSITE                                        identifier 
  .env._PYTHON_HOST_PLATFORM                                   identifier 
  .env.depsBuildTarget                                         identifier 
  .env.out                                                     path       
  .env.stdenv                                                  path       
  .env.STRINGS                                                 identifier 
  .env.doInstallCheck                                          identifier 
  .env.depsBuildBuildPropagated                                identifier 
  .env.CONFIG_SHELL                                            path       
  .env._                                                       path       
  .env.OPENCODE_TERMINAL                                       identifier 
  .env.AGENT                                                   identifier 
  .env.OPENCODE                                                identifier 
  .env.AI_AGENT                                                identifier 
  .env.OPENCODE_SESSION_ID                                     identifier 
  .env.CGO_ENABLED                                             identifier 
  .env.PASTURE_BIN                                             path       
  .env.PASTURE_DB_PATH                                         path       
  .env.PASTURE_CAPTURE_DIR                                     path       
  .env.npm_config_user_agent                                   free-text    FREE TEXT: substitute with free-text-v1
  REFUSED environment-dump: .env (139 bytes); this payload cannot be committed
  UNCLEARABLE: field .env is refused as environment-dump (139 bytes)
opencode_tool_execute_after_2_0_20.1.json
  .tool                                                        identifier 
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .messageID                                                   identifier 
  .id                                                          identifier 
  .input.path                                                  identifier 
  .input.content                                               free-text    FREE TEXT: substitute with free-text-v1
  .status                                                      identifier 
  .result.output.operation                                     identifier 
  .result.output.target                                        path       
  .result.output.resource                                      identifier 
  .result.output.existed                                       bool       
  .result.content[0].type                                      identifier 
  .result.content[0].text                                      free-text    FREE TEXT: substitute with free-text-v1
opencode_tool_execute_after_2_0_20.2.json
  .tool                                                        identifier 
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .messageID                                                   identifier 
  .id                                                          identifier 
  .input.command                                               free-text    FREE TEXT: substitute with free-text-v1
  .status                                                      identifier 
  .result.output.exit                                          number     
  .result.output.truncated                                     bool       
  .result.output.output                                        free-text    FREE TEXT: substitute with free-text-v1
  .result.output.status                                        identifier 
  .result.content[0].type                                      identifier 
  .result.content[0].text                                      free-text    FREE TEXT: substitute with free-text-v1
  .result.metadata.status                                      identifier 
  .result.metadata.truncated                                   bool       
  .result.metadata.exit                                        number     
opencode_tool_execute_before_2_0_20.1.json
  .tool                                                        identifier 
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .messageID                                                   identifier 
  .id                                                          identifier 
  .input.command                                               free-text    FREE TEXT: substitute with free-text-v1
opencode_tool_execute_before_2_0_20.2.json
  .tool                                                        identifier 
  .sessionID                                                   identifier 
  .agent                                                       identifier 
  .messageID                                                   identifier 
  .id                                                          identifier 
  .input.path                                                  identifier 
  .input.content                                               free-text    FREE TEXT: substitute with free-text-v1
20 payloads inventoried in ~/.local/share/pasture-captures/opencode-v2
```

### Third batch — OpenCode 2.0.21

Output of the inventory report (`PASTURE_INVENTORY_DIR` over the capture
directory, all three payloads). No refused class and no unclearable reason is
named. The chosen session.created payload carries no free text; its two path
fields carry the home directory. The two prompt payloads flag `.prompt.text`
as free text; they are not selected.

```
=== RUN   TestFixtureInventoryReport
opencode_session_created_2_0_21.1.json
  .id                                                          identifier 
  .created                                                     number     
  .type                                                        identifier 
  .durable.aggregateID                                         identifier 
  .durable.seq                                                 number     
  .durable.version                                             number     
  .location.directory                                          path       
  .data.sessionID                                              identifier 
  .data.projectID                                              identifier 
  .data.location.directory                                     path       
  .data.subpath                                                identifier 
  .data.slug                                                   identifier 
  .data.model.id                                               identifier 
  .data.model.providerID                                       identifier 
  .data.version                                                identifier 
opencode_session_prompt_2_0_21.1.json
  .sessionID                                                   identifier 
  .messageID                                                   identifier 
  .prompt.text                                                 free-text    FREE TEXT: substitute with free-text-v1
  .delivery                                                    identifier 
opencode_session_prompt_2_0_21.2.json
  .sessionID                                                   identifier 
  .messageID                                                   identifier 
  .prompt.text                                                 free-text    FREE TEXT: substitute with free-text-v1
  .delivery                                                    identifier 
3 payloads inventoried in ~/.local/share/pasture-captures/opencode-v2-sessioncreated
--- PASS: TestFixtureInventoryReport (0.00s)
```

The same report re-run over the committed fixture bytes names the same fifteen
fields with the same classes and no refused class.

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

### Second batch — OpenCode 2.0.20

`home-path-v1` first, in all three spellings, then `free-text-v1` over every
field the inventory flagged. Structure, keys, types and nulls are unchanged:
the committed bytes were compared field by field with the raw bytes and the
two carry the same paths, the same value types, the same array lengths and the
same nulls. Every digest below was recomputed over the committed bytes. Rules
applied per fixture, in order:

- `opencode_session_prompt_2_0_20.1.json`: free-text-v1 (.prompt.text); no home path occurs in it
- `opencode_session_context_2_0_20.1.json`: home-path-v1 (two absolute `/home/user` paths inside .system[2].text free text), free-text-v1 (57 paths: .system[0].text, .system[1].text, .system[2].text, .system[3].text, .messages[0].content[0].text, plus 52 tool description fields under .tools.*)
- `opencode_session_title_2_0_20.1.json`: free-text-v1 (.system[0].text, .messages[0].content[0].text); no home path occurs in it
- `opencode_session_model_request_2_0_20.2.json`: none (identifiers only); no home path, no free text
- `opencode_session_http_request_2_0_20.2.json`: none (identifiers only); no home path, no free text
- `opencode_session_http_response_2_0_20.1.json`: none (identifiers only); no home path, no free text
- `opencode_tool_execute_before_2_0_20.1.json`: free-text-v1 (.input.command); no home path occurs in it
- `opencode_tool_execute_after_2_0_20.1.json`: home-path-v1 (one absolute `/home/user` path in .result.output.target), free-text-v1 (.input.content, .result.content[0].text)
- `opencode_permission_evaluate_2_0_20.2.json`: free-text-v1 (.resources[0]); no home path occurs in it

Two of the three spellings of the home directory occur in this batch, and
both were rewritten where they occur: the absolute path and (inside free text)
the same absolute path. The relative spelling `home/user/` occurs in none of
the nine chosen payloads, checked by search, and the directory slug
`-home-user-` occurs in none of them, checked by search; the rule covers both
wherever they do occur. After both rules the committed bytes carry no
occurrence of the capturing user's name in any spelling, which the corpus
guard asserts over every file of this directory, this record included.

### Third batch — OpenCode 2.0.21

- `opencode_session_created_2_0_21.1.json`: home-path-v1 (two absolute paths, `.location.directory` and `.data.location.directory`, rewritten to `/home/user/...`); free-text-v1 not applied because the inventory flagged no free text in it.

The relative spelling and the directory slug occur nowhere in the payload,
checked by search. Keys, nesting, types and nulls are unchanged: the committed
bytes were compared field by field with the raw bytes (same paths, same value
types). The committed bytes carry no occurrence of the capturing user's name.
The provenance sidecar lists `home-path-v1`.

## Secret scan

`TestNoCommittedTestdataCarriesASecretShape` (internal/lifecycle/ingress/secretscan_test.go)
run over the whole module with these fixtures and their sidecars in place:
PASS, zero hits, 2026-09-05. Reach control on the same run: an Anthropic
API-key shape planted into a copy of one new fixture in this directory turned
the scan RED naming that file and the byte offset; the copy was discarded.
`TestSecretScanIsRedOnEachPlantedShape` (all nine shapes): PASS.

### Second batch — OpenCode 2.0.20

`TestNoCommittedTestdataCarriesASecretShape`
(internal/lifecycle/ingress/secretscan_test.go) run over the whole module with
these nine fixtures and their sidecars in place: PASS, zero hits, 2026-10-01.
`TestSecretScanIsRedOnEachPlantedShape`, the nine-shape non-vacuity control:
PASS. Reach control on the same tree, which is what proves the scan reached
these files: an Anthropic API-key shape was planted into a COPY of one of
these new fixtures in this directory, the scan turned RED naming that copy,
the shape and the byte offset, and the copy was then discarded and the scan
returned to PASS.

### Third batch — OpenCode 2.0.21

`TestNoCommittedTestdataCarriesASecretShape` run over the whole module with
this fixture and its sidecar in place: PASS, zero hits, 2026-10-02.
`TestSecretScanIsRedOnEachPlantedShape`: PASS. Reach control on the same tree:
an Anthropic API-key shape planted into a COPY of the new fixture in this
directory turned the scan RED naming that copy, the shape and byte offset 502;
the copy was removed and the scan returned to PASS.

## Refused classes

No fixture carries a tool response (tool.execute.before precedes the tool),
so none is above 4096 bytes. No payload is an environment dump. Both free-text
fields were substituted. The model id, provider id, session slug and project
id are identifiers and are kept. No payload was unclearable.

### Second batch — OpenCode 2.0.20

Refused payload classes are never committed whatever the substitution: a tool
response above 4096 bytes (raw file contents), an environment dump, and any
free-text field on a prompt or message event that was not substituted by the
rules above. The corpus test refuses the first two by shape.

- The 42/44 KB session.context payloads are CLEARABLE. Neither carries a
  refused class: the inventory names no tool-response-over-limit and no
  environment-dump on either file, and `Unclearable` names no reason. Their
  size is many small free-text fields (57 on the chosen .1, more on .2:
  system prompts plus the full tool description dump), no single
  tool-response value above 4096 bytes. The largest whole payload chosen is
  the context at 42837 committed bytes, far below the 1 MiB capture bound.
  Every free-text field on both was substituted by free-text-v1. The smaller
  .1 is the fixture; the larger .2 (44374 bytes raw) is recorded above as a
  size not chosen.
- The 29 KB shell.create.before payload is UNCLEARABLE and stays withheld.
  The inventory names `REFUSED environment-dump: .env (139 members)` and
  `UNCLEARABLE: field .env is refused as environment-dump`. It is an
  environment dump whatever the substitution, so no fixture is committed for
  shell.create.before in this slice.
- The 1079-byte tool.execute.after .2 (shell `ls -la` output) is clearable
  (free-text substituted) but larger than the chosen 497-byte write .1, so it
  is recorded as a size not chosen, not as unclearable.
- No other chosen payload carries a refused class, and the same report over
  the committed bytes names none either. The seven coordinates that did not
  fire get no fixture and stay withheld for missing-fixture, not for
  clearance reasons.

### Third batch — OpenCode 2.0.21

The chosen payload carries no refused class: no tool response, no
environment dump, no free text. The two unselected prompt payloads are not
refused; they are sizes not chosen because the coordinate already has a
cleared fixture.

## Fixtures

- `session_created_1_18_29.json` — session.created — sha256:71c8de3aadd8019b7e4123076625a0be6e3faaadd56a23c2a79c28a58f7ab591 (654 bytes)
- `tool_execute_before_1_18_29.json` — tool.execute.before — sha256:4ac8bef2356d19aa2972e61d1f6e50fe1bf3a3ebf187382f6591a5630d548053 (150 bytes)

### Second batch — OpenCode 2.0.20, nine fixtures — accepted 2026-10-01

These nine fixtures are cleared authentic captures of the ten coordinates
that fired. Committing them activates nothing: no 2.0.20 row of the activation
table names them yet, and nothing enables an event because a fixture exists.
The shell coordinate stays withheld as unclearable; the seven coordinates
that did not fire stay withheld for missing-fixture.

- `opencode_session_prompt_2_0_20.1.json` — session.prompt — sha256:f3c62d3564e443606d8ae537c04053e39661de4e4c50e29f0ebcd4ff5f92f2a8 (246 bytes)
- `opencode_session_context_2_0_20.1.json` — session.context — sha256:6a79e867dcf6a0575646d162c7438e1f470b81b3ad5ff67f70c9d28a1126148d (42837 bytes)
- `opencode_session_title_2_0_20.1.json` — session.title — sha256:f0df594d2e4fc506d55934d84f9288c821dc2a87ad7d4cd85c2d74f447a0b4c0 (2562 bytes)
- `opencode_session_model_request_2_0_20.2.json` — session.model.request — sha256:cea5dc5168c0cbe36654a9d3621e62d7ea08d6153a000a1e9c1d77b6af86280b (504 bytes)
- `opencode_session_http_request_2_0_20.2.json` — session.http.request — sha256:62bae0127b62eba1e6f85aa0676e745bd308e90daefed74f9c5f22258867581b (162 bytes)
- `opencode_session_http_response_2_0_20.1.json` — session.http.response — sha256:f0d8e7ac5bafed0dcfa624aa5e7f7bc12134776d4d4b50f9f80936e7b73d380f (176 bytes)
- `opencode_tool_execute_before_2_0_20.1.json` — tool.execute.before — sha256:ccfc79264f6f5814d3d98442f4a3d0d26da251b85585becb8c5d14c09de6c5d5 (191 bytes)
- `opencode_tool_execute_after_2_0_20.1.json` — tool.execute.after — sha256:7aed62a209ddeaedca5ea8e24a25103cdf99fc19887f1d06cd83b6bab9a6095d (497 bytes)
- `opencode_permission_evaluate_2_0_20.2.json` — permission.evaluate — sha256:94e2525cc2b0446d07bc0f72996defd0cc56471159b0a06b73aa0f618c426729 (229 bytes)

Withheld in this batch: `shell.create.before` (unclearable environment dump,
29339 bytes raw, see refused classes) and the seven coordinates that did not
fire (session.created, session.compaction, session.generate, session.retry,
session.experimental.ws.handshake, session.experimental.ws.send,
session.experimental.ws.receive).

### Third batch — OpenCode 2.0.21, one fixture — accepted 2026-10-02

- `opencode_session_created_2_0_21.1.json` — session.created — sha256:7eda755b4bb9d82a44e057742f0ede1d8ff90280e607ccf900cefe9776f5cc5c (600 bytes)

Sizes not chosen: `opencode_session_prompt_2_0_21.1.json` (156 bytes raw) and
`opencode_session_prompt_2_0_21.2.json` (162 bytes raw). This fixture enables
no activation row; the recorded-version invariant amendment and the row
enablement are a separate change after the user's acceptance.

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

### Second batch — OpenCode 2.0.20 — accepted 2026-10-01

Accepted by the user on 2026-10-01, for the nine cleared fixtures above, after
the clearance evidence was presented (fixture list, rules applied, secret-scan
result, sizes not chosen, and the withheld coordinates). The user was asked
for their acceptance wording, verbatim, and answered, verbatim:

```
ACCEPT
```

The seven coordinates that did not fire and the unclearable shell dump stay
withheld; this acceptance enables no row by itself, and a later change enables
a row from recorded proof.

### Third batch — OpenCode 2.0.21 — accepted 2026-10-02

Accepted by the user on 2026-10-02, for the one cleared fixture above, after
the clearance evidence was presented (fixture digest, rules applied, scan
result, sizes not chosen). The user was asked for their acceptance wording,
verbatim, and answered, verbatim:

```
ACCEPT
```

The recorded-version invariant amendment and the row enablement are a separate
change; this acceptance itself enables no row.

## Pull request

Appended by the integrator in the landing commit: the pull request URL.

### Second batch — OpenCode 2.0.20

Landing pull request: https://github.com/dayvidpham/pasture/pull/156 — opened
after the acceptance above was recorded; the accepted captures first reach a
remote through it.

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

## Permission-enforcement transport addendum — 2026-10-01

The generated 2.0.20 plugin is wired to refuse a pasture Denial through the
host's typed permission channel (the effect/message mutation that evaluateInput
in packages/core/src/permission.ts returns to its caller): on a
`{"decision":"deny","reason":<text>}` answer
the `permission.evaluate` helper assigns the host evaluation's `effect` to
`"deny"` and its `message` to the durable reason verbatim, and on any other
answer it leaves the host evaluation untouched. Every other helper keeps its
report-and-continue discipline. The committed
`.opencode/plugins/pasture-lifecycle.ts` is now sha256
`d6debd1110bdf3e1c28eed27b38af7ffac7275165080bce6d932dd30bbdb7cca`.
Their source remains `internal/codegen/opencode_hooks.go`. This is a transport
identity, not a new capture-kit or fixture identity. A reader who hashes the
shipped plugin gets the digest above.

This addendum records no new live capture, fixture acceptance or host
capability. No 2.0.20 capture has been cleared into this directory, so every
2.0.20 row stays withheld for missing-fixture and every OpenCode row still
derives response capability None: the shipped binary keeps downgrading
evaluated denials to proceed with the unenforced reason until the committed
2.0.20 capture sitting (with inventory, substitution, secret scan and
clearance) supplies the response-channel evidence. The wired refusal above
fires only where the gate answers deny, which that evidence has not yet
enabled. All historical fixture bytes, sidecars, pairing and acceptance
records above remain unchanged.

## Host-version carriage transport addendum — 2026-10-01

The generated 2.0.20 plugin now carries the running host's own release on
every lifecycle invocation: setup captures `ctx.app.version` once and each
spawn appends it as `--host-version` unless that invocation already names its
own (the `session.created` observation keeps its occurrence-local bus-data
version where present). When the context reports no usable version nothing is
sent and the binary resolves the version itself, so no version is ever
invented. Only a release-shaped report is captured: a source build reports
"local" and unconfigured metadata reports "unknown", neither of which is a
release, so neither is forwarded and the probe resolves instead. The
`session.created` bus version is held to the same shape. The binary's
version probe accepts the real v2 banner alongside the
bare release (`opencode v2.0.20` as well as `2.0.20`); anything else is still
refused. The committed `.opencode/plugins/pasture-lifecycle.ts` is now sha256
`3df44c5bafc464a76ea0177119ede4bf57a94e9e9280042ad269af6ed64450f8`.
Its source remains `internal/codegen/opencode_hooks.go`; the probe change
lives in `cmd/pasture/hook_lifecycle_host_version.go`. This is a transport
identity,
not a new capture-kit or fixture identity. A reader who hashes the shipped
plugin gets the digest above.

This addendum records no new live capture, fixture acceptance or host
capability. No 2.0.20 capture has been cleared into this directory, so every
2.0.20 row stays withheld for missing-fixture. All historical fixture bytes,
sidecars, pairing and acceptance records above remain unchanged.

## No-runtime-import transport addendum — 2026-10-01

The generated 2.0.20 plugin no longer performs any runtime import: it
default-exports the plain definition object directly instead of calling a
plugin-package helper, so the real host loads it with nothing to resolve. The
registered hook set, ids and behaviour are unchanged. The committed
`.opencode/plugins/pasture-lifecycle.ts` is now sha256
`65d58932be728b76f1bd2b3eb812de56089469d3650f70d0c9fd86e84f388fc2`.
Its source remains `internal/codegen/opencode_hooks.go`. This is a transport
identity, not a new capture-kit or fixture identity. A reader who hashes the
shipped plugin gets the digest above.

This addendum records no new live capture, fixture acceptance or host
capability. No 2.0.20 capture has been cleared into this directory, so every
2.0.20 row stays withheld for missing-fixture. All historical fixture bytes,
sidecars, pairing and acceptance records above remain unchanged.
