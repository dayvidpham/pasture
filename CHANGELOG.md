# Changelog

## [0.0.13] - 2026-10-09

### Other
- Merge pull request #171 from dayvidpham/pasture-43--test--combined-validation
- style: integrate diagnostic mutation comment wrap
- test: integrate extracted diagnostic source guard correction
- test: combine reviewed workflow CLI and template slices

## [0.0.12] - 2026-10-08

### Added
- feat(ingress): clear shell.create.before under an environment-dump exemption

### Fixed
- fix(pasture): narrow the claim mismatch diagnostic and pin its bounds
- fix(tasks): return typed session-claim outcome and diagnose actor mismatch

### Documentation
- docs(ingress): append the landing pull request for the fifth batch
- docs(ingress): record the fifth batch fixture acceptance
- docs(ingress): mark the fifth fixture batch proposed; acceptance pending

### Other
- Merge pull request #169 from dayvidpham/pasture-f714--feat--shell-env-clearing
- Merge pull request #168 from dayvidpham/pasture-fb13--fix--claim-race-outcome

## [0.0.11] - 2026-10-08

### Fixed
- fix(tasks): bind session claim on first observed event
- fix(opencode): tolerate diagnostic forwarding failures

### Documentation
- docs(opencode): keep the operator directive while qualifying best-effort forwarding
- docs(opencode): qualify the best-effort diagnostic wording (review MINORs)
- docs(tasks): qualify the first-observation claim wording per review

### Other
- Merge pull request #164 from dayvidpham/fix/opencode-best-effort-diagnostics
- test(opencode): assert the qualified best-effort diagnostic wording
- Merge pull request #165 from dayvidpham/pasture-157--fix--first-session-claim

## [0.0.10] - 2026-10-07

### Added
- feat(opencode): derive the enforceable deny for permission.evaluate from its accepted capture
- feat(opencode): enable the six 2.0.21 coordinates from their accepted captures

### Documentation
- docs(opencode): record the landing pull request URL for the second-sitting batch
- docs(opencode): record the user's ACCEPT for the 2.0.21 second-sitting captures

### Other
- Merge pull request #160 from dayvidpham/pasture-i8kt1v--feat--opencode-v2-second-batch
- test(opencode): clear the 2.0.21 second-sitting captures (acceptance owed)

## [0.0.9] - 2026-10-02

