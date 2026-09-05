# Changelog

## [Unreleased]

### Added
- The lifecycle contracts record Claude Code 2.1.261, Codex 0.153.0 and
  OpenCode 1.18.29. Admission is a floor at each recorded version: a host at
  or above it is admitted, a host below it is refused with the version it
  needs. The floor decides installation and fixture admission; on the live
  hook path the observed host version is recorded as provenance and never
  judged, because some routes pass no usable version at all. The twelve
  enabled events were recaptured at those versions in live host sessions and
  cleared under the documented procedure; every committed
  fixture carries a provenance sidecar that names its event, its substitution
  rules and the `CLEARANCE.md` holding the user's verbatim acceptance, and no
  committed capture is exempt from those three fields any more. Claude Code
  2.1.261 writes `scratchpad_dir` on every payload; the registration allows
  it. Newly registered and withheld until captured: Claude Code
  `PreModelSwitch`, `PostModelSwitch` and `DirectoryAdded`; Codex `SessionEnd`
  (emitted since before 0.146.0 and never registered) and `Interrupt`. The
  OpenCode event type `installation.update-available` is spelled as the host
  emits it; the old underscore spelling matched nothing a host sends. Enabled
  sets are unchanged: 8 Claude Code, 2 Codex, 2 OpenCode events, and a test
  holds each set at that floor by name.

- `pasture hook lifecycle orphans` counts the payload blobs that no recorded
  occurrence names. A hook writes the payload blob before it appends the journal
  row, so an invocation abandoned between those two writes leaves one blob
  behind, and at most one per abandoned invocation. The count reports how many
  of those are present. It deletes nothing and changes no journal truth. The
  number ships with the sentence that says what it means, in both `--format
  text` and `--format json`, because the number alone reads as damage: it is
  expected and reclaimable, and a large reading points at store contention that
  made invocations get abandoned, not at a corrupt store. Use it after a run
  where hooks were slow or a writer held the database.

### Fixed
- When you set `PASTURE_HOOK_FAIL_CLOSED=1` and an event continues anyway,
  `pasture hook lifecycle` now tells you the reason that is true of THAT event.
  A gate that declares the blocking exit code but has no host citation for it
  was told that its failure mode cannot refuse through an exit code. That was
  false about the gate, and it withheld the only action available. Such an event
  now reads that it carries no host evidence, and that supplying the host
  documentation or a committed capture would make it able to block. An event
  that really is non-blocking reads the same sentence as before. The diagnostic
  also names the declared mode and the effective mode separately, so you can see
  when a gate runs as report-and-continue only because its citation is missing.
  An event this build does not declare — an unsupported harness, or an event
  name a stale generated hook still sends — is reported as declaring no failure
  mode and being treated as observe-only, and its line in
  `lifecycle-faults.jsonl` writes `"declaredFailureMode":"undeclared"`; it
  used to read as an observe-only declaration beside a cause saying nothing
  declares the event. No exit code changed.
