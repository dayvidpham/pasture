// Package tasks — well_known_cache.go
//
// WellKnownAgentCache is the in-memory map populated by RegisterWellKnownAgents
// at `pastured` startup (PROPOSAL-2 §7.7.3). It maps each well-known logical
// name (the PK in `pasture_well_known_agents`) to the `provenance.AgentID`
// minted (or recovered) for that name.
//
// Lifecycle:
//
//   1. `cmd/pastured/main.go` constructs an empty cache via NewWellKnownAgentCache.
//   2. `RegisterWellKnownAgents(tracker, cache)` populates it during startup,
//      using `ensureWellKnownAgent` per row in the canonical registry.
//   3. The populated cache is injected into `temporal.Activities.WellKnownAgents`
//      via constructor wiring; S8's activities consult it to look up the
//      `agent_id` to attach to each emitted audit event.
//
// Concurrency: Lookups (Get / Names) happen on the workflow hot path from
// many concurrent activity goroutines. Writes happen ONLY during startup
// before the Temporal worker is started — there is a clear happens-before
// ordering enforced by the daemon's main loop. We use a sync.RWMutex so
// concurrent readers do not contend, and writes (during startup) take the
// exclusive lock; this is over-cautious for the current single-writer model
// but cheap insurance against future code that mutates the cache after
// startup (e.g. dynamic agent registration).

package tasks

import (
	"fmt"
	"sort"
	"sync"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// WellKnownAgentCache holds the well-known-name → AgentID mapping for use by
// `temporal.Activities` (S8). Construct via NewWellKnownAgentCache; populate
// via RegisterWellKnownAgents; inject into Activities. Cache lookups
// (Get / MustGet / Names) are safe for concurrent use after population.
//
// A nil *WellKnownAgentCache is NOT a valid cache: all methods other than
// Len would panic. RegisterWellKnownAgents asserts the pointer is non-nil
// before populating.
type WellKnownAgentCache struct {
	mu      sync.RWMutex
	entries map[string]provenance.AgentID
}

// NewWellKnownAgentCache returns an empty cache ready for population by
// RegisterWellKnownAgents. The initial capacity is sized to fit the canonical
// 15-entry registry without a rehash.
func NewWellKnownAgentCache() *WellKnownAgentCache {
	return &WellKnownAgentCache{
		entries: make(map[string]provenance.AgentID, WellKnownAgentCount),
	}
}

// set stores name → id. Package-private — only RegisterWellKnownAgents writes
// to the cache. Idempotent: re-setting the same (name, id) pair is a no-op;
// re-setting (name, different id) overwrites the previous value (this should
// not happen in practice because the SQLite UNIQUE constraint on `name`
// enforces a single AgentID per logical name).
func (c *WellKnownAgentCache) set(name string, id provenance.AgentID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = id
}

// Get returns the AgentID for name. The boolean is true when name is a known
// entry, false otherwise. Callers that have a static guarantee that name is
// in the registry (e.g. a constant from well_known_registry.go) may prefer
// MustGet; callers reading user-provided strings should prefer this method.
func (c *WellKnownAgentCache) Get(name string) (provenance.AgentID, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.entries[name]
	return id, ok
}

// MustGet returns the AgentID for name or returns a *StructuredError of
// CategoryValidation if name is not in the cache. Use from activity hot
// paths where the caller statically knows which well-known name it wants
// (e.g. CheckConstraints always wants check-constraints) — a missing entry
// then indicates a wiring bug (the cache was not populated, or the registry
// was edited without re-registering).
//
// Returns the actionable error rather than panicking so the activity can
// surface it through Temporal's error channel without crashing the worker.
func (c *WellKnownAgentCache) MustGet(name string) (provenance.AgentID, error) {
	id, ok := c.Get(name)
	if !ok {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture asked for the built-in agent %q but it isn't registered.", name),
			Why: "Either the name isn't one of the built-in agents pasture knows about, or\n" +
				"the daemon didn't get a chance to register them at startup.",
			Impact: "The action that needed this agent can't be attributed to anyone in the\n" +
				"audit log, so it won't be recorded.",
			Fix: "1. Confirm the name is spelled exactly as one of the built-in agents:\n" +
				"     pasture task agents list --well-known\n" +
				"2. Restart the daemon so it re-registers the built-in agents at startup:\n" +
				"     pkill -f pastured && pastured\n" +
				"3. If the name is from your own code, file a bug or add it to the built-in\n" +
				"   registry before using it.",
		}
	}
	return id, nil
}

// Names returns the sorted slice of all well-known names currently in the
// cache. Sorted output makes test assertions stable (no map iteration
// nondeterminism) and supports diff-friendly debug logging. The returned
// slice is a fresh copy; callers may mutate it without affecting the cache.
func (c *WellKnownAgentCache) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.entries))
	for name := range c.entries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Len returns the number of entries in the cache. A nil receiver returns 0
// so the daemon can safely log `cache.Len()` before construction completes
// (defensive for failure paths in main.go that may have a nil cache pointer).
func (c *WellKnownAgentCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
