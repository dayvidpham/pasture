package laneserve

import (
	"context"
	"errors"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// ErrLaneHeld marks a lane this process did NOT get: another daemon holds the
// election. It is a distinct sentinel because the two refusals lead to
// different endings — a held lane lets THIS daemon run without the lane role,
// while a socket that cannot be created at all is a startup failure.
//
// It is declared here, with no build constraint, because the daemon's wiring
// must compile on a platform with no lane transport too.
var ErrLaneHeld = errors.New("another pastured already holds the lifecycle write lane")

// ErrNoTransport is the platform boundary of phase 1: without a Unix domain
// socket transport there is no lifecycle write lane, so the daemon hosts no
// lane role and every hook writes directly. It is declared with no build
// constraint for the same reason as ErrLaneHeld.
var ErrNoTransport = errors.New("this platform has no Unix domain socket transport, so the lifecycle write lane is unavailable; hooks write directly, exactly as a build without a lane does")

// RequestHandler is one daemon's served lifecycle path. It is an interface so
// the listener can be driven in a test without a store, and so the platform
// without a lane transport can declare the same shape.
type RequestHandler interface {
	Serve(ctx context.Context, in handlers.HookLifecycleServeInput) (handlers.HookLifecycleServed, error)
}