- A lifecycle hook that could not evaluate an event no longer looks like
  permission granted (#54). The `pasture hook lifecycle` command printed its
  error and exited 0 with empty standard output, which every host reads as
  "proceed", so a validation refusal, a withheld event, a storage error and a
  recovered panic were all indistinguishable from a granted tool call. The exit
  code now follows the event's own declared failure mode: an evaluation fault of
  a documented blocking gate exits 2 and refuses the operation when you opt in
  with `PASTURE_HOOK_FAIL_CLOSED=1`, and every other case exits 0 with the
  reason on standard error. The default is to let the host continue, so a broken
  hook does not stop you working. Each fault is also appended to
  `lifecycle-faults.jsonl` beside the database, which is written outside the
  database on purpose, because the commonest fault is that the database could
  not be opened.
- A hook that cannot evaluate an event no longer stops a tool call on OpenCode
  or Codex. Those two hosts read the hook's STANDARD OUTPUT to decide whether
  you may carry on, so an empty answer is not a "carry on" there: the OpenCode
  plugin treated it as a broken answer and aborted the tool call it was
  watching. When pasture cannot evaluate an event it now answers with that
  host's own "carry on" bytes and puts the reason on standard error, so your
  action proceeds. On Claude Code the "carry on" answer is still no output at
  all, so nothing changes there. On OpenCode this applies to the events a plugin
  callback waits on; the OpenCode event stream is only watched, nothing there
  reads standard output, and a failure on it writes nothing, which is what a
  success on it writes. If you read hook output while debugging an integration,
  note that these bytes say only "do not stop on our account": they are not a
  decision, and the invocation that wrote them is recorded as a fault.
- One whole hook invocation is now bounded at 5 seconds. Measured against a
  database held under a write lock, a hook took about 31 seconds to return,
  which is more than three times the 10-second budget pasture's own hook
  configuration (`hooks/hooks.json`) gives each of its Claude Code lifecycle
  hooks, so the session was frozen while it waited. The hook now stops first
  and reports the expiry as a fault.

### Changed
- A blocking exit code must cite the host documentation or a committed capture
  that shows the host blocks on it. A row with no citation runs as
  report-and-continue instead, and code generation refuses a row that claims a
  blocking exit code without one. Four Claude Code events keep their blocking
  exit code: UserPromptSubmit, Stop, PreToolUse and SubagentStop. Eleven other
  Claude Code events and all eight Codex gates now report instead of blocking
  until their citation exists. No OpenCode event changes.
- The three internal failure-mode vocabularies are now one. Two of them folded
  six native behaviours into two, so a generated OpenCode manifest labelled a
  plugin throw as a Claude exit-2 block. OpenCode rows now carry their real
  behaviour. No Claude Code or Codex value changes.

## [0.0.8] - 2026-08-29

### Added
- `pasture queue concurrency get <queue>` and `pasture queue concurrency set
  <queue> <jobs>` show and change how many jobs a queue runs at once in one
  process (#121). The setting lives in the pasture database, so a running
  daemon adopts a new slice limit about a second later without a restart, and
  work already running is not interrupted. What is printed is the setting read
  back from the database, not what was asked for. The change lasts until a
  daemon starts again: a start writes the limit the daemon was configured with,
  so a limit that must survive a restart belongs in `--slice-concurrency` or
  `PASTURE_SLICE_CONCURRENCY`. The control queue is read only; it runs one
  epoch control workflow per process by design, and `set` on it is refused with
  that reason.

### Changed
- The durable runtime is updated to dbos-transact-golang v1.2.0, with
  provenance v0.0.7 built on the same version (#116). Queue settings are now
  rows in the pasture database that every process reads, instead of state held
  inside one process, which is what makes the run-time concurrency command
  above possible. Recovery after a crash keeps a workflow on the queue it ran
  on, so a recovered slice stays under the slice limit; work that ran on no
  queue is resumed on the runtime's own reserved queue, which carries no limit.
- Building pasture from source now needs Go 1.26 or newer (#122). The published
  binaries are unaffected.
- A pasture database written by an older build is refused, not upgraded (#123).
  The move to the new durable runtime is a clean cut: the daemon and the
  commands check the recorded durable layout before they write anything, and
  stop with a storage error (exit 5) that names the file, the recorded and the
  supported version, and the steps to recover — stop every pasture process, then
  delete the file with its -wal and -shm sidecars, or point --db at another path.
  The refused file is left byte for byte as it was, sidecars included, so
  nothing is lost while you decide.
- `pastured` now chooses its exit code from the kind of failure instead of
  reporting 1 for everything: 1 for bad input or an unclassified failure, 2
  when the database cannot be opened, 3 when a stop does not finish inside its
  budget, 4 for a configuration problem, and 5 for a storage or schema
  failure. A stop that the operator asked for still exits 0, at any point in
  the daemon's life. Scripts and service units that treated any non-zero exit
  as the same fault need no change; those that test for exactly 1 must be
  updated.
- `pastured` reports a stop that did not finish in time. The message names the
  parts of the durable engine that were still running, and the process exits 3
  instead of 0, so a service manager sees that the stop was not orderly. Work
  that was cut off is left pending rather than cancelled, so the next start
  finishes it.

### Fixed
- Command output in JSON is one clean document again (#123). The durable runtime
  wrote its start-up lines to standard output, which broke any reader that
  parsed the whole of it. Those lines now go to standard error.
- A build that never linked the SQLite driver now fails with a message that
  names the exact import to add, and exits 5 (#120). It previously arrived under
  a generic message that pointed at the database instead of at the build.
- `pastured` answers a stop signal that arrives while it is still starting.
  The signal was previously held until startup finished, so a daemon blocked
  on a slow database could be ended only by a kill.

## [0.0.7] - 2026-08-28

### Changed
- Provenance dependency updated to v0.0.6 (#112): a deadline-expired contended
  write always carries its SQLite busy evidence in the error chain (the busy
  error observed by any earlier acquisition attempt is joined into the deadline
  return, with a post-expiry zero-budget probe covering an interrupted first
  attempt). The receipt appender's contention-versus-deadline classification is
  therefore deterministic under load.

## [0.0.6] - 2026-08-26

### Changed
- Provenance dependency updated to v0.0.5 (#110): caller deadlines are now a
  real bound on contended SQLite writes (busy acquisition is retried until the
  deadline actually expires, with the busy detail preserved in the error
  chain), reference-data seeding runs through prepared statements, and fresh
  databases create the operation journal in its completed shape without an
  immediate rebuild.
- The governed-allocation composed request and result types carry their final
  names (`GovernedAllocationComposedRequest` / `...ComposedResult`); the
  transitional `ComposedBatch` names are gone (#109).

### Fixed
- Writer contention during lifecycle occurrence commits is classified from the
  error chain rather than from a timer race: a commit that stays contended
  until its ingress deadline reports the typed contention error (with the
  SQLite cause attached), and only a deadline that expires without contention
  reports the deadline error (#110).

## [0.0.5] - 2026-08-25

### Added
- Global multi-harness installer: `pasture install` / `pasture uninstall` for Claude Code, OpenCode, and Codex skills/agents/hooks, with a shared registry, per-cell reporting, exact preservation of external installs, and the Claude v0.0.4 monolith migration (#99)
- `pasture install status --json` and `pasture install plan` read-only surfaces; hidden `apply-selection` / `apply-cell` for Home Manager and automation (#99)
- `pasture bundle export`: canonical nine-cell release component archives, digests, and bundle IDs in a deterministic, documented format for the aggregate release pipeline (#101)
- Immutable release catalog: exact-tag GitHub release selection with checksum verification and redirect trust validation (#99)
- Production-CLI integration validation suite for the installer (`checks.installer`) (#100)

### Fixed
- `apply-selection` / `apply-cell` now exit non-zero when any cell fails, matching the human verbs (#100)
- Apply text rows print the live observation, so an untouched cell after a first-failure stop no longer reads as a success (#105)
- Durable-execution start-up retries when it loses the schema-bootstrap race against a concurrent process (#102)
- The provenance dependency pins the activation write-lock fix, removing an instant-SQLITE_BUSY failure under concurrent starts (#102, #103)
- `pasture --version` and `pastured --version` report the real release tag; unstamped builds report `devel` instead of a fictional version (#106)

## [0.0.4] - 2026-06-07

### Added
- feat(hook): pasture hook record — fail-hard gather, event id, --format json, repo+remotes (#18)
- feat(pasture): 6l5yo — graduate git_recorder.go via 'pasture hook record' (Manager path) (#17)

### Fixed
- fix(audit): resolve closed-but-unfixed review residuals (#16)

### Documentation
- docs: References & Internal Identifiers standard + scrub user-facing help text (#19)

### Other
- test(hooks): make NonBlocking dispatch test event-based (fix y8r75 flakiness) (#12)
- ci(release): tag-on-merge — auto-tag + publish when a version bump lands on main (#10)

## [0.0.3] - 2026-06-05

### Other
- registry sync-versions: newest-wins marketplace reconciliation + interactive confirm (#7)

## [0.0.2] - 2026-06-04

### Other
- Epoch-protocol improvements I1–I10 + Impl-UAT-2 refinements (#6)

