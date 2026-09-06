# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

## Harness and pinned version

Claude Code 2.1.261, verified with `claude --version` immediately before the
session on 2026-09-05: `2.1.261 (Claude Code)`. Admission is a floor: this
version and every later release; the contract records 2.1.261.

A second batch of fourteen captures was taken on 2026-09-05 from the same host
release. `claude --version` printed `2.1.261 (Claude Code)` immediately before
the sittings and again immediately after them, so the host did not move while
the bytes were produced. The pinned version and the floor above are unchanged
by this batch.

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

### The second batch, fourteen events, 2026-09-05

These fourteen captures cover events the product does NOT enable today. They
are cleared and committed here as evidence; the events stay unactivated, and
the work that enables a row is separate from this record.

Captured in two live sittings on 2026-09-05, into
`~/.local/share/pasture-captures/s4/probe`, with `PASTURE_CAPTURE_DIR` set. The
fourteen chosen files were copied, never moved, into
`~/.local/share/pasture-captures/s4/batch`; the originals stay outside the
repository. Paths in this record are spelled with `~` for the capturing user's
home directory; the fixture bytes themselves carry the `/home/user` placeholder
that `home-path-v1` writes. The sessions were interactive terminal sessions of
the real host binary on the user's machine, run at the user's direction by the
team's operators; the user did not type the prompts. Nothing captured reached
any remote before the user's acceptance recorded below.

Build kit: one `pasture` binary, sha256
`c2c2ad98940f312f94b8ee88052c84e07da9ab0c5c7d54bf339e31a835af46d6`, built with
`CGO_ENABLED=0` from an archive copy of head
`0b33f0760602fb7522a18afe73a5ae91011c090b` in
`~/.local/share/pasture-captures/s4/kit/src`. The copy carries ONE deliberate
edit, and it never reaches this repository: every registered Claude row was
added to `internal/lifecycle/activation/claude_targets.go` with placeholder
proof strings that name themselves as kit placeholders, and `make generate` was
run there. The edit exists because the generator writes a transport row only
for an enabled event, so a host cannot fire a hook for an event the shipped
transport does not carry. The kit's generated `hooks/hooks.json` then holds 33
event keys, sha256
`f7ab20b86ad928c1cb7e3668eeed96304f4cee1d5d27d5810fb73c2cbb6d0a73`, and the
kit's `hooks/pasture-activation.json` is sha256
`b7911f6f59612558504d3de459389bb0c799411b0579710c59c5a009b86dc8bc`. Nothing was
hand-shaped: the generator refused no row. All four kit digests were verified
read-only again before the clearing work and they still match.

Transport mechanism, measured and not assumed: a project-level
`.claude/settings.json` in the throwaway project directory
`~/.local/share/pasture-captures/s4/project`, sha256
`ae58b608b44be3765fb641cd0d177d77702888b818d846eeaaae3970d83a927d`, holding the
kit's hooks object with 33 event keys. Three rows of the kit file were dropped
from it because they name a plugin-root variable that does not resolve outside
a plugin. The installed plugin was NOT edited. Environment in the host's shell:
`PASTURE_BIN` = the kit binary, `PASTURE_CAPTURE_DIR` = the capture directory
above, `PASTURE_DB_PATH` = a scratch database file under
`~/.local/share/pasture-captures/s4/scratch` (the live store was not touched),
and `CLAUDE_CODE_VERSION` exported from `claude --version`, without which every
captured file is named `unknown`.