### Added
- feat(opencode): enable 2.0.20-contract session.created from its 2.0.21 bus capture
- feat(opencode): switch the production runtime contract to OpenCode 2.0.20
- feat(opencode): enable the nine cleared 2.0.20 coordinates with capture and Bun-driven production proofs
- feat(opencode): clear 9 OpenCode 2.0.20 captures (acceptance owed)
- feat(opencode): stage v2 activation as pure data flip with withheld posture
- feat(opencode): enforce pasture Denial through the v2 permission evaluate hook
- feat(opencode): regenerate transport as OpenCode v2 Plugin.define plugin with version-routed gate wiring
- feat(opencode): derive OpenCode 2.0.20 host contract alongside v1
- feat(handlers): wire the gate Reader into the lifecycle hook
- feat(gate): read the active owner with one claim read and one public call
- feat(audit): add the v8 to v9 migration that removes the retired assignment index
- feat(gate): retire snapshot completeness contract
- feat(claude): activate accepted FileChanged observations
- feat(opencode): derive lifecycle dispatch from enabled registrations
- feat(lifecycle): combine version provenance and diagnostic paths
- feat(lifecycle): retain optional host version provenance
- feat(hooks): attribute the lifecycle version observation source
- feat(lifecycle): integrate committed outcomes and harness transports
- feat(lifecycle): persist and preserve committed outcomes
- feat(opencode): bound lifecycle children and parse decisions
- feat(lifecycle): evaluate ordered assignment gate policy
- feat(lifecycle): integrate gate foundations and clarify tests
- feat(tasks): recover assignment index and repair command authority
- feat(lifecycle): add evidence-bound response contracts
- feat: persist session claims and bounded assignment pages
- feat(tasks): record who holds a task, at one place, for every writer
- feat(gateauthority): one class per host event, and what can reach a denial
- feat(activation): mint WithheldTriggerNotExercised for a declined condition
- feat(gateauthority): the two legality tables, built from one rule
- feat(activation): add three closed-set withholding reasons
- feat(gateauthority): value types the gate decision reads
- feat(lifecycle): register the hook events the 2.1.261 and 0.153.0 hosts added
- feat(lifecycle): record Claude Code 2.1.261, Codex 0.153.0 and OpenCode 1.18.29 with a recaptured corpus
- feat(codex): register the SessionEnd hook event the host has emitted all along
- feat(lifecycle): map every registered event and resolve names through one lookup
- feat(runtime): admit every host at or above its recorded version
- feat(runtime): add the open version floor as an admission shape
- feat(projection): reach the orphan reclaim from every read command; the store owns its clock and its diagnostic sink
- feat(receipt): refuse a lifecycle delivery whose context is not bounded by the writer window
- feat(receipt): stamp the payload blob write instant from a clock the store holds
- feat(projection): reclaim orphan payload blobs inside the projection rebuild
- feat(pasture): bound the raw import at the WorkflowResult tier
- feat(audit): stamp payload blobs with a written-at time (schema v8)
- feat(pasture): wire the capture sink into the lifecycle hook
- feat(ingress): add the clearance procedure with its enforced steps
- feat(ingress): share the payload refusals and label rows by identity policy
- feat(handlers): add the hardened in-binary capture sink
- feat(codegen): OpenCode activation report file; capability and evidence columns on all three reports
- feat(activation): harness-parameterised corpus evaluator with per-harness version admission
- feat(activation): per-harness target files with generated proof arms in per-harness ordinal ranges
- feat(acceptance): capture sidecars carry event, redaction and clearance; clearance is a committed path
- feat(lifecycle): count the orphan payload blobs, and say what the number means
- feat(lifecycle): make the hook deadline tier an in-process parameter, and pin the production call site
- feat(timeouts): a five-second deadline for one whole lifecycle hook invocation
- feat(hook): parse the lifecycle hook environment behind an injected seam
- feat(lifecycle): hostexit, the exit authority of the lifecycle hook
- feat(lifecycle): a blocking exit code must cite its host evidence

