package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/laneclient"
	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// lifecycleWriterDaemon and lifecycleWriterDirect are the two values of the
// writer member this command records on every fault line. They name WHICH
// writer the invocation attempted, so an operator reading the record can tell
// "the daemon was holding the event when this fault happened" from "the hook
// wrote directly because no daemon was there".
const (
	lifecycleWriterDaemon = "daemon"
	lifecycleWriterDirect = "direct"
)

var (
	lifecycleWriterMu   sync.Mutex
	lifecycleWriterMode = lifecycleWriterDirect
)

// setLifecycleWriter records which writer this invocation attempted. One hook
// process handles one event, so the last writer set before a fault is the
// writer that fault belongs to.
func setLifecycleWriter(mode string) {
	lifecycleWriterMu.Lock()
	lifecycleWriterMode = mode
	lifecycleWriterMu.Unlock()
}

// lifecycleWriter reports which writer this invocation attempted.
func lifecycleWriter() string {
	lifecycleWriterMu.Lock()
	defer lifecycleWriterMu.Unlock()
	return lifecycleWriterMode
}

// lifecycleLaneConnectTimeout bounds ONE connect attempt. It is derived from the
// hook-invocation tier so it stays far below the budget the host pays: a daemon
// that is absent or starting must cost the host almost nothing before the hook
// writes directly. One hundredth of the tier is 50 ms on the production tier,
// which is the design's stated budget.
func lifecycleLaneConnectTimeout(budget timeouts.Profile) time.Duration {
	timeout := budget.HookInvocation() / 100
	if timeout < time.Millisecond {
		timeout = time.Millisecond
	}
	return timeout
}

// laneSocketExists reports whether anything stands at the socket path. It is
// deliberately a stat and not a dial: the dial is the attempt itself, and this
// only decides whether the operator is told about its failure.
func laneSocketExists(socket string) bool {
	if socket == "" {
		return false
	}
	_, err := os.Lstat(socket)
	return err == nil
}

// lifecycleLaneSocket resolves the socket the hook asks. An explicit override
// wins; otherwise the path is derived from the store path the hook itself would
// use, exactly as the daemon derives it from the store it serves.
func lifecycleLaneSocket(env HookEnvironment) string {
	if env.WriteLaneSocket != "" {
		return env.WriteLaneSocket
	}
	return lanewire.SocketPath(lifecycleStorePath())
}

// lifecycleLaneAttempt is the client-first branch of one hook invocation.
//
// It returns one of three things:
//
//   - a completed work (decided=true): the daemon answered, or the connection
//     broke after the request was sent. Either way the host answer is settled
//     here and the direct path must NOT run, because a sent request may already
//     have committed.
//   - a fallback reader (decided=false): the lane is disabled, unavailable, or
//     speaks another protocol. Nothing reached the daemon's storage path, so
//     the caller hands this reader to the direct path and the invocation
//     behaves exactly as a build without a lane does.
//
// The payload is read exactly once, under the same bound the handler applies,
// so the two paths see the same bytes.
func lifecycleLaneAttempt(
	cmd *cobra.Command,
	ctx context.Context,
	coords lifecycleCoordinates,
	env HookEnvironment,
	connectTimeout time.Duration,
	fallbackOnce *sync.Once,
	input io.Reader,
) (work *lifecycleWork, fallback io.Reader, decided bool) {
	if env.WriteLane == WriteLaneDirect {
		return nil, input, false
	}
	if runtime.GOOS == "windows" {
		// Phase 1 has no Unix domain socket transport on Windows. The hook writes
		// directly, which is stated here rather than left implicit.
		return nil, input, false
	}
	socket := lifecycleLaneSocket(env)

	raw, readErr := io.ReadAll(io.LimitReader(input, model.MaxNativePayloadBytes+1))
	if readErr != nil {
		// The stdin failure must reach the handler unchanged, exactly as it would
		// have without the lane in front of it.
		return nil, io.MultiReader(bytes.NewReader(raw), failingReader{err: readErr}), false
	}

	setLifecycleWriter(lifecycleWriterDaemon)
	client := laneclient.New(socket, connectTimeout)
	response, err := client.Consult(ctx, lanewire.Request{
		Harness:           string(coords.Harness),
		Event:             coords.Event,
		HostVersion:       coords.HostVersion,
		HostVersionSource: string(coords.HostVersionSource),
		ActorClaim:        env.ActorClaim,
	}, raw)

	switch {
	case err == nil:
		return lifecycleLaneAnswered(coords, response), nil, true
	case errors.Is(err, laneclient.ErrUnavailable):
		reportLifecycleLaneFallback(cmd, socket, fallbackOnce, err)
		return nil, bytes.NewReader(raw), false
	default:
		// Record-unknown, and anything else the client could not classify: the
		// request was sent, so nothing here may claim it was not recorded, and
		// nothing here may write directly either.
		return &lifecycleWork{err: fmt.Errorf(
			"the lifecycle write lane did not answer for event %q of harness %q at %s: %w",
			coords.Event, coords.Harness, socket, err)}, nil, true
	}
}

