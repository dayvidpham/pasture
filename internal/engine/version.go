package engine

// DefaultApplicationVersion is the pinned DBOS recovery COHORT marker.
//
// Role: DBOS filters crash-recovery by (ExecutorID, ApplicationVersion) and
// otherwise defaults the version to a per-build binary hash. Pinning a stable,
// build-independent value here is precisely what lets a REBUILT binary still
// recover epochs that an earlier build left in flight — without it, every
// rebuild would start a new cohort and silently orphan the previous build's
// in-flight epochs.
//
// Bump criteria: increment this ONLY on an incompatible change to the
// EpochWorkflow / EpochControlWorkflow shape that makes already-in-flight
// workflows non-resumable. A routine rebuild MUST NOT bump it (that would
// abandon in-flight epochs); after a deliberate bump, old in-flight workflows
// are resumed manually rather than auto-recovered.
//
// Cross-binary invariant: every pasture process that opens the engine — the
// local CLI (epoch start) and the daemon — MUST pin this same value (together
// with DefaultExecutorID and DefaultAppName). If the CLI and the daemon use
// different values, each silently fails to recover the other's in-flight
// epochs.
const DefaultApplicationVersion = "1"
