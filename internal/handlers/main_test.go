package handlers_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestMain(m *testing.M) {
	// Sample the goroutine baseline before any test runs; see
	// internal/testutil/goleak.go for why the order matters.
	checkLeaks := testutil.GoleakVerifier()
	cleanup, err := testutil.SetHermeticEnv("pasture-handlers")
	if err != nil {
		fmt.Fprintf(os.Stderr, "handler tests: hermetic env setup failed: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	if err := checkLeaks(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
