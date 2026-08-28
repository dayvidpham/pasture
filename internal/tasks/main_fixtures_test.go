package tasks_test

// main_fixtures_test.go exists for one job: delete the shared fixture databases
// this test binary built.
//
// A fixture that is built once per binary outlives the test that happened to
// build it, so t.Cleanup cannot remove it and TestMain is the only place that
// can. This package had no TestMain, and so left one database behind on every
// run — see internal/testutil/fixture_dirs.go for what that cost.

import (
	"os"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.RemoveFixtureDirs()
	os.Exit(code)
}
