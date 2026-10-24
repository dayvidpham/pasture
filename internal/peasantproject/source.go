// Package peasantproject reads the Peasant-owned session-to-project mapping for
// Pasture's opt-in project enrichment.
//
// Peasant owns the project identity: ProjectHash is an opaque Peasant-owned
// value. This package never recomputes a hash and never derives project
// identity from a working directory. It only joins Peasant's own session ID to
// the project label Peasant recorded.
//
// The integration is opt-in. No source is constructed unless the operator asked
// for one, and a missing, unreachable, or incompatible source reports itself as
// unavailable rather than failing a Pasture session read.
package peasantproject

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxLookupKeys is the shared batch bound every source enforces. The session API
// requests at most one page (100 sessions) or one detail, so production never
// approaches it; a larger direct caller is refused instead of building an
// unbounded query or request.
const maxLookupKeys = 1000

// maxCacheEntries bounds the enrichment cache. It is far above one page of
// sessions, so a long-lived serve process cannot grow the cache without bound as
// sessions come and go.
const maxCacheEntries = 4096

// Key is the join identity for one session. Harness travels with the session so
// a returned project can be verified against the Pasture session it claims to
// describe; a project whose stored harness disagrees is never applied.
type Key struct {
	Harness string
	Session string
}

// Project is Peasant's canonical project identity for one session. Hash is the
// Peasant-owned project hash, preserved exactly; Name is the display label the
// configured source resolves for that project.
type Project struct {
	Name string
	Hash string
}

// State is the integration state reported by Status and by the session API's
// inspect payload.
type State string

const (
	// StateOK means the configured source answered or opened successfully.
	StateOK State = "ok"
	// StateUnavailable means the configured source could not be opened, reached,
	// or read. Pasture session reads continue with empty project fields.
	StateUnavailable State = "unavailable"
)

// Source tokens identify which backend backs the integration.
const (
	SourceDisabled = "none"
	SourceDB       = "db"
	SourceAPI      = "api"
)

// Status is the integration state the session API exposes.
type Status struct {
	Enabled bool
	Source  string
	State   State
	Message string
}

// Source resolves the project for a batch of sessions. Lookup returns only the
// keys it could map; a missing mapping is not an error. An error means the
// source itself is unavailable, and Pasture must still serve the session read.
type Source interface {
	Lookup(ctx context.Context, keys []Key) (map[Key]Project, error)
	Status() Status
}

// statusHolder is the shared, concurrency-safe integration status of one
// configured source. Both backends use it so their failure rules cannot drift.
type statusHolder struct {
	mu     sync.Mutex
	status Status
}

func newStatusHolder(source string) statusHolder {
	return statusHolder{status: Status{Enabled: true, Source: source, State: StateOK}}
}

func (h *statusHolder) get() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *statusHolder) set(state State, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.State = state
	h.status.Message = message
}

// markFailure records a source fault. A caller cancellation is not a source
// fault: when ctx is already done, the status is left untouched and the caller
// reports its own context error.
func (h *statusHolder) markFailure(ctx context.Context, message string) {
	if ctx != nil && ctx.Err() != nil {
		return
	}
	h.set(StateUnavailable, message)
}

func (h *statusHolder) markOK() {
	h.set(StateOK, "")
}

// DefaultCacheTTL is how long a resolved project (or a confirmed absence) is
// reused. The WebSocket refresh runs every second, so without a cache a page of
// sessions would query Peasant on every tick; 30s keeps the integration cheap
// while remaining short enough that a newly recorded project appears promptly.
const DefaultCacheTTL = 30 * time.Second

// NewCached wraps a source in the short-lived cache and coalescing layer the
// serve process uses. The cache stores positive mappings and negative results
// (a session Peasant does not know) for DefaultCacheTTL. Concurrent lookups for
// the same missing key set share one call into the underlying source.
func NewCached(source Source) *CachedSource {
	return newCached(source, DefaultCacheTTL, time.Now)
}

type cacheEntry struct {
	project *Project
	err     error
	expires time.Time
}

// errSourceLookupTimeout marks a failure at a source's own lookup bound while
// the caller's context was still live. The cache treats it as cacheable: the
// source was contacted and did not answer within its budget, which is a source
// result rather than a caller cancellation.
var errSourceLookupTimeout = errors.New("the Peasant source exceeded its lookup bound")

type pendingLookup struct {
	done chan struct{}
	// cached reports whether the leader wrote its outcome to the cache before
	// releasing waiters. err is the leader's result for the case it did not.
	cached bool
	err    error
	// waiters counts callers currently blocked on this group. It is diagnostic
	// state and never changes the lookup behavior.
	waiters int
}

// CachedSource is the caching/coalescing wrapper. Its Close releases the
// underlying source when it holds a resource.
type CachedSource struct {
	inner Source
	ttl   time.Duration
	now   func() time.Time
	limit int

	mu      sync.Mutex
	entries map[Key]cacheEntry
	pending map[string]*pendingLookup
}

