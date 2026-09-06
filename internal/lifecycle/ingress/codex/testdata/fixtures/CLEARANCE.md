# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

Two batches of fixtures sit in this directory and this one file records both.
The FIRST BATCH is the two fixtures SessionStart and PreToolUse, cleared from
the first sitting. The SECOND BATCH is the ten fixtures UserPromptSubmit,
PermissionRequest, PostToolUse, PreCompact, PostCompact, SubagentStart,
SubagentStop, Stop, SessionEnd and Interrupt, cleared from the second sitting.
The first batch's two fixtures are unchanged: the second sitting captured those
two events again, and those newer files are recorded below as captured and not
used. Each section below states which batch a paragraph belongs to.

## Harness and pinned version

Codex 0.153.0. Admission is a floor: this version and every later release; the
contract records 0.153.0.

FIRST BATCH: verified with `codex --version` immediately before the session on
2026-09-05: `codex-cli 0.153.0`.

SECOND BATCH: `codex --version` was taken at both ends. Immediately before the
sitting on 2026-09-05 it printed `codex-cli 0.153.0`. After the batch was
checked, on the same day, it printed `codex-cli 0.153.0` again. The host did
not move between the two readings.

## Capture

Paths in this record are spelled with `~` for the capturing user's home
directory; the fixture bytes themselves carry the `/home/user` placeholder that
`home-path-v1` writes. The sessions were interactive terminal sessions of the
real host binary on the user's machine, run at the user's direction by the
team's operators; the user did not type the prompts. Nothing captured reached
any remote before the user's acceptance recorded below.

### First batch

Captured in one live session on 2026-09-05, into
`~/.local/share/pasture-captures/codex`, with `PASTURE_CAPTURE_DIR`
set.

