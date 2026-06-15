package engine_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	cleanup, err := testutil.SetHermeticEnv("pasture-engine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine tests: hermetic env setup failed: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()
	goleak.VerifyTestMain(m, testutil.GoleakOptions()...)
}