One line per chosen event: event, file, trigger, capture time, raw digest with
its byte size, and committed digest with its byte size. The difference between
the two digests is what the substitution changed, and the byte sizes show how
much.
- InstructionsLoaded: `instructions_loaded_2_1_261.json` — trigger: the launch of the session itself, with no prompt — 2026-09-05T21:10:22Z — raw sha256:02a8392af005a09afddf0d45df70b10b6492f89cc203468c3bbed897d035947d (541 bytes) — committed sha256:07eeb7ad0b1bcce0623ec64020d25d0e8a005f91d69bb4e6c95798abc9bb0eda (526 bytes)
- UserPromptSubmit: `user_prompt_submit_2_1_261.json` — trigger: the prompt "Reply with the single word ok." — 2026-09-05T21:10:37Z — raw sha256:546f71ffbc64ec03ed7077f58e6a6c13152b1f1083f2033dfec3785b4596825c (560 bytes) — committed sha256:1f928b9073362ebaa2c0e2bc261cb6e02cde7d158f7101d597f65dc8976426e5 (548 bytes)
- Stop: `stop_2_1_261.json` — trigger: the same turn ending — 2026-09-05T21:10:39Z — raw sha256:f06ecf93102314d7e025caf417d92619f59149f57881cb1d111853d579935f77 (628 bytes) — committed sha256:db86aac9557f32615ea1446eb5599fb3dea7df35a6e95aa9b2970749063b6d79 (616 bytes)
- MessageDisplay: `message_display_2_1_261.json` — trigger: the same turn, the answer displayed — 2026-09-05T21:10:39Z — raw sha256:ae01383e52f525c6a1e6391e7ce9a03853a51337ae4cd1dd710d9863bccffee7 (628 bytes) — committed sha256:e41da860b97f9e4e5fa26e007d64964db2ef7dfc0d6a0f6c713c3ba45abf1c46 (616 bytes)
- DirectoryAdded: `directory_added_2_1_261.json` — trigger: the command "/add-dir <a scratch directory>", answered "1. Yes, for this session" — 2026-09-05T21:11:05Z — raw sha256:336d883d15c0303d5f8473c376c16dca75f40d54d9c3557132c88a985be91e3e (585 bytes) — committed sha256:e864af13aed72eef6ad686e9cd8fa40a9c9bea8576ada65feceb273e9b08a874 (570 bytes)
- PreModelSwitch: `pre_model_switch_2_1_261.json` — trigger: the command "/model", another model highlighted, then chosen and confirmed — 2026-09-05T21:11:30Z — raw sha256:a7187569d8031035df2d6f416e5b35871e777a84e754c1e46010b65dd8850a6c (726 bytes) — committed sha256:268f7bdf78221e312e69cbfaa8de869f85aa6c816898d95c7b87e9c26dcfbc19 (714 bytes)
- PostModelSwitch: `post_model_switch_2_1_261.json` — trigger: the same model change completing — 2026-09-05T21:11:39Z — raw sha256:31d1d5d0b73c398e343f1461dc1a73067f9abebe82030bacabae8f898380b0ad (727 bytes) — committed sha256:c92a88e2d6fd90ac9a6ffcd5a96c62006b478c8ef5abed9134bb0aa5d2bd6523 (715 bytes)
- PermissionRequest: `permission_request_2_1_261.json` — trigger: the prompt "Create a file named a.txt in this directory containing the single word ok.", with the host asking for permission — 2026-09-05T21:13:01Z — raw sha256:cddd1c4e64c2d56779aad96f3a33d601742a73d6289a6c5be8ad43429bf6aef4 (738 bytes) — committed sha256:daff36bd80b769b087f340d798497d7f074a5d49360cc53f227be3f8001c9904 (723 bytes)
- Notification: `notification_2_1_261.json` — trigger: the same permission dialog — 2026-09-05T21:13:07Z — raw sha256:38aeb866fd607268f487279aa3cf6c5215867b8213a919328768357141f19e5a (570 bytes) — committed sha256:aab36db4e6ba2df02660c92ab744ac1e60159f077b8ed974c12ee9848ffd08ff (558 bytes)
- UserPromptExpansion: `user_prompt_expansion_2_1_261.json` — trigger: the command "/probe", with a probe command file in the throwaway project directory — 2026-09-05T21:17:30Z — raw sha256:19b8804b288e5f6e31a79b37245e91c43d6e8a94bb286075b8739dc694207ef5 (651 bytes) — committed sha256:dbfccc625b2f833876b4fa02a0b7a31447b6c73d3d780439e271ef80a6825ce6 (639 bytes)
- ConfigChange: `config_change_2_1_261.json` — trigger: a settings file written in the throwaway project directory from another shell while the session ran — 2026-09-05T21:17:44Z — raw sha256:c3dc244c88ecc8e5dc3623e713d43476760b992fc154a93487be2a9038a08a6d (612 bytes) — committed sha256:f28231875ec9f1485caed5f91e014d44ac740a1105b8fb9fed44b097c8e575cf (597 bytes)
- CwdChanged: `cwd_changed_2_1_261.json` — trigger: the prompt "Run the shell command: cd <a scratch directory>", approved — 2026-09-05T21:19:00Z — raw sha256:7144a86f307f00b0eaa0ed7e6fe3e95ca53971514378865eec857ea35e065d3b (621 bytes) — committed sha256:0dd92eadb211df083321ab0b64a683876f8779343dac1de6a339fb938774ea00 (603 bytes)
- SubagentStart: `subagent_start_2_1_261.json` — trigger: the prompt "Use the Agent tool to launch one general-purpose subagent whose whole task is to reply with the word ok." — 2026-09-05T21:19:17Z — raw sha256:38bbc416ff37ceaf683263515e79a8c481bc930cb94f8c81727b7b3a123dbba5 (552 bytes) — committed sha256:ba8a32004176dffc6c094b3d056b818887f0fc08e72caebc1569053367853372 (540 bytes)
- SubagentStop: `subagent_stop_2_1_261.json` — trigger: the same subagent finishing — 2026-09-05T21:19:19Z — raw sha256:97beaa03d0559d3556822c5323002ec2a3e739f30501f5006ad8cd3fd611bb70 (1004 bytes) — committed sha256:d3f0e8dfbe6f7aa472f99107f950587b60f7299d66e1f3fd093847fa731bef37 (986 bytes)

