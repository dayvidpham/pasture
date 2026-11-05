package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// dbosContextFactory constructs a durable context. It is a package variable
// shape so tests can inject a failing constructor.
type dbosContextFactory func(context.Context, dbos.Config) (dbos.Context, error)

// newDurableContext constructs the durable context and turns a failure into
// the actionable shape operators need.
//
// DBOS v1.8.0 made SQLite schema bootstrap safe when several processes open a
// new database at once: the version table is created with IF NOT EXISTS and each
// migration re-reads the version inside its own write transaction, so a losing
// process skips what the winner applied instead of colliding with it. The retry
// this function used to run for v1.2.0's probe-then-create race is gone with it.
func newDurableContext(ctx context.Context, newCtx dbosContextFactory, cfg dbos.Config) (dbos.Context, error) {
	dbosCtx, err := newCtx(ctx, cfg)
	if err != nil {
		return nil, DescribeDurableStartupFailure(engineConstructionSite, err)
	}
	return dbosCtx, nil
}

const engineConstructionSite = "Constructing the engine (internal/engine/engine.go in engine.New)."

// dbosMissingSQLiteDriverMarker is how the durable runtime refuses a binary
// that never linked its SQLite driver. The runtime keeps the driver in a
// registry that only the driver package's init populates, so a binary that
// drops the blank import compiles, links, and then fails at the first attempt
// to open a system database.
//
// Evidence in the pinned library version:
// dbos/internal/sysdb/sqlite_driver.go:47-53 (registeredSQLiteDriver) writes
// this message; dbos/internal/sysdb/sqlite_pool.go:72 and :141 are the two
// callers, and :141 runs even when the caller supplies its own handle, which is
// pasture's case. dbos/dbos.go:636-638 then re-wraps it as an initialisation
// error, so the text below arrives inside a *dbos.Error.
//
// Matching text rather than an error value is forced by the library: the
// message is built with fmt.Errorf and then flattened into the *dbos.Error
// message, and the library exports no sentinel for it. The match is coupled to
// the version pinned in go.mod and is re-checked by
// TestDBOSVersionPinMatchesRacePredicate.
const dbosMissingSQLiteDriverMarker = "SQLite support is not linked into this binary"

// dbosSQLiteDriverPackage is the package whose blank import registers the
// SQLite driver. It is named in the fix text below, and pinned in every binary
// that opens a system database by the blank-import guards in
// internal/engine/dbosinit_test.go and internal/handlers/controller_test.go.
const dbosSQLiteDriverPackage = "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite"

// isMissingDBOSSQLiteDriver reports whether err is the durable runtime's
// refusal of a binary that did not link the SQLite driver.
//
// This failure is permanent for the life of the process: no other process can
// repair it, and re-attempting it only repeats the same registry lookup. It
// must therefore never enter the lost-race retry above, and
// isDBOSBootstrapRace does not match it — the two predicates require different
// text and this one carries no migration wrapper.
func isMissingDBOSSQLiteDriver(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), dbosMissingSQLiteDriverMarker)
}

// DescribeDurableStartupFailure returns an actionable replacement for a
// durable-runtime start-up failure whose cause pasture can name, and returns
// err unchanged when it cannot name one. It returns nil for a nil error.
//
// where is the caller's own location, used for the Where line: the engine and
// the epoch controller both build a durable runtime over the same shared
// handle, and an operator needs to know which of them refused to start.
//
// Callers keep their own wrapping for the errors this function passes through.
func DescribeDurableStartupFailure(where string, err error) error {
	if err == nil {
		return nil
	}
	if isMissingDBOSSQLiteDriver(err) {
		return missingDBOSSQLiteDriverError(where, err)
	}
	return err
}

// missingDBOSSQLiteDriverError explains a binary that did not link the durable
// runtime's SQLite driver.
//
// The category is storage because that is what failed for the caller: pasture
// could not open its database. The repair, however, is a source change in this
// repository, not an operator action on the machine — the Fix line says so
// plainly, because an operator who reads "storage" alone would go looking at
// the file.
func missingDBOSSQLiteDriverError(where string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "This pasture build can't open a pasture database: its SQLite support is missing.",
		Why: "The durable-execution runtime finds its SQLite driver through a registry that only the\n" +
			"driver package fills in, and this binary never linked that package. Nothing in the code\n" +
			"names the package, so the compiler and the linker can't notice it is gone: the build\n" +
			"succeeds and the failure appears here, at the first attempt to open the database.",
		Where:  where,
		Impact: "No epoch can start or be inspected from this binary. No data was read or written.",
		Fix: "This is a defect in the build, not in the database or the machine — an operator can't\n" +
			"work around it.\n" +
			"1. Report it, with the version of pasture you are running:\n" +
			"     https://github.com/dayvidpham/pasture/issues\n" +
			"2. To repair it in a source checkout, add this blank import to the file that opens the\n" +
			"   database (internal/engine/engine.go for the engine, internal/handlers/controller.go\n" +
			fmt.Sprintf("   for lifecycle commands):\n     _ %q\n", dbosSQLiteDriverPackage) +
			"3. Rebuild, then run the tests in those two packages: they check that the import is\n" +
			"   present in each file that needs it.",
		Cause: cause,
	}
}
