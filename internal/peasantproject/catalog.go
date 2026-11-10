package peasantproject

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	// sessionCatalogTimeout bounds one id-list read. The catalog is optional
	// decoration on the session table: a slow Peasant database must not hold
	// the request open for the whole serve budget.
	sessionCatalogTimeout = 5 * time.Second

	// sessionCatalogLimit is the most session ids one database catalog will
	// return. A larger result is refused rather than truncated, because the
	// transcript route treats a database catalog as complete.
	sessionCatalogLimit = 100000

	// sessionCatalogCacheTTL is how long one successful id list is reused. It
	// is a process-local reuse window, not a cancellation budget.
	sessionCatalogCacheTTL = 30 * time.Second
)

// sessionIDCache remembers one successful catalog. Failures are not cached, so
// a missing file or a locked database is retried on the next request.
type sessionIDCache struct {
	mu    sync.Mutex
	ids   []string
	at    time.Time
	valid bool
}

func (c *sessionIDCache) get(now time.Time) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || now.Sub(c.at) >= sessionCatalogCacheTTL {
		return nil, false
	}
	return cloneSessionIDs(c.ids), true
}

func (c *sessionIDCache) put(ids []string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = cloneSessionIDs(ids)
	c.at = at
	c.valid = true
}

func cloneSessionIDs(ids []string) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

// SessionIDs lists every ingested Peasant session id. The query is read-only:
// the handle is opened with mode=ro and query_only, and this method never
// migrates or writes the file. A successful list, including an empty one, is
// reused for sessionCatalogCacheTTL.
func (s *dbSource) SessionIDs(ctx context.Context) ([]string, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: no Peasant database source is configured", ErrSessionCatalogUnavailable)
	}
	if ids, ok := s.sessionIDs.get(time.Now()); ok {
		return ids, nil
	}
	ids, err := s.querySessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	s.sessionIDs.put(ids, time.Now())
	return cloneSessionIDs(ids), nil
}

func (s *dbSource) querySessionIDs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, sessionCatalogTimeout)
	defer cancel()

	db, problem := s.handle(queryCtx)
	if problem != "" {
		if err := queryCtx.Err(); err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// markFailure treats a done context as caller cancellation and leaves
		// status unchanged. Pass the caller context, not the 5s query deadline:
		// a timeout is a source fault, and the integration pill must show it.
		s.status.markFailure(ctx, problem)
		return nil, fmt.Errorf("the Peasant database catalog could not be opened: %s; check that the Peasant database exists and is readable, then retry", problem)
	}

	rows, err := db.QueryContext(queryCtx, `SELECT session_id FROM sessions`)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		s.status.markFailure(ctx, "the Peasant session catalog could not be read: "+err.Error())
		return nil, fmt.Errorf("reading session ids from the Peasant database at %q: %w; the catalog was not served, retry or check that the file is readable and still has a sessions table", s.path, err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			s.status.markFailure(ctx, "a Peasant session id could not be decoded: "+err.Error())
			return nil, fmt.Errorf("decoding a session id from the Peasant database at %q: %w; the catalog was not served", s.path, err)
		}
		if !id.Valid || id.String == "" {
			continue
		}
		if _, ok := seen[id.String]; ok {
			continue
		}
		if len(ids) == sessionCatalogLimit {
			s.status.markFailure(ctx, fmt.Sprintf("the Peasant session catalog has more than %d sessions", sessionCatalogLimit))
			return nil, fmt.Errorf("the Peasant database at %q has more than %d ingested sessions; Pasture refuses a partial catalog so later sessions are not marked as having no transcript", s.path, sessionCatalogLimit)
		}
		seen[id.String] = struct{}{}
		ids = append(ids, id.String)
	}
	if err := rows.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		s.status.markFailure(ctx, "iterating Peasant session ids failed: "+err.Error())
		return nil, fmt.Errorf("iterating session ids from the Peasant database at %q: %w; the catalog was not served, retry", s.path, err)
	}
	sort.Strings(ids)
	s.status.markOK()
	return ids, nil
}

var (
	_ SessionCatalog = (*dbSource)(nil)
	_ SessionCatalog = (*CachedSource)(nil)
	_ SessionCatalog = (*Switch)(nil)
)
