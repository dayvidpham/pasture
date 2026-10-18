package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Both phases execute the same real store/daemon/hook workloads. Only timing
// assertions move out of the concurrent race wave; correctness never moves.
func performanceTimingEnabled(t *testing.T) bool {
	t.Helper()
	enabled, err := validationTiming(os.Getenv("PASTURE_TEST_PHASE"), underRaceDetector)
	if err != nil {
		t.Fatal(err)
	}
	phase := os.Getenv("PASTURE_TEST_PHASE")
	if phase != "" {
		if testing.Short() {
			t.Fatal("required validation phases refuse -short")
		}
		for _, entry := range os.Environ() {
			key, value, _ := strings.Cut(entry, "=")
			if (strings.HasPrefix(key, "PASTURE_LOADTEST_") || phase == "performance" && key == "GITHUB_ACTIONS") && value != "" {
				t.Fatalf("controlled performance phase refuses %s: use the unchanged default shape and unscaled enforced bands", key)
			}
		}
	}
	return enabled
}

func validationTiming(phase string, race bool) (bool, error) {
	switch phase {
	case "": // Ordinary ad-hoc invocations preserve the original checks.
		return true, nil
	case "race-correctness":
		if race {
			return false, nil
		}
	case "performance":
		if !race {
			return true, nil
		}
	}
	return false, fmt.Errorf("validation phase %q is incompatible with race=%t; run make test-race for both ordered required phases", phase, race)
}

func TestValidationPhasesRefuseWrongInstrumentation(t *testing.T) {
	for _, phase := range []string{"", "race-correctness", "performance", "typo"} {
		for _, race := range []bool{false, true} {
			enabled, err := validationTiming(phase, race)
			valid := phase == "" || phase == "race-correctness" && race || phase == "performance" && !race
			if (err == nil) != valid || valid && enabled != (phase != "race-correctness") {
				t.Fatalf("phase=%q race=%t: enabled=%t err=%v", phase, race, enabled, err)
			}
		}
	}
}
