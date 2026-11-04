// Package sessioncache defines errors shared by cache producers and readers.
package sessioncache

import "errors"

// ErrStale identifies an uncertified or dirty disposable session cache.
var ErrStale = errors.New("session cache is stale")

// ErrNewerLayout indicates that the store requires a newer Pasture binary.
var ErrNewerLayout = errors.New("session cache layout is newer than this Pasture version")
