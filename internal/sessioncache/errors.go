// Package sessioncache defines errors shared by cache producers and readers.
package sessioncache

import "errors"

// ErrStale identifies a cache whose version cannot be read safely.
var ErrStale = errors.New("session cache is stale")