### Fixed
- fix(opencode): bind the embedded target descriptor to the production 2.0.20 contract
- fix(opencode): load v2 plugin without runtime package import
- fix(opencode): carry only release-shaped host versions from the v2 plugin
- fix(opencode): emit the evidenced deny refusal from EncodeOpenCode and correct v2 test precision
- fix(opencode): accept the v2 version banner and carry the host version on every hook
- fix(opencode): apply review findings on the v2 permission deny wiring
- fix(opencode): correct v2 bus envelope, raw version routing, install floor, stub pin
- fix(opencode): address V2-A review findings on the 2.0.20 contract
- fix(s3r): address review on deterministic byte-limit proof
- fix(s3r): make the byte-limit proof deterministic at the handler layer
- fix(dbconn): normalize a leading // so the file: URI opens the named file
- fix(dbconn): route every raw DSN open through the escaping layer
- fix(dbconn): escape URI delimiters in the database path
- fix(gate): report the stage the store read named for a subtype fault
- fix(gate): pin the claim-lease release, the transfer residual, and the fault text
- fix(codex): retain installed runners across compatible host updates
- fix(lifecycle): query native Codex and OpenCode versions by default
- fix(opencode): select occurrence-local version metadata
- fix(lifecycle): discover Claude from optional hint or first PATH executable
- fix(cli): join abandoned lifecycle test workers
- fix(tasks): close the tracker-owned audit database pool
- fix(lifecycle): integrate CLI contract and provenance corrections
- fix(hooks): preserve caller version provenance through raw ingestion
- fix(claude): tolerate additive lifecycle payload members
- fix(lifecycle): carry precise capture refusal causes
- fix(hooks): query the explicit Claude executable version
- fix(pasture): harden rebuild-index operator path
- fix: preserve cancellation causes and share consultation kind
- fix(tasks): atomically guard review action authority
- fix(ingress): admit authentic FileChanged member
- fix: record every batch assignment in its atomic operation
- fix(codex): remove unpinned catalogue prose
- fix(lifecycle): the Codex catalogue is proved to be a function of the rows it reads
- fix(lifecycle): the Codex divergence subject names the stale-manifest state once
- fix(lifecycle): the Codex failure read is proved on a constructed row
- fix(lifecycle): the Codex divergence messages name the state they fire in
- fix(lifecycle): the Codex catalogue refusal counts no callers
- fix(lifecycle): the Codex pins read the state instead of assuming it
- fix(lifecycle): the Codex catalogue refusal states the cost it can reach
- fix(lifecycle): correct the Codex SessionEnd and Interrupt host citations
- fix(lifecycle): the Codex catalogue reads its failure mode from the runtime profile
- fix(runtime): narrow the instruction guard to numbered tracking references
- fix(runtime): drop a leftover claim word from the Codex skill instruction
- fix(runtime): state what each contract binds instead of what the host exposes
- fix(codegen): stop the generated Codex agent header claiming the host has no skill or spawn function
- fix(codex): state the true event counts and the true withholding mechanism
- fix(lifecycle): record the Claude model-switch hook as the gate the host says it is
- fix(opencode): spell installation.update-available as the host emits it
- fix(codegen): default-export the object OpenCode's plugin loader reads
- fix(handlers): join the two sentences of the orphans note with a space, and pin the join
- fix(audit): stamp pre-existing payload blobs with the migration instant; an unknown age is never reclaimed
- fix(receipt): accept a delivery context with no deadline; bound production writers by a derived guard
- fix(ingress): read the published redaction shape and name what the exemption counts
- fix(pasture): prove the stalled-stdin bound on the binary and classify the capture test file
- fix(engine): report a cancelled look as cancelled, pin the real clock's context arm, cap the scripted looks, and state the stable rule exactly
- fix(tasks): let two daemons register the built-in agents at once
- fix(audit): retry the open's first statements when another process holds the file
- fix(engine): wait out a durable layout another process is writing instead of refusing it as an older build's
- fix(lifecycle): give the abandonment proof a deadline it trips itself, so no clock orders the expiry against the commit
- fix(codegen): read every callee shape in the Generate gate order guard and every letter case of the Git hooks-path key
- fix(lifecycle): recognise every task-id prefix over the floor, pin the project's as a fact, and make six guards and sentences say what they read
- fix(lifecycle): close the last round's findings on every axis, and pin what a re-read could miss
- fix(lifecycle): widen each guard along every axis, not the one that was probed
- fix(lifecycle): make each derivation as wide as the list it replaced
- fix(lifecycle): re-derive the durable region from its writes, not from a line
- fix(lifecycle): make every claim as wide as its evidence, and no wider
- fix(lifecycle): claim only what the region, the route and the parser support
- fix(hostexit): a delivery that was written must not be reported as lost
- fix(lifecycle): an event that could not be bound is not a decision
- fix(opencode): forward the diagnostic the plugin sends the operator to read
- fix(lifecycle): make every population sentence name the reader that holds it
- fix(opencode): offer the fault record, do not promise it
- fix(lifecycle): read the fault writer over the populations its promises need
- fix(lifecycle): pin the exit ARM, and finish the fault writer's sweep
- fix(lifecycle): report the fault record that has no directory to sit in
- fix(lifecycle): give the second exit-1 arm to the exit authority
- fix(lifecycle): give both exit-1 arms the same, narrowed claim
- fix(lifecycle): make the durable fault record true on both of its arms
- fix(lifecycle): make the unclassifiable-fault surfaces true for their reader
- fix(hostexit): give ForFault back its documentation, and guard the attachment
- fix(lifecycle): record the declared mode and name the unusable fault inputs
- fix(lifecycle): tell a blocking gate with no citation the reason it can act on
- fix(lifecycle): tell an operator who opted into fail-closed why the event continued, and stop claiming the generated plugin throws
- fix(lifecycle): deliver the orphan note on the text output and correct two operator refusals
- fix(lifecycle): an OpenCode observation fault says no more than its success
- fix(hook): a fail-open fault lets the host continue on every harness
- fix(hook): the lifecycle hook exit follows the declared failure mode

