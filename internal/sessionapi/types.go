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

// BindingSource identifies the store that produced a binding interval.
// Explicit rows are written by bind/unbind and the session-end tail. Inferred
// rows are derived from store observations and never override explicit ones.
type BindingSource string

const (
	// BindingSourceExplicit is an authoritative bind, unbind, or session-end row.
	BindingSourceExplicit BindingSource = "explicit"
	// BindingSourceInferred is a derived row from the inference rebuild.
	BindingSourceInferred BindingSource = "inferred"
)

// SessionBindingInterval is one historical binding for one session. Intervals
// are ordered by BoundAt ascending, then BindingID, so the last interval is the
// most recently established one. UnboundAt is nil while the binding is open.
type SessionBindingInterval struct {
	BindingID string        `json:"bindingId"`
	Role      string        `json:"role"`
	BoundAt   time.Time     `json:"boundAt"`
	UnboundAt *time.Time    `json:"unboundAt,omitempty"`
	EndReason string        `json:"endReason,omitempty"`
	Source    BindingSource `json:"source"`
	Epoch     *EpochRef     `json:"epoch,omitempty"`
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
// Binding and Epoch describe the current explicit binding. Bindings carries
// every known interval in chronological order without changing page rows.
type SessionView struct {
	Harness         string                   `json:"harness"`
	NativeSessionID string                   `json:"nativeSessionId"`
	Epoch           *EpochRef                `json:"epoch,omitempty"`
	Activity        SessionActivity          `json:"activity"`
	Binding         *SessionBinding          `json:"binding,omitempty"`
	Bindings        []SessionBindingInterval `json:"bindings"`
	End             *EndMarker               `json:"end,omitempty"`
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

// EpochView is one projected epoch and its current session population.
type EpochView struct {
	ID           string `json:"id"`
	Title        string `json:"title,omitempty"`
	Phase        string `json:"phase"`
	Role         string `json:"role"`
	SessionCount int    `json:"sessionCount"`
	ActiveCount  int    `json:"activeCount"`
}

// EpochPage is the epoch list response.
type EpochPage struct {
	Revision int64       `json:"revision"`
	AsOf     time.Time   `json:"asOf"`
	Items    []EpochView `json:"items"`
}

// EpochDetail is the single-epoch response.
type EpochDetail struct {
	Revision int64     `json:"revision"`
	AsOf     time.Time `json:"asOf"`
	Epoch    EpochView `json:"epoch"`
}

// InspectPayload is the combined epoch and session snapshot.
type InspectPayload struct {
	Revision  int64         `json:"revision"`
	AsOf      time.Time     `json:"asOf"`
	Truncated bool          `json:"truncated"`
	Epochs    []EpochView   `json:"epochs"`
	Sessions  []SessionView `json:"sessions"`
}
