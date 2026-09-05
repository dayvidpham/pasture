# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

## Harness and pinned version

Claude Code 2.1.261, verified with `claude --version` immediately before the
session on 2026-09-05: `2.1.261 (Claude Code)`. Admission is a floor: this
version and every later release; the contract records 2.1.261.

## Capture

Captured in one live session on 2026-09-05, into
`~/.local/share/pasture-captures/claude`, with `PASTURE_CAPTURE_DIR`
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

Claude Code transport: `hooks/hooks.json` from the kit, sha256 `c361d66f810d6d26b0fb8e3f1faa6f63dee306da5b4a854912279a4d84b8418a`
(byte-identical to the committed file; the host version reaches the hook
through `CLAUDE_CODE_VERSION`, exported from `claude --version`). Project
directory `~/.local/share/pasture-captures/project-claude`
holding README.md and NOTES.md from the kit; the folder was trusted at the
session's prompt.

Fixture selection: where an event fired more than once, the `.1` file (the
first occurrence in the session) is the fixture; the reason is that the first
occurrence is the one the trigger above produced directly. `session_start .1`
is the launch; `.2` is the resume the compact produced. One line per chosen
event: event, file, trigger, time, raw digest, committed digest.
- SessionStart: `claude-code_session_start_2_1_261.1.json` — trigger: launch of the session (source startup) — 2026-09-05T12:18:49Z — raw sha256:d11f08b68758cf362ac5bdff6757bbf1ac13630e0a36350f2441da01eb7bf87b (496 bytes) — committed sha256:36a997c6ca7aa99a6afcea563b1f9bde361410ce2181f8e29b03b53ec245b79d (484 bytes)
- PreToolUse: `claude-code_pre_tool_use_2_1_261.1.json` — trigger: prompt "Read the file README.md in this directory and tell me its first line."; the host satisfied it with a Bash tool call (head), so the payload carries tool_name Bash — 2026-09-05T12:20:53Z — raw sha256:80df576dde1643ab13e59bbcfb96dea557f1867af577294f277458d54b003ab1 (773 bytes) — committed sha256:12aa285113d5df968f6005151feee52e7d7b4bcc70327cef7fcc63a42822db94 (758 bytes)
- PostToolUse: `claude-code_post_tool_use_2_1_261.1.json` — trigger: same prompt, the Bash call returning — 2026-09-05T12:20:53Z — raw sha256:4af6583f1d01dd357acbd0204b601f6da71385cea4529d14ba71c59e1bb30ae5 (945 bytes) — committed sha256:6e11c48e34132e8fb89d4756d832fe331c58d95bff608c617795e35b0800009f (930 bytes)
- PostToolBatch: `claude-code_post_tool_batch_2_1_261.1.json` — trigger: same prompt, the batch of one Bash call completing — 2026-09-05T12:20:53Z — raw sha256:1a7eda7183784d07e53c430140d7da6a39b7116459a8807c8fc8b22b11672997 (862 bytes) — committed sha256:0b99d39dbb55c9e67624036063b12506700b39a52bfaa9f8cc3dee8d6efc2f7f (847 bytes)
- PostToolUseFailure: `claude-code_post_tool_use_failure_2_1_261.1.json` — trigger: prompt "Read the file does-not-exist.txt in this directory."; the Bash call (cat) failed with exit 1 — 2026-09-05T12:21:18Z — raw sha256:09c6c98d66c09fc9827283e5c06bb17b328b4ad28011b8a510680fcb8e80f2af (962 bytes) — committed sha256:503db810df1d91cde24cad6e4589dd6558d6c84296cf996b7ba93456719bee36 (944 bytes)
- PreCompact: `claude-code_pre_compact_2_1_261.1.json` — trigger: /compact (trigger manual) — 2026-09-05T12:21:37Z — raw sha256:90698e49a1b8fa269c0bfca1175b381e7032b83987dec7b428b9bd617dd97475 (545 bytes) — committed sha256:4f9c4cf072d7380cd19a1bd312a6ac2af6a8da8621490634e71472c275b8a0ae (533 bytes)
- PostCompact: `claude-code_post_compact_2_1_261.1.json` — trigger: /compact completing — 2026-09-05T12:22:12Z — raw sha256:c73e625995599be5c66376893f5274d46850032e733ccfff61cad7b4b1cafcf9 (7734 bytes) — committed sha256:eb1c3342e5c025532d462892170548f1b7389e6c6e899fd30b2568eb0d7c90c6 (7686 bytes)
- SessionEnd: `claude-code_session_end_2_1_261.1.json` — trigger: /exit (reason prompt_input_exit) — 2026-09-05T12:22:29Z — raw sha256:4100e19fcfa826bdd522e0bef7d26fdb5c8cc3fa19a8340cadec382b04e76f2e (528 bytes) — committed sha256:6fa41ebf242de64078de64bf28a500b2c42b8721281afdb481511818e436475c (516 bytes)