// lifecycleLaneAnswered turns the daemon's answer into the work this command
// publishes. A committed answer becomes the host Outcome the daemon encoded; a
// refusal becomes a fault carrying the daemon's own stage, so the stage table is
// never asked to infer a durable state from a text whose sentinel it cannot see.
func lifecycleLaneAnswered(coords lifecycleCoordinates, response lanewire.Response) *lifecycleWork {
	if response.Kind == lanewire.KindCommitted {
		status := hostexit.ExitStatus(response.ExitStatus)
		if !status.IsValid() {
			return &lifecycleWork{err: fmt.Errorf(
				"the lifecycle write lane answered with exit status %d for event %q of harness %q, which this build does not declare; the occurrence %s may or may not be committed, so the host is told record-unknown",
				response.ExitStatus, coords.Event, coords.Harness, response.OccurrenceID)}
		}
		return &lifecycleWork{outcome: hostexit.Outcome{Exit: status, Stdout: response.Stdout, Stderr: response.Stderr}}
	}
	stage, known := faultStageFromName(response.Stage)
	if !known {
		stage = hostexit.FaultStageRecordUnknown
	}
	return &lifecycleWork{
		err: fmt.Errorf("the lifecycle write lane refused to record event %q of harness %q: %s",
			coords.Event, coords.Harness, response.Refusal),
		stage: stage,
	}
}

// faultStageFromName reads one stage name from the wire. It compares against the
// canonical String() renderings, so a renamed stage cannot silently change what
// the daemon's answer means; an unknown name is the weakest claim.
func faultStageFromName(name string) (hostexit.FaultStage, bool) {
	switch name {
	case hostexit.FaultStageNotRecorded.String():
		return hostexit.FaultStageNotRecorded, true
	case hostexit.FaultStageRecordUnknown.String():
		return hostexit.FaultStageRecordUnknown, true
	case hostexit.FaultStageRecorded.String():
		return hostexit.FaultStageRecorded, true
	default:
		return hostexit.FaultStageRecordUnknown, false
	}
}

// reportLifecycleLaneFallback tells the operator, once per invocation, that
// this invocation wrote directly because the lane was unreachable, and records
// that the writer was the direct path.
//
// IT SPEAKS ONLY WHEN A LANE WAS EXPECTED. A machine that has never run
// pastured has no socket, and a hook there must behave EXACTLY as a build
// without a lane does — same bytes on standard output, same standard error,
// same durable state. Printing a line for every event on such a machine would
// be noise the operator cannot act on, and it would change the hook's
// host-visible contract for everyone who never installs the daemon. So the
// diagnostic is emitted when the socket EXISTS and could not be used (a daemon
// that is starting, has crashed, or speaks another protocol version), which is
// the case where something really is wrong; otherwise the only trace is the
// fault record's writer=direct member, and the operator who wants the word can
// set PASTURE_WRITE_LANE=auto and watch for it.
//
// THE ONCE IS PER INVOCATION, and a hook process handles one event, so this is
// one line per hook process. It is passed in rather than held in a package
// variable so an in-process caller that drives many invocations gets the same
// one-line-per-invocation behaviour instead of one line for the first and
// silence after.
func reportLifecycleLaneFallback(cmd *cobra.Command, socket string, once *sync.Once, cause error) {
	setLifecycleWriter(lifecycleWriterDirect)
	if !laneSocketExists(socket) {
		return
	}
	if once == nil {
		once = &sync.Once{}
	}
	once.Do(func() {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"pasture: the lifecycle write lane is unavailable at %s (%v); this hook writes directly to the store for "+
				"this invocation, which is the behaviour of a build without a lane, and the fault record (when one "+
				"occurs) carries writer=direct; start pastured to restore the single-writer lane\n",
			socket, cause)
	})
}