func newCached(inner Source, ttl time.Duration, now func() time.Time) *CachedSource {
	return newCachedWithLimit(inner, ttl, now, maxCacheEntries)
}

func newCachedWithLimit(inner Source, ttl time.Duration, now func() time.Time, limit int) *CachedSource {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	if limit <= 0 {
		limit = maxCacheEntries
	}
	return &CachedSource{
		inner:   inner,
		ttl:     ttl,
		now:     now,
		limit:   limit,
		entries: make(map[Key]cacheEntry),
		pending: make(map[string]*pendingLookup),
	}
}

// Status passes through to the underlying source: the cache never changes the
// integration's state.
func (c *CachedSource) Status() Status { return c.inner.Status() }

func (c *CachedSource) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	if len(keys) == 0 {
		return make(map[Key]Project), nil
	}

	for {
		out := make(map[Key]Project, len(keys))

		// Deduplicate the requested keys and serve everything still fresh from
		// the cache. A cached error is replayed without another source call,
		// which is what keeps an unavailable Peasant from being retried every
		// refresh tick.
		c.mu.Lock()
		now := c.now()
		missing := make([]Key, 0, len(keys))
		seen := make(map[Key]struct{}, len(keys))
		for _, key := range keys {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			entry, ok := c.entries[key]
			if ok && now.Before(entry.expires) {
				if entry.err != nil {
					c.mu.Unlock()
					return nil, entry.err
				}
				if entry.project != nil {
					out[key] = *entry.project
				}
				continue
			}
			missing = append(missing, key)
		}
		if len(missing) == 0 {
			c.mu.Unlock()
			return out, nil
		}

		// One lookup at a time for the same missing set. A second caller waits
		// for the leader and then reads the outcome the leader published.
		group := cacheGroupKey(missing)
		if pending, ok := c.pending[group]; ok {
			pending.waiters++
			c.mu.Unlock()
			select {
			case <-pending.done:
			case <-ctx.Done():
				c.mu.Lock()
				pending.waiters--
				c.mu.Unlock()
				return nil, ctx.Err()
			}
			if !pending.cached {
				if pending.err != nil {
					return nil, pending.err
				}
				// The leader returned without publishing a positive or
				// negative entry (its caller was canceled after a successful
				// source read). Try again instead of reporting a false absence.
				continue
			}
			c.mu.Lock()
			now := c.now()
			for _, key := range missing {
				entry, ok := c.entries[key]
				if !ok || !now.Before(entry.expires) {
					continue
				}
				if entry.err != nil {
					c.mu.Unlock()
					return nil, entry.err
				}
				if entry.project != nil {
					out[key] = *entry.project
				}
			}
			c.mu.Unlock()
			return out, nil
		}
		pending := &pendingLookup{done: make(chan struct{})}
		c.pending[group] = pending
		c.mu.Unlock()

		projects, err := c.inner.Lookup(ctx, missing)

		// A caller cancellation is not evidence about the source, so neither
		// its error nor any stale partial result is cached. A failure at the
		// source's OWN bound (errSourceLookupTimeout) is evidence and is
		// cached, so a hung Peasant is not retried on every refresh tick.
		cacheable := ctx.Err() == nil &&
			!errors.Is(err, context.Canceled) &&
			(!errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errSourceLookupTimeout))

		c.mu.Lock()
		now = c.now()
		pending.err = err
		if cacheable {
			c.pruneExpiredLocked(now)
			for _, key := range missing {
				entry := cacheEntry{err: err, expires: now.Add(c.ttl)}
				if err == nil {
					if project, ok := projects[key]; ok {
						copied := project
						entry.project = &copied
					}
				}
				c.entries[key] = entry
			}
			c.evictToLimitLocked()
			pending.cached = true
		}
		delete(c.pending, group)
		close(pending.done)
		c.mu.Unlock()

		if err != nil {
			return nil, err
		}
		for key := range seen {
			if project, ok := projects[key]; ok {
				out[key] = project
			}
		}
		return out, nil
	}
}

func (c *CachedSource) pruneExpiredLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
		}
	}
}

func (c *CachedSource) evictToLimitLocked() {
	if len(c.entries) <= c.limit {
		return
	}
	// Snapshot the excess before deleting: the map shrinks as entries are
	// removed, so re-reading len(entries) in the loop condition would stop
	// after half the required evictions.
	excess := len(c.entries) - c.limit
	type keyExpiry struct {
		key     Key
		expires time.Time
	}
	list := make([]keyExpiry, 0, len(c.entries))
	for key, entry := range c.entries {
		list = append(list, keyExpiry{key: key, expires: entry.expires})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].expires.Before(list[j].expires)
	})
	if excess > len(list) {
		excess = len(list)
	}
	for i := 0; i < excess; i++ {
		delete(c.entries, list[i].key)
	}
}

// Close releases the underlying source when it holds a resource. The Source
// interface stays minimal; callers that own a source close it through this
// optional method.
func (c *CachedSource) Close() error {
	if closer, ok := c.inner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func cacheGroupKey(keys []Key) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key.Harness+"\x00"+key.Session)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}