### Changed
- refactor(tasks): finish the retired-recovery rename and name refusals by function
- refactor(tasks): re-home assignment authentication and drop the retired index write path
- refactor(hostcontractgen): derive generated file and function names from Contract.Version
- refactor(runtime): split the lifecycle profiles into one file per harness
- refactor(lifecycle): one failure-mode vocabulary for the whole tree

### Documentation
- docs(opencode): record the landing pull request URL for the 2.0.21 batch
- docs(opencode): record the user's ACCEPT for the 2.0.21 session.created fixture
- docs(opencode): record the landing pull request URL for the accepted 2.0.20 batch
- docs(opencode): record the user's ACCEPT for the 2.0.20 clearance batch
- docs(audit): put the v8 to v9 cost on the operator surfaces and drop the impossible remedy
- docs: distinguish atomic commits from integration gates
- docs(claude): record filechanged publication PR
- docs(activation): state the cited-absence rule on the not-emitted arm
- docs(changelog): say where the host version admission floor is judged
- docs(opencode): record that the shipped plugin differs from the capture kit
- docs(changelog): record the host-version move, the recaptured corpus and the new registrations
- docs(audit): the migration list ends at version 8, and the ceiling is stated as the constant, not as a number in prose
- docs(receipt): state what the clockless test deadline does not do, and the measured two-clock number
- docs(lifecycle): fix a residual unqualified 'nothing to observe' sentence in the pin's doc comment
- docs(lifecycle): narrow the seam doc's fail-invisibly claim to the barrier, and fix the abandonment helper's clock sentence
- docs(lifecycle): name every route that loses the fault record, and count them
- docs(lifecycle): make the record's promise true, and make its guard see placement
- docs(hostexit): write the unusable-input trigger once, and pin both ends
- docs(agents): say where the unusable-input sentences may and may not be keyed
- docs(hostexit): say why the unusable-input result is a sentence, not a typed value
- docs(lifecycle): name what turns each assertion of the derived walk red
- docs(lifecycle): name the mutation that turns the refusal table red
- docs(lifecycle): stop the last comment claiming the generated plugin throws
- docs: announce the orphan count in the CHANGELOG and record the two injected seams of the hook command
- docs(lifecycle): the release note must follow the observation arm too
- docs(lifecycle): say what a fail-open fault writes, and what it may contradict later
- docs(lifecycle): state the two unreclaimed surfaces and pin the manifest divergence
- docs(timeouts): describe the five tiers truthfully and pin the two budget lists

