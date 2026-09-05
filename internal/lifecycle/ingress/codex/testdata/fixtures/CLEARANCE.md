# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

## Harness and pinned version

Codex 0.153.0, verified with `codex --version` immediately before the session
on 2026-09-05: `codex-cli 0.153.0`. Admission is a floor: this version and
every later release; the contract records 0.153.0.

## Capture

Captured in one live session on 2026-09-05, into
`~/.local/share/pasture-captures/codex`, with `PASTURE_CAPTURE_DIR`
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

Codex transport from the kit, in the project directory
`~/.local/share/pasture-captures/project-codex/.codex`:
`hooks.json` sha256 `0afc4938056274f3a3e9989d16630bb1080dfd7b8976cd6b1ae7fdae6ac79dce`,
`hooks/events/SessionStart.sh` sha256 `e51312d01df72ac8983c89e4cd1532b0c4f102d6b2abc25e79ca6239951486db`,
`hooks/events/PreToolUse.sh` sha256 `18bfdd48a63ab3f45e52d677ec4be35372fd25df5291c753f0c61441a9835566`.
At the startup review ("Hooks need review") the project was trusted; the host
wrote `trust_level = "trusted"` for it into `~/.codex/config.toml`.

Fact recorded: at 0.153.0 the SessionStart hook is queued at session
construction (codex-rs/core/src/session/session.rs:1623) and runs at the start
of the first turn (codex-rs/core/src/session/turn.rs:264), not at process
startup; no file appeared until the first prompt. One line per event:
- SessionStart: `codex_session_start_0_153_0.1.json` — trigger: the first prompt, "Run the shell command `ls -la` and tell me what you see." (fired 1 s after Enter) — 2026-09-05T12:30:24Z — raw sha256:4803435c52143e90a32eedd7a7b2de95847d4ddf90f5654034a4043f2ff8d380 (353 bytes) — committed sha256:cd5256a1d139adad49f0755096914b64bb7662384d3166240223703b0f732601 (347 bytes)
- PreToolUse: `codex_pre_tool_use_0_153_0.1.json` — trigger: the same prompt, the Bash call `ls -la` (fired 3 s after Enter; no approval prompt, read-only sandbox) — 2026-09-05T12:30:28Z — raw sha256:dc80e09555150d40a6ac8b5d69649785da258d8ff1e8b48660fb91eb07b18ed5 (492 bytes) — committed sha256:8da92d236f18516fec7995e1a6e1456ef67a93a03d970ce3d6a8f91b10f6735d (486 bytes)

Both files carry one session_id. No other file was produced; none was
discarded.

## Inventory

```
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
codex_session_start_0_153_0.1.json
  .session_id                                                  identifier 
  .transcript_path                                             path       
  .cwd                                                         path       
  .hook_event_name                                             identifier 
  .model                                                       identifier 
  .permission_mode                                             identifier 
  .source                                                      identifier 
2 payloads inventoried in ~/.local/share/pasture-captures/codex
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

- session_start_0_153_0.json: home-path-v1
- pre_tool_use_0_153_0.json: home-path-v1, free-text-v1 (.tool_input.command)

## Secret scan

`TestNoCommittedTestdataCarriesASecretShape` (internal/lifecycle/ingress/secretscan_test.go)
run over the whole module with these fixtures and their sidecars in place:
PASS, zero hits, 2026-09-05. Reach control on the same run: an Anthropic
API-key shape planted into a copy of one new fixture in this directory turned
the scan RED naming that file and the byte offset; the copy was discarded.
`TestSecretScanIsRedOnEachPlantedShape` (all nine shapes): PASS.

## Refused classes

No fixture carries a tool response (PreToolUse precedes the tool), so none is
above 4096 bytes. No payload is an environment dump. The one free-text field
(.tool_input.command) was substituted. No payload was unclearable.

## Fixtures

- `pre_tool_use_0_153_0.json` — PreToolUse — sha256:8da92d236f18516fec7995e1a6e1456ef67a93a03d970ce3d6a8f91b10f6735d (486 bytes)
- `session_start_0_153_0.json` — SessionStart — sha256:cd5256a1d139adad49f0755096914b64bb7662384d3166240223703b0f732601 (347 bytes)

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