Two facts a reader will otherwise misread, both measured:

1. In the capture directory the already-enabled events arrive in exact digest
   PAIRS: two files per firing with an identical digest, while every
   not-yet-enabled event appears once per firing. This is NOT a host
   double-firing defect. The installed plugin registers exactly those same
   events and runs the same command with the same binary, so each of those
   firings is recorded twice, once by the project transport and once by the
   plugin. None of those files is in this batch: the eight already-enabled
   events keep the fixtures recorded above, because new bytes would need a new
   acceptance for no gain.
2. PreModelSwitch fires once per model the picker HIGHLIGHTS, not once per
   model change, and an automatic model change fires no PreModelSwitch at all.
   The committed pair is therefore the MATCHED picker pair: both files carry
   source `picker`, the same `from_model` and `to_model`, and the same
   `prompt_id` `b00aa439-bb17-4596-9f84-3ef106a968e4`, which is the host's own
   identifier for one operation. A picker payload paired with an automatic one
   would put a false story in this record.

Captured and not used (not committed; listed so the record is complete): the
capture directory holds 102 files over 20 event stems, and a later sitting,
described below, wrote 47 more files into a directory of its own. Everything
outside the fourteen files above is either an already-enabled event, a repeated
firing of a chosen event with a longer payload, or probe traffic. Nothing was
deleted or moved.

That later sitting looked for three more events and none of them fired:
TaskCreated, TaskCompleted and TeammateIdle. The sitting made two genuine
attempts. It created a named teammate, gave it work, and saw it reply and go
idle, both times. Ten other events fired normally in the same sitting, and the
transport carried the subscription for all three throughout, so the cause is
not ours. The three stay unenabled with that cause recorded, and they are not
in this batch.

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

### The second batch

Output of the inventory report (`PASTURE_INVENTORY_DIR` over the batch
directory, all 14 payloads, each of which is a fixture below). The report names
every refused class and every unclearable reason it finds; it named none.

