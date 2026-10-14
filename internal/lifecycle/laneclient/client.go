// Package laneclient is the hook's side of the lifecycle write lane: connect
// within a short budget, ask the daemon to consult and record one event, and
// classify every failure so the command can tell the operator the truth about
// the record's durable state.
//
// THE CLASSIFICATION IS THE POINT. Three outcomes exist and they are not
// interchangeable:
//
//   - the daemon answered: the answer says committed or refused, and the
//     refusal carries the stage that says what is known;
//   - the daemon could not be reached at all: provably nothing was committed,
//     so the hook writes directly, which is exactly today's behaviour;
//   - the request was sent and no answer came back: the record's durable state
//     is UNKNOWN, and the hook must say so rather than claim either.
//
// A request is never re-sent, and a fallback never follows a send: the daemon
// is the single writer, and writing directly after a request that may have
// committed would duplicate the occurrence.
package laneclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
)

// ErrUnavailable marks a lane the hook could not reach, or one that speaks a
// different protocol. It is PROVABLY not-recorded: no request body ever reached
// the daemon's storage path, so the caller may write directly.
var ErrUnavailable = errors.New("the lifecycle write lane is unavailable")

// ErrRecordUnknown marks a request that was sent and never answered. The daemon
// may have committed it, so the caller must report record-unknown and must not
// fall back.
var ErrRecordUnknown = errors.New("the lifecycle write lane connection broke after the request was sent")

// Client is the hook's connection to one lane socket.
type Client struct {
	socketPath     string
	connectTimeout time.Duration
	dial           func(ctx context.Context, network, address string) (net.Conn, error)
}

// Option configures a Client. It exists so a test can substitute the dialer
// without a second code path.
type Option func(*Client)

// WithDialer replaces the Unix-socket dialer. Production never sets it.
func WithDialer(dial func(ctx context.Context, network, address string) (net.Conn, error)) Option {
	return func(c *Client) { c.dial = dial }
}

// New builds a client for one socket path. connectTimeout bounds the connect
// alone and is deliberately far below the hook-invocation tier, so a daemon
// that is starting or a socket that does not exist costs the host almost
// nothing before the hook writes directly.
func New(socketPath string, connectTimeout time.Duration, opts ...Option) *Client {
	client := &Client{
		socketPath:     socketPath,
		connectTimeout: connectTimeout,
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// SocketPath returns the socket this client talks to.
func (c *Client) SocketPath() string { return c.socketPath }

// Consult sends one consult-and-record request and returns the daemon's answer.
//
// The returned error is exactly one of: nil (answered, inspect the response),
// an error wrapping ErrUnavailable (nothing was sent to storage; the caller may
// write directly), or an error wrapping ErrRecordUnknown (the request was sent;
// do not write directly).
func (c *Client) Consult(ctx context.Context, request lanewire.Request, payload []byte) (lanewire.Response, error) {
	if c == nil {
		return lanewire.Response{}, fmt.Errorf("%w: no client was constructed; no request was sent and nothing was committed", ErrUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request.Version = lanewire.ProtocolVersion

	connectCtx, cancel := context.WithTimeout(ctx, c.connectTimeout)
	defer cancel()
	conn, err := c.dial(connectCtx, "unix", c.socketPath)
	if err != nil {
		// The caller names the socket: it is the process that knows which path it
		// chose, and a path repeated here would appear twice in the diagnostic.
		return lanewire.Response{}, fmt.Errorf("%w: %w; no request was sent and nothing was committed for this event", ErrUnavailable, err)
	}
	defer conn.Close()

	// The connection carries the caller's own deadline, so a daemon that
	// accepts the request and then stalls cannot outlive the hook's budget.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := lanewire.WriteMessage(conn, request, payload); err != nil {
		return lanewire.Response{}, fmt.Errorf("%w: the request could not be written to the lane at %s (%v); whether the daemon received it is unknown, so no direct write follows", ErrRecordUnknown, c.socketPath, err)
	}

	var response lanewire.Response
	if _, err := lanewire.ReadMessage(conn, &response); err != nil {
		return lanewire.Response{}, fmt.Errorf("%w: the lane at %s sent no answer (%v); the daemon may have committed the record, so no direct write follows", ErrRecordUnknown, c.socketPath, err)
	}
	if err := lanewire.CheckVersion(response.Version); err != nil {
		// A version mismatch is caught BEFORE any semantic reading of the
		// response: a field this build does not know could mean anything.
		return lanewire.Response{}, fmt.Errorf("%w: %w; the hook falls back to the direct write path, which cannot double-write because this answer carries no committed occurrence", ErrUnavailable, err)
	}
	switch response.Kind {
	case lanewire.KindCommitted, lanewire.KindRefused:
		return response, nil
	default:
		return lanewire.Response{}, fmt.Errorf("%w: the lane at %s answered with kind %q, which this build does not declare; no answer can be trusted, and whether the record committed is unknown", ErrRecordUnknown, c.socketPath, response.Kind)
	}
}

// WaitForSocket reports whether a lane socket is present and dialable right
// now. It exists for the daemon's own tests and for a startup preflight; the
// hook never calls it, because a connect attempt is the only honest test.
func (c *Client) WaitForSocket(ctx context.Context) error {
	if c == nil {
		return ErrUnavailable
	}
	connectCtx, cancel := context.WithTimeout(ctx, c.connectTimeout)
	defer cancel()
	conn, err := c.dial(connectCtx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("%w at %s: %w", ErrUnavailable, c.socketPath, err)
	}
	return conn.Close()
}
