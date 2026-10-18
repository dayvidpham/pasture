# Lifecycle fixture setup

Measured on this worktree, not carried from the release logs. The release
diagnosis remains at `/tmp/opencode/pasture-v0.0.21-release-diagnosis.md`. Those
CI package times are not restated here as new measurements. No calibration
factor was measured, so these numbers are not scaled.

## What changed

`initializeLifecycleTestDatabase` used to call `OpenTaskTracker` and `Create`
on every case. It now copies one process-local template. The template is built
once, through that same opener and `Create`, then closed, checkpointed, and
published read-only. Each case gets a new file in its own directory. The copy
is not a hardlink. Later commands still open the copy with the production
opener. Migrations are not skipped on that open. The template build is inside
the measured process; it is not subtracted.

Discovery scripts were not shared. Each case writes its own marker and path.
That payload is the assertion, not repeated neutral setup. No new CPU profile
was taken. The release stack that named `seedMLModels` inside this helper is
carried attribution, not a speedup number.

## Left on the real opener

These subjects still do not receive a prepared file:

- `TestInvalidLifecycleInvocationCreatesNoDatabase` — the path must not exist.
- `TestLifecycleListRejectsCursorBeforeDatabaseOpen` — refusal before open.
- `TestLifecycleTestInvocationJoinsEarlyHoldFailure` — withheld activation must
  not open a store.
- `TestRawSchemaVersionRefusalCreatesNoDatabaseFile`,
  `TestRawMalformedStdinCreatesNoDatabaseFile`, and the other raw tests that
  assert no database file. They never call the helper.
- `migrate_v8_v9_cli_test.go` and `integration_test.go` — fresh create and
  migration. They do not use this helper.
- `hook_lifecycle_ledger_pin_test.go` uses `initializeWarmLaneTestDatabase`.
  The ledger advance is that test's subject. It was not converted.
- `initializePostCommitTestDatabase` still publishes the caller's contract
  after the copy. That publication is not part of the neutral template.
- Lane, hold, and cancellation tests that need an initialized store copy the
  template, then exercise the real opener. The early-refusal case above does
  not.

## Focused cohort

Same command before and after. `GOMAXPROCS` was unset, so it defaulted to
`nproc` 32. `-parallel` was not set. Go 1.26.9, `GOWORK=off`,
`CGO_ENABLED=1`, `GOTOOLCHAIN=local`. Build cache
`/home/minttea/.cache/go-build` was warm after one discarded
`go test -race -c` on the baseline tree. The after run also had that warm
cache, plus the one template build inside the process.

```
go test -race -count=1 -timeout 10m -json \
  -run '^(TestClaudeDefaultDiscoverySelectsOneExecutable|TestGeneratedClaudeCommandRetainsObservedVersionAndCaptureName|TestLifecycleVersionSourceReachesDurableAndFaultRecords)$' \
  ./cmd/pasture
```

The three tests call `t.Parallel`. Their elapsed sum is packed occupancy, not
focused serial cost. Wall and user/sys are the process totals from GNU time 1.10.

| | baseline `6c7d24e` | after, same tree plus this change |
|---|---:|---:|
| UTC start | 2026-10-18T07:10:47Z | 2026-10-18T07:14:06Z |
| load at start | 3.20 1.66 1.06 | 1.67 1.63 1.17 |
| wall | 34.09s | 13.14s |
| user | 61.14s | 20.65s |
| sys | 5.38s | 2.15s |
| CPU percent | 195% | 173% |
| sum of the three top-level elapsed values | 49.790s | 23.470s |
| exit | 0 | 0 |

Per-test elapsed, packed inside that one process:

| test | before | after |
|---|---:|---:|
| `TestClaudeDefaultDiscoverySelectsOneExecutable` | 28.820s | 10.170s |
| `TestGeneratedClaudeCommandRetainsObservedVersionAndCaptureName` | 6.230s | 4.170s |
| `TestLifecycleVersionSourceReachesDurableAndFaultRecords` | 14.740s | 9.130s |

Load was lower on the after run. User+sys fell from 66.52s to 22.80s, which is
work in the process, not only a shorter queue. This is a local 32-core box, not
a CI runner, and it does not prove the release host's CPU count.

## Package attempt

One `cmd/pasture` race package was run after the first conversion and before
the parent-directory fix:

```
go test -race -count=1 -timeout 30m -json ./cmd/pasture
```

Start 2026-10-18T07:14:44Z, load 2.10, `GOMAXPROCS` unset, `-parallel` unset.
GNU time: wall 5:43.65, user 787.73s, sys 43.58s, 241% CPU, exit 1. The test
binary reported 342.63s and failed three raw parity tests because `CopyFile`
did not create a missing parent directory. The production opener does. That
was fixed. Those three tests, the new guards, and the named absent-path tests
then passed together in 10.524s. The full package was not run again.

`go test -list '^Test' ./cmd/pasture` gained only:

- `TestConcurrentLifecycleCopiesDoNotShareWrites`
- `TestLifecycleCopyGuardRejectsASharedInode`
- `TestLifecycleTemplateFingerprintRejectsMutation`

No previous test name was removed. The list command was not a race wave.
