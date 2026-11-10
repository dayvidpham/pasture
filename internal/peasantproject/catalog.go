package peasantproject

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
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
	mu        sync.Mutex
	ids       []string
	subagents []string
	at        time.Time
	valid     bool
}

func (c *sessionIDCache) get(now time.Time) (SessionCatalogList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || now.Sub(c.at) >= sessionCatalogCacheTTL {
		return SessionCatalogList{}, false
	}
	return cloneCatalog(SessionCatalogList{IDs: c.ids, Subagents: c.subagents}), true
}

func (c *sessionIDCache) put(listed SessionCatalogList, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	listed = cloneCatalog(listed)
	c.ids = listed.IDs
	c.subagents = listed.Subagents
	c.at = at
	c.valid = true
}

func cloneSessionIDs(ids []string) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

func cloneCatalog(listed SessionCatalogList) SessionCatalogList {
	if listed.IDs == nil {
		listed.IDs = []string{}
	}
	if listed.Subagents == nil {
		listed.Subagents = []string{}
	}
	return SessionCatalogList{IDs: cloneSessionIDs(listed.IDs), Subagents: cloneSessionIDs(listed.Subagents)}
}

// SessionIDs lists every ingested Peasant session id and the subset that has a
// parent. The query is read-only: the handle is opened with mode=ro and
// query_only, and this method never migrates or writes the file. A successful
// list, including an empty one, is reused for sessionCatalogCacheTTL.
func (s *dbSource) SessionIDs(ctx context.Context) (SessionCatalogList, error) {
	if s == nil {
		return SessionCatalogList{}, fmt.Errorf("%w: no Peasant database source is configured", ErrSessionCatalogUnavailable)
	}
	if listed, ok := s.sessionIDs.get(time.Now()); ok {
		return listed, nil
	}
	listed, err := s.querySessionIDs(ctx)
	if err != nil {
		return SessionCatalogList{}, err
	}
	s.sessionIDs.put(listed, time.Now())
	return cloneCatalog(listed), nil
}

func (s *dbSource) querySessionIDs(ctx context.Context) (SessionCatalogList, error) {
	if err := ctx.Err(); err != nil {
		return SessionCatalogList{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, sessionCatalogTimeout)
	defer cancel()

	db, problem := s.handle(queryCtx)
	if problem != "" {
		if err := queryCtx.Err(); err != nil && ctx.Err() != nil {
			return SessionCatalogList{}, ctx.Err()
		}
		// markFailure treats a done context as caller cancellation and leaves
		// status unchanged. Pass the caller context, not the 5s query deadline:
		// a timeout is a source fault, and the integration pill must show it.
		s.status.markFailure(ctx, problem)
		return SessionCatalogList{}, fmt.Errorf("the Peasant database catalog could not be opened: %s; check that the Peasant database exists and is readable, then retry", problem)
	}

	rows, err := db.QueryContext(queryCtx, `SELECT session_id, parent_id FROM sessions`)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SessionCatalogList{}, ctxErr
		}
		s.status.markFailure(ctx, "the Peasant session catalog could not be read: "+err.Error())
		return SessionCatalogList{}, fmt.Errorf("reading session ids from the Peasant database at %q: %w; the catalog was not served, retry or check that the file is readable and still has a sessions table", s.path, err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	ids := make([]string, 0)
	subagents := make([]string, 0)
	for rows.Next() {
		var id, parent sql.NullString
		if err := rows.Scan(&id, &parent); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return SessionCatalogList{}, ctxErr
			}
			s.status.markFailure(ctx, "a Peasant session id could not be decoded: "+err.Error())
			return SessionCatalogList{}, fmt.Errorf("decoding a session id from the Peasant database at %q: %w; the catalog was not served", s.path, err)
		}
		if !id.Valid || id.String == "" {
			continue
		}
		if _, ok := seen[id.String]; ok {
			continue
		}
		if len(ids) == sessionCatalogLimit {
			s.status.markFailure(ctx, fmt.Sprintf("the Peasant session catalog has more than %d sessions", sessionCatalogLimit))
			return SessionCatalogList{}, fmt.Errorf("the Peasant database at %q has more than %d ingested sessions; Pasture refuses a partial catalog so later sessions are not marked as having no transcript", s.path, sessionCatalogLimit)
		}
		seen[id.String] = struct{}{}
		ids = append(ids, id.String)
		if parent.Valid && strings.TrimSpace(parent.String) != "" {
			subagents = append(subagents, id.String)
		}
	}
	if err := rows.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SessionCatalogList{}, ctxErr
		}
		s.status.markFailure(ctx, "iterating Peasant session ids failed: "+err.Error())
		return SessionCatalogList{}, fmt.Errorf("iterating session ids from the Peasant database at %q: %w; the catalog was not served, retry", s.path, err)
	}
	sort.Strings(ids)
	sort.Strings(subagents)
	s.status.markOK()
	return SessionCatalogList{IDs: ids, Subagents: subagents}, nil
}

var (
	_ SessionCatalog = (*dbSource)(nil)
	_ SessionCatalog = (*CachedSource)(nil)
	_ SessionCatalog = (*Switch)(nil)
)
