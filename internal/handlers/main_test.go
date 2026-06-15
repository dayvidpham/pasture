package handlers_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	cleanup, err := testutil.SetHermeticEnv("pasture-handlers")
	if err != nil {
		fmt.Fprintf(os.Stderr, "handler tests: hermetic env setup failed: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()
	goleak.VerifyTestMain(m, testutil.GoleakOptions()...)
}
