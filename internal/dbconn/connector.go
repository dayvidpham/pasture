package dbconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"modernc.org/sqlite"
)

// sharedConnector retains only configuration. Every pool expansion/replacement
// gets its own Connect context; no opening caller context survives publication.
// Driver.Open and Close have no context API. They are synchronous local-file
// operations; cancellation here bounds SQLite contention, not a stalled filesystem.
type sharedConnector struct {
	dsn     string
	profile timeouts.Profile
	driver  sqlite.Driver
}

const sharedSetupRetryCeiling = 30 * time.Second

func (c *sharedConnector) Driver() driver.Driver { return &c.driver }

func (c *sharedConnector) Connect(ctx context.Context) (conn driver.Conn, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	conn, err = c.driver.Open(c.dsn) // deliberately NO _pragma: modernc applies DSN SQL with Background.
	if err != nil {
		return nil, err
	}
	owned := conn
	defer func() {
		if err != nil {
			if ctx.Err() != nil {
				err = errors.Join(ctx.Err(), err)
			}
			err = errors.Join(err, owned.Close())
			conn = nil
		}
	}()
	exec := conn.(driver.ExecerContext)
	query := conn.(driver.QueryerContext)
	standing := c.profile.SQLiteBusy().Milliseconds()
	cap := c.profile.SQLiteBusy()
	if deadline, ok := ctx.Deadline(); ok {
		cap = min(cap, max(time.Until(deadline), 0))
	}
	statements := []struct {
		set, read string
		want      driver.Value
	}{
		{"PRAGMA busy_timeout=" + strconv.FormatInt(cap.Milliseconds(), 10), "PRAGMA busy_timeout", cap.Milliseconds()},
		{"PRAGMA journal_mode=WAL", "PRAGMA journal_mode", "wal"},
		{"PRAGMA synchronous=NORMAL", "PRAGMA synchronous", int64(1)},
		{"PRAGMA foreign_keys=ON", "PRAGMA foreign_keys", int64(1)},
		{"PRAGMA busy_timeout=" + strconv.FormatInt(standing, 10), "PRAGMA busy_timeout", standing},
	}
	for _, statement := range statements {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if _, err = exec.ExecContext(ctx, statement.set, nil); err != nil {
			if ctx.Err() != nil {
				err = errors.Join(ctx.Err(), err)
			}
			return nil, err
		}
		var rows driver.Rows
		rows, err = query.QueryContext(ctx, statement.read, nil)
		if err != nil {
			return nil, err
		}
		values := make([]driver.Value, len(rows.Columns()))
		err = rows.Next(values)
		closeErr := rows.Close()
		if err == io.EOF {
			err = fmt.Errorf("setting read-back returned no row")
		}
		if err = errors.Join(err, closeErr); err != nil {
			return nil, err
		}
		if len(values) != 1 || values[0] != statement.want {
			return nil, fmt.Errorf("dbconn.Connect: %s read-back %v does not match %v; connection not published; retry on a writable local SQLite store", statement.read, values, statement.want)
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return conn, nil
}

// NewSharedDBWithProfile creates a lazy owned pool whose setup SQL receives the
// context of each acquisition. Callers run their own context-aware first SQL.
func NewSharedDBWithProfile(path string, profile timeouts.Profile) (*sql.DB, error) {
	if err := profile.Validate(); err != nil {
		return nil, setupError(path, "validate configuration", err)
	}
	return sql.OpenDB(&sharedConnector{dsn: "file:" + encodeDSNPath(path) + "?_txlock=immediate", profile: profile}), nil
}

// OpenSharedDBContextWithProfile eagerly verifies the same pool used by audit,
// unified tasks and the durable engine, and closes it on initialization failure.
func OpenSharedDBContextWithProfile(ctx context.Context, path string, profile timeouts.Profile) (*sql.DB, error) {
	if ctx == nil {
		return nil, setupError(path, "validate context", fmt.Errorf("nil context; supply a live context"))
	}
	if err := ctx.Err(); err != nil {
		return nil, setupError(path, "before connection acquisition", err)
	}
	db, err := NewSharedDBWithProfile(path, profile)
	if err != nil {
		return nil, err
	}
	// journal_mode can return BUSY without invoking SQLite's busy handler.
	// Retry the fresh connection, never publish a partly configured one.
	ceiling := time.Now().Add(sharedSetupRetryCeiling)
	var lastErr error
	for {
		err = ctx.Err()
		if err != nil {
			break
		}
		if lastErr != nil && !time.Now().Before(ceiling) {
			err = lastErr
			break
		}
		err = db.PingContext(ctx)
		if err == nil {
			return db, nil
		}
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
			break
		}
		lastErr = err
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&255 != 5 && sqliteErr.Code()&255 != 6) || !time.Now().Before(ceiling) {
			break
		}
		timer := time.NewTimer(min(25*time.Millisecond, time.Until(ceiling)))
		select {
		case <-ctx.Done():
			timer.Stop()
			err = errors.Join(ctx.Err(), err)
		case <-timer.C:
			continue
		}
		break
	}
	if err != nil {
		return nil, setupError(path, "initialize connection", errors.Join(err, db.Close()))
	}
	return db, nil
}

func setupError(path, stage string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryConnection,
		What: "Couldn't initialize the shared SQLite connection.", Why: cause.Error(),
		Where:  fmt.Sprintf("dbconn connector for %q, stage %s (internal/dbconn/connector.go)", path, stage),
		Impact: "No configured connection was returned; the caller cannot use this pool.",
		Fix:    "Check storage permissions and lock contention, then retry with a live context and a validated timeout profile.", Cause: cause}
}
