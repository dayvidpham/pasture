//go:build race

package main

// underRaceDetector reports whether this test binary was built with -race.
// It exists so the band checks can tell instrumented measurement loops apart
// from the product behaviour they measure: the race detector slows THIS
// binary's in-process read/open loops ~20x (measured 2026-10-16), while the
// daemon and hook children are separate uninstrumented binaries whose
// metrics stay banded. See loadtest_under_race_norace_test.go.
const underRaceDetector = true
