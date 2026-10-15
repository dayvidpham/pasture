package writelane

import (
	"context"
	"sync"
)

// TurnScope carries state shared by every record prepared in one commit turn.
// A turn is one trip through the lane's prepare-every-record loop: the lane
// creates a scope, prepares every record of the flush under it, applies the
// batch, and drops it. Records use it to share preparation reads that are
// constant within a turn — content-addressed owner facts resolved from
// monotonic journal truth, where every record of the turn would otherwise
// repeat the identical indexed lookup and resolution against the same rows.
//
// The scope never outlives the batch apply, so no invalidation or cross-turn
// staleness exists: the next turn re-resolves everything. A failed share is
// never stored, so one record's pre-transaction fault cannot poison the turn.
// Records prepared outside a lane (the direct path) see no scope and prepare
// standalone, exactly as before.
type TurnScope struct {
	mu    sync.Mutex
	items map[any]any
}

// NewTurnScope returns an empty per-turn share.
func NewTurnScope() *TurnScope { return &TurnScope{items: make(map[any]any)} }

// Get returns the value stored under key, if any.
func (s *TurnScope) Get(key any) (any, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.items[key]
	return value, ok
}

// Set stores value under key, overwriting any prior entry.
func (s *TurnScope) Set(key any, value any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = value
}

type turnScopeKey struct{}

// ContextWithTurnScope returns a context carrying scope for one commit turn.
func ContextWithTurnScope(ctx context.Context, scope *TurnScope) context.Context {
	return context.WithValue(ctx, turnScopeKey{}, scope)
}

// TurnScopeFrom returns the commit-turn share carried by ctx, or nil when the
// record is prepared outside a lane turn.
func TurnScopeFrom(ctx context.Context) *TurnScope {
	if ctx == nil {
		return nil
	}
	scope, _ := ctx.Value(turnScopeKey{}).(*TurnScope)
	return scope
}
