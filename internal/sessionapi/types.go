// Package sessionapi serves the local, read-only projection of active harness
// sessions mapped to epoch bindings. It never reads the lifecycle journal
// directly; tasks.EnsureSessionState owns the rebuild and writes the cache.
package sessionapi

import (
	"errors"
	"time"
)

// ActivityState is the derived liveness of one session.
type ActivityState string

const (
	StateActive  ActivityState = "active"
	StateIdle    ActivityState = "idle"
	StateEnded   ActivityState = "ended"
	StateUnknown ActivityState = "unknown"
)

// SessionActivity is the activity half of the session view.
type SessionActivity struct {
	State     ActivityState `json:"state"`
	FirstSeen *time.Time    `json:"firstSeen,omitempty"`
	LastSeen  *time.Time    `json:"lastSeen,omitempty"`
	LastEvent string        `json:"lastEvent,omitempty"`
}

// SessionBinding is the latest explicit binding for a session.
type SessionBinding struct {
	BindingID string    `json:"bindingId"`
	Role      string    `json:"role"`
	BoundAt   time.Time `json:"boundAt"`
}

// EpochRef is the epoch half of the session view. Phase 0 deliberately omits
// phase because a freshly created epoch has no projection row to source it.
type EpochRef struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// EndMarker is present exactly when the session is ended.
type EndMarker struct {
	EndedAt   time.Time `json:"endedAt"`
	EndReason string    `json:"endReason"`
}

// SessionView is one session mapped to the latest known epoch binding.
type SessionView struct {
	Harness         string          `json:"harness"`
	NativeSessionID string          `json:"nativeSessionId"`
	Epoch           *EpochRef       `json:"epoch,omitempty"`
	Activity        SessionActivity `json:"activity"`
	Binding         *SessionBinding `json:"binding,omitempty"`
	End             *EndMarker      `json:"end,omitempty"`
}

// Page is the list response. Items are ordered by the cache journal and
// updated time, then harness/session for a stable tie-break.
type Page struct {
	Revision int64         `json:"revision"`
	AsOf     time.Time     `json:"asOf"`
	Items    []SessionView `json:"items"`
	Next     string        `json:"next,omitempty"`
}

// Detail is the single-session response.
type Detail struct {
	Revision int64       `json:"revision"`
	AsOf     time.Time   `json:"asOf"`
	Session  SessionView `json:"session"`
}

// ListOptions is the supported filter set for GET /api/v1/sessions.
type ListOptions struct {
	Epoch   string
	State   ActivityState
	Harness string
	Limit   int
	Cursor  string
}

// Errors returned to the HTTP layer.
var (
	ErrNotFound         = errors.New("session not found")
	ErrInvalidPageToken = errors.New("invalid page token")
	ErrInvalidFilter    = errors.New("invalid filter")
	ErrStaleCache       = errors.New("session cache is stale")
)
