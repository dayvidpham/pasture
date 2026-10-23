// Package peasantproject reads the Peasant-owned session-to-project mapping for
// Pasture's opt-in project enrichment.
//
// Peasant owns the project identity: ProjectHash is a salted HMAC-SHA256 value
// minted and validated by Peasant. This package never recomputes a hash and
// never derives project identity from a working directory. It only joins
// Peasant's own session ID to the project label Peasant recorded.
//
// The integration is opt-in. No source is constructed unless the operator asked
// for one, and a missing, unreachable, or incompatible source reports itself as
// unavailable rather than failing a Pasture session read.
package peasantproject

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Key is the join identity for one session. Harness travels with the session so
// a returned project can be verified against the Pasture session it claims to
// describe; a project whose stored harness disagrees is never applied.
type Key struct {
	Harness string
	Session string
}

// Project is Peasant's canonical project identity for one session. Hash is the
// Peasant-owned project hash, preserved exactly; Name is Peasant's display
// fallback chain (canonical remote, then canonical cwd, then the hash).
type Project struct {
	Name string
	Hash string
}

// State is the integration state reported by Status and by the session API's
// inspect payload.
type State string

const (
	// StateDisabled means no source was configured. Unlike StateUnavailable,
	// nothing was attempted and no Peasant dependency is expected.
	StateDisabled State = "disabled"
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

// DefaultCacheTTL is how long a resolved project (or a confirmed absence) is
// reused. The WebSocket refresh runs every second, so without a cache a page of
// sessions would query Peasant on every tick; 30s keeps the integration cheap
// while remaining short enough that a newly recorded project appears promptly.
const DefaultCacheTTL = 30 * time.Second

// NewCached wraps a source in the short-lived cache and coalescing layer the
// serve process uses. The cache stores positive mappings and negative results
// (a session Peasant does not know) for DefaultCacheTTL. Concurrent lookups for
// the same missing key set share one call into the underlying source.
func NewCached(source Source) Source {
	return newCached(source, DefaultCacheTTL, time.Now)
}

type cacheEntry struct {
	project *Project
	err     error
	expires time.Time
}

type pendingLookup struct {
	done chan struct{}
}

type cachedSource struct {
	inner Source
	ttl   time.Duration
	now   func() time.Time

	mu      sync.Mutex
	entries map[Key]cacheEntry
	pending map[string]*pendingLookup
}

func newCached(inner Source, ttl time.Duration, now func() time.Time) *cachedSource {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	return &cachedSource{
		inner:   inner,
		ttl:     ttl,
		now:     now,
		entries: make(map[Key]cacheEntry),
		pending: make(map[string]*pendingLookup),
	}
}

// Status passes through to the underlying source: the cache never changes the
// integration's state.
func (c *cachedSource) Status() Status { return c.inner.Status() }

func (c *cachedSource) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	out := make(map[Key]Project, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	// Deduplicate the requested keys and serve everything still fresh from the
	// cache. A cached error is replayed without another source call, which is
	// what keeps an unavailable Peasant from being retried every refresh tick.
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

	// One lookup at a time for the same missing set. A second caller waits for
	// the leader and then reads the cache the leader populated.
	group := cacheGroupKey(missing)
	if pending, ok := c.pending[group]; ok {
		c.mu.Unlock()
		select {
		case <-pending.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		now := c.now()
		for _, key := range missing {
			entry, ok := c.entries[key]
			if !ok || !now.Before(entry.expires) {
				continue
			}
			if entry.err != nil {
				return nil, entry.err
			}
			if entry.project != nil {
				out[key] = *entry.project
			}
		}
		return out, nil
	}
	pending := &pendingLookup{done: make(chan struct{})}
	c.pending[group] = pending
	c.mu.Unlock()

	projects, err := c.inner.Lookup(ctx, missing)

	c.mu.Lock()
	now = c.now()
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

// Close releases the underlying source when it holds a resource. The Source
// interface stays minimal; callers that own a source close it through this
// optional method.
func (c *cachedSource) Close() error {
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
