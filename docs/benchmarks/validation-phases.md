# Required correctness and controlled performance

Both phases are release gates. Operator approval on 2026-10-16 separates
measurement from concurrent race-suite load; it does not relax any band or
remove any workload. Production remains 20 records / 25 ms.

Run the default pair on exactly one backend. The local pair and the Nix race
derivation execute the same default race-correctness suite and the same
controlled performance tests; a sitting must not run both. GitHub CI uses the
local backend as two dependent jobs: `Build & Test` runs race correctness and
recovery, then `Controlled performance` runs on a fresh runner after it. The
measured window therefore stays quiet, and a miss on a slow runner is retried
without rerunning the race suite. Release consumes that CI result rather than
running the pair again. Do not add a Nix race job to CI.
Prefer the Nix derivation when that sitting's release vehicle is the hermetic
builder and the local pair has not already passed on the same tree. An
interrupted Nix race build is not a pass, and it is not evidence that the
local backend failed.

Local backend: `GOWORK=off make test-race`. Its recipe invokes these
**sequentially**, including under `make -j`:

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

CI invokes that local pair in the two dependent jobs above. Release does not
run it a second time: after CI completes for a push to main, `release.yml`
verifies that the
trusted CI run for the exact commit has every job green, including the named
required jobs, and only then tags that commit, builds its binaries and
publishes. A missing, failed, cancelled, skipped, stale or foreign CI result
fails closed. Manual dispatch applies the same check to the current main
commit, which also recovers a pushed version whose tag was never created. The
tag lands on the newest CI-verified main commit carrying an untagged version,
not necessarily on the bump commit.
Pull-request CI remains the pre-merge proof.

Fixture-setup work behind this suite is measured in
[fixture-setup-performance.md](fixture-setup-performance.md).

Nix hermetic backend: `nix build .#checks.<system>.race --no-link` (on this
host, `x86_64-linux`). That one derivation runs both phases in order. It
depends on the installer and CGO-disabled check outputs, so those independent
builders finish before its measured window. `nix flake check` without
`--no-build` builds this same derivation; it is that backend, not a second gate
after local success. `nix flake check --no-build` evaluates only and clears
neither phase. All Nix builders use `GOWORK=off`. Both phases must still exit 0
inside the chosen backend. Bands stay 80 committed events/s, 250 ms p95, and
relative 1.25 where that ratio applies.

The Nix race derivation does not pass `-tags=pasture_experiment` or
`-tags=recovery`. Whichever default backend is chosen, these local proofs
remain required: tagged full race
(`PASTURE_TEST_PHASE=race-correctness CGO_ENABLED=1 go test -race -count=1
-timeout=30m -tags=pasture_experiment ./...`) and recovery race
(`CGO_ENABLED=1 go test -race -count=1 -timeout=30m -tags=recovery
./internal/engine/...`). Vet, CGO-disabled build, Python, generation/drift,
package builds, the CGO-disabled package check, and the installer check are
separate. They do not replace the default pair or the tagged/recovery proofs.
Run controlled performance only inside the chosen backend, after that backend's
race wave and other heavy builders have exited. Do not schedule a second
performance wave. Every log must identify the exact SHA/tree; old failures
remain preserved.