Build kit: one `pasture` binary, sha256
`0b9a6fbde02493ba7325e772e78703adf2dc11533c882854ac0f7defa9ce5441`, built with
`CGO_ENABLED=0` from an archive copy of head 47d1c94 with the two version
roots moved to the versions above (artifact/aggregate_types.go, the three
`Contract.Version` literals under internal/lifecycle/ingress/internal/hostcontract)
and `make generate` run, so every transport carried the new version.
Environment in the host's shell: `PASTURE_BIN` = that binary,
`PASTURE_CAPTURE_DIR` = the directory above (absolute, outside the repository,
pre-existing), `PASTURE_DB_PATH` = `~/.local/share/pasture-captures/scratch/pasture.db`
(a scratch database; the live store was not touched). On this scratch
database every hook printed one fail-open fault line ("cannot resolve
Pasture's persisted system identity") after the capture was written; the host
proceeded and the capture is unaffected.

Codex transport from the kit, in the project directory
`~/.local/share/pasture-captures/project-codex/.codex`:
`hooks.json` sha256 `0afc4938056274f3a3e9989d16630bb1080dfd7b8976cd6b1ae7fdae6ac79dce`,
`hooks/events/SessionStart.sh` sha256 `e51312d01df72ac8983c89e4cd1532b0c4f102d6b2abc25e79ca6239951486db`,
`hooks/events/PreToolUse.sh` sha256 `18bfdd48a63ab3f45e52d677ec4be35372fd25df5291c753f0c61441a9835566`.
These three digests are the digests of the transport files this repository
ships today. At the startup review ("Hooks need review") the project was
trusted; the host wrote `trust_level = "trusted"` for it into
`~/.codex/config.toml`.

Fact recorded: at 0.153.0 the SessionStart hook is queued at session
construction (codex-rs/core/src/session/session.rs:1623) and runs at the start
of the first turn (codex-rs/core/src/session/turn.rs:264), not at process
startup; no file appeared until the first prompt. One line per event:
- SessionStart: `codex_session_start_0_153_0.1.json` — trigger: the first prompt, "Run the shell command `ls -la` and tell me what you see." (fired 1 s after Enter) — 2026-09-05T12:30:24Z — raw sha256:4803435c52143e90a32eedd7a7b2de95847d4ddf90f5654034a4043f2ff8d380 (353 bytes) — committed sha256:cd5256a1d139adad49f0755096914b64bb7662384d3166240223703b0f732601 (347 bytes)
- PreToolUse: `codex_pre_tool_use_0_153_0.1.json` — trigger: the same prompt, the Bash call `ls -la` (fired 3 s after Enter; no approval prompt, read-only sandbox) — 2026-09-05T12:30:28Z — raw sha256:dc80e09555150d40a6ac8b5d69649785da258d8ff1e8b48660fb91eb07b18ed5 (492 bytes) — committed sha256:8da92d236f18516fec7995e1a6e1456ef67a93a03d970ce3d6a8f91b10f6735d (486 bytes)

Both files carry one session_id. No other file was produced in that session;
none was discarded.

### Second batch

ONE live sitting was run for this batch, on 2026-09-05, into
`~/.local/share/pasture-captures/s5/codex`, with `PASTURE_CAPTURE_DIR` set to
that directory: an absolute path, outside the repository, that already existed.
That sitting was run to answer two open questions: whether the generated
transport carries a hook whose matcher is the empty string, and whether every
one of the ten uncaptured events has a trigger a person can reach. It answered
both, and it wrote one or more real files for every one of the twelve events.
Its files are adopted as this batch. No second sitting was run, and this record
claims none.

THE KIT. It was built in a throwaway archive copy of head
`0b33f0760602fb7522a18afe73a5ae91011c090b`, never in a working tree of the
repository. One file was edited in that copy, the Codex activation target
declaration, to add the ten missing rows; `make generate` then ENABLED ALL
TWELVE ROWS IN THAT THROWAWAY COPY, so the host had a runner for every event
and no event could be missed for want of a transport. The generator accepted
every added row and refused nothing, so no file was hand-shaped and nothing
hand-shaped exists. Nothing from that copy is committed: the repository's own
transport still carries the two enabled events, and the ten rows reach the
committed transport only when a later change enables them from recorded proof.

Kit digests, taken in the throwaway copy:
```
7f0b255f85c85b41bb6b844899a345f074aaaeec09d8dfd9746a3946bd0115eb  pasture
8ebc9776ffc40afdb02608c8f05533ab27d73588282e1cd62b6057e9629932d8  .codex/hooks.json
d60f82dd630a989e36849efb2bc3af9a07e7c9bbe3d36c909e38d1e93937ad32  .codex/pasture-codex-activation.json
e51312d01df72ac8983c89e4cd1532b0c4f102d6b2abc25e79ca6239951486db  .codex/hooks/events/SessionStart.sh
18bfdd48a63ab3f45e52d677ec4be35372fd25df5291c753f0c61441a9835566  .codex/hooks/events/PreToolUse.sh
4b5d0b8d227db37dff911dc6bfa0e107a7eb172f8f2dca92642a9316db59ce24  .codex/hooks/events/Interrupt.sh
e1b3ddf52c1b99cc127a855bec33d6f390b84bbe65b141c65fdaa768895fb388  .codex/hooks/events/PermissionRequest.sh
40e17bf1b20edd68d089bbf43ba79859f0ad78e74823c56d78f75c0526441932  .codex/hooks/events/PostCompact.sh
b5a1578d8660157110e8eb0a6bada34a22143afebb07ae5bbea54fbe15c3331f  .codex/hooks/events/PostToolUse.sh
47dbd754d116786ebd8463d68bb66d1d654842d8a61773400230f54eb496731b  .codex/hooks/events/PreCompact.sh
452ae1620c7881b8529a30aa9167b4a9213d3eef0b9656bc09be4f479c23a754  .codex/hooks/events/SessionEnd.sh
7247715c1124a0ccb974649becd6d9bd68ec5400819a9b9b9125bf3058e3d7e3  .codex/hooks/events/Stop.sh
763ea068715b1859bfc6c6d4424eeb1b48fdf2640238143d8afc4d045c9f094d  .codex/hooks/events/SubagentStart.sh
10ec0ce4e2a2fd8e37c1af5497ae5369382baab775e26f7f2fcb77dfc938da49  .codex/hooks/events/SubagentStop.sh
4db000072d90bda56b57f91bc99649340356d3c9eb31added5d9f78321978fbf  .codex/hooks/events/UserPromptSubmit.sh
```
Two of those digests are the shipped files: `SessionStart.sh` and
`PreToolUse.sh` are byte-identical in the kit and in this repository. The
kit's `hooks.json` is NOT the shipped file: it carries twelve entries, so its
digest `8ebc9776…` differs from the shipped
`0afc4938056274f3a3e9989d16630bb1080dfd7b8976cd6b1ae7fdae6ac79dce`, which
carries two. The difference is the enablement, and it is stated here so a
reader who hashes the shipped file is not left to guess which number is right.

ENVIRONMENT in the host's shell: `PASTURE_BIN` = the kit binary,
`PASTURE_CAPTURE_DIR` = `~/.local/share/pasture-captures/s5/codex`,
`PASTURE_DB_PATH` = `~/.local/share/pasture-captures/s5/scratch/pasture.db`, a
scratch database. On that fresh scratch database every hook printed one
fail-open fault line ("cannot resolve Pasture's persisted system identity")
after the capture was written; the host proceeded and no capture is affected.

THE LIVE-STORE INCIDENT, stated plainly. One plumbing command in the
preparation for this sitting omitted the scratch database path, so it ran
against the live store at `~/.local/share/pasture`. The person who ran it found
it, disclosed it before being asked, and set the path on every later command.
The measured result: exactly one line in that store's fault journal names this
harness. Its stage shows the run failed at identity resolution, which is
before any occurrence was attributed, so nothing was attributed and no
consultation was recorded. Nothing was repaired and nothing was deleted: a
line in a fault journal is a truthful record of an invocation that really
happened, and editing it to hide a mistake would be worse than the mistake.
The rule the incident produced is now written in the capture instructions:
set the scratch database path in the same command as any hook invocation,
including a plumbing dry run, with no exceptions.

THE SITTING, six steps, one session, driven a step at a time through a
terminal multiplexer:
1. the prompt "Run the shell command `ls -la` and tell me what you see." —
   SessionStart, UserPromptSubmit, PreToolUse, PostToolUse and Stop.
   `ls -la` was chosen deliberately in a near-empty directory, because the
   PostToolUse payload carries the tool response and this record refuses a raw
   tool response above 4096 bytes whatever the substitution.
2. `/compact` — PreCompact and PostCompact.
3. a prompt to spawn one sub-agent that runs `echo hi` — SubagentStart and
   SubagentStop. The sub-agent's own single small command keeps its payload
   small.
4. the session's permission profile moved to the host's "Ask for approval",
   chosen from the host's own permissions menu, then a prompt to run a Bash
   curl — PermissionRequest. The profile change IS the set-up. The account
   default is the host's "Approve for me", which the host's status display
   shows, and it approves without asking. The earlier sitting saw no approval
   prompt for a read-only command for exactly that reason, and this sitting's
   own first network attempt also ran silently before the profile was moved.
5. a `sleep 20` prompt, interrupted with Escape — Interrupt.
6. `/quit` — SessionEnd.

TWO APPROVAL RUNS, NOT A PREVIEW AND A CONFIRMATION. Two PermissionRequest
files exist. They are not one request seen twice. Their payloads carry two
different turn ids and two different command lines, one curl to example.com and
one to example.org: the approval trigger was simply run twice, and each run
carried its own full cycle of UserPromptSubmit, PreToolUse, PermissionRequest,
PostToolUse and Stop. This measurement says nothing about whether the host has
a preview-and-confirm behaviour. One approval trigger produces one file.

THE CHOICE, one file per event, and the reason. The default is the first
occurrence, the `.1` file, because it is the plainest instance of its trigger
and it is the file a reader of the recipe would expect. Every event took its
`.1` file, PermissionRequest included: the two PermissionRequest files are two
separate runs, so the first one is the first occurrence and no other rule is
needed to pick it.

One line per fixture of this batch, with its trigger step, its write time, the
digest of the captured bytes and the digest of the committed bytes:

- UserPromptSubmit: `codex_user_prompt_submit_0_153_0.1.json` — trigger: step 1, the first prompt of the session: "Run the shell command `ls -la` and tell me what you see." — 2026-09-05T21:15:44Z — raw sha256:7fbb6a9ae4e8e7aed048e6a23725ddcfa7cbffb352ab32ffbdaca0b4be26f40d (458 bytes) — committed sha256:22816dfb47f1ec3ee90131a0b8f16cc524ccad8a34ab6d07099322c984d7fa02 (452 bytes) — rules: home-path-v1, free-text-v1 (.prompt)
- PermissionRequest: `codex_permission_request_0_153_0.1.json` — trigger: step 4, the approval menu the host raised for a Bash curl to example.com, after the session's permission profile was moved to ask for approval — 2026-09-05T21:18:37Z — raw sha256:d026931736cd177a9e1b2fa77ef687ed2c1b1d016fcbdc4152363dc9fa907055 (589 bytes) — committed sha256:45a1c75f09a01dcbc5d0691590711276b013a6691de8e1a9d62dc452a5e2654b (583 bytes) — rules: home-path-v1, free-text-v1 (.tool_input.command, .tool_input.description)
- PostToolUse: `codex_post_tool_use_0_153_0.1.json` — trigger: step 1, the result of the Bash `ls -la` the first prompt asked for — 2026-09-05T21:15:47Z — raw sha256:88b49d67577717439865b3d290e5102ae53f3523c0269fddd24fcb0ae85bce2d (780 bytes) — committed sha256:003de5982c9594426ebf8d7900c71735309a8e26106b14f1b1d8166c133b181c (774 bytes) — rules: home-path-v1, free-text-v1 (.tool_input.command, .tool_response)
- PreCompact: `codex_pre_compact_0_153_0.1.json` — trigger: step 2, `/compact` — 2026-09-05T21:16:10Z — raw sha256:9dcba1377fb401e7c0a9f8e5fbdf9c949e288ed3b2e82dc47fbfa09140156e7f (375 bytes) — committed sha256:759eaff346b108b0351c7cd151327eb9ca147aaf58fd93398eeba95cb558298a (369 bytes) — rules: home-path-v1
- PostCompact: `codex_post_compact_0_153_0.1.json` — trigger: step 2, `/compact` — 2026-09-05T21:16:25Z — raw sha256:8c87530b1d02e4a36bf5b2118de9352020cc095a22663c40bf554ab069fa29ba (376 bytes) — committed sha256:edc3062c3ef70c366f65e6dcfc6ccbb566144cd679acb944741d99d44c3745b1 (370 bytes) — rules: home-path-v1
- SubagentStart: `codex_subagent_start_0_153_0.1.json` — trigger: step 3, the session start of the sub-agent the spawn tool created — 2026-09-05T21:17:51Z — raw sha256:64950f83c8d52256dea312f722570840d8a755234d50431bedf1630a0e1a9e70 (460 bytes) — committed sha256:91269feefa5413cb3ad87bc191657868825837d3ca4808a4f998936aa1b2d5be (454 bytes) — rules: home-path-v1
- SubagentStop: `codex_subagent_stop_0_153_0.1.json` — trigger: step 3, the close of that sub-agent — 2026-09-05T21:17:58Z — raw sha256:f104d56513c5c10dc010edce16dd272364b552e9e0d34758bd2714acd9a5e024 (672 bytes) — committed sha256:15045b526999daa83ad41872f0d4f5263e400cfb5a83b3d2481cf4acbe8c41c7 (663 bytes) — rules: home-path-v1, free-text-v1 (.last_assistant_message)
- Stop: `codex_stop_0_153_0.1.json` — trigger: step 1, the end of the `ls -la` turn; this event fires at the end of every turn — 2026-09-05T21:15:55Z — raw sha256:d6a64fe2819203024da878bd9b7f2ad2edb63c7ea7a0882b42c7dfc2ca3b79d5 (881 bytes) — committed sha256:8f9597237c56262e93dd3b87bba1d9d5731a294c8e52e7c3f6286fb84fa0d80d (875 bytes) — rules: home-path-v1, free-text-v1 (.last_assistant_message)
- SessionEnd: `codex_session_end_0_153_0.1.json` — trigger: step 6, `/quit` — 2026-09-05T21:21:08Z — raw sha256:5f1b59d29cc1793c21ea397dcae10ba670c84d6dd8a26e01a03b36f0fc7a39ce (302 bytes) — committed sha256:06d4d6d272600634def30067f238c3080b1f5f6b8abb0b9e2e821584b87965b7 (296 bytes) — rules: home-path-v1
- Interrupt: `codex_interrupt_0_153_0.1.json` — trigger: step 5, Escape while the status line offered to interrupt a `sleep 20` — 2026-09-05T21:21:00Z — raw sha256:2f6838c45e34264b2c1bb4cbb503ce7f388c2352bfce651d6513b44be27f37d5 (383 bytes) — committed sha256:5e5fb554efd6fd1f4db52656cdd6a6c2103b28f2288ea6f451983961f749fcf3 (377 bytes) — rules: home-path-v1

CAPTURED AND NOT USED. The sitting wrote 39 files. Twelve are the chosen ones
above and in the first batch's list; the other 29 are recorded here so the
record is complete, and none of them is committed. Two of them, the newer
SessionStart and PreToolUse captures, are the recaptured first-batch events:
those two events keep the fixtures cleared from the first sitting and are NOT
replaced.

- `codex_permission_request_0_153_0.2.json` — raw sha256:810073436438a40d1749bfddd0bff2dd922f1a2b6d7690c5b930a6fe51c44590 (589 bytes)
- `codex_post_tool_use_0_153_0.10.json` — raw sha256:9b188e60a5b8800ecd02b5768b14a1abef0a539046429b6c0e463da0c848afe6 (1137 bytes)
- `codex_post_tool_use_0_153_0.2.json` — raw sha256:76427fd371e0446e19e02647560a21170e884c46ea0dc0df29ad04ff78fac8b1 (840 bytes)
- `codex_post_tool_use_0_153_0.3.json` — raw sha256:23ac83b3ed47efd29a97a31874a07c135d73c39cd0b421688eaaca5bb5185f1b (593 bytes)
- `codex_post_tool_use_0_153_0.4.json` — raw sha256:473aaf6b006fbe003fd3824fff4fcf5cea1ae9a8fa4a21273c02a1dbed1a3c86 (575 bytes)
- `codex_post_tool_use_0_153_0.5.json` — raw sha256:76a4e3336e1e21fcdb4f1023680506a550aee4232c2986f16d4fe5ae0381ec61 (591 bytes)
- `codex_post_tool_use_0_153_0.6.json` — raw sha256:4cf9533d784075c757b1a5427039be206a2e020ff720fbf76d24220c0f59f604 (548 bytes)
- `codex_post_tool_use_0_153_0.7.json` — raw sha256:13a0ddfd02fb911a2880cb426fb87cfbd1ed0b42e9fbf38e884a7e752256410a (565 bytes)
- `codex_post_tool_use_0_153_0.8.json` — raw sha256:b014e9d728ad87c8946b11faf04efb4b8e473783181dc82b794c26ae6a2b2b23 (1137 bytes)
- `codex_post_tool_use_0_153_0.9.json` — raw sha256:e59199bd2645b813ea5eeb197bf3bc2b6e062fb97d3d39f7d1fe6087fc58f0ef (565 bytes)
- `codex_pre_tool_use_0_153_0.1.json` — raw sha256:a0f998c7092a531f69007b934d0034e1cde21cb984994edd5a2c98a6b823a0cd (495 bytes)
- `codex_pre_tool_use_0_153_0.10.json` — raw sha256:e9fe44518aa26aa228d5c80d99139588b5e8bb96ac74cd7d77d6680c0bfe97c8 (545 bytes)
- `codex_pre_tool_use_0_153_0.11.json` — raw sha256:49cc70844caaa9077ba167dd7590a8ee1493010dc26e9198270c7262593c0124 (497 bytes)
- `codex_pre_tool_use_0_153_0.2.json` — raw sha256:9229002144e00f0e323b6f82ddc2890d279ee03c52aac7b42ed5fd11b83bad17 (787 bytes)
- `codex_pre_tool_use_0_153_0.3.json` — raw sha256:89418be7d9e4a58235095435d03d8cb46e485aa2ddc418c3e3d4899d4d861f3f (569 bytes)
- `codex_pre_tool_use_0_153_0.4.json` — raw sha256:bd5996834337b1a8d47ce9d1fba059d8a02080fedb908c58bb229adbbbc96ebe (502 bytes)
- `codex_pre_tool_use_0_153_0.5.json` — raw sha256:c37f02657fc6a025a9ff04f592d6e000d34ce9b37ea51ab6ef0681aec26b3c64 (507 bytes)
- `codex_pre_tool_use_0_153_0.6.json` — raw sha256:15d840654e3e120cc2f0bfed6b60f3d23b7bc2b098423903be35d53892333794 (528 bytes)
- `codex_pre_tool_use_0_153_0.7.json` — raw sha256:5281a7e21dfdf6f728ea0d334c64354df1b5415c10e92f701ff03f0723ef89bd (545 bytes)
- `codex_pre_tool_use_0_153_0.8.json` — raw sha256:83eefad31472d7f6be4f1b51502e2e4af203b820b330434dec5b398487e0478c (545 bytes)
- `codex_pre_tool_use_0_153_0.9.json` — raw sha256:4afc30dfd9d7348a4100550ec745605eb9a65184a731e88c28ea996b02edcbfb (545 bytes)
- `codex_session_start_0_153_0.1.json` — raw sha256:398bf485c055a8418268c9f24de50d33dab5d82deb77099a9648fe7e3117ee21 (356 bytes)
- `codex_stop_0_153_0.2.json` — raw sha256:e48de4558b9387ec9385d7a9dbbea55db99efa0409dcfb9a0cedd6b4d6a35ea0 (554 bytes)
- `codex_stop_0_153_0.3.json` — raw sha256:6a4f8a7fde315f14efbd0d1925ecfc0daac7d9a2ccfd9b1d133aee5998a898af (1015 bytes)
- `codex_stop_0_153_0.4.json` — raw sha256:83aa33bbd0d211bbe872f8c7950d4a69069d3f00a9c9840ec0eefd8cfb7972cf (1015 bytes)
- `codex_user_prompt_submit_0_153_0.2.json` — raw sha256:714cbed3a1bab34e36fbaa76652e15fbf144540f16a8f54e6e0aec1dbf63a73a (529 bytes)
- `codex_user_prompt_submit_0_153_0.3.json` — raw sha256:2a47d95520237abef03bbd8c4484bc42c835abf183bc242d67ebe72e2aa33657 (491 bytes)
- `codex_user_prompt_submit_0_153_0.4.json` — raw sha256:2f503617978b0261043d6a7b62b5c34369088b74457563251c45e33471d90553 (478 bytes)
- `codex_user_prompt_submit_0_153_0.5.json` — raw sha256:4c21399713939b6ebbd3952e1fccf6bed9f3273e4e9e9c2b4bdfdd671edcc63d (464 bytes)

### Pairing, by the host's own identifiers

Two files belong together only when the HOST'S OWN per-operation identifier
says so. Field values do not pair anything: two separate operations can carry
the same command, the same tool name and the same shape, and a reading by value
would call them one. Codex carries three granularities, and the right one
depends on the event.

MEASURED over the 39 files of the second sitting: ONE session id; SEVEN
distinct turn ids; TWO files carry no turn id at all, SessionStart and
SessionEnd.

SESSION SCOPE CARRIES NO INFORMATION HERE, and this is the trap. Every file in
this batch shares the one session id, so a session-level match is satisfied by
every pair a reader could form. An identifier every member shares pairs
nothing. No claim in this record rests on it.

TOOL-CALL SCOPE, the finest: PreToolUse and PostToolUse carry `tool_use_id`.
The committed PostToolUse fixture carries turn
`01a0736d-6431-7730-9321-53425562ab77` and tool call
`exec-c18b3026-7f48-406a-a700-cd07054bf30c`. Its PreToolUse counterpart, the
same turn and the same tool call, is in the capture directory and is NOT in
this repository: the committed PreToolUse fixture comes from the FIRST sitting
and carries a different session, a different turn and a different tool call
(`01a0718c-6f88-73c2-8d82-013e647cc02f`,
`exec-8d2f7dd7-3b46-40af-af0a-252dad7bbbec`). THE TWO COMMITTED TOOL EVENTS ARE
THEREFORE NOT A PAIR, and this record asserts none between them.

TURN SCOPE. Three pairs are asserted among the committed fixtures of this
batch, each by its turn id, and one of them by a second identifier as well:
- UserPromptSubmit, PostToolUse and Stop share turn
  `01a0736d-6431-7730-9321-53425562ab77`: one turn, the prompt that ran
  `ls -la`, its tool result and its end.
- PreCompact and PostCompact share turn
  `01a0736d-ca76-78a2-bfef-180da6c981f0`: one compaction.
- SubagentStart and SubagentStop share turn
  `01a0736f-5397-7b20-b1a0-ca343c2d5327` AND agent
  `01a0736f-5370-7d02-9eb9-7a28a3563b32`: one sub-agent, paired at the finest
  identifier the two events carry.

FILES THIS RECORD DOES NOT PAIR, said plainly rather than left to assumption:
- PermissionRequest, turn `01a0736f-d539-7812-9525-a5731c764606`. It carries
  NO tool call id, so it can be placed in a turn but never tied to one
  specific tool call by identifier alone. This record claims no tool-call link
  for it.
- Interrupt, turn `01a07372-18ec-70a1-9875-279ffcbc2478`. No other committed
  fixture shares that turn.
- SessionEnd carries no turn id at all, so it pairs only by session, which
  pairs nothing here.
- SessionStart, the committed fixture, is from the first sitting and shares no
  identifier with any fixture of this batch.

THE TWO APPROVAL FILES ARE TWO OPERATIONS. `codex_permission_request_0_153_0.1.json`
carries turn `01a0736f-d539-7812-9525-a5731c764606` and a curl to example.com;
`codex_permission_request_0_153_0.2.json` carries turn
`01a07371-81f6-7f52-aefb-3615338a4b46` and a curl to example.org. Two turn ids
and two commands: two operations, and nothing about a preview-and-confirm
behaviour of the host is measured or claimed.

## Inventory

The inventory ran in report mode over the whole capture directory, over all 39
files and not only the chosen twelve. Its output decides which fields are free
text; no field was judged by eye. It printed no REFUSED line and no
UNCLEARABLE line for any file.

The output is pasted below as it was printed, with ONE change: the capture
directory in its last line is spelled with `~`, because this record must obey
the same home-path rule the fixtures obey.

```
codex_interrupt_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
codex_permission_request_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
codex_permission_request_0_153_0.2.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_input.description                                      free-text    FREE TEXT: substitute with free-text-v1
codex_post_compact_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .trigger                                                     identifier
codex_post_tool_use_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.10.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.2.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.task_name                                        identifier
  .tool_input.fork_turns                                       identifier
  .tool_input.message                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               identifier
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.3.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .agent_id                                                    identifier
  .agent_type                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.4.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.timeout_ms                                       number
  .tool_response                                               free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.5.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.target                                           identifier
  .tool_response                                               free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.6.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               identifier
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.7.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               identifier
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.8.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_post_tool_use_0_153_0.9.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_response                                               identifier
  .tool_use_id                                                 identifier
codex_pre_compact_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .trigger                                                     identifier
codex_pre_tool_use_0_153_0.1.json
  .session_id                                                  identifier 
  .turn_id                                                     identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .hook_event_name                                             identifier 
  .model                                                       identifier 
  .permission_mode                                             identifier 
  .tool_name                                                   identifier 
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier 
codex_pre_tool_use_0_153_0.10.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.11.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.2.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.task_name                                        identifier
  .tool_input.fork_turns                                       identifier
  .tool_input.message                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.3.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .agent_id                                                    identifier
  .agent_type                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.4.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.timeout_ms                                       number
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.5.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.target                                           identifier
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.6.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.7.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.8.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_pre_tool_use_0_153_0.9.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .tool_name                                                   identifier
  .tool_input.command                                          free-text    FREE TEXT: substitute with free-text-v1
  .tool_use_id                                                 identifier
codex_session_end_0_153_0.1.json
  .session_id                                                  identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .reason                                                      identifier
codex_session_start_0_153_0.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .hook_event_name                                             identifier 
  .model                                                       identifier 
  .permission_mode                                             identifier 
  .source                                                      identifier 
codex_stop_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .stop_hook_active                                            bool
  .last_assistant_message                                      free-text    FREE TEXT: substitute with free-text-v1
codex_stop_0_153_0.2.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .stop_hook_active                                            bool
  .last_assistant_message                                      free-text    FREE TEXT: substitute with free-text-v1
codex_stop_0_153_0.3.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .stop_hook_active                                            bool
  .last_assistant_message                                      free-text    FREE TEXT: substitute with free-text-v1
codex_stop_0_153_0.4.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .stop_hook_active                                            bool
  .last_assistant_message                                      free-text    FREE TEXT: substitute with free-text-v1
codex_subagent_start_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .agent_id                                                    identifier
  .agent_type                                                  identifier
codex_subagent_stop_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .agent_transcript_path                                       path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .stop_hook_active                                            bool
  .agent_id                                                    identifier
  .agent_type                                                  identifier
  .last_assistant_message                                      free-text    FREE TEXT: substitute with free-text-v1
codex_user_prompt_submit_0_153_0.1.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .prompt                                                      free-text    FREE TEXT: substitute with free-text-v1
codex_user_prompt_submit_0_153_0.2.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .prompt                                                      free-text    FREE TEXT: substitute with free-text-v1
codex_user_prompt_submit_0_153_0.3.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .prompt                                                      free-text    FREE TEXT: substitute with free-text-v1
codex_user_prompt_submit_0_153_0.4.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .prompt                                                      free-text    FREE TEXT: substitute with free-text-v1
codex_user_prompt_submit_0_153_0.5.json
  .session_id                                                  identifier
  .turn_id                                                     identifier
  .transcript_path                                             path
  .cwd                                                         path
  .hook_event_name                                             identifier
  .model                                                       identifier
  .permission_mode                                             identifier
  .prompt                                                      free-text    FREE TEXT: substitute with free-text-v1
39 payloads inventoried in ~/.local/share/pasture-captures/s5/codex
```

## Rules applied, in order

Per fixture, the value-only rules applied in the order applied, as listed in
the provenance sidecar: `home-path-v1`, then `free-text-v1` where the
inventory flagged free text. Structure, keys, types and nulls are unchanged.

`home-path-v1` rewrites every spelling of the capturing user's home directory
to the `user` placeholder: the absolute path `/home/<user>`, the relative
spelling `home/<user>/`, and the directory slug a host derives from a path,
which is how the earlier committed corpus carries it. It applies wherever any
spelling occurs, including inside free text. In this batch every payload
carried the absolute spelling, two or three times each; no payload carried the
relative spelling or the slug.
`free-text-v1` replaces each free-text string the inventory flagged by `x`
placeholder text of the same raw length. Keys, nesting, types and nulls are
unchanged; after both rules the committed bytes contain no occurrence of the
user name (asserted by the clearing run, which refuses to write a file that
still carries it).

A MEASURED PROPERTY OF THE SECOND RULE, recorded because a reader will meet
it. `free-text-v1` is a fixed point in BYTES but not in FLAGS. A placeholder is
written to the same raw length as the value it replaced, so a long placeholder
is still longer than the free-text length limit and the inventory still class
it as free text. Running the rule again therefore reports the field and changes
nothing. The corpus test uses the BYTES for that reason: a committed fixture
must come back unchanged when the rule runs again, and only a placeholder does.

Rules applied per fixture, in order:

- session_start_0_153_0.json: home-path-v1
- pre_tool_use_0_153_0.json: home-path-v1, free-text-v1 (.tool_input.command)
- user_prompt_submit_0_153_0.json: home-path-v1, free-text-v1 (.prompt)
- permission_request_0_153_0.json: home-path-v1, free-text-v1 (.tool_input.command, .tool_input.description)
- post_tool_use_0_153_0.json: home-path-v1, free-text-v1 (.tool_input.command, .tool_response)
- pre_compact_0_153_0.json: home-path-v1
- post_compact_0_153_0.json: home-path-v1
- subagent_start_0_153_0.json: home-path-v1
- subagent_stop_0_153_0.json: home-path-v1, free-text-v1 (.last_assistant_message)
- stop_0_153_0.json: home-path-v1, free-text-v1 (.last_assistant_message)
- session_end_0_153_0.json: home-path-v1
- interrupt_0_153_0.json: home-path-v1

## Secret scan

`TestNoCommittedTestdataCarriesASecretShape` (internal/lifecycle/ingress/secretscan_test.go)
run over the whole module with all twelve fixtures and their sidecars in place:
PASS, zero hits, 2026-09-05. `TestSecretScanIsRedOnEachPlantedShape` (all nine
shapes): PASS.

REACH CONTROL, because a scan that has not been shown to fail proves nothing.
An API-key shape was planted into a COPY of one fixture of this batch, in this
directory. The scan turned RED and named the file and the offset:

```
internal/lifecycle/ingress/codex/testdata/fixtures/reach_control_copy.json carries a Anthropic API key at byte 394 (43 bytes); a committed fixture must never carry a credential, so remove or substitute it before committing
```

The copy was then deleted and the scan was green again. Nothing of the control
is committed.

WHAT THE SCAN KNOWS AND DOES NOT KNOW, stated as a measurement. The committed
set holds nine key shapes: a private key block, two Amazon shapes, two Google
shapes, two GitHub shapes, one Anthropic shape and a JSON web token. It holds
NO generic provider-key shape, so a credential in a spelling outside those nine
would pass it. That gap is bounded here by a second measurement, not by hope:
the inventory above lists every field of every one of the 39 captured payloads,
and the twelve committed payloads carry only session, turn, tool-call and agent
identifiers, four filesystem paths, a model name, a permission mode, a trigger
or reason word, two booleans, one number, and the free-text fields the rules
substituted. Not one field of this harness's payloads is a credential carrier.

## Refused classes

No payload of this batch reaches a refused class, and this is a measurement,
not an expectation.
- A raw tool response above 4096 bytes: the largest raw tool response in the
  whole capture directory is 559 bytes, in a file that is not committed; the
  committed PostToolUse fixture's raw tool response is 259 bytes. The largest
  capture FILE of any event is 1137 bytes. The ceiling is not approached, and
  the `ls -la` trigger was chosen to keep it that way.
- An environment dump: no payload carries one.
- A free-text field on a prompt or message event that the rules did not
  substitute: none. Every field the inventory flagged as free text was
  substituted, on every committed fixture, and the corpus test proves it by
  running the rule again and requiring the bytes to come back unchanged.
The inventory printed no REFUSED line and no UNCLEARABLE line for any of the 39
files. No payload was unclearable.

## Fixtures

Twelve fixtures, one per registered Codex event at this version. Each is
committed with the provenance sidecar beside it, and each sidecar names this
file by its committed path.

- `session_start_0_153_0.json` — SessionStart — sha256:cd5256a1d139adad49f0755096914b64bb7662384d3166240223703b0f732601 (347 bytes)
- `pre_tool_use_0_153_0.json` — PreToolUse — sha256:8da92d236f18516fec7995e1a6e1456ef67a93a03d970ce3d6a8f91b10f6735d (486 bytes)
- `user_prompt_submit_0_153_0.json` — UserPromptSubmit — sha256:22816dfb47f1ec3ee90131a0b8f16cc524ccad8a34ab6d07099322c984d7fa02 (452 bytes)
- `permission_request_0_153_0.json` — PermissionRequest — sha256:45a1c75f09a01dcbc5d0691590711276b013a6691de8e1a9d62dc452a5e2654b (583 bytes)
  CONDITION ON THIS EVENT, not on this capture: the host emits PermissionRequest
  only when the session's permission profile is the host's "Ask for approval".
  The account default is the host's "Approve for me", which approves without
  asking, so on a host left at the default THIS EVENT NEVER FIRES, whatever
  command runs, and its absence there is not a defect.
- `post_tool_use_0_153_0.json` — PostToolUse — sha256:003de5982c9594426ebf8d7900c71735309a8e26106b14f1b1d8166c133b181c (774 bytes)
- `pre_compact_0_153_0.json` — PreCompact — sha256:759eaff346b108b0351c7cd151327eb9ca147aaf58fd93398eeba95cb558298a (369 bytes)
- `post_compact_0_153_0.json` — PostCompact — sha256:edc3062c3ef70c366f65e6dcfc6ccbb566144cd679acb944741d99d44c3745b1 (370 bytes)
- `subagent_start_0_153_0.json` — SubagentStart — sha256:91269feefa5413cb3ad87bc191657868825837d3ca4808a4f998936aa1b2d5be (454 bytes)
- `subagent_stop_0_153_0.json` — SubagentStop — sha256:15045b526999daa83ad41872f0d4f5263e400cfb5a83b3d2481cf4acbe8c41c7 (663 bytes)
- `stop_0_153_0.json` — Stop — sha256:8f9597237c56262e93dd3b87bba1d9d5731a294c8e52e7c3f6286fb84fa0d80d (875 bytes)
- `session_end_0_153_0.json` — SessionEnd — sha256:06d4d6d272600634def30067f238c3080b1f5f6b8abb0b9e2e821584b87965b7 (296 bytes)
- `interrupt_0_153_0.json` — Interrupt — sha256:5e5fb554efd6fd1f4db52656cdd6a6c2103b28f2288ea6f451983961f749fcf3 (377 bytes)

THAT IS THE WHOLE LIST OF CONDITIONS. PermissionRequest is the ONLY event of
this harness whose capture needed a setting away from the host's defaults, and
this was measured over all twelve, not noticed on one. The sub-agent spawn tool
that produces SubagentStart and SubagentStop is a stable, default-enabled
surface and needed no configuration. Every other trigger is a plain prompt or a
slash command a user types in a default session. No other fixture in this
directory carries a condition, and none is omitted here.

COMMITTED, NOT YET ENABLED. Ten of these twelve fixtures are cleared and
committed, and their events are still withheld: the ingress parser has no
binding arm for them and the activation target set does not name them. A
committed fixture is evidence of a capture, never of an enabled row. The
corpus test states that boundary directly: it drives each of the ten through
the production parser and requires the exact bytes back with an unsupported
schema and no identity binding, so the change that adds an arm must move the
row out of that closed set deliberately.

## User acceptance

Filled after the user accepts this batch, with the verbatim acceptance, its
date, and the questions asked.

The first batch of two fixtures was accepted by the user on 2026-09-05, with
the whole first batch across the three harnesses, after its clearance evidence
was presented. The user was asked three questions:

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

That acceptance covers the two first-batch fixtures and remains unchanged.

Second-batch acceptance recorded on 2026-09-06. The user accepted the combined
24 sanitized fixtures: 14 Claude fixtures and the ten Codex second-batch
fixtures listed in this file. This record applies that acceptance only to
those ten Codex fixtures.

The user said, verbatim:

```
I accept teh 24 sanitized fixtures. we don't really even need to apply the documentation corrections, it won't matter once we've published them.
```

The user's pairing choice is to retain the earlier accepted Codex fixtures
and state the mismatch. SessionStart and PreToolUse were not replaced. As
recorded under "Pairing, by the host's own identifiers", the retained
PreToolUse and the second-batch PostToolUse are not one tool operation, and
the retained SessionStart is from a different session. No fixture or sidecar
bytes changed for this acceptance.

The user waived documentation corrections as a prerequisite for publication.
Those corrections have not been applied. This acceptance grants no approval
to withhold events and does not enable any event.

Nothing in this directory reaches a remote before this section is filled. This
file is the clearance authority a fixture's provenance names by path: a fixture
may name this file only after this section holds the acceptance, so that a
reader who follows the path finds the grant recorded and never a blank form.

## Pull request

Appended by the integrator in the landing commit: the pull request URL.