### Other
- Merge pull request #158 from dayvidpham/pasture-o05p1y--feat--opencode-sessioncreated
- test(opencode): remove unreachable wantResolved banner-test branch
- test(opencode): clear 2.0.21 session.created capture (acceptance owed)
- Merge pull request #156 from dayvidpham/integration/rp2tju-opencode-v2
- merge(rp2tju): switch the production runtime contract to OpenCode 2.0.20 (5d28fe1)
- merge(rp2tju): enable the nine cleared OpenCode 2.0.20 coordinates with capture and Bun-driven production proofs (d4d4f18)
- test(opencode): bind the 2.0.20 production proof to its activation citations
- merge(rp2tju): clear nine OpenCode 2.0.20 captures and record the user's acceptance (1addc10)
- merge(rp2tju): share the no-runtime-import guard and scan every import form (ca075df)
- test(opencode): share no-runtime-import guard and scan all import forms
- merge(rp2tju): load the OpenCode v2 plugin without a runtime package import (a80d1b4)
- merge(rp2tju): stage v2 activation as a data flip and emit the evidenced deny refusal (e40354a)
- merge(rp2tju): carry the OpenCode v2 host version on every hook, and accept the real v2 banner (dad1542)
- merge(rp2tju): enforce pasture Denial through the v2 permission evaluate hook (b91c2ed)
- merge(rp2tju): regenerate the OpenCode transport as a v2 plugin (54fb407)
- merge(rp2tju): derive the OpenCode 2.0.20 host contract (69ffd70)
- Merge pull request #155 from dayvidpham/integration/aajt22-s3r
- merge(aajt22): make the byte-limit proof deterministic at the handler layer (b2fcc23)
- merge(aajt22): CI runs the test suite once, without the race detector (d3015fc)
- ci: run the test suite once, without the race detector, for fast iteration
- merge(aajt22): CI runs without the race detector by default for fast PR iteration (3ae81b4)
- ci: turn the race detector off by default so PR iteration is fast
- merge(6ch2wv): isolate the release tests from operator git signing config (16c6b63)
- test(release): isolate release tests from operator git signing config
- merge(o9d9fn): escape DSN paths, route every open through dbconn, normalize leading // (2d62538, bc0ba72, 6b00e87)
- merge(s3r): close the S3-R7 review residuals (7d98fa4, e2b8c9a)
- test(pasture): correct the reason/kind entailment comment
- test(pasture): derive the byte-limit token and drop one entailed reader assertion
- merge(s3r): three-harness gate host proofs, reader re-baseline and cost report (1380ea5)
- test(pasture): delete subsumed reader assertions
- test(pasture): remove assertions that cannot fail independently
- test(pasture): harden the reader host proofs after round-1 review
- test(pasture): record a measured built gate-invocation cost without a ceiling
- test(pasture): prove the gate reader on every harness and re-baseline the consultation reason
- merge(s3r): drop the write-only refused flag from the fake reader (c4ddb2b)
- test(handlers): drop the write-only refused flag from the fake reader
- merge(s3r): drop the derived refused assertion and pin the deny-path lease (65f0b42)
- test(handlers): drop the derived refused flag and assert the deny-path lease
- merge(s3r): harden the fault-row guard and merge the gate-proof seed helpers (90d3a92)
- test(handlers): pin the fault-row reader state and merge the gate-proof seed helpers
- merge(s3r): wire the gate Reader into the lifecycle hook (db4eb42)
- merge(s3r): re-pin Pasture to Provenance v0.3.0, drop the replace (6d5b7df)
- chore(deps): pin provenance v0.3.0
- chore(deps): point the provenance replace at 0fdb7a4 (wire-guard boundary floor)
- merge(s3r): B-6b assert the superseded owner's empty answer (e1a28b8)
- test(gate): assert the superseded owner's empty answer
- merge(s3r): A-1 refuse a statement-less func literal instead of dereferencing it (cf5a82e)
- test(tasks): refuse a statement-less func literal instead of dereferencing it
- merge(s3r): B-4 drive every driver refusal and every literal (3e79e50)
- chore(deps): point the provenance replace at b523af2 (per-column wire guard + occupant budget)
- test(tasks): drive every driver refusal and every literal in the authentication file
- merge(s3r): A1 consumer half - report the stage the store read named (541b26a)
- merge(s3r): fix round 1 - pasture-aajt22--refactor--write-path-cleanup
- merge(s3r): fix round 1 - pasture-aajt22--feat--gate-reader
- merge(s3r): fix round 1 - pasture-aajt22--feat--audit-v9-migration
- chore(deps): point the provenance replace at the fixed wire-guard commit 8cbc447
- merge(s3r): resolve assignment_authentication_test.go (keep cleanup suite, add seedFeasibilityEpisode)
- merge(s3r): read-side active-owner Reader (v4j9f8)
- merge(s3r): audit v8->v9 migration (nk1kw2)
- merge(s3r): gate authority API contract (1dwnlm)
- Merge pull request #153 from dayvidpham/pasture-ul61mp--feat--independent-harness-integration
- test(lifecycle): restore executable Claude event proof locators
- test(lifecycle): align installer and missing-host oracles with discovery
- test(opencode): isolate the real storage fault version query
- test(opencode): prove installed plugin survives compatible updates
- test(opencode): control executable observations in CLI proofs
- test(lifecycle): preserve event proof paths and refuse missing native identities
- test(lifecycle): count every selected executable query
- test(lifecycle): prove default discovery shares process and pipe bounds
- test(lifecycle): update activation-dependent floor and hold assumptions
- test(claude): preserve activation negatives after FileChanged enablement
- test(lifecycle): prove unenforced decision receipt agreement
- Merge pull request #152 from dayvidpham/pasture-b40x22--feat--hook-compatibility
- test(lifecycle): pin exact unbindable payload advice
- test(hooks): classify capture and private version pipe reads
- test(lifecycle): include new subjects in scope audit
- test(hooks): update the current lifecycle help contract
- test(cli): declare race-instrumented proof families
- test(lifecycle): account for host version refusal route
- Merge pull request #151 from dayvidpham/pasture-6s9lie--feat--outcome-harness-integration
- Merge pull request #150 from dayvidpham/pasture-eubbuk--fix--atomic-review-authority
- wip: checkpoint assignment index recovery
- chore(pasture): integrate recovery branch with main
- Merge pull request #149 from dayvidpham/pasture-f4vf0a--chore--provenance-patch-pin
- chore: pin Provenance v0.1.1 producer-field repair
- Merge pull request #148 from dayvidpham/pasture-g7jmun--test--filechanged-fixture
- test(claude): publish filechanged fixture with matcher metadata
- chore(pasture): pin provenance v0.1.0
- Merge pull request #146 from dayvidpham/slice/rp2tju-s5
- Merge pull request #147 from dayvidpham/slice/rp2tju-s4
- test(codex): accept sanitized lifecycle fixtures
- test: accept claude fixture clearance
- test(tasks): pin what a transfer must leave behind
- Merge pull request #145 from dayvidpham/fix/withholding-not-exercised
- Merge pull request #144 from dayvidpham/fix/withholding-arms
- test(tasks): keep the backfill-cost probe cheap and true to what it measured
- test(tasks): probe that the pinned journal module supports the gate read model
- Merge pull request #143 from dayvidpham/slice/rp2tju-s2
- test(pasture): let the hook-guard and capture-refusal tests run outside a Git checkout
- test(projection): keep the frozen legacy record at the host version that wrote it
- test(handlers): skip the real-git remote test where the repository has no origin
- test(acceptance): derive the sample host versions from the contract
- test(pasture): read the OpenCode host version from the contract, not a retired number
- test(codegen): derive the never-drops enabled floor from the activation manifests
- test(codegen): hold the enabled events of every harness at their floor by name
- test(ingress): hold every committed corpus file to the home-path placeholder, and state home-path-v1's spellings
- test: derive the capture file name and two sample versions from the registration
- test: read schema identities and host versions from the registration manifests
- test: restate host-version literals as reads of the recorded runtime contract
- test(codegen): derive the generated-output inventory from the emitters
- test(pasture): pin the second built-binary refusal on the shared lookup text
- Merge pull request #141 from dayvidpham/slice/rp2tju-s1
- test(tasks): make the opener guard check what its name claims: the options value each opener passes is rooted in the production defaults
- test(ingress): state the free-text exemption count and how it is counted beside the list
- test: state the mechanism, not the way this repository is worked on, in six comments (corrects d1feeb0, 06df190, 3710f49, d9a2382 and 53fb6ee)
- test(ingress): read sidecar redaction through the provenance parser
- test(waist): add the permanent structural guard on L2 content
- test(pasture): state the schema-ceiling derivation as a mechanism in the comment (corrects f7556a7 and 73ea25b)
- test: sweep of hard-coded audit schema ceilings in test files (addendum to f7556a7)
- test(pasture): derive the "newer than this build knows" schema version from the constant
- test: reusable enum-sync helper; proof enums and withheld reasons mirrored both ways
- Merge pull request #129 from dayvidpham/fix/scenario-test-names
- test(audit): make the two-daemon race test wait on its condition and refuse any loser that is not a safe loss
- test(audit,tasks): name eight scenario-numbered tests after the behaviour they prove, and strip process references from their files
- Merge pull request #128 from dayvidpham/fix/rp2tju-s0-deadline-flake
- test(cmd/pasture): make the serial pin see methods and its writes rule, and tell the truth about raw's race coverage
- test(cmd/pasture): say in source why each serial test stays serial, and narrow the child-binary race claim
- test(cmd/pasture): restore the shared cobra tree after in-process help and hook runs
- ci: bound go test with -timeout 30m on the race steps
- test(cmd/pasture): run every test that owns its state in parallel
- test(cmd/pasture): build one shared child binary per test process and drop -race from it
- Merge pull request #127 from dayvidpham/slice/rp2tju-s0-failure-modes
- test(lifecycle): derive each guard's population, or say what it reads
- test(lifecycle): make each guard read the population its own sentence claims
- test(hostexit): match the cited test name whole, not as a substring
- test(lifecycle): pin the STREAM every fault-writer arm speaks on
- test(lifecycle): derive the declared-mode guard population from the catalogs
- test(lifecycle): walk the whole repository for a second caller of the orphan count
- test(lifecycle): name the mutation of the two pins that did not carry one
- test(lifecycle): pin the orphan-count help bytes, and make the recapture escape real
- test(lifecycle): follow the OpenCode observation arm into the built-binary proof
- test(lifecycle): make three unmutatable claims structural
- test(codegen): guard the surfaces this work must never touch
- style(codegen): gofmt the two files the earlier commits of this slice touched

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
- OpenCode lifecycle generation now joins enabled activation rows to registered
  events and their runtime surfaces instead of selecting two callbacks by hand.
  The event listener forwards only enabled observations; named callbacks preserve
  host-owned objects without writing to them. The production set is still two
  enabled events out of 47 registered, both with response capability `None`.
  This generator change does not clear new captures or prove host denial.

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
- The generated Codex registration manifest states the failure mode the Codex
  runtime profile holds, because the Codex host-contract catalogue now READS
  that field from the profile instead of declaring it independently. Nothing a
  host sees changes: the hook path already read the profile. Other catalogue
  properties remain independently declared.
- The three internal failure-mode vocabularies are now one. Two of them folded
  six native behaviours into two, so a generated OpenCode manifest labelled a
  plugin throw as a Claude exit-2 block. OpenCode rows now carry their real
  behaviour. No Claude Code or Codex value changes.
- The audit database floor moves to version 9. The step removes the six
  bookkeeping tables, their index, and the two triggers behind the retired gate
  assignment index — an index of started assignments, a build watermark, and
  some rebuild scratch space — and keeps every session claim. No task,
  assignment, audit event, or session claim is read, copied, or rewritten, so
  there is nothing to rebuild afterwards. The cost is that version 9 is a floor
  that only moves up: once your file has been upgraded, ANY older `pasture`
  binary refuses the whole database for EVERY command — tasks, epochs, hooks,
  the daemon, and `pasture migrate` itself — and says the file was written by a
  newer pasture than that build supports. The upgrade runs automatically the
  first time a build with this release opens the file, so nothing asks you
  first; `pasture migrate --dry-run` prints the step and this consequence
  before it is applied. The upgrade is not reversible: there is no shim and no
  compatibility mode, and the way back is a newer binary, never a
  hand-downgraded file.

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
