//go:build !unix

package laneserve

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Server is the connection server shape for a platform with no lane transport.
// It exists so the daemon's wiring compiles everywhere; it never serves.
type Server struct {
	handler        RequestHandler
	requestTimeout time.Duration
}

// NewServer validates the wiring and returns the non-serving server.
func NewServer(handler RequestHandler, requestTimeout time.Duration) (*Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("construct the lifecycle lane connection server: no request handler was supplied; no request could be answered")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("construct the lifecycle lane connection server: the request timeout is %s; it must be positive", requestTimeout)
	}
	return &Server{handler: handler, requestTimeout: requestTimeout}, nil
}

// ServeConn always reports the absent transport; nothing is ever served here.
func (s *Server) ServeConn(context.Context, net.Conn) error { return ErrNoTransport }

// Lane is the non-serving listener shape.
type Lane struct {
	socketPath string
}

// Listen always reports the absent transport.
func Listen(socketPath string, server *Server) (*Lane, error) {
	if server == nil {
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: no connection server was supplied", socketPath)
	}
	return nil, ErrNoTransport
}

// SocketPath returns the path this lane would have used.
func (l *Lane) SocketPath() string {
	if l == nil {
		return ""
	}
	return l.socketPath
}

// Serve returns immediately: there is nothing to serve.
func (l *Lane) Serve(context.Context) error { return nil }

// Close is a no-op.
func (l *Lane) Close(context.Context) error { return nil }