```
claude-code_config_change_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .source                                                      identifier
  .file_path                                                   path
claude-code_cwd_changed_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .old_cwd                                                     path
  .new_cwd                                                     path
claude-code_directory_added_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .directory                                                   path
  .source                                                      identifier
claude-code_instructions_loaded_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .hook_event_name                                             identifier
  .file_path                                                   path
  .memory_type                                                 identifier
  .load_reason                                                 identifier
claude-code_message_display_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .turn_id                                                     identifier
  .message_id                                                  identifier
  .index                                                       number
  .final                                                       bool
  .delta                                                       identifier
claude-code_notification_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .message                                                     free-text    FREE TEXT: substitute with free-text-v1
  .notification_type                                           identifier
claude-code_permission_request_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .permission_mode                                             identifier
  .hook_event_name                                             identifier
  .tool_name                                                   identifier
  .tool_input.file_path                                        path
  .tool_input.content                                          identifier
  .permission_suggestions[0].type                              identifier
  .permission_suggestions[0].mode                              identifier
  .permission_suggestions[0].destination                       identifier
claude-code_post_model_switch_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .from_model                                                  identifier
  .to_model                                                    identifier
  .requested_model                                             identifier
  .source                                                      identifier
  .context_tokens                                              number
  .prompt_cache_warm                                           bool
  .cache_ttl                                                   identifier
  .estimated_cache_write_usd                                   number
  .pricing                                                     identifier
claude-code_pre_model_switch_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .hook_event_name                                             identifier
  .from_model                                                  identifier
  .to_model                                                    identifier
  .requested_model                                             identifier
  .source                                                      identifier
  .context_tokens                                              number
  .prompt_cache_warm                                           bool
  .cache_ttl                                                   identifier
  .estimated_cache_write_usd                                   number
  .pricing                                                     identifier
claude-code_stop_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .permission_mode                                             identifier
  .effort.level                                                identifier
  .hook_event_name                                             identifier
  .stop_hook_active                                            bool
  .last_assistant_message                                      identifier
claude-code_subagent_start_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .agent_id                                                    identifier
  .agent_type                                                  identifier
  .hook_event_name                                             identifier
claude-code_subagent_stop_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .permission_mode                                             identifier
  .agent_id                                                    identifier
  .agent_type                                                  identifier
  .hook_event_name                                             identifier
  .stop_hook_active                                            bool
  .agent_transcript_path                                       path
  .last_assistant_message                                      identifier
  .background_tasks[0].id                                      identifier
  .background_tasks[0].type                                    identifier
  .background_tasks[0].status                                  identifier
  .background_tasks[0].description                             free-text    FREE TEXT: substitute with free-text-v1
  .background_tasks[0].agent_type                              identifier
claude-code_user_prompt_expansion_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .permission_mode                                             identifier
  .hook_event_name                                             identifier
  .expansion_type                                              identifier
  .command_name                                                identifier
  .command_args                                                identifier
  .command_source                                              identifier
  .prompt                                                      path
claude-code_user_prompt_submit_2_1_261.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .scratchpad_dir                                              path
  .prompt_id                                                   identifier
  .permission_mode                                             identifier
  .hook_event_name                                             identifier
  .prompt                                                      free-text    FREE TEXT: substitute with free-text-v1
14 payloads inventoried in ~/.local/share/pasture-captures/s4/batch
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

### The second batch

`home-path-v1` first, in all three spellings, then `free-text-v1` over every
field the inventory flagged. Structure, keys, types and nulls are unchanged:
the committed bytes were compared field by field with the raw bytes and the two
carry the same paths, the same value types, the same array lengths and the same
nulls. Every digest above was recomputed over the committed bytes. Rules
applied per fixture, in order:

- config_change_2_1_261.json: home-path-v1
- cwd_changed_2_1_261.json: home-path-v1
- directory_added_2_1_261.json: home-path-v1
- instructions_loaded_2_1_261.json: home-path-v1
- message_display_2_1_261.json: home-path-v1
- notification_2_1_261.json: home-path-v1, free-text-v1 (.message)
- permission_request_2_1_261.json: home-path-v1
- post_model_switch_2_1_261.json: home-path-v1
- pre_model_switch_2_1_261.json: home-path-v1
- stop_2_1_261.json: home-path-v1
- subagent_start_2_1_261.json: home-path-v1
- subagent_stop_2_1_261.json: home-path-v1, free-text-v1 (.background_tasks[0].description)
- user_prompt_expansion_2_1_261.json: home-path-v1
- user_prompt_submit_2_1_261.json: home-path-v1

Two of the three spellings of the home directory occur in these payloads, and
both were rewritten: the absolute path, and the directory slug a host derives
from a path. Every payload carries both, in `transcript_path`, `cwd` and
`scratchpad_dir`. The relative spelling occurs in none of the fourteen, checked
by search, and the rule covers it wherever it does occur. After the two rules
the committed bytes carry no occurrence of the capturing user's name in any
spelling, which the corpus guard asserts over every file of this directory,
this record included.

Five committed values carry short host or assistant text that the classifier
does not class as free text, so the second rule did not fire on them, and they
are listed here rather than left for a reader to find: `.delta` on
MessageDisplay and `.last_assistant_message` on Stop and on SubagentStop are
each the two letters `ok`, the whole answer the trigger asked for; `.prompt` on
UserPromptExpansion is `/probe`, the name of the probe command written for the
capture, which the classifier reads as a path because it starts with a slash;
and `.tool_input.content` on PermissionRequest is the two letters `ok`. They
were not substituted by hand, because a hand edit would make the committed
bytes impossible to reproduce from the two documented rules.

## Secret scan

`TestNoCommittedTestdataCarriesASecretShape` (internal/lifecycle/ingress/secretscan_test.go)
run over the whole module with these fixtures and their sidecars in place:
PASS, zero hits, 2026-09-05. Reach control on the same run: an Anthropic
API-key shape planted into a copy of one new fixture in this directory turned
the scan RED naming that file and the byte offset; the copy was discarded.
`TestSecretScanIsRedOnEachPlantedShape` (all nine shapes): PASS.

### The second batch

`TestNoCommittedTestdataCarriesASecretShape`
(internal/lifecycle/ingress/secretscan_test.go) run over the whole module with
these fourteen fixtures and their sidecars in place: PASS, zero hits,
2026-09-05. `TestSecretScanIsRedOnEachPlantedShape`, the nine-shape
non-vacuity control: PASS. Reach control on the same tree, which is what proves
the scan reached these files: an Anthropic API-key shape was planted into a
COPY of one of these new fixtures in this directory, the scan turned RED naming
that copy, the shape and the byte offset, and the copy was then discarded and
the scan returned to PASS.

## Refused classes

No fixture carries a tool response above 4096 bytes: the largest tool
response is 52 bytes (post_tool_batch) and 50 bytes (post_tool_use stdout).
The 7734-byte post_compact payload carries `compact_summary`, the host's own
summary of the session (7085 bytes of free text, not a tool response); it was
substituted by free-text-v1 and is committed as placeholder text of the same
length. No payload is an environment dump. Every free-text field on every
event was substituted. No payload was unclearable; no event is left withheld
for clearance reasons.

### The second batch

No fixture of this batch carries a tool response at all, so none can be over
the limit: the largest value under any response-shaped path is
`.tool_input.content` on PermissionRequest, at 2 bytes. The largest whole
payload is SubagentStop at 986 committed bytes, far below the 4096-byte
threshold. No payload is an environment dump, and no object of these payloads
holds three or more members shaped like environment variables. The inventory
report over the batch named no refused class and no unclearable reason, and the
same report over the committed bytes names none either. No payload was
unclearable; no event of this batch is withheld for clearance reasons.

Provider credentials, stated as a measurement rather than an expectation: no
payload of this batch carries a field that could hold one. The fourteen
payloads carry identifiers, paths, host enumerations, model names, two numbers
about cache pricing, and the short text listed above; the secret scan over the
nine committed shapes finds nothing in them; and no member name in the batch
reads as a credential. This is a statement about these fourteen payloads, which
were each read in full, and not a general claim about what the host may send.

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

### The second batch, committed and NOT yet activated

These fourteen fixtures are cleared authentic captures of events the product
does not enable. They are committed so that the work which enables a row has
real bytes to hold it against. Committing them activates nothing: no row of the
activation table names them, and nothing enables an event because a fixture
exists.

- `config_change_2_1_261.json` — ConfigChange — sha256:f28231875ec9f1485caed5f91e014d44ac740a1105b8fb9fed44b097c8e575cf (597 bytes)
- `cwd_changed_2_1_261.json` — CwdChanged — sha256:0dd92eadb211df083321ab0b64a683876f8779343dac1de6a339fb938774ea00 (603 bytes)
- `directory_added_2_1_261.json` — DirectoryAdded — sha256:e864af13aed72eef6ad686e9cd8fa40a9c9bea8576ada65feceb273e9b08a874 (570 bytes)
- `instructions_loaded_2_1_261.json` — InstructionsLoaded — sha256:07eeb7ad0b1bcce0623ec64020d25d0e8a005f91d69bb4e6c95798abc9bb0eda (526 bytes)
- `message_display_2_1_261.json` — MessageDisplay — sha256:e41da860b97f9e4e5fa26e007d64964db2ef7dfc0d6a0f6c713c3ba45abf1c46 (616 bytes)
- `notification_2_1_261.json` — Notification — sha256:aab36db4e6ba2df02660c92ab744ac1e60159f077b8ed974c12ee9848ffd08ff (558 bytes)
- `permission_request_2_1_261.json` — PermissionRequest — sha256:daff36bd80b769b087f340d798497d7f074a5d49360cc53f227be3f8001c9904 (723 bytes)
- `post_model_switch_2_1_261.json` — PostModelSwitch — sha256:c92a88e2d6fd90ac9a6ffcd5a96c62006b478c8ef5abed9134bb0aa5d2bd6523 (715 bytes)
- `pre_model_switch_2_1_261.json` — PreModelSwitch — sha256:268f7bdf78221e312e69cbfaa8de869f85aa6c816898d95c7b87e9c26dcfbc19 (714 bytes)
- `stop_2_1_261.json` — Stop — sha256:db86aac9557f32615ea1446eb5599fb3dea7df35a6e95aa9b2970749063b6d79 (616 bytes)
- `subagent_start_2_1_261.json` — SubagentStart — sha256:ba8a32004176dffc6c094b3d056b818887f0fc08e72caebc1569053367853372 (540 bytes)
- `subagent_stop_2_1_261.json` — SubagentStop — sha256:d3f0e8dfbe6f7aa472f99107f950587b60f7299d66e1f3fd093847fa731bef37 (986 bytes)
- `user_prompt_expansion_2_1_261.json` — UserPromptExpansion — sha256:dbfccc625b2f833876b4fa02a0b7a31447b6c73d3d780439e271ef80a6825ce6 (639 bytes)
- `user_prompt_submit_2_1_261.json` — UserPromptSubmit — sha256:1f928b9073362ebaa2c0e2bc261cb6e02cde7d158f7101d597f65dc8976426e5 (548 bytes)

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

### The second batch, fourteen fixtures

Accepted by the user on 2026-09-06, after the clearance evidence was presented.
This is part of the 24-fixture cross-batch acceptance: 14 existing Claude
fixtures plus 10 existing Codex fixtures. This Claude record covers only the
14 existing Claude fixtures listed in the second batch in this file.

The user answered, verbatim:

```
I accept teh 24 sanitized fixtures. we don't really even need to apply the documentation corrections, it won't matter once we've published them.
```

This acceptance excludes future FileChanged captures, sidecars and clearance
addenda, and later generated transport or activation output. The user accepts
publication without requiring the documented redaction-description and
pending-state wording corrections. Those documentation corrections were not
applied.

## Pull request

Appended by the integrator in the landing commit: the pull request URL.
