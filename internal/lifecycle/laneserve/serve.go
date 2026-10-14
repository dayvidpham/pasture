//go:build unix

// Package laneserve is the daemon's side of the lifecycle write lane: the
// per-connection request handler, the single-instance election, and the
// listener that hosts them.
//
// ONE REQUEST PER CONNECTION, ONE LANE PER DAEMON. Each hook opens one
// connection, sends one consult-and-record request, waits for one answer, and
// closes. The daemon reads requests concurrently — WAL makes reads concurrent —
// and hands every durable write to the ONE lane goroutine, so the single-writer
// property is enforced by the lane and not by a connection limit.
package laneserve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// Server answers one connection's request.
type Server struct {
	handler        RequestHandler
	requestTimeout time.Duration
}

// NewServer validates and returns the per-connection server. requestTimeout
// bounds ONE request end to end on the daemon's side: a hook that sends a frame
// and then stalls must not pin a goroutine forever.
func NewServer(handler RequestHandler, requestTimeout time.Duration) (*Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("construct the lifecycle lane connection server: no request handler was supplied; no request could be answered")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("construct the lifecycle lane connection server: the request timeout is %s; it must be positive, because it bounds one request so a stalled peer cannot pin the daemon's goroutine", requestTimeout)
	}
	return &Server{handler: handler, requestTimeout: requestTimeout}, nil
}

// ServeConn reads one request, serves it, and writes one answer.
//
// The returned error is a TRANSPORT or framing failure only. A served refusal
// (a pre-admission refusal, or a commit failure) is delivered to the peer as a
// typed response and is NOT an error here: the peer, not this process, decides
// what a refusal means for its host.
func (s *Server) ServeConn(ctx context.Context, conn net.Conn) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = conn.SetDeadline(time.Now().Add(s.requestTimeout))
	requestCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	var request lanewire.Request
	payload, err := lanewire.ReadMessage(conn, &request)
	if err != nil {
		return fmt.Errorf("read the lifecycle lane request: %w", err)
	}
	if err := lanewire.CheckVersion(request.Version); err != nil {
		// The peer speaks another protocol. This answer states OUR version, so a
		// peer that can read it falls back; nothing was read past the frame and
		// nothing was committed.
		return s.refuse(conn, hostexit.FaultStageNotRecorded, fmt.Errorf("%w; nothing was read past the frame, so nothing was committed for this event", err))
	}

	served, serveErr := s.handler.Serve(requestCtx, handlers.HookLifecycleServeInput{
		Harness:           ir.HarnessID(request.Harness),
		Event:             request.Event,
		HostVersion:       request.HostVersion,
		HostVersionSource: model.HostVersionSource(request.HostVersionSource),
		Payload:           payload,
		ActorClaim:        tasks.ActorClaim(request.ActorClaim),
	})
	if serveErr != nil {
		return s.refuse(conn, handlers.LifecycleFaultStageForError(serveErr), serveErr)
	}

	if _, known := served.Outcome.Exit.Code(); !known {
		return s.refuse(conn, hostexit.FaultStageRecordUnknown, fmt.Errorf(
			"the served lifecycle outcome named no exit status, so no host answer can be constructed; the occurrence %s may or may not be committed and the answer is record-unknown",
			served.OccurrenceID))
	}
	if err := lanewire.WriteMessage(conn, lanewire.Response{
		Version:      lanewire.ProtocolVersion,
		Kind:         lanewire.KindCommitted,
		ExitStatus:   int(served.Outcome.Exit),
		Stdout:       served.Outcome.Stdout,
		Stderr:       served.Outcome.Stderr,
		OccurrenceID: served.OccurrenceID,
		ClaimState:   served.ClaimState,
	}, nil); err != nil {
		return fmt.Errorf("write the committed lifecycle lane answer: %w", err)
	}
	return nil
}

