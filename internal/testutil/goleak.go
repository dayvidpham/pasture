package testutil

import "go.uber.org/goleak"

// GoleakVerifier samples the goroutines that are already running, and returns a
// check that reports the goroutines started after that sample and never stopped.
//
// Call it at the TOP of TestMain, BEFORE m.Run, and call the returned check
// after m.Run. The order is load-bearing: goleak.IgnoreCurrent samples the live
// goroutine set when the OPTION IS BUILT, not when the check runs. Building the
// options after m.Run samples every goroutine the tests leaked, then adds all of
// them to the ignore list, so the check can never fail.
func GoleakVerifier() func() error {
	opts := []goleak.Option{
		// The pre-test goroutines: the test binary's own runtime and testing
		// machinery, which no test starts and no test can stop.
		goleak.IgnoreCurrent(),
		// database/sql starts one connection-opener goroutine per *sql.DB and
		// stops it only when that handle is closed. Test fixtures that open a
		// handle and let process exit release it therefore leave this goroutine
		// running: a full run of internal/handlers ends with roughly 95 of
		// them and internal/engine with a handful, both counts varying with
		// which tests interleave. Those unclosed fixture handles are a real
		// (pre-existing) test-hygiene debt, not a defect in the code under
		// test, and they are tracked with the rest of the durable-runtime work
		// in https://github.com/dayvidpham/pasture/issues/104. Ignoring them by
		// top function keeps the check sensitive to every other leak.
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	}
	return func() error { return goleak.Find(opts...) }
}
