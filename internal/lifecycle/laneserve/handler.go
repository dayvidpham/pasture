package laneserve

import (
	"context"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// RequestHandler is one daemon's served lifecycle path. It is an interface so
// the listener can be driven in a test without a store, and so the platform
// without a lane transport can declare the same shape.
type RequestHandler interface {
	Serve(ctx context.Context, in handlers.HookLifecycleServeInput) (handlers.HookLifecycleServed, error)
}
