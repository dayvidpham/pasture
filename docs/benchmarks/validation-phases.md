# Required correctness and controlled performance

Both phases are release gates. Operator approval on 2026-10-16 separates
measurement from concurrent race-suite load; it does not relax any band or
remove any workload. Production remains 20 records / 25 ms.

Run `GOWORK=off make test-race`. Its recipe invokes these **sequentially**,
including under `make -j`:

1. `make test-race-correctness`: full `go test -race -count=1 -timeout 30m
   ./...`, with `PASTURE_TEST_PHASE=race-correctness`. Every test still runs,
   including the two complete built-binary workloads below. Only their timing
   assertions move to phase 2. All process errors, empty probes, faults, store
   shape/read consistency and committed receipt accounting remain enforced.
2. `make test-performance`: non-race `go test -count=1 -timeout 30m -v
   ./cmd/pastured -run
   '^(TestEighteenConcurrentHooksThroughTheDaemonLaneIsTheAcceptanceRow|TestDisposableStoreLoadHarnessIsTheRegressionTripwire)$'`,
   with `PASTURE_TEST_PHASE=performance`. No concurrent race suite or independent
   heavy builder may run during this phase. Both tests are serial. Performance
   rejects `-short`, race instrumentation, report-only, shape/window/worker
   overrides and CI scaling. The target clears `GITHUB_ACTIONS`; it never
   changes the existing CI factor or constants. Default ad-hoc invocations
   without a phase keep their previous checks and exploratory controls.

The acceptance row retains 18 workers, the real CGO-free daemon/hook binaries,
the same fixture and initialized store, zero faults, absolute p95 <=250ms and
same-run direct median ratio <=1.25. The disposable-store row retains the same
600-task synthetic shape, 18 workers, 3-second burst, 10-second sustained run,
20 opens, 30 reads, zero faults, <=250ms hook p95, burst ratio <=5, >=80 committed
events/s, <=100ms read p95 and <=50ms steady-open p95. Copy-shape controls and
their original thresholds remain available in the original exploratory harness;
the required gate explicitly refuses a private source-store override.

Phase 1 retains the existing race-instrumented in-process work. Its children
remain the same CGO-free shipped shape as before; this is not a claim that the
child binaries were race-instrumented. The full race suite separately exercises
the lane, receipt, store and handler implementations with instrumentation.
Phase 2 additionally enforces the in-process read/open bands without detector
overhead. Neither a focused success nor phase 1 alone clears the release.

CI `Build & Test` and release `verify` both invoke the combined target; tag
creation depends on `verify`. Nix `checks.<system>.race` runs the two phases
in the same derivation in order. It depends on installer and CGO-disabled check
outputs, so `nix flake check` cannot schedule those independent builders over
its measured windows. Run `nix build .#checks.x86_64-linux.race --no-link` or
`nix flake check`; both phases must exit0. All Nix builders use `GOWORK=off`.

Additional release integration commands remain required: tagged full race
(`PASTURE_TEST_PHASE=race-correctness CGO_ENABLED=1 go test -race -count=1
-timeout=30m -tags=pasture_experiment ./...`), recovery race, vet, CGO0 build,
Python, generation/drift and actual packages. Run controlled performance only
after those race waves and other heavy builders have exited. Every log must
identify the exact SHA/tree; old failures remain preserved.
