//go:build unix

package laneserve

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/laneclient"
	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// claudeVersion is the pinned contract version the Claude fixtures were
// captured at.
var claudeVersion = registration.ClaudeCode2_1_261().Version

// shortSocketPath returns a socket path inside a short private directory.
//
// A UNIX socket path is bounded by sun_path (about 108 bytes), and a test name
// can be longer than that on its own, so every socket this file binds lives
// under a short temporary directory rather than under t.TempDir().
func shortSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "pasture-lane-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "lane.sock")
}

// journalEvidenceCount counts the committed evidence rows a store holds. It is
// the durable-state read the "the record still committed" assertion needs, and
// it reads through the SAME journal API the gate tests use.
func journalEvidenceCount(t *testing.T, tracker protocol.TaskTracker) int {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds: []provenance.EvidenceKind{
			provenance.EvidenceKind("pasture.lifecycle.occurrence.v1"),
			provenance.EvidenceKind("pasture.lifecycle.interpreted.v2"),
			provenance.EvidenceKind("pasture.lifecycle.consultation.v3"),
			provenance.EvidenceKind("pasture.lifecycle.definition.v1"),
		},
		Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	require.Nil(t, page.Next, "one page must hold every row this proof committed")
	return len(page.Rows)
}

// fakeHandler is a RequestHandler under a test's control.
type fakeHandler struct {
	served handlers.HookLifecycleServed
	err    error
	block  <-chan struct{}
}

func (f *fakeHandler) Serve(ctx context.Context, _ handlers.HookLifecycleServeInput) (handlers.HookLifecycleServed, error) {
	if f.block != nil {
		<-f.block
	}
	return f.served, f.err
}

// serveOverSocket starts one Server on a private socket and returns a client
// bound to it.
func serveOverSocket(t *testing.T, handler RequestHandler) (*laneclient.Client, func()) {
	t.Helper()
	server, err := NewServer(handler, 5*time.Second)
	require.NoError(t, err)
	socketPath := shortSocketPath(t)
	listener, err := Listen(socketPath, server)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Serve(ctx) }()

	client := laneclient.New(socketPath, time.Second)
	stop := func() {
		cancel()
		require.NoError(t, listener.Close(context.Background()))
		<-done
	}
	return client, stop
}

func TestServeConnCarriesACommittedOutcome(t *testing.T) {
	handler := &fakeHandler{served: handlers.HookLifecycleServed{
		Outcome:      hostexit.Outcome{Exit: hostexit.ExitContinue, Stdout: []byte("{}"), Stderr: "note"},
		OccurrenceID: "17",
		ClaimState:   "written",
	}}
	client, stop := serveOverSocket(t, handler)
	defer stop()

	response, err := client.Consult(context.Background(), lanewire.Request{Harness: string(ir.HarnessClaudeCode)}, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, lanewire.KindCommitted, response.Kind)
	require.Equal(t, int(hostexit.ExitContinue), response.ExitStatus)
	require.Equal(t, []byte("{}"), response.Stdout)
	require.Equal(t, "note", response.Stderr)
	require.Equal(t, "17", response.OccurrenceID)
}

func TestServeConnMapsAPreAdmissionRefusalToNotRecorded(t *testing.T) {
	handler := &fakeHandler{err: handlers.ErrLifecycleBeforeDurableWrite}
	client, stop := serveOverSocket(t, handler)
	defer stop()

	response, err := client.Consult(context.Background(), lanewire.Request{Harness: string(ir.HarnessClaudeCode)}, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, lanewire.KindRefused, response.Kind)
	require.Equal(t, hostexit.FaultStageNotRecorded.String(), response.Stage,
		"a refusal that never reached a write must be answered not-recorded")
}

func TestServeConnMapsAnUnknownFailureToRecordUnknown(t *testing.T) {
	handler := &fakeHandler{err: errors.New("the journal failed after the commit")}
	client, stop := serveOverSocket(t, handler)
	defer stop()

	response, err := client.Consult(context.Background(), lanewire.Request{Harness: string(ir.HarnessClaudeCode)}, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, lanewire.KindRefused, response.Kind)
	require.Equal(t, hostexit.FaultStageRecordUnknown.String(), response.Stage,
		"an unclassified served failure must fall to the weakest claim")
}

func TestServeConnRefusesADifferentProtocolVersion(t *testing.T) {
	handler := &fakeHandler{served: handlers.HookLifecycleServed{Outcome: hostexit.Outcome{Exit: hostexit.ExitContinue}}}
	server, err := NewServer(handler, 5*time.Second)
	require.NoError(t, err)
	socketPath := shortSocketPath(t)
	listener, err := Listen(socketPath, server)
	require.NoError(t, err)
	defer listener.Close(context.Background())
	go func() { _ = listener.Serve(context.Background()) }()

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, lanewire.WriteMessage(conn, lanewire.Request{Version: lanewire.ProtocolVersion + 7, Harness: "claude-code"}, nil))

	var response lanewire.Response
	_, err = lanewire.ReadMessage(conn, &response)
	require.NoError(t, err)
	require.Equal(t, lanewire.ProtocolVersion, response.Version)
	require.Equal(t, lanewire.KindRefused, response.Kind)
	require.Equal(t, hostexit.FaultStageNotRecorded.String(), response.Stage)
}

