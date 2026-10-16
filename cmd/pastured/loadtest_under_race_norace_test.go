//go:build !race

package main

// underRaceDetector reports whether this test binary was built with -race.
// See loadtest_under_race_race_test.go.
const underRaceDetector = false
