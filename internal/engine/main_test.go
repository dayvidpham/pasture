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
	code := m.Run()
	cleanup()
	if err := goleak.Find(testutil.GoleakOptions()...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
