package audit_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	cleanup, err := testutil.SetHermeticEnv("pasture-audit")
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit tests: hermetic env setup failed: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()
	goleak.VerifyTestMain(m, testutil.GoleakOptions()...)
}