// refuse delivers a typed refusal and reports only a transport failure.
func (s *Server) refuse(conn net.Conn, stage hostexit.FaultStage, cause error) error {
	refusal := cause.Error()
	if err := lanewire.WriteMessage(conn, lanewire.Response{
		Version: lanewire.ProtocolVersion,
		Kind:    lanewire.KindRefused,
		Stage:   stage.String(),
		Refusal: refusal,
	}, nil); err != nil {
		return fmt.Errorf("write the lifecycle lane refusal (%s): %w", refusal, err)
	}
	return nil
}

// Lane is the elected, listening daemon side. At most ONE process may hold a
// given lane: the exclusive flock on the socket's lock file is the election,
// and a second process is refused rather than allowed to double-write.
type Lane struct {
	socketPath string
	lockFile   *os.File
	listener   net.Listener
	server     *Server

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool

	wg sync.WaitGroup
}

// Listen takes the lane election and starts listening.
//
// THE ELECTION COMES FIRST, AND THE SOCKET IS RECLAIMED THROUGH IT. A stale
// socket file left by a crashed daemon cannot be distinguished from a live one
// by connect alone, so the socket is unlinked only after this process has
// proven it is the only lane holder. A second daemon therefore never removes a
// live socket.
func Listen(socketPath string, server *Server) (*Lane, error) {
	if server == nil {
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: no connection server was supplied", socketPath)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: the socket directory could not be created: %w", socketPath, err)
	}
	lockFile, err := os.OpenFile(lanewire.LockPath(socketPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: the election lock could not be opened: %w", socketPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf(
			"%w at %s (its lock is %s), so this process refuses the lane role rather than becoming a second writer to the same store; the holder serves every hook for this store, so this daemon runs without the lane instead: %w",
			ErrLaneHeld, socketPath, lanewire.LockPath(socketPath), err)
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: a stale socket could not be removed through the election this process just won: %w", socketPath, err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		listener.Close()
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return nil, fmt.Errorf("listen on the lifecycle lane socket %s: the socket permissions could not be restricted to this user: %w", socketPath, err)
	}
	return &Lane{socketPath: socketPath, lockFile: lockFile, listener: listener, server: server, conns: map[net.Conn]struct{}{}}, nil
}

// SocketPath returns the socket this lane is bound to.
func (l *Lane) SocketPath() string { return l.socketPath }

// Serve accepts connections until ctx ends or the lane is closed. A closed
// listener ends it normally, which is what a stop signal produces.
func (l *Lane) Serve(ctx context.Context) error {
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if l.isClosed() || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept a lifecycle lane connection on %s: %w", l.socketPath, err)
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			conn.Close()
			return nil
		}
		l.conns[conn] = struct{}{}
		l.mu.Unlock()

		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			defer l.forget(conn)
			// A transport failure is the peer's, and the peer learns about it
			// from the connection it opened; the daemon keeps serving. A record
			// the peer sent and never heard about is reported by the PEER as
			// record-unknown, which is the only honest claim either process can
			// make.
			_ = l.server.ServeConn(ctx, conn)
		}()
	}
}

func (l *Lane) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (l *Lane) forget(conn net.Conn) {
	_ = conn.Close()
	l.mu.Lock()
	delete(l.conns, conn)
	l.mu.Unlock()
}

// Close stops accepting, waits for in-flight connections inside the budget,
// and releases the election and the socket.
func (l *Lane) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	l.closed = true
	conns := make([]net.Conn, 0, len(l.conns))
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()

	if err := l.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("stop the lifecycle lane listener on %s: %w", l.socketPath, err)
	}
	for _, conn := range conns {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("the lifecycle lane did not finish its in-flight connections inside its stop budget; every hook they belonged to reports record-unknown rather than recorded: %w", ctx.Err())
	}

	if err := os.Remove(l.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the lifecycle lane socket %s while stopping: %w", l.socketPath, err)
	}
	if err := syscall.Flock(int(l.lockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("release the lifecycle lane election lock %s while stopping: %w", lanewire.LockPath(l.socketPath), err)
	}
	if err := l.lockFile.Close(); err != nil {
		return fmt.Errorf("close the lifecycle lane election lock %s while stopping: %w", lanewire.LockPath(l.socketPath), err)
	}
	return nil
}