Captured and not used (not committed; listed so the record is complete):
- `claude-code_post_tool_batch_2_1_261.2.json` — raw sha256:3ea89de7b2f620f57b9b0d55b72a13e61d966596c90e3a5db4a4a277ed1d67ea (944 bytes)
- `claude-code_post_tool_batch_2_1_261.3.json` — raw sha256:15c126b0102057179442324912210a02a5ccb6c687899a2f5c00737739fe1279 (1022 bytes)
- `claude-code_post_tool_use_2_1_261.2.json` — raw sha256:58c83b2e7e18c8087b6618658d82995c8a1d4211af4d2dcb691f3fdbaa6c0042 (1104 bytes)
- `claude-code_pre_tool_use_2_1_261.2.json` — raw sha256:712f37935b38eeaf7fadd13c87920722079c71ecb603fca4098b56282fdeab1d (783 bytes)
- `claude-code_pre_tool_use_2_1_261.3.json` — raw sha256:db3bd2daccab0d0de2209ac9a20c4c4e78fa2a616e4b86b54bd3528948be74c1 (763 bytes)
- `claude-code_session_start_2_1_261.2.json` — raw sha256:e8d7f523729b366aa24691ddd83cf817a975aa8ae88a956d4fa0429a58939156 (547 bytes)

## Inventory

Output of the inventory report (`PASTURE_INVENTORY_DIR` over the capture
directory, all 14 payloads; the 8 chosen ones are the fixtures):