// TestListenRefusesASecondLaneOnTheSameSocket is the election: two daemons
// configured for one store must never both become the lane's writer.
func TestListenRefusesASecondLaneOnTheSameSocket(t *testing.T) {
	server, err := NewServer(&fakeHandler{}, time.Second)
	require.NoError(t, err)
	socketPath := shortSocketPath(t)

	first, err := Listen(socketPath, server)
	require.NoError(t, err)
	defer first.Close(context.Background())

	_, err = Listen(socketPath, server)
	require.Error(t, err, "a second daemon must be refused the lane role")
	require.Contains(t, err.Error(), "already holds the lifecycle write lane",
		"the refusal must name the condition so an operator knows what to stop")
}

// TestListenReclaimsAStaleSocketThroughTheElection proves a crashed daemon's
// socket does not require manual cleanup, and that the reclaim happens only
// after this process has proven it holds the election.
func TestListenReclaimsAStaleSocketThroughTheElection(t *testing.T) {
	server, err := NewServer(&fakeHandler{}, time.Second)
	require.NoError(t, err)
	socketPath := shortSocketPath(t)
	require.NoError(t, os.WriteFile(socketPath, []byte("stale"), 0o600))

	listener, err := Listen(socketPath, server)
	require.NoError(t, err)
	defer listener.Close(context.Background())
	info, err := os.Stat(socketPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSocket, "the stale regular file must have been replaced by a socket")
}

// blockingLane wraps the real lane and holds each commit at a point a test
// chooses, so the "answer lost, record still committed" case is ordered by a
// condition and never by a clock.
type blockingLane struct {
	inner   *writelane.Lane
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *blockingLane) Submit(ctx context.Context, bytes int64, commit writelane.Commit) error {
	return b.inner.Submit(ctx, bytes, func(laneCtx context.Context) error {
		b.once.Do(func() { close(b.entered) })
		<-b.release
		return commit(laneCtx)
	})
}

// TestALostAnswerLeavesTheRecordCommittedAndTheHookReportingRecordUnknown is the
// socketpair integration the design asks for: the daemon is cut off after the
// request is sent, the client reports record-unknown, and the record the lane
// had already admitted still commits.
func TestALostAnswerLeavesTheRecordCommittedAndTheHookReportingRecordUnknown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tracker.Create("file://lane-serve-proof", "bootstrap", "initialize the persisted ingress identity",
		provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	baseline := journalEvidenceCount(t, tracker)

	host, err := tasks.NewLifecycleLaneHost(tracker, tasks.LaneClock{}, tasks.LaneOperationIDs{}, timeouts.TestProfile())
	require.NoError(t, err)
	budget, err := writelane.BudgetFromProfile(timeouts.TestProfile())
	require.NoError(t, err)
	inner, err := writelane.New(budget)
	require.NoError(t, err)
	lane := &blockingLane{inner: inner, entered: make(chan struct{}), release: make(chan struct{})}

	server, err := handlers.NewHookLifecycleServer(handlers.HookLifecycleServerConfig{
		Tracker: host.Tracker(), Lane: lane, Clock: host.Clock(), Operations: host.Operations(),
		Profile: host.Profile(), Readers: host.NewGateReaderFactory(),
	})
	require.NoError(t, err)

	connServer, err := NewServer(server, 5*time.Second)
	require.NoError(t, err)
	socketPath := shortSocketPath(t)
	listener, err := Listen(socketPath, connServer)
	require.NoError(t, err)
	serveCtx, cancelServe := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- listener.Serve(serveCtx) }()

	raw, err := os.ReadFile(filepath.Join("..", "ingress", "claude", "testdata", "fixtures", "pre_tool_use_2_1_261.json"))
	require.NoError(t, err)

	client := laneclient.New(socketPath, time.Second)
	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelRequest()
	_, consultErr := client.Consult(requestCtx, lanewire.Request{
		Harness:     string(ir.HarnessClaudeCode),
		Event:       "PreToolUse",
		HostVersion: claudeVersion,
	}, raw)
	require.ErrorIs(t, consultErr, laneclient.ErrRecordUnknown,
		"a sent request whose answer never arrived must be reported record-unknown, never not-recorded")

	<-lane.entered // the record is admitted and its commit is in flight
	close(lane.release)
	require.Eventually(t, func() bool { return inner.Stats().Records >= 1 }, 5*time.Second, time.Millisecond,
		"the admitted record must still commit after the hook gave up")

	require.Eventually(t, func() bool { return journalEvidenceCount(t, tracker) > baseline }, 5*time.Second, time.Millisecond,
		"the committed occurrence must be durable, with no partial batch left behind")

	cancelServe()
	require.NoError(t, listener.Close(context.Background()))
	<-serveDone
	require.NoError(t, inner.Close(context.Background()))
	require.NoError(t, tracker.Close())
}
