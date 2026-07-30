package budget

import (
	"context"
	"database/sql"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	_ "modernc.org/sqlite"
)

// HoldWriter is deterministic load-test instrumentation. Production ingress
// never calls it; it exists so the honest-failure case does not depend on a
// scheduler race to keep SQLite's writer lock occupied.
func HoldWriter(ctx context.Context, dbPath string, held chan<- struct{}, release <-chan struct{}) error {
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS lifecycle_budget_lock (id INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	close(held)
	select {
	case <-release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return tx.Commit()
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