```
claude-code_post_compact_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .hook_event_name                                             identifier 
  .trigger                                                     identifier 
  .compact_summary                                             free-text    FREE TEXT: substitute with free-text-v1
claude-code_post_tool_batch_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_calls[0].tool_name                                     identifier 
  .tool_calls[0].tool_input.command                            free-text    FREE TEXT: substitute with free-text-v1
  .tool_calls[0].tool_input.description                        free-text    FREE TEXT: substitute with free-text-v1
  .tool_calls[0].tool_use_id                                   identifier 
  .tool_calls[0].tool_response                                 free-text    FREE TEXT: substitute with free-text-v1
claude-code_post_tool_batch_2_1_261.2.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_calls[0].tool_name                                     identifier 
  .tool_calls[0].tool_input.command                            free-text    FREE TEXT: substitute with free-text-v1
  .tool_calls[0].tool_input.description                        free-text    FREE TEXT: substitute with free-text-v1
  .tool_calls[0].tool_use_id                                   identifier 
  .tool_calls[0].tool_response                                 free-text    FREE TEXT: substitute with free-text-v1
claude-code_post_tool_batch_2_1_261.3.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_calls[0].tool_name                                     identifier 
  .tool_calls[0].tool_input.command                            free-text    FREE TEXT: substitute with free-text-v1
  .tool_calls[0].tool_input.description                        free-text    FREE TEXT: substitute with free-text-v1
  .tool_calls[0].tool_use_id                                   identifier 
  .tool_calls[0].tool_response                                 free-text    FREE TEXT: substitute with free-text-v1
claude-code_post_tool_use_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tool_response.stdout                                        free-text    FREE TEXT: substitute with free-text-v1
  .tool_response.stderr                                        identifier 
  .tool_response.interrupted                                   bool       
  .tool_response.isImage                                       bool       
  .tool_response.noOutputExpected                              bool       
  .tool_use_id                                                 identifier 
  .duration_ms                                                 number     
claude-code_post_tool_use_2_1_261.2.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tool_response.stdout                                        free-text    FREE TEXT: substitute with free-text-v1
  .tool_response.stderr                                        identifier 
  .tool_response.interrupted                                   bool       
  .tool_response.isImage                                       bool       
  .tool_response.noOutputExpected                              bool       
  .tool_use_id                                                 identifier 
  .duration_ms                                                 number     
claude-code_post_tool_use_failure_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier 
  .error                                                       free-text    FREE TEXT: substitute with free-text-v1
  .is_interrupt                                                bool       
  .duration_ms                                                 number     
claude-code_pre_compact_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .hook_event_name                                             identifier 
  .trigger                                                     identifier 
  .custom_instructions                                         null       
claude-code_pre_tool_use_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier 
claude-code_pre_tool_use_2_1_261.2.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier 
claude-code_pre_tool_use_2_1_261.3.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .permission_mode                                             identifier 
  .effort.level                                                identifier 
  .hook_event_name                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier 
claude-code_session_end_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .hook_event_name                                             identifier 
  .reason                                                      identifier 
claude-code_session_start_2_1_261.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .hook_event_name                                             identifier 
  .source                                                      identifier 
  .model                                                       identifier 
claude-code_session_start_2_1_261.2.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .scratchpad_dir                                              path       
  .prompt_id                                                   identifier 
  .hook_event_name                                             identifier 
  .source                                                      identifier 
  .model                                                       identifier 
14 payloads inventoried in ~/.local/share/pasture-captures/claude
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

- session_start_2_1_261.json: home-path-v1
- pre_tool_use_2_1_261.json: home-path-v1, free-text-v1 (.tool_input.command, .tool_input.description)
- post_tool_use_2_1_261.json: home-path-v1, free-text-v1 (.tool_input.command, .tool_input.description, .tool_response.stdout)
- post_tool_batch_2_1_261.json: home-path-v1, free-text-v1 (.tool_calls[0].tool_input.command, .tool_calls[0].tool_input.description, .tool_calls[0].tool_response)
- post_tool_use_failure_2_1_261.json: home-path-v1, free-text-v1 (.tool_input.command, .tool_input.description, .error)
- pre_compact_2_1_261.json: home-path-v1
- post_compact_2_1_261.json: home-path-v1, free-text-v1 (.compact_summary)
- session_end_2_1_261.json: home-path-v1
- the three controls below carry the session_start bytes and its rules.

## Secret scan

`TestNoCommittedTestdataCarriesASecretShape` (internal/lifecycle/ingress/secretscan_test.go)
run over the whole module with these fixtures and their sidecars in place:
PASS, zero hits, 2026-09-05. Reach control on the same run: an Anthropic
API-key shape planted into a copy of one new fixture in this directory turned
the scan RED naming that file and the byte offset; the copy was discarded.
`TestSecretScanIsRedOnEachPlantedShape` (all nine shapes): PASS.

## Refused classes

No fixture carries a tool response above 4096 bytes: the largest tool
response is 52 bytes (post_tool_batch) and 50 bytes (post_tool_use stdout).
The 7734-byte post_compact payload carries `compact_summary`, the host's own
summary of the session (7085 bytes of free text, not a tool response); it was
substituted by free-text-v1 and is committed as placeholder text of the same
length. No payload is an environment dump. Every free-text field on every
event was substituted. No payload was unclearable; no event is left withheld
for clearance reasons.

## Fixtures

- `post_compact_2_1_261.json` — PostCompact — sha256:eb1c3342e5c025532d462892170548f1b7389e6c6e899fd30b2568eb0d7c90c6 (7686 bytes)
- `post_tool_batch_2_1_261.json` — PostToolBatch — sha256:0b99d39dbb55c9e67624036063b12506700b39a52bfaa9f8cc3dee8d6efc2f7f (847 bytes)
- `post_tool_use_2_1_261.json` — PostToolUse — sha256:6e11c48e34132e8fb89d4756d832fe331c58d95bff608c617795e35b0800009f (930 bytes)
- `post_tool_use_failure_2_1_261.json` — PostToolUseFailure — sha256:503db810df1d91cde24cad6e4589dd6558d6c84296cf996b7ba93456719bee36 (944 bytes)
- `pre_compact_2_1_261.json` — PreCompact — sha256:4f9c4cf072d7380cd19a1bd312a6ac2af6a8da8621490634e71472c275b8a0ae (533 bytes)
- `pre_tool_use_2_1_261.json` — PreToolUse — sha256:12aa285113d5df968f6005151feee52e7d7b4bcc70327cef7fcc63a42822db94 (758 bytes)
- `session_end_2_1_261.json` — SessionEnd — sha256:6fa41ebf242de64078de64bf28a500b2c42b8721281afdb481511818e436475c (516 bytes)
- `session_start_2_1_261.json` — SessionStart — sha256:36a997c6ca7aa99a6afcea563b1f9bde361410ce2181f8e29b03b53ec245b79d (484 bytes)

Controls, derived from the committed session_start bytes (same body, one
sidecar field changed each): `session_start_2_1_261_digest_mismatch.json`
(rawFileDigest all zeros), `session_start_2_1_261_origin_authored.json`
(origin `authored`), `session_start_2_1_261_version_out_of_range.json`
(harnessVersion 2.1.260, one patch below the floor). Body sha256:36a997c6ca7aa99a6afcea563b1f9bde361410ce2181f8e29b03b53ec245b79d.

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
