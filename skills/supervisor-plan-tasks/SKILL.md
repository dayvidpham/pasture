# Supervisor Plan Tasks

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:supervisor:plan-tasks` — Decompose ratified plan into vertical slices (SLICE-N)

### Layer Cake — TDD Parallelism Within Vertical Slices

```text
Layer 0: Shared infrastructure (common types, enums — optional, parallel)
   │
Vertical Slices (parallel, each worker owns one slice):
   │
   ├─ Layer 1: Types for this slice (e.g. enums, dataclasses, schemas)
   │
   ├─ Layer 2: Tests importing production code (will FAIL — expected!)
   │
   ├─ ...  (additional layers as needed)
   │
   └─ Layer M: Implementation + wiring (makes tests PASS)
   │
IMPLEMENTATION COMPLETE

Each layer completes before the next begins.
Within a layer, all tasks run in parallel.

Key TDD principle:
  Layer 2 tests will fail initially — this is expected.
  Layer M workers implement code to make those tests pass.

L2 Test File Requirements:
  1. Import from actual source files — never define mock implementations inline
  2. Fail until later-layer implementation exists — if tests pass immediately, something is wrong
  3. Test behavior via DI mocks — mock dependencies, not the code under test
  4. Define expected API contracts — tests specify what the implementation should do

```
<!-- END GENERATED FROM aura schema -->

Hand-authored body goes here.
