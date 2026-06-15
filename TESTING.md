# Pasture Testing Guide

This repo uses three separate testing concerns:

1. Fast unit and integration tests in normal `go test ./...`
2. Parallel-safe engine and CLI tests
3. Recovery and performance gates for DBOS execution

The testing strategy is:

- Put `t.Parallel()` first in tests that do not mutate shared process state.
- Do not use `t.Setenv` for path isolation. It is not compatible with parallel
  tests and it makes the isolation model ambiguous.
- Inject per-test paths and IDs directly into the code under test.
- Use `os.Setenv` in `TestMain` only for process-wide knobs such as `HOME`,
  `XDG_DATA_HOME`, and pool-size overrides.

## Parallel-safe pattern

Use direct injection instead of environment coupling:

- CLI tests pass `--db <tempdir>/pasture.db`
- engine tests pass `engine.Config.DBPath`
- parallel engine tests must also use distinct `ExecutorID` and
  `ApplicationVersion`
- shared fixtures should create their own temp directory per invocation

This keeps each test isolated without relying on global environment mutation.

If a test needs to mutate the current working directory, it should stay
serial. `t.Chdir` and `t.Parallel` do not mix with repository-root tests that
exercise git commands or relative paths.

## Hermetic environment

Process-wide setup belongs in `TestMain`, not in individual tests. The current
helpers use `os.Setenv` to redirect:

- `HOME`
- `XDG_DATA_HOME`
- pool-size knobs that should stay small in tests

Production defaults stay unchanged. The point is to make the test process
hermetic, not to change runtime behavior.

## Golden database and migration carve-out

Use the shared golden unified `pasture.db` fixture when a test needs a
pre-migrated database image.

Rules:

- use the golden copy for normal CLI and engine smoke tests
- do not use skip-migration helpers in migration-specific tests
- keep migration guards explicit so stale-schema coverage stays honest

The shared helper lives in `internal/testutil`.

## Memory and OOM profiling

The important failure mode for this repo is not CPU saturation, it is RSS
growth from parallel test execution and DBOS worker state.

When measuring memory:

- pin `GOMAXPROCS` to the CI core count used for the gate
- run the full suite with the same `-p` and `-parallel` settings used in CI
- capture peak RSS from `/proc/<pid>/VmHWM`
- watch for arena/cache growth in the daemon and recovery tests

## Time and contention profiling

Use the following tools when a suite regresses:

- `go test -run <pattern> -count=1 -v`
- `-blockprofile`
- `-mutexprofile`
- `-trace`
- `-cpuprofile`
- `-memprofile`

Prefer `-blockprofile`, `-mutexprofile`, and `-trace` first. They usually tell
you whether the wall time is real work or serialized waiting.

## Recovery and end-to-end tests

The recovery suite is a separate gate and should stay explicit.

```bash
make test-recovery
```

Build-tagged recovery tests live under `//go:build recovery`. They exercise
the DBOS recovery path and should run against a real on-disk SQLite database,
not `:memory:`.

## Local helpers

- `internal/testutil.SetHermeticEnv(prefix)` sets up temporary `HOME` and
  `XDG_DATA_HOME` values for a whole package.
- `internal/testutil.SetEnv(t, key, value)` and `UnsetEnv(t, key)` are for
  serial tests that need temporary env overrides without `t.Setenv`.
- `internal/testutil.GoleakOptions()` provides the shared goroutine leak
  ignore list for DBOS and SQLite background goroutines.
