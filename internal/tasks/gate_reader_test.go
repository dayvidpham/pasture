package tasks

// gate_reader_test.go proves the read side of the gate decision: one claim
// read, at most one public ownership read, and one sealed immutable answer.
//
// Every subject here drives the production store through OpenTaskTracker and
// the production commands, and reaches the read through the exported
// NewGateReader. The only white-box reach is the nil-by-default claim hook,
// which a source pin holds to test files only.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/gatepolicy"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

const gateHarness = ir.HarnessClaudeCode

// ─── statement counting ───────────────────────────────────────────────────────

// gateStatementCounter counts the statements a handle issued whose text carries
// a named token. It exists so "exactly one claim SELECT" is a measurement
// rather than an inference from a result.
type gateStatementCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newGateStatementCounter(tokens ...string) *gateStatementCounter {
	counter := &gateStatementCounter{counts: map[string]int{}}
	for _, token := range tokens {
		counter.counts[token] = 0
	}
	return counter
}

func (c *gateStatementCounter) record(query string) {
	lowered := strings.ToLower(query)
	c.mu.Lock()
	defer c.mu.Unlock()
	for token := range c.counts {
		if strings.Contains(lowered, token) {
			c.counts[token]++
		}
	}
}

func (c *gateStatementCounter) count(token string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[token]
}

// gateClaimReadToken is the only token the reader's SQL carries, so its count is
// the claim-read count.
const gateClaimReadToken = "pasture_session_claim"

type gateCountingConnector struct {
	dsn    string
	inner  *sqlite.Driver
	counts *gateStatementCounter
}

func (c gateCountingConnector) Connect(context.Context) (driver.Conn, error) {
	raw, err := c.inner.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	conn := gateCountingConn{Conn: raw, counts: c.counts}
	if queryer, ok := raw.(driver.QueryerContext); ok {
		conn.queryer = queryer
	}
	if execer, ok := raw.(driver.ExecerContext); ok {
		conn.execer = execer
	}
	if beginner, ok := raw.(driver.ConnBeginTx); ok {
		conn.beginner = beginner
	}
	if resetter, ok := raw.(driver.SessionResetter); ok {
		conn.resetter = resetter
	}
	if validator, ok := raw.(driver.Validator); ok {
		conn.validator = validator
	}
	return conn, nil
}

func (c gateCountingConnector) Driver() driver.Driver { return c.inner }

// gateCountingConn forwards every optional interface the wrapped connection
// offers and counts statements on both entry points a query can take. A
// connection that kept only some of them would change the handle's behaviour
// under test, so the forwarding is explicit rather than a subset.
type gateCountingConn struct {
	driver.Conn
	counts    *gateStatementCounter
	queryer   driver.QueryerContext
	execer    driver.ExecerContext
	beginner  driver.ConnBeginTx
	resetter  driver.SessionResetter
	validator driver.Validator
}

func (c gateCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.counts.record(query)
	return c.queryer.QueryContext(ctx, query, args)
}

func (c gateCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.counts.record(query)
	return c.execer.ExecContext(ctx, query, args)
}

func (c gateCountingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.beginner.BeginTx(ctx, opts)
}

func (c gateCountingConn) ResetSession(ctx context.Context) error {
	if c.resetter == nil {
		return nil
	}
	return c.resetter.ResetSession(ctx)
}

func (c gateCountingConn) IsValid() bool {
	if c.validator == nil {
		return true
	}
	return c.validator.IsValid()
}

func (c gateCountingConn) Prepare(query string) (driver.Stmt, error) {
	c.counts.record(query)
	return c.Conn.Prepare(query)
}

// ─── journal wrappers ─────────────────────────────────────────────────────────

// gateCountingJournal counts the public ownership reads and refuses every other
// journal read, so a second read path cannot pass unnoticed.
type gateCountingJournal struct {
	provenance.Journal

	mu              sync.Mutex
	ownershipCalls  int
	otherCalls      int
	beforeOwnership func() error
}

func (j *gateCountingJournal) QueryActorOwnership(ctx context.Context, query provenance.ActorOwnershipQuery) (provenance.ActorOwnershipSnapshot, error) {
	j.mu.Lock()
	j.ownershipCalls++
	before := j.beforeOwnership
	j.beforeOwnership = nil
	j.mu.Unlock()
	if before != nil {
		if err := before(); err != nil {
			return provenance.ActorOwnershipSnapshot{}, err
		}
	}
	return j.Journal.(provenance.ActorOwnershipQueryAPI).QueryActorOwnership(ctx, query)
}

func (j *gateCountingJournal) QueryTaskEvents(provenance.JournalQueryV1) (provenance.JournalTaskEventPageV1, error) {
	j.countOther()
	return provenance.JournalTaskEventPageV1{}, errors.New("the gate reader reached a private material read")
}

func (j *gateCountingJournal) QueryAssignmentStarts(provenance.AssignmentStartQuery) (provenance.AssignmentStartPage, error) {
	j.countOther()
	return provenance.AssignmentStartPage{}, errors.New("the gate reader reached an assignment-start read")
}

func (j *gateCountingJournal) Facts() provenance.FactQueryAPI {
	j.countOther()
	return nil
}

func (j *gateCountingJournal) LookupCommitted(provenance.OperationID) (provenance.CommittedResult, error) {
	j.countOther()
	return provenance.CommittedResult{}, errors.New("the gate reader reached a committed-operation read")
}

func (j *gateCountingJournal) VerifyIntegrity() error {
	j.countOther()
	return errors.New("the gate reader reached an integrity read")
}

func (j *gateCountingJournal) AuthorityGovernsTaskAt(provenance.JournalID, provenance.TaskID, provenance.JournalID) (bool, error) {
	j.countOther()
	return false, errors.New("the gate reader reached a governance predicate")
}

func (j *gateCountingJournal) countOther() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.otherCalls++
}

func (j *gateCountingJournal) counts() (ownership, other int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ownershipCalls, j.otherCalls
}

// gateHiddenJournal exposes the plain Journal surface only. The ownership API is
// not part of that surface, so this wrapper is how a store without the
// capability looks.
type gateHiddenJournal struct {
	provenance.Journal
}

// gateFaultJournal returns one prepared error from the public call.
type gateFaultJournal struct {
	provenance.Journal
	err error
}

func (j gateFaultJournal) QueryActorOwnership(context.Context, provenance.ActorOwnershipQuery) (provenance.ActorOwnershipSnapshot, error) {
	return provenance.ActorOwnershipSnapshot{}, j.err
}

func (j gateFaultJournal) QueryTaskEvents(provenance.JournalQueryV1) (provenance.JournalTaskEventPageV1, error) {
	return provenance.JournalTaskEventPageV1{}, errors.New("the gate reader reached a private material read")
}

// gateCancelJournal cancels the invocation context and then delegates to the
// real API, so the read the Reader made is a real read that loses its context.
type gateCancelJournal struct {
	provenance.Journal
	cancel context.CancelFunc
}

func (j gateCancelJournal) QueryActorOwnership(ctx context.Context, query provenance.ActorOwnershipQuery) (provenance.ActorOwnershipSnapshot, error) {
	j.cancel()
	return j.Journal.(provenance.ActorOwnershipQueryAPI).QueryActorOwnership(ctx, query)
}

type gateJournalTracker struct {
	provenance.Tracker
	journal provenance.Journal
}

func (t gateJournalTracker) Journal() provenance.Journal { return t.journal }

// gateForeignTracker is a tracker that is not the unified Pasture store. Its
// embedded interface is nil on purpose: the reader must refuse it on the type
// alone, without calling anything.
type gateForeignTracker struct {
	protocol.TaskTracker
}

// ─── fixtures ─────────────────────────────────────────────────────────────────

// gateClock is the fixed instant the claim writer stamps rows with.
type gateClock struct{}

func (gateClock) Now() time.Time { return time.Unix(1700000000, 0).UTC() }

// openGateStore opens the production store on a fresh file. Every subject uses
// this opener, so no subject runs against a store production does not open.
func openGateStore(t *testing.T, options ...OpenTaskTrackerOption) (*trackerImpl, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	opened, err := OpenTaskTrackerWithOptions(path, options...)
	require.NoError(t, err)
	store, ok := opened.(*trackerImpl)
	require.True(t, ok, "OpenTaskTrackerWithOptions returned %T, want *trackerImpl", opened)
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

// countGateStore replaces the store's Pasture-owned handle with a pool of one
// that counts the statements it issues. The original handle stays open because
// Provenance borrows it.
func countGateStore(t *testing.T, store *trackerImpl, path string, tokens ...string) *gateStatementCounter {
	t.Helper()
	counts := newGateStatementCounter(tokens...)
	original := store.auditDB
	counted := sql.OpenDB(gateCountingConnector{
		dsn:    dbconn.SharedDSNWithProfile(path, store.timeoutProfile),
		inner:  &sqlite.Driver{},
		counts: counts,
	})
	counted.SetMaxOpenConns(1)
	counted.SetMaxIdleConns(1)
	store.auditDB = counted
	t.Cleanup(func() {
		store.auditDB = original
		_ = counted.Close()
	})
	return counts
}

// gateJournal installs a journal wrapper for the duration of one subject.
func gateJournal(t *testing.T, store *trackerImpl, journal provenance.Journal) {
	t.Helper()
	original := store.prov
	store.prov = gateJournalTracker{original, journal}
	t.Cleanup(func() { store.prov = original })
}

// gateClaim writes a real session claim through the production claim writer, so
// no subject inserts a claim row behind the writer's back.
func gateClaim(t *testing.T, store *trackerImpl, actor provenance.ActorID, session string) {
	t.Helper()
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: session}}
	require.NoError(t, RecordLifecycleSessionClaim(
		t.Context(), store, gateHarness, registration.EventSessionStart, bindings,
		ActorClaim(actor.String()), gateClock{},
	))
}

// gateSnapshot opens a reader and takes one snapshot, requiring it to succeed.
func gateSnapshot(t *testing.T, store *trackerImpl, session string) gateauthority.Snapshot {
	t.Helper()
	reader, err := NewGateReader(store, gateHarness, session)
	require.NoError(t, err)
	snapshot, err := reader.Snapshot(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Close()) })
	return snapshot
}

// gateEpisode requires the named assignment among the actor's episodes and
// returns it. Selecting by name is what makes the per-writer table a mapping
// proof rather than a population count: a reader that answered with the wrong
// episode fails by name, and a reader that answered with the wrong ROLE fails
// on the role.
func gateEpisode(t *testing.T, store *trackerImpl, actor provenance.ActorID, session string, assignment provenance.AssignmentID) gateauthority.Episode {
	t.Helper()
	snapshot := gateSnapshot(t, store, session)
	authority, err := snapshot.Authority(actor)
	require.NoError(t, err)
	for _, episode := range authority.Episodes {
		if episode.Assignment == assignment {
			return episode
		}
	}
	t.Fatalf("the actor's authority carries no assignment %q; it carries %+v", assignment, authority.Episodes)
	return gateauthority.Episode{}
}

// gateRawEpisode appends one assignment start under one operation, with the
// assignment-start material the caller supplies. The subjects that need a role
// no supported writer produces use it, because no production writer can be
// asked to write a malformed record.
func gateRawEpisode(t *testing.T, store *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, occupant provenance.ActorID, operation provenance.OperationID, payloads ...string) {
	t.Helper()
	extra := make([]provenance.Effect, 0, len(payloads))
	for index, payload := range payloads {
		extra = append(extra, provenance.Effect{
			Sort:       provenance.EffectTaskEvent,
			ResultSlot: provenance.ResultSlotID(fmt.Sprintf("gate-material-%d", index)),
			TaskID:     task,
			EventKind:  FamilyAssignmentStarted.EventKind(),
			Payload:    []byte(payload),
		})
	}
	gateRawStart(t, store, task, assignment, occupant, operation, extra...)
}

// gateCommandEvidenceEpisode starts one episode under one operation and writes
// the caller's command-evidence payload in the SAME operation, which is what the
// ownership read joins on: evidence reaches an episode through the operation
// that started it. The evidence carries no material, so the episode's role can
// only come from the record the caller plants.
func gateCommandEvidenceEpisode(t *testing.T, store *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, occupant provenance.ActorID, operation provenance.OperationID, record string) {
	t.Helper()
	payload := []byte(record)
	digest := sha256.Sum256(payload)
	gateRawStart(t, store, task, assignment, occupant, operation, provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    assignmentCommandResultSlot,
		TaskID:        task,
		EvidenceKind:  assignmentCommandEvidenceKind,
		ContentDigest: digest[:],
		Payload:       payload,
	})
}

// gateRawStart applies one operation that starts one owner-slot assignment
// episode and appends whatever extra effects the caller supplies. Every raw
// fixture in this file goes through here, so they all share one shape: an
// assignment start under the system authority, produced by the operation the
// ownership read names as the episode's producer.
func gateRawStart(t *testing.T, store *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, occupant provenance.ActorID, operation provenance.OperationID, extra ...provenance.Effect) {
	t.Helper()
	_, authority, found, err := readSystemIdentity(store.auditDB)
	require.NoError(t, err)
	require.True(t, found)
	effects := append([]provenance.Effect{{
		Sort:         provenance.EffectAssignmentStart,
		ResultSlot:   "authority",
		TaskID:       task,
		AssignmentID: assignment,
		SlotID:       provenance.SlotOwnerResponsibility,
		Occupant:     occupant,
	}}, extra...)
	_, err = store.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            occupant,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte(operation),
		Effects:            effects,
	})
	require.NoError(t, err)
}

// gateCommandRecord builds one command-evidence payload in the exact shape a
// Pasture assignment command writes: the closed record, canonically encoded. The
// reader decodes command evidence strictly and re-checks that the stored bytes
// are canonical, so a hand-written JSON literal would fail at the DECODE arm and
// every refusal below it would go untested. Encoding the production struct is
// what makes each subject reach the arm it names.
func gateCommandRecord(t *testing.T, record assignmentCommandRecord) string {
	t.Helper()
	encoded, err := canonicalJSON(record)
	require.NoError(t, err)
	return string(encoded)
}

// gateMaterialPayload builds one assignment-start material payload with a role
// token of the caller's choosing, valid or not.
func gateMaterialPayload(t *testing.T, assignment, role, occupant string) string {
	t.Helper()
	encoded, err := canonicalJSON(assignmentStartPayload{Assignment: assignment, Role: role, Occupant: occupant})
	require.NoError(t, err)
	return string(encoded)
}

// gateAbsentMaterialRunner removes every assignment-start material effect from a
// composed allocation and delegates to the real allocator. It models the
// historical producer footprint that wrote a command's evidence and its child
// assignments but no material, which is the only shape the command-evidence role
// path exists for.
type gateAbsentMaterialRunner struct {
	composedAllocationRunner
}

// strip is applied ONLY on the batch path. The single-allocation path is left
// alone because the commands below it resolve their parent assignment through
// the material the earlier command wrote; a store with no material anywhere
// cannot reach the command that omits it.
func (r gateAbsentMaterialRunner) strip(request provenance.GovernedAllocationComposedRequest) provenance.GovernedAllocationComposedRequest {
	kept := make([]provenance.Effect, 0, len(request.SupplementalEffects))
	for _, effect := range request.SupplementalEffects {
		if effect.Sort == provenance.EffectTaskEvent && effect.EventKind == FamilyAssignmentStarted.EventKind() {
			continue
		}
		kept = append(kept, effect)
	}
	request.SupplementalEffects = kept
	return request
}

func (r gateAbsentMaterialRunner) RunAllocateComposedBatch(ctx context.Context, workflow string, authority provenance.JournalID, request provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error) {
	return r.composedAllocationRunner.RunAllocateComposedBatch(ctx, workflow, authority, r.strip(request))
}

// gatePlanFixture is the smallest store the composed writers accept: an epoch
// root and a plan with a governing supervisor on it. Every composed writer
// resolves that assignment before it writes anything.
type gatePlanFixtureValue struct {
	store   *trackerImpl
	service EpochService
	actor   provenance.ActorID
	epoch   EpochRootID
	plan    provenance.TaskID
}

func newGatePlanFixture(t *testing.T, handle string) gatePlanFixtureValue {
	t.Helper()
	store, _ := openGateStore(t)
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, handle)
	epoch := createHumanTestTask(t, store, handle+"-epoch")
	plan := createHumanTestTask(t, store, handle+"-plan")
	seedAssignmentEpisode(t, store, plan, "gate-supervisor", RoleGoverningSupervisor, actor, provenance.OperationID(handle+"-supervisor"))
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	return gatePlanFixtureValue{store: store, service: service, actor: actor, epoch: EpochRootID(epoch.String()), plan: plan}
}

// gateSlice drives a real slice creation and returns the slice task.
func (f gatePlanFixtureValue) gateSlice(t *testing.T, operation provenance.OperationID) provenance.TaskID {
	t.Helper()
	_, err := f.service.CreateSlice(t.Context(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: operation},
		Epoch:      f.epoch,
		Plan:       f.plan,
		Assignment: "gate-supervisor",
	})
	require.NoError(t, err)
	return deterministicTask(operation, "slice")
}

// gatePolicyInput is a gate consultation that reached the assignment rules: a
// valid runtime vocabulary, the session claim, and the authority.
func gatePolicyInput(claim gateauthority.SessionClaim, authority gateauthority.ActorAuthority) gatepolicy.Input {
	return gatepolicy.Input{
		Event:      registration.EventPreToolUse,
		Action:     gateauthority.ActionToolUse,
		Semantic:   runtime.SemanticGateConsultation,
		StopLoop:   runtime.StopLoopNotApplicable,
		Capability: runtime.CapabilityDeny,
		Claim:      claim,
		Authority:  authority,
	}
}

func gatePolicyReason(t *testing.T, result gatepolicy.Result) backend.DecisionReason {
	t.Helper()
	decision, decided := result.Decision()
	require.True(t, decided, "expected a decision, got %+v", result)
	return decision.Reason()
}

// ─── the bound path ───────────────────────────────────────────────────────────

// TestGateReaderBoundPathMakesOneClaimReadAndOnePublicCall is the whole read
// contract in one subject: a bound session costs exactly one Pasture-owned
// claim SELECT and exactly one public ownership call, reaches no other journal
// read, and leaves the claim connection behind before the public call so that
// call is not waiting on a lease the reader still holds.
//
// MUTATION: add a second claim read, or reach any other journal read, and the
// counted statements or the forbidden-read counter turn this RED. Hold the claim
// connection across the public call — a dedicated lease on the store's own pool
// that Snapshot releases only after QueryActorOwnership returns — and the
// InUse reading at the interleave point turns this RED, because the pool-of-one
// starvation subject cannot see it (it swaps in a second pool, so the two
// subsystems never compete there; the release is observed here instead).
func TestGateReaderBoundPathMakesOneClaimReadAndOnePublicCall(t *testing.T) {
	t.Parallel()
	store, path := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-bound")
	task := createHumanTestTask(t, store, "bound")
	seedAssignmentEpisode(t, store, task, "bound-owner", RoleOwnerResponsibility, actor, "bound-owner-start")
	gateClaim(t, store, actor, "bound-session")
	// The counter is installed after the claim row exists, so it measures the
	// reader's read and not the writer's write.
	counts := countGateStore(t, store, path, gateClaimReadToken)

	// A second pool on the same file, so a write can be committed from inside
	// the public call without borrowing a connection the reader still holds.
	writer, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`CREATE TABLE gate_bound_interleave (note TEXT NOT NULL)`)
	require.NoError(t, err)

	journal := &gateCountingJournal{Journal: store.Journal()}
	journal.beforeOwnership = func() error {
		// The boundary this subject exists for. The claim read is complete and
		// its connection is back in the pool, so the public call below is about to
		// compete for the same pool of one with nothing held. A reader that kept
		// the claim lease would show InUse=1 here and would make the public call
		// queue behind its own lease; the write that follows commits only because
		// the lease is free, and this is the assertion that says so.
		require.Zero(t, store.auditDB.Stats().InUse,
			"the claim lease must be released before the public ownership call; a connection still checked out here is the reader waiting on itself")
		bounded, cancel := context.WithTimeout(t.Context(), timeouts.TestProfile().SQLiteBusy())
		defer cancel()
		_, err := writer.ExecContext(bounded, `INSERT INTO gate_bound_interleave (note) VALUES ('between')`)
		return err
	}
	gateJournal(t, store, journal)

	snapshot := gateSnapshot(t, store, "bound-session")
	ownership, other := journal.counts()
	require.Equal(t, 1, counts.count(gateClaimReadToken), "a bound snapshot must read the claim table exactly once")
	require.Equal(t, 1, ownership, "a bound snapshot must make exactly one public ownership call")
	require.Zero(t, other, "a bound snapshot must reach no other journal read")

	claim, err := snapshot.ResolveSession(gateHarness, "bound-session")
	require.NoError(t, err)
	require.True(t, claim.Bound)
	require.True(t, claim.Known)
	require.Equal(t, actor, claim.Actor)
	require.Equal(t, "bound-session", claim.Session)
	authority, err := snapshot.Authority(actor)
	require.NoError(t, err)
	require.Equal(t, actor, authority.Actor)
	require.Equal(t, []gateauthority.Episode{{
		Assignment: "bound-owner",
		Task:       task,
		Role:       gateauthority.RoleOwnerResponsibility,
		Phase:      gateauthority.PhaseUnscoped,
	}}, authority.Episodes)

	var notes int
	require.NoError(t, writer.QueryRow(`SELECT COUNT(*) FROM gate_bound_interleave`).Scan(&notes))
	require.Equal(t, 1, notes, "the interleaving write must commit between the claim read and the public call")
}

// TestGateReaderUnboundSessionMakesNoProvenanceCall proves both unbound paths.
// A session with no claim row and an invocation that carried no session at all
// are both unbound, and neither may reach the ownership read.
func TestGateReaderUnboundSessionMakesNoProvenanceCall(t *testing.T) {
	t.Parallel()
	store, path := openGateStore(t)
	counts := countGateStore(t, store, path, gateClaimReadToken)
	journal := &gateCountingJournal{Journal: store.Journal()}
	gateJournal(t, store, journal)

	snapshot := gateSnapshot(t, store, "unclaimed-session")
	claim, err := snapshot.ResolveSession(gateHarness, "unclaimed-session")
	require.NoError(t, err)
	require.False(t, claim.Bound)
	require.False(t, claim.Known)
	require.Equal(t, "unclaimed-session", claim.Session)
	authority, err := snapshot.Authority(provenance.ActorID{})
	require.NoError(t, err)
	require.Empty(t, authority.Episodes)
	require.Equal(t, 1, counts.count(gateClaimReadToken), "a session with no claim row costs one claim read")

	before := counts.count(gateClaimReadToken)
	empty := gateSnapshot(t, store, "")
	emptyClaim, err := empty.ResolveSession(gateHarness, "")
	require.NoError(t, err)
	require.False(t, emptyClaim.Bound)
	require.Equal(t, "", emptyClaim.Session)
	require.Equal(t, before, counts.count(gateClaimReadToken), "an invocation with no session must issue no SQL at all")

	ownership, other := journal.counts()
	require.Zero(t, ownership, "an unbound snapshot must make no public ownership call")
	require.Zero(t, other, "an unbound snapshot must reach no journal read at all")
}

// TestGateReaderClaimedUnknownActor proves a claim that parses but names no
// registered actor is a complete bound fact with no episodes. The policy denies
// it as an unknown actor, which is a different answer from an unbound session.
func TestGateReaderClaimedUnknownActor(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	known := feasibilityActor(t, store, "gate-known")
	stranger, err := provenance.ParseActorID("gate-stranger--0193f1c0-0000-7000-8000-0000000000fe")
	require.NoError(t, err)
	gateClaim(t, store, known, "known-session")
	gateClaim(t, store, stranger, "stranger-session")

	snapshot := gateSnapshot(t, store, "stranger-session")
	claim, err := snapshot.ResolveSession(gateHarness, "stranger-session")
	require.NoError(t, err)
	require.True(t, claim.Bound, "a claim that parses is a bound session, not an unbound one")
	require.Equal(t, stranger, claim.Actor)
	require.False(t, claim.Known, "an actor that was never registered is not known")
	authority, err := snapshot.Authority(stranger)
	require.NoError(t, err)
	require.Empty(t, authority.Episodes)

	result, err := gatepolicy.Decide(gatePolicyInput(claim, authority))
	require.NoError(t, err)
	require.Equal(t, backend.ReasonUnknownActor, gatePolicyReason(t, result))
}

// TestGateReaderMalformedClaimFaults proves a claim row whose actor is not an
// actor identifier is a fault. It is never downgraded to an unbound session or
// to an unknown actor, because both of those are policy answers.
func TestGateReaderMalformedClaimFaults(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	_, err := store.auditDB.Exec(
		`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)`,
		string(gateHarness), "malformed-session", "not-an-actor-id", int64(1),
	)
	require.NoError(t, err)
	reader, err := NewGateReader(store, gateHarness, "malformed-session")
	require.NoError(t, err)
	_, err = reader.Snapshot(t.Context())
	var malformed *GateClaimMalformedError
	require.ErrorAs(t, err, &malformed)
	require.Equal(t, gateHarness, malformed.Harness)
	require.Equal(t, "malformed-session", malformed.Session)
	require.NotNil(t, malformed.Cause, "the parse cause must stay reachable")
	for _, phrase := range []string{"malformed-session", "pasture_session_claim", "Fix:"} {
		require.Contains(t, err.Error(), phrase)
	}
	var read *GateReadError
	require.False(t, errors.As(err, &read), "a malformed claim is not a read fault: %v", err)
}

// TestGateReaderClaimReadFaultsWhenThePoolStaysBusy takes the store's only
// pooled connection and requires the claim read to fault at the store's own busy
// tier. A claim read that answered anything here would be answering from
// nothing, because the session is unknowable while the file is locked.
func TestGateReaderClaimReadFaultsWhenThePoolStaysBusy(t *testing.T) {
	store, _ := openGateStore(t, WithTimeoutProfile(timeouts.DeadlineTestProfile()))
	gateClaim(t, store, feasibilityActor(t, store, "gate-busy"), "busy-session")

	held, err := store.auditDB.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })

	reader, err := NewGateReader(store, gateHarness, "busy-session")
	require.NoError(t, err)
	_, err = reader.Snapshot(t.Context())
	var read *GateReadError
	require.ErrorAs(t, err, &read)
	require.Equal(t, GateReadStageClaim, read.Stage)
	require.Equal(t, "gate claim read", read.Stage.String())
	require.Equal(t, gateHarness, read.Harness)
	require.Equal(t, "busy-session", read.Session)
	require.NotNil(t, read.Cause)
	// The Impact clause names BOTH policies, because the host's own setting
	// decides whether a fault continues the action or refuses it. A clause that
	// asserted "failed open" here would describe a configuration the operator
	// never chose.
	for _, phrase := range []string{"gate claim read", "under the default policy the action continued unjudged", "PASTURE_HOOK_FAIL_CLOSED=1", "Fix:"} {
		require.Contains(t, err.Error(), phrase)
	}
}

// ─── the claim insert interleave ──────────────────────────────────────────────

// TestGateReaderClaimInsertRacingTheSnapshotCannotTearIt puts a concurrent claim
// INSERT at the three positions that matter around the claim read and requires
// one whole answer each time. A snapshot that tore would report a session the
// store never had, or a claim the store already had.
//
// RED when: the sealed answer depends on when the insert lands, the answer moves
// after it is sealed, or the reader issues a second claim read to notice.
func TestGateReaderClaimInsertRacingTheSnapshotCannotTearIt(t *testing.T) {
	t.Parallel()
	t.Run("present-before-read", func(t *testing.T) {
		t.Parallel()
		store, path := openGateStore(t)
		actor := feasibilityActor(t, store, "gate-present")
		gateClaim(t, store, actor, "present-session")
		counts := countGateStore(t, store, path, gateClaimReadToken)
		snapshot := gateSnapshot(t, store, "present-session")
		claim, err := snapshot.ResolveSession(gateHarness, "present-session")
		require.NoError(t, err)
		require.True(t, claim.Bound)
		require.Equal(t, actor, claim.Actor)
		require.Equal(t, 1, counts.count(gateClaimReadToken))
	})
	t.Run("insert-at-read-boundary", func(t *testing.T) {
		t.Parallel()
		store, path := openGateStore(t)
		counts := countGateStore(t, store, path, gateClaimReadToken)
		actor := feasibilityActor(t, store, "gate-boundary")
		reader, err := NewGateReader(store, gateHarness, "boundary-session")
		require.NoError(t, err)
		concrete, ok := reader.(*gateReader)
		require.True(t, ok, "NewGateReader must return the concrete reader the hook belongs to")

		claimRead := make(chan struct{})
		allowSeal := make(chan struct{})
		concrete.afterClaimRead = func() {
			close(claimRead)
			<-allowSeal
		}
		type outcome struct {
			snapshot gateauthority.Snapshot
			err      error
		}
		sealed := make(chan outcome, 1)
		go func() {
			snapshot, snapshotErr := reader.Snapshot(t.Context())
			sealed <- outcome{snapshot, snapshotErr}
		}()
		<-claimRead

		// A second connection commits the claim while the Reader is stopped
		// after its completed no-row read and its released claim connection.
		writer, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
		require.NoError(t, err)
		_, err = writer.Exec(
			`INSERT INTO pasture_session_claim (harness, session, actor, claimed_at) VALUES (?, ?, ?, ?)`,
			string(gateHarness), "boundary-session", actor.String(), gateClock{}.Now().UnixNano(),
		)
		require.NoError(t, err, "a writer must be able to commit while the Reader is stopped at the read boundary")
		require.NoError(t, writer.Close())

		close(allowSeal)
		result := <-sealed
		require.NoError(t, result.err)
		t.Cleanup(func() { require.NoError(t, result.snapshot.Close()) })
		claim, err := result.snapshot.ResolveSession(gateHarness, "boundary-session")
		require.NoError(t, err)
		require.False(t, claim.Bound, "the no-row read completed before the insert, so the answer is unbound")
		require.Equal(t, 1, counts.count(gateClaimReadToken), "the Reader must not re-read the claim to notice the insert")

		// A later fresh read sees the claim; the sealed answer did not move.
		fresh := gateSnapshot(t, store, "boundary-session")
		freshClaim, err := fresh.ResolveSession(gateHarness, "boundary-session")
		require.NoError(t, err)
		require.True(t, freshClaim.Bound)
		require.Equal(t, actor, freshClaim.Actor)
		again, err := result.snapshot.ResolveSession(gateHarness, "boundary-session")
		require.NoError(t, err)
		require.Equal(t, claim, again, "a sealed answer must not change after it is sealed")
	})
	t.Run("insert-after-seal", func(t *testing.T) {
		t.Parallel()
		store, _ := openGateStore(t)
		actor := feasibilityActor(t, store, "gate-after-seal")
		snapshot := gateSnapshot(t, store, "after-seal-session")
		claim, err := snapshot.ResolveSession(gateHarness, "after-seal-session")
		require.NoError(t, err)
		require.False(t, claim.Bound)
		gateClaim(t, store, actor, "after-seal-session")
		again, err := snapshot.ResolveSession(gateHarness, "after-seal-session")
		require.NoError(t, err)
		require.Equal(t, claim, again, "a sealed answer must not observe a later claim write")
		require.False(t, again.Bound)
	})
}

// TestGateReaderAfterClaimReadHookIsTestOnly holds the claim hook to test files.
// Without this pin production could arm a hook whose only purpose is to stop a
// snapshot at a boundary, and no subject would notice.
//
// The pin covers every way a field can be armed, not one: an assignment whose
// left-hand side is a selector ending in the field name, whatever that selector
// is applied to, and a composite literal that names the field as a key. The
// second is why an AssignStmt-only walk is not enough —
// `&gateReader{afterClaimRead: hook}` is a key in a literal, not an assignment,
// and it arms the hook just as well.
//
// RED when: any non-test file names the field in any of those positions.
func TestGateReaderAfterClaimReadHookIsTestOnly(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	arms := 0
	report := func(path, shape string) {
		arms++
		t.Errorf("%s arms afterClaimRead in non-test code with %s; the hook exists for in-process interleave proofs only", path, shape)
	}
	// namesHook reports whether an expression reaches the field: a selector
	// ending in the field name, whatever it is selected from, or a literal key
	// spelled with it. Both are the two shapes a field is written through.
	namesHook := func(expr ast.Expr) bool {
		switch typed := expr.(type) {
		case *ast.SelectorExpr:
			return typed.Sel != nil && typed.Sel.Name == "afterClaimRead"
		case *ast.KeyValueExpr:
			if ident, ok := typed.Key.(*ast.Ident); ok {
				return ident.Name == "afterClaimRead"
			}
		}
		return false
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err, "%s must parse; the pin reads every non-test file in the package", path)
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				for _, lhs := range typed.Lhs {
					if namesHook(lhs) {
						report(path, "an assignment")
					}
				}
			case *ast.KeyValueExpr:
				if namesHook(typed) {
					report(path, "a composite literal key")
				}
			}
			return true
		})
	}
	require.Zero(t, arms, "no non-test file may arm the claim hook")
}

// TestGateReaderCompletesOnAPoolOfOne is the starvation regression. The Pasture
// handle pools one connection and Provenance borrows the same pool, so a reader
// that held its claim lease across the public call would queue behind itself.
// The subject completes, and the bound is a deadline rather than a sleep.
//
// It is a REGRESSION and not the proof of the release. The counting pool this
// subject installs is a second *sql.DB on the same file, so the two subsystems
// stop competing for the same connection here and a held lease would not starve
// the public call. The release itself is observed where the lease would show, in
// TestGateReaderBoundPathMakesOneClaimReadAndOnePublicCall, and the pool the
// public read really borrows is proved by the subject below.
func TestGateReaderCompletesOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	store, path := openGateStore(t)
	require.Equal(t, 1, store.auditDB.Stats().MaxOpenConnections, "the Pasture handle must pool exactly one connection")
	actor := feasibilityActor(t, store, "gate-pool-one")
	task := createHumanTestTask(t, store, "pool-one")
	seedAssignmentEpisode(t, store, task, "pool-one-owner", RoleOwnerResponsibility, actor, "pool-one-owner-start")
	gateClaim(t, store, actor, "pool-one-session")
	counts := countGateStore(t, store, path, gateClaimReadToken)

	bounded, cancel := context.WithTimeout(t.Context(), timeouts.TestProfile().WorkflowResult())
	defer cancel()
	reader, err := NewGateReader(store, gateHarness, "pool-one-session")
	require.NoError(t, err)
	snapshot, err := reader.Snapshot(bounded)
	require.NoError(t, err, "a snapshot on a pool of one must complete; holding the claim lease would deadlock it")
	t.Cleanup(func() { require.NoError(t, snapshot.Close()) })
	require.Equal(t, 1, counts.count(gateClaimReadToken))
}

// TestGateReaderPublicCallBorrowsTheStoresOwnConnection is the hazard the claim
// read's release exists for, shown on the pool the production topology actually
// shares. Nothing is substituted: this store is the unified one, Provenance
// borrowed its audit handle (open_unified.go), and the connection the subject
// takes is the same connection the claim read borrows.
//
// The subject takes the store's single connection and issues the public
// ownership read the Reader issues. The read queues for that connection — the
// pool records the wait and no second connection appears — and it is still
// queued when the caller cancels, because the borrowed handle's liveness
// precheck pings with a background context and so is not bounded by the
// caller's. Only releasing the connection ends it. That is the hazard: a reader
// holding the claim lease across this call would be waiting for the connection
// it is itself holding, and nothing in its own context would end that wait.
//
// RED when: the public read stops borrowing this store's pool, because then a
// held claim lease would not be a self-wait at all and the release would be
// unexplained tidiness rather than a requirement.
func TestGateReaderPublicCallBorrowsTheStoresOwnConnection(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-lease-hazard")
	task := createHumanTestTask(t, store, "lease-hazard")
	seedAssignmentEpisode(t, store, task, "lease-hazard-owner", RoleOwnerResponsibility, actor, "lease-hazard-owner-start")
	gateClaim(t, store, actor, "lease-hazard-session")

	api, ok := store.Journal().(provenance.ActorOwnershipQueryAPI)
	require.True(t, ok, "the unified store's journal must offer the public ownership read")
	query := provenance.ActorOwnershipQuery{
		Actor:         actor,
		MaterialKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		EvidenceKinds: []provenance.EvidenceKind{assignmentCommandEvidenceKind},
	}

	// Baseline first: the identical read answers when the pool is free, so what
	// follows is attributable to the hold and to nothing else.
	freed, err := api.QueryActorOwnership(t.Context(), query)
	require.NoError(t, err, "the public read must answer while the pool is free")
	require.Len(t, freed.Tasks, 1)

	require.Equal(t, 1, store.auditDB.Stats().MaxOpenConnections, "the hazard needs a pool of one")
	held, err := store.auditDB.Conn(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, store.auditDB.Stats().InUse, "the store's only connection must be the one held")

	bounded, cancel := context.WithCancel(t.Context())
	type answer struct {
		ownership provenance.ActorOwnershipSnapshot
		err       error
	}
	answered := make(chan answer, 1)
	go func() {
		ownership, readErr := api.QueryActorOwnership(bounded, query)
		answered <- answer{ownership, readErr}
	}()

	// A condition on the pool's own counters, never a sleep: WaitCount rises
	// only once the read's connection request has actually queued behind the
	// held one, and the tick is a poll cadence rather than part of the claim.
	require.Eventually(t, func() bool { return store.auditDB.Stats().WaitCount > 0 },
		timeouts.TestProfile().WorkflowResult(), time.Millisecond,
		"the public read must queue for the connection the store is still holding")
	require.Equal(t, 1, store.auditDB.Stats().InUse, "a second connection must not appear while one is held")

	// The wait is not bounded by the caller's context: cancelling here must not
	// release the read, because the liveness precheck pings with a background
	// context of its own.
	cancel()
	select {
	case <-answered:
		t.Fatal("the queued read finished while the only connection was still held")
	default:
	}

	require.NoError(t, held.Close())
	var result answer
	select {
	case result = <-answered:
	case <-time.After(timeouts.TestProfile().WorkflowResult()):
		t.Fatal("the queued read did not settle after the held connection was released")
	}
	// A precheck that had honoured the cancellation would have reported the
	// borrowed handle as unavailable; this read got its connection and failed on
	// its own context, which is what proves the precheck waited past it.
	var unavailable *provenance.StoreUnavailableError
	require.NotErrorAs(t, result.err, &unavailable,
		"the liveness precheck pinged with a background context, so the wait outlived the caller's cancellation")
	require.ErrorIs(t, result.err, context.Canceled)

	recovered, err := api.QueryActorOwnership(t.Context(), query)
	require.NoError(t, err, "the read is sound; only the held connection stood between it and its answer")
	require.Len(t, recovered.Tasks, 1)
}

// TestGateReaderReadsUnderAHeldWriter proves a read is not a write in disguise.
// Another handle holds the file's write transaction for as long as the subject
// needs, and the reader still completes. The hold is released by a condition
// the subject signals, never by a sleep.
func TestGateReaderReadsUnderAHeldWriter(t *testing.T) {
	t.Parallel()
	store, path := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-held-writer")
	task := createHumanTestTask(t, store, "held-writer")
	seedAssignmentEpisode(t, store, task, "held-writer-owner", RoleOwnerResponsibility, actor, "held-writer-owner-start")
	gateClaim(t, store, actor, "held-writer-session")

	holder, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })
	_, err = holder.Exec(`CREATE TABLE gate_held_writer (note TEXT NOT NULL)`)
	require.NoError(t, err)
	held := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		tx, beginErr := holder.BeginTx(t.Context(), nil)
		if beginErr != nil {
			holderDone <- beginErr
			return
		}
		if _, execErr := tx.Exec(`INSERT INTO gate_held_writer (note) VALUES ('held')`); execErr != nil {
			_ = tx.Rollback()
			holderDone <- execErr
			return
		}
		close(held)
		<-release
		holderDone <- tx.Rollback()
	}()
	<-held

	bounded, cancel := context.WithTimeout(t.Context(), timeouts.TestProfile().WorkflowResult())
	defer cancel()
	reader, err := NewGateReader(store, gateHarness, "held-writer-session")
	require.NoError(t, err)
	snapshot, err := reader.Snapshot(bounded)
	close(release)
	require.NoError(t, <-holderDone)
	require.NoError(t, err, "a read must not need the file's write lock")
	t.Cleanup(func() { require.NoError(t, snapshot.Close()) })
	authority, err := snapshot.Authority(actor)
	require.NoError(t, err)
	require.Len(t, authority.Episodes, 1)
}

// TestGateReaderUnmappedPhaseReachesPolicyAsUnset is the reader's own mapping
// contract. A phase the gate does not know must arrive as the unset phase and
// become the policy's refusal, never a guessed phase.
//
// The row is built directly because the real store cannot deliver one: the
// pinned task-store read refuses an out-of-range phase before the reader sees
// it, which is the sibling subject below. Driving ownedTaskEpisode directly is
// what keeps the reader's arm covered.
//
// RED when: the reader renames an unknown phase to a known one, or the policy
// answers an unset phase with anything other than a refusal.
func TestGateReaderUnmappedPhaseReachesPolicyAsUnset(t *testing.T) {
	t.Parallel()
	// The row carries a readable role and one unmapped phase, so the only
	// thing under test is the phase arm.
	payload, err := canonicalJSON(assignmentStartPayload{
		Assignment: "unmapped-assignment",
		Role:       RoleOwnerResponsibility.String(),
		Occupant:   "human-service--0193f1c0-0000-7000-8000-0000000000ff",
	})
	require.NoError(t, err)
	episode, err := ownedTaskEpisode(provenance.ActorID{}, provenance.OwnedTaskRow{
		AssignmentID: "unmapped-assignment",
		Phase:        provenance.Phase(99),
		Materials:    []provenance.OwnedMaterialRow{{Payload: payload}},
	})
	require.NoError(t, err, "an unmapped phase is not a reader fault; it is the policy's refusal")
	require.Equal(t, gateauthority.TaskPhaseUnset, episode.Phase)
	require.Equal(t, "phase-unset", episode.Phase.String())

	claim := gateauthority.SessionClaim{Bound: true, Known: true}
	result, err := gatepolicy.Decide(gatePolicyInput(claim, gateauthority.ActorAuthority{Episodes: []gateauthority.Episode{episode}}))
	require.NoError(t, err)
	refusal, refused := result.Refusal()
	require.True(t, refused, "an unset phase is a refusal, not a denial: %+v", result)
	require.Equal(t, gatepolicy.RefusalPhaseUnknown, refusal.Kind())
}

// TestGateReaderTransferPredecessorGrantsOwnerRoleIsTheAcceptedResidual pins the
// one inference in the reader that is accepted rather than prevented.
//
// A transfer successor names its predecessor and the reader reads the role from
// that naming alone, so a successor of a REVIEWER episode is reported as the
// owner role. Pasture's own transfer command only ever moves an owner episode,
// so a supported writer cannot produce this row. A transfer written through
// Provenance's RAW API by a writer that is not that command can, and the
// successor then holds the owner role here.
//
// That residual is ACCEPTED, not prevented: the landed write path refuses the
// raw move, and the correction belongs there. This subject exists so the
// acceptance is pinned rather than implied — if a later change tightens this
// inference, the tightening is a decision to be taken deliberately, and this
// subject is where it shows up.
//
// The row carries the predecessor naming AND a material row that names this
// very episode as a reviewer. The material is what the reader would read if it
// did not trust the predecessor, and the assertion is that it does not read it.
// A reader that fell through to the material would report the reviewer role and
// go red on the assertion below, with the accepted answer spelled out.
//
// RED when: the reader stops granting the owner role to a transfer successor
// (the tightening the acceptance is waiting for) or starts reading the
// successor's own material instead of its predecessor's naming.
func TestGateReaderTransferPredecessorGrantsOwnerRoleIsTheAcceptedResidual(t *testing.T) {
	t.Parallel()
	actor, err := provenance.ParseActorID("gate-residual--0193f1c0-0000-7000-8000-0000000000f7")
	require.NoError(t, err)
	task, err := provenance.ParseTaskID("task-residual--0193f1c0-0000-7000-8000-0000000000f8")
	require.NoError(t, err)
	predecessor := provenance.AssignmentID("residual-reviewer-episode")
	payload, err := canonicalJSON(assignmentStartPayload{
		Assignment: "residual-successor",
		Role:       RoleAxisReviewer.String(),
		Occupant:   actor.String(),
	})
	require.NoError(t, err)

	episode, err := ownedTaskEpisode(actor, provenance.OwnedTaskRow{
		AssignmentID:            "residual-successor",
		TaskID:                  task,
		PredecessorAssignmentID: &predecessor,
		Phase:                   provenance.PhaseCodeReview,
		Materials: []provenance.OwnedMaterialRow{{
			EventKind: FamilyAssignmentStarted.EventKind(),
			Payload:   payload,
		}},
	})
	require.NoError(t, err,
		"a transfer successor's role is read from its predecessor naming, which names a reviewer episode here")
	require.Equal(t, "residual-successor", string(episode.Assignment))
	require.Equal(t, gateauthority.RoleOwnerResponsibility, episode.Role,
		"the ACCEPTED answer: a transfer successor inherits the owner role from its predecessor's naming, "+
			"even when that predecessor is a reviewer episode, because a supported transfer command can only "+
			"move an owner episode. A reviewer role here is the tightening the accepted residual is waiting "+
			"for; it is not a fix, and the raw-transfer path that can produce this row is refused on the "+
			"write side")
}

// TestGateReaderUnmappedPhaseInTheStoreIsAnIntegrityFault records what the real
// store does with a phase outside its own vocabulary. The pinned task-store read
// refuses the row itself, so the reader never receives a phase to map and the
// answer is an integrity fault rather than an unset phase. Keeping this beside
// the mapping subject above is what makes the difference deliberate.
//
// The two writes are labelled test-only fixture boundaries on Provenance-owned
// tables. The insert exists because tasks.phase_id really references phases.id,
// so the update would otherwise violate a live foreign key.
func TestGateReaderUnmappedPhaseInTheStoreIsAnIntegrityFault(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-unmapped")
	task := createHumanTestTask(t, store, "unmapped")
	seedAssignmentEpisode(t, store, task, "unmapped-owner", RoleOwnerResponsibility, actor, "unmapped-owner-start")
	gateClaim(t, store, actor, "unmapped-session")

	// TEST-ONLY FIXTURE BOUNDARY on Provenance-owned tables.
	_, err := store.auditDB.Exec(`INSERT INTO phases(id,name) VALUES (99,'test-unmapped-phase')`)
	require.NoError(t, err)
	// TEST-ONLY FIXTURE BOUNDARY on Provenance-owned tables.
	_, err = store.auditDB.Exec(`UPDATE tasks SET phase_id=99 WHERE id=?`, task.String())
	require.NoError(t, err)

	reader, err := NewGateReader(store, gateHarness, "unmapped-session")
	require.NoError(t, err)
	_, err = reader.Snapshot(t.Context())
	var integrity *GateReadIntegrityError
	require.ErrorAs(t, err, &integrity)
	require.Equal(t, provenance.ActorOwnershipStageOwnedTasks, integrity.Stage)
	require.ErrorIs(t, err, provenance.ErrSubtypeIntegrity)
	require.Contains(t, err.Error(), "gate ownership read (stage owned-tasks)")
}

// ─── the per-writer mapping ───────────────────────────────────────────────────

// TestGateReaderMapsOwnedTasksPerWriter drives the real assignment writers and
// requires the reader to name the role each one wrote. A role inference that is
// right for one writer and wrong for another is a fault a per-reader subject
// cannot see.
//
// RED when: any writer's role is read from a different source, or the transfer
// successor stops carrying the owner role it was written with.
func TestGateReaderMapsOwnedTasksPerWriter(t *testing.T) {
	t.Parallel()
	t.Run("slice-owner", func(t *testing.T) {
		t.Parallel()
		fixture := newGatePlanFixture(t, "gate-slice-owner")
		fixture.gateSlice(t, "gate-slice")
		gateClaim(t, fixture.store, fixture.actor, "slice-owner-session")
		episode := gateEpisode(t, fixture.store, fixture.actor, "slice-owner-session", "gate-slice-slice-owner")
		require.Equal(t, provenance.AssignmentID("gate-slice-slice-owner"), episode.Assignment)
		require.Equal(t, gateauthority.RoleOwnerResponsibility, episode.Role, "a slice owner writes owner responsibility")
	})
	t.Run("candidate-owner", func(t *testing.T) {
		t.Parallel()
		fixture := newGatePlanFixture(t, "gate-candidate-owner")
		slice := fixture.gateSlice(t, "gate-candidate-slice")
		_, err := fixture.service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
			Meta:       CommandMeta{OperationID: "gate-candidate"},
			Epoch:      fixture.epoch,
			Slice:      slice,
			Repository: "repo",
			Commit:     "0123456789abcdef0123456789abcdef01234567",
			Assignment: "gate-candidate-slice-slice-owner",
		})
		require.NoError(t, err)
		gateClaim(t, fixture.store, fixture.actor, "candidate-owner-session")
		episode := gateEpisode(t, fixture.store, fixture.actor, "candidate-owner-session", "gate-candidate-candidate-owner")
		require.Equal(t, provenance.AssignmentID("gate-candidate-candidate-owner"), episode.Assignment)
		require.Equal(t, gateauthority.RoleOwnerResponsibility, episode.Role, "a slice candidate owner writes owner responsibility")
	})
	t.Run("supervisor-child", func(t *testing.T) {
		t.Parallel()
		fixture := newGatePlanFixture(t, "gate-integration")
		slice := fixture.gateSlice(t, "gate-integration-slice")
		member, err := fixture.service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
			Meta:       CommandMeta{OperationID: "gate-integration-member"},
			Epoch:      fixture.epoch,
			Slice:      slice,
			Repository: "repo-a",
			Commit:     "0123456789abcdef0123456789abcdef01234567",
			Assignment: "gate-integration-slice-slice-owner",
		})
		require.NoError(t, err)
		_, err = fixture.service.CreateIntegrationCandidate(t.Context(), CreateIntegrationCandidateInput{
			Meta:       CommandMeta{OperationID: "gate-integration-create"},
			Epoch:      fixture.epoch,
			Plan:       fixture.plan,
			Assignment: "gate-supervisor",
			Repositories: []RepositoryCandidate{{
				Repository: "repo-a",
				Candidate:  member.Candidate,
				Commit:     "0123456789abcdef0123456789abcdef01234567",
			}},
		})
		require.NoError(t, err)
		gateClaim(t, fixture.store, fixture.actor, "supervisor-child-session")
		episode := gateEpisode(t, fixture.store, fixture.actor, "supervisor-child-session", "gate-integration-create-candidate-owner")
		require.Equal(t, provenance.AssignmentID("gate-integration-create-candidate-owner"), episode.Assignment)
		require.Equal(t, gateauthority.RoleGoverningSupervisor, episode.Role, "an integration candidate's child writes governing supervisor")
	})
	t.Run("review-axis", func(t *testing.T) {
		t.Parallel()
		fixture := newGatePlanFixture(t, "gate-review-axis")
		slice := fixture.gateSlice(t, "gate-review-slice")
		member, err := fixture.service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
			Meta:       CommandMeta{OperationID: "gate-review-member"},
			Epoch:      fixture.epoch,
			Slice:      slice,
			Repository: "repo",
			Commit:     "0123456789abcdef0123456789abcdef01234567",
			Assignment: "gate-review-slice-slice-owner",
		})
		require.NoError(t, err)
		started, err := fixture.service.StartReview(t.Context(), StartReviewInput{
			Meta:    CommandMeta{OperationID: "gate-review-start"},
			Epoch:   fixture.epoch,
			Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(member.Candidate)},
		})
		require.NoError(t, err)
		axis := canonicalReviewAxes()[0]
		action := provenance.AssignmentID(string(started.OperationID) + "-axis-" + axis.String())
		gateClaim(t, fixture.store, fixture.actor, "review-axis-session")
		episode := gateEpisode(t, fixture.store, fixture.actor, "review-axis-session", action)
		require.Equal(t, action, episode.Assignment)
		require.Equal(t, deterministicTask(started.OperationID, "axis-"+axis.String()), episode.Task)
		require.Equal(t, gateauthority.RoleAxisReviewer, episode.Role, "a review axis child writes axis reviewer")
	})
	t.Run("transfer-successor", func(t *testing.T) {
		t.Parallel()
		fixture := newTaskAssignmentTransferFixture(t)
		fixture.seedOwnerAssignment(t, "before")
		_, err := fixture.tracker.TransferTaskAssignment(t.Context(), protocol.TransferTaskAssignmentRequest{
			TaskID:           fixture.task,
			Slot:             provenance.SlotOwnerResponsibility,
			NextAssignmentID: "after",
			ActorID:          fixture.actorA,
			NextOccupant:     fixture.actorB,
		})
		require.NoError(t, err)
		gateClaim(t, fixture.tracker, fixture.actorB, "transfer-session")
		snapshot := gateSnapshot(t, fixture.tracker, "transfer-session")
		authority, err := snapshot.Authority(fixture.actorB)
		require.NoError(t, err)
		require.Equal(t, []gateauthority.Episode{{
			Assignment: "after",
			Task:       fixture.task,
			Role:       gateauthority.RoleOwnerResponsibility,
			Phase:      gateauthority.PhaseWorkerSlices,
		}}, authority.Episodes, "a transfer successor is read by its predecessor, with no material of its own")
	})
}

// ─── the command-evidence role path ───────────────────────────────────────────

// TestGateReaderLegacyStartReviewWithoutMaterialUsesCommandEvidence drives the
// one shape whose role lives only in command evidence: a composed allocation
// that wrote the command and its children but no assignment-start material.
//
// The subtests are the ways that evidence can fail to answer, and each is a
// fault. A role the gate cannot source is never read as the owner role by
// default, and never becomes a policy answer.
func TestGateReaderLegacyStartReviewWithoutMaterialUsesCommandEvidence(t *testing.T) {
	t.Parallel()
	t.Run("legacy-review-absent", func(t *testing.T) {
		t.Parallel()
		fixture := newGatePlanFixture(t, "gate-legacy-review")
		fixture.store.allocationRunner = gateAbsentMaterialRunner{fixture.store.allocationRunner}
		slice := fixture.gateSlice(t, "legacy-slice")
		member, err := fixture.service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{
			Meta:       CommandMeta{OperationID: "legacy-member"},
			Epoch:      fixture.epoch,
			Slice:      slice,
			Repository: "repo",
			Commit:     "0123456789abcdef0123456789abcdef01234567",
			Assignment: "legacy-slice-slice-owner",
		})
		require.NoError(t, err)
		started, err := fixture.service.StartReview(t.Context(), StartReviewInput{
			Meta:    CommandMeta{OperationID: "legacy-review-start"},
			Epoch:   fixture.epoch,
			Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(member.Candidate)},
		})
		require.NoError(t, err)
		axis := canonicalReviewAxes()[0]
		action := provenance.AssignmentID(string(started.OperationID) + "-axis-" + axis.String())
		gateClaim(t, fixture.store, fixture.actor, "legacy-review-session")
		episode := gateEpisode(t, fixture.store, fixture.actor, "legacy-review-session", action)
		require.Equal(t, action, episode.Assignment, "the review axis is the episode whose material is absent")
		require.Equal(t, gateauthority.RoleAxisReviewer, episode.Role, "a started review's child role is axis reviewer")
	})
	t.Run("command-absent", func(t *testing.T) {
		t.Parallel()
		store, _ := openGateStore(t)
		actor := feasibilityActor(t, store, "gate-no-command")
		task := createHumanTestTask(t, store, "no-command")
		// An owner start whose producing operation wrote neither material nor
		// command evidence. Only a foreign writer produces this shape, which is
		// why the reader refuses it instead of assuming the owner role.
		gateRawEpisode(t, store, task, "no-command-owner", actor, "no-command-start")
		gateClaim(t, store, actor, "no-command-session")
		reader, err := NewGateReader(store, gateHarness, "no-command-session")
		require.NoError(t, err)
		_, err = reader.Snapshot(t.Context())
		requireRoleSourceFault(t, err, "no assignment-start material", "0 of its command records name a role")
	})
	t.Run("command-is-not-a-review", func(t *testing.T) {
		t.Parallel()
		store, _ := openGateStore(t)
		actor := feasibilityActor(t, store, "gate-not-a-review")
		task := createHumanTestTask(t, store, "not-a-review")
		// The evidence IS there and it IS readable, and it is a real command
		// record written by the same closed type a Pasture command writes. What
		// it is not is a review. The reader refuses it here rather than reading
		// the role off a command that never started a review, so this is the arm
		// a role guess would remove.
		gateCommandEvidenceEpisode(t, store, task, "not-a-review-owner", actor, "not-a-review-start",
			gateCommandRecord(t, assignmentCommandRecord{
				Mutation:  MutationSetSliceCandidate,
				Payload:   []byte(`{}`),
				Request:   []byte(`{}`),
				Authority: 1,
				Task:      task,
			}))
		gateClaim(t, store, actor, "not-a-review-session")
		reader, err := NewGateReader(store, gateHarness, "not-a-review-session")
		require.NoError(t, err)
		_, err = reader.Snapshot(t.Context())
		requireRoleSourceFault(t, err, "did not start a review")
	})
	t.Run("command-is-an-unreadable-review", func(t *testing.T) {
		t.Parallel()
		store, _ := openGateStore(t)
		actor := feasibilityActor(t, store, "gate-unreadable-review")
		task := createHumanTestTask(t, store, "unreadable-review")
		// A start-review record whose nested payload is not a review command. The
		// outer record decodes, so this is the arm AFTER the decode: the binding
		// check refused what the command claims to be.
		gateCommandEvidenceEpisode(t, store, task, "unreadable-review-owner", actor, "unreadable-review-start",
			gateCommandRecord(t, assignmentCommandRecord{
				Mutation:  MutationStartReview,
				Payload:   []byte(`{"kind":"","subject":{}}`),
				Request:   []byte(`{}`),
				Authority: 1,
				Task:      task,
			}))
		gateClaim(t, store, actor, "unreadable-review-session")
		reader, err := NewGateReader(store, gateHarness, "unreadable-review-session")
		require.NoError(t, err)
		_, err = reader.Snapshot(t.Context())
		requireRoleSourceFault(t, err, "could not be read as a review command")
	})
	t.Run("present-malformed", func(t *testing.T) {
		t.Parallel()
		store, _ := openGateStore(t)
		actor := feasibilityActor(t, store, "gate-malformed")
		task := createHumanTestTask(t, store, "malformed-material")
		// The writer that produced this episode wrote an assignment-start
		// material row that names no role. A supported writer cannot do that.
		gateRawEpisode(t, store, task, "malformed-owner", actor, "malformed-start",
			`{"assignment":"malformed-owner","role":"owner-responsibility"}`)
		gateClaim(t, store, actor, "malformed-session")
		reader, err := NewGateReader(store, gateHarness, "malformed-session")
		require.NoError(t, err)
		_, err = reader.Snapshot(t.Context())
		requireRoleSourceFault(t, err, "could not be read", "assignment-start material")
	})
}

func requireRoleSourceFault(t *testing.T, err error, phrases ...string) {
	t.Helper()
	var fault *GateRoleSourceError
	require.ErrorAs(t, err, &fault)
	require.NotEmpty(t, fault.Assignment)
	require.NotEmpty(t, fault.Reason)
	for _, phrase := range append(phrases, "Fix:") {
		require.Contains(t, err.Error(), phrase)
	}
}

// TestGateReaderCommandEvidenceRoleVocabularyIsMirrored is the guard on the one
// legacyReviewRole arm no store can reach today.
//
// The role on the command-evidence path is not read from the record. It is
// computed by the closed command binding, which returns one of a fixed set of
// child roles, and the reader then bridges that role into the gate's own
// vocabulary. The bridge refuses a token it does not know, and while the two
// vocabularies name the same roles that refusal is unreachable: every role the
// binding can return is a role the gate knows.
//
// This subject pins the precondition, so the arm stays a stated contract rather
// than an untested guess. If a role is added to ONE vocabulary and not the
// other, the bridge's refusal becomes reachable and a reader that had replaced
// it with a guess would hand the gate an owner role on a fact it does not
// understand — and this subject goes red on the divergence instead.
//
// RED when: the task vocabulary and the gate vocabulary stop naming the same
// roles, in either direction.
func TestGateReaderCommandEvidenceRoleVocabularyIsMirrored(t *testing.T) {
	t.Parallel()
	// Every role the task store declares valid, found by probing the closed
	// enum rather than by a hand-kept list, so a new arm is seen here.
	store := map[string]gateauthority.AssignmentRole{}
	for arm := 0; arm <= 255; arm++ {
		role := AssignmentRole(arm)
		if !role.valid() {
			continue
		}
		token := role.String()
		require.NotEqual(t, fmt.Sprintf("AssignmentRole(%d)", arm), token,
			"the task role %d has no canonical token, so the bridge could never name it", arm)
		require.NotContains(t, store, token, "two task roles must not share one token")
		store[token] = gateauthority.RoleUnset
	}
	bridged := map[string]gateauthority.AssignmentRole{}
	for _, role := range gateauthority.AssignmentRoles() {
		bridged[role.String()] = role
	}
	require.Equal(t, len(store), len(bridged),
		"the task vocabulary declares %d role tokens and the gate vocabulary %d; a role in one and not the other makes the reader's unknown-token refusal reachable",
		len(store), len(bridged))
	for token := range store {
		role, ok := gateauthority.RoleFromToken(token)
		require.True(t, ok, "the task store writes the role token %q and the gate cannot read it", token)
		require.Equal(t, bridged[token], role, "the token %q must bridge to one gate role", token)
	}
	for token := range bridged {
		require.Contains(t, store, token, "the gate knows the role token %q and no task role writes it", token)
	}
}

// TestGateReaderRoleSourceFaults covers the three ways an episode's own material
// cannot answer which role it holds. Each is a fault.
func TestGateReaderRoleSourceFaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		payloads func(t *testing.T, actor provenance.ActorID) []string
		phrase   string
	}{
		{
			name: "duplicate",
			payloads: func(t *testing.T, actor provenance.ActorID) []string {
				return []string{
					gateMaterialPayload(t, "duplicate-owner", RoleOwnerResponsibility.String(), actor.String()),
					gateMaterialPayload(t, "duplicate-owner", RoleAxisReviewer.String(), actor.String()),
				}
			},
			phrase: "one episode has one role",
		},
		{
			name: "undecodable",
			payloads: func(*testing.T, provenance.ActorID) []string {
				// Valid JSON the store accepts, and a member the strict
				// assignment-start decoder does not know.
				return []string{`{"assignment":"undecodable-owner","role":"owner-responsibility","occupant":"x","surprise":true}`}
			},
			phrase: "could not be read",
		},
		{
			name: "unknown-token",
			payloads: func(t *testing.T, actor provenance.ActorID) []string {
				return []string{gateMaterialPayload(t, "unknown-token-owner", "owner-responsibility-of-the-future", actor.String())}
			},
			phrase: "this build does not know",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store, _ := openGateStore(t)
			actor := feasibilityActor(t, store, "gate-"+testCase.name)
			task := createHumanTestTask(t, store, testCase.name)
			assignment := provenance.AssignmentID(testCase.name + "-owner")
			gateRawEpisode(t, store, task, assignment, actor, provenance.OperationID(testCase.name+"-start"), testCase.payloads(t, actor)...)
			gateClaim(t, store, actor, testCase.name+"-session")
			reader, err := NewGateReader(store, gateHarness, testCase.name+"-session")
			require.NoError(t, err)
			_, err = reader.Snapshot(t.Context())
			var fault *GateRoleSourceError
			require.ErrorAs(t, err, &fault)
			require.Equal(t, task, fault.Task)
			require.Equal(t, assignment, fault.Assignment)
			require.Contains(t, fault.Reason, testCase.phrase)
		})
	}
}

// ─── owner-only visibility ────────────────────────────────────────────────────

// TestGateReaderOwnerOnlyVisibility is the owner-only contract. An actor that
// held a review episode on a task somebody else owns holds nothing the gate can
// see, because the gate answers from what the actor currently owns. The policy
// then denies for the one reason that fits: no active assignment of its own.
func TestGateReaderOwnerOnlyVisibility(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	owner := feasibilityActor(t, store, "gate-owner")
	reviewer := feasibilityActor(t, store, "gate-reviewer")
	task := createHumanTestTask(t, store, "owned")
	seedAssignmentEpisode(t, store, task, "reviewer-before", RoleAxisReviewer, reviewer, "reviewer-before-start")
	seedAssignmentEpisode(t, store, task, "owner-after", RoleOwnerResponsibility, owner, "owner-after-start")

	gateClaim(t, store, owner, "owner-session")
	gateClaim(t, store, reviewer, "reviewer-session")

	ownerSnapshot := gateSnapshot(t, store, "owner-session")
	ownerClaim, err := ownerSnapshot.ResolveSession(gateHarness, "owner-session")
	require.NoError(t, err)
	ownerAuthority, err := ownerSnapshot.Authority(owner)
	require.NoError(t, err)
	require.Len(t, ownerAuthority.Episodes, 1)

	reviewerSnapshot := gateSnapshot(t, store, "reviewer-session")
	claim, err := reviewerSnapshot.ResolveSession(gateHarness, "reviewer-session")
	require.NoError(t, err)
	require.True(t, claim.Bound)
	authority, err := reviewerSnapshot.Authority(reviewer)
	require.NoError(t, err)
	require.Empty(t, authority.Episodes, "a role the actor does not currently own is invisible to the gate")

	result, err := gatepolicy.Decide(gatePolicyInput(claim, authority))
	require.NoError(t, err)
	require.Equal(t, backend.ReasonNoActiveAssignment, gatePolicyReason(t, result))

	ownerResult, err := gatepolicy.Decide(gatePolicyInput(ownerClaim, ownerAuthority))
	require.NoError(t, err)
	require.Equal(t, backend.DecisionProceed, gateDecisionKind(t, ownerResult), "the owner of the task is not denied")
}

func gateDecisionKind(t *testing.T, result gatepolicy.Result) backend.DecisionKind {
	t.Helper()
	decision, decided := result.Decision()
	require.True(t, decided, "expected a decision, got %+v", result)
	return decision.Kind()
}

// TestGateReaderNewestOwnerWinsWithTwoActiveEpisodes proves two active owner
// episodes on one task are a complete answer, not a fault. The writer's newest
// active episode is the one that survives into the reader's answer.
func TestGateReaderNewestOwnerWinsWithTwoActiveEpisodes(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	first := feasibilityActor(t, store, "gate-first")
	second := feasibilityActor(t, store, "gate-second")
	task := createHumanTestTask(t, store, "two-owners")
	seedAssignmentEpisode(t, store, task, "owner-a", RoleOwnerResponsibility, first, "owner-a-start")
	seedAssignmentEpisode(t, store, task, "owner-b", RoleOwnerResponsibility, second, "owner-b-start")
	gateClaim(t, store, second, "newest-session")
	snapshot := gateSnapshot(t, store, "newest-session")
	authority, err := snapshot.Authority(second)
	require.NoError(t, err)
	require.Equal(t, []gateauthority.Episode{{
		Assignment: "owner-b",
		Task:       task,
		Role:       gateauthority.RoleOwnerResponsibility,
		Phase:      gateauthority.PhaseUnscoped,
	}}, authority.Episodes, "the writer's newest active episode is the one the reader reports")
}

// ─── faults from the ownership read ───────────────────────────────────────────

// TestGateReaderCapabilityMissing proves the reader refuses a store that cannot
// answer, and does not fall back to a second read path.
func TestGateReaderCapabilityMissing(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-capability")
	task := createHumanTestTask(t, store, "capability")
	seedAssignmentEpisode(t, store, task, "capability-owner", RoleOwnerResponsibility, actor, "capability-owner-start")
	gateClaim(t, store, actor, "capability-session")
	gateJournal(t, store, gateHiddenJournal{Journal: store.Journal()})

	reader, err := NewGateReader(store, gateHarness, "capability-session")
	require.NoError(t, err)
	_, err = reader.Snapshot(t.Context())
	var capability *GateReaderCapabilityError
	require.ErrorAs(t, err, &capability)
	require.Equal(t, "provenance.ActorOwnershipQueryAPI", capability.Missing)
	for _, phrase := range []string{"ActorOwnershipQueryAPI", "Fix:", "upgrade pasture to a build", "PASTURE_HOOK_FAIL_CLOSED=1"} {
		require.Contains(t, err.Error(), phrase)
	}
}

// TestGateReaderCancellationSurfacesTheContextError proves a context that ends
// during the ownership read is a read fault carrying the context cause, not a
// denial and not an empty answer.
func TestGateReaderCancellationSurfacesTheContextError(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-cancel")
	task := createHumanTestTask(t, store, "cancel")
	seedAssignmentEpisode(t, store, task, "cancel-owner", RoleOwnerResponsibility, actor, "cancel-owner-start")
	gateClaim(t, store, actor, "cancel-session")
	bounded, cancel := context.WithCancel(t.Context())
	gateJournal(t, store, gateCancelJournal{Journal: store.Journal(), cancel: cancel})
	reader, err := NewGateReader(store, gateHarness, "cancel-session")
	require.NoError(t, err)
	_, err = reader.Snapshot(bounded)
	var read *GateReadError
	require.ErrorAs(t, err, &read)
	require.Equal(t, GateReadStageOwnership, read.Stage)
	require.ErrorIs(t, err, context.Canceled, "the context cause must stay in the chain")
}

// TestGateReaderFaultStringsNameBothPolicies is the pin on the operator text of
// every read fault. A read fault is not a denial, and what the host then does
// with it is the OPERATOR'S setting, not the reader's: under the default policy
// the action continues unjudged, and on an evidenced blocking host with
// PASTURE_HOOK_FAIL_CLOSED=1 the host refuses it. A fault string that says
// "failed open" claims a configuration the operator never chose, so the retired
// phrasing is refused here by name and the setting is named instead.
//
// The six parts are checked on each of the five faults, because a fault that
// loses one of them is a fault an operator cannot act on.
//
// RED when: any of the five drops a part, or asserts a single outcome for a
// policy the operator controls.
func TestGateReaderFaultStringsNameBothPolicies(t *testing.T) {
	t.Parallel()
	faults := map[string]error{
		"read":       &GateReadError{Stage: GateReadStageClaim, Harness: gateHarness, Session: "s", Cause: context.Canceled},
		"integrity":  &GateReadIntegrityError{Stage: provenance.ActorOwnershipStageOwnedTasks, Cause: provenance.ErrSubtypeIntegrity},
		"claim":      &GateClaimMalformedError{Harness: gateHarness, Session: "s", Cause: context.Canceled},
		"capability": &GateReaderCapabilityError{Missing: "provenance.ActorOwnershipQueryAPI"},
		"role":       &GateRoleSourceError{Assignment: "a", Reason: "r"},
	}
	// The house shape for these strings is a leading What sentence and the five
	// labelled parts after it, so the What is checked as the opening clause
	// rather than as a label none of them carries.
	parts := []string{"Why:", "Where:", "When:", "Impact:", "Fix:"}
	for name, fault := range faults {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			text := fault.Error()
			require.True(t, strings.HasPrefix(text, "Pasture "), "the %s fault must open with what it is, got %q", name, text)
			for _, part := range parts {
				require.Contains(t, text, part, "the %s fault must carry its %s part", name, part)
			}
			require.Contains(t, text, "PASTURE_HOOK_FAIL_CLOSED=1",
				"a read fault's outcome is the operator's setting, so the string must name it")
			require.Contains(t, text, "under the default policy",
				"the default policy is the outcome most operators see, so the string must say what it is")
			require.NotContains(t, text, "failed open",
				"\"failed open\" asserts a configuration the operator did not choose and is not the reader's to claim")
		})
	}
}

var errGateStoreUnavailable = errors.New("gate test store is unavailable")

// TestGateReaderErrorCausesRemainInTheChain is the public boundary. Every cause
// the ownership read can produce must stay reachable through the Pasture error,
// and the outer Pasture stage, harness and session fields must stay readable
// beside it.
func TestGateReaderErrorCausesRemainInTheChain(t *testing.T) {
	t.Parallel()
	// The store's own ids are not the subject here; the stage, the recovered
	// cause and the sentinels are. The zero ids keep the text free of a
	// fabricated identity.
	limit := &provenance.ActorOwnershipLimitError{
		Stage:         provenance.ActorOwnershipStageMaterials,
		LimitBytes:    provenance.MaxActorOwnershipResultBytes,
		ObservedBytes: provenance.MaxActorOwnershipResultBytes + 1,
	}
	mismatch := &provenance.OwnerProjectionMismatchError{}
	cases := []struct {
		name  string
		cause error
		check func(t *testing.T, err error)
	}{
		{
			name:  "byte-limit",
			cause: limit,
			check: func(t *testing.T, err error) {
				var integrity *GateReadIntegrityError
				require.ErrorAs(t, err, &integrity)
				require.Equal(t, provenance.ActorOwnershipStageMaterials, integrity.Stage)
				var typed *provenance.ActorOwnershipLimitError
				require.ErrorAs(t, err, &typed)
				require.EqualValues(t, limit.ObservedBytes, typed.ObservedBytes)
				require.ErrorIs(t, err, provenance.ErrActorOwnershipLimit)
				require.Contains(t, err.Error(), "gate ownership read (stage materials)")
			},
		},
		{
			name:  "projection-mismatch",
			cause: mismatch,
			check: func(t *testing.T, err error) {
				var integrity *GateReadIntegrityError
				require.ErrorAs(t, err, &integrity)
				require.Equal(t, provenance.ActorOwnershipStageOwnedTasks, integrity.Stage)
				var typed *provenance.OwnerProjectionMismatchError
				require.ErrorAs(t, err, &typed)
				require.Equal(t, mismatch.Task, typed.Task)
				require.ErrorIs(t, err, provenance.ErrProjectionDivergence)
			},
		},
		{
			name:  "subtype",
			cause: fmt.Errorf("wrapped: %w", provenance.ErrSubtypeIntegrity),
			check: func(t *testing.T, err error) {
				var integrity *GateReadIntegrityError
				require.ErrorAs(t, err, &integrity)
				require.ErrorIs(t, err, provenance.ErrSubtypeIntegrity)
			},
		},
		{
			name:  "store-unavailable",
			cause: errGateStoreUnavailable,
			check: func(t *testing.T, err error) {
				var read *GateReadError
				require.ErrorAs(t, err, &read)
				require.Equal(t, GateReadStageOwnership, read.Stage)
				require.ErrorIs(t, err, errGateStoreUnavailable)
			},
		},
		{
			name:  "deadline",
			cause: fmt.Errorf("read: %w", context.DeadlineExceeded),
			check: func(t *testing.T, err error) {
				var read *GateReadError
				require.ErrorAs(t, err, &read)
				require.ErrorIs(t, err, context.DeadlineExceeded)
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store, _ := openGateStore(t)
			actor := feasibilityActor(t, store, "gate-chain")
			task := createHumanTestTask(t, store, "chain")
			seedAssignmentEpisode(t, store, task, "chain-owner", RoleOwnerResponsibility, actor, "chain-owner-start")
			gateClaim(t, store, actor, "chain-session")
			gateJournal(t, store, gateFaultJournal{Journal: store.Journal(), err: testCase.cause})
			reader, err := NewGateReader(store, gateHarness, "chain-session")
			require.NoError(t, err)
			_, err = reader.Snapshot(t.Context())
			require.Error(t, err)
			testCase.check(t, err)
			var read *GateReadError
			if errors.As(err, &read) {
				require.Equal(t, gateHarness, read.Harness, "the outer harness field must stay readable")
				require.Equal(t, "chain-session", read.Session, "the outer session field must stay readable")
			}
		})
	}
}

// ─── scope, close and construction ────────────────────────────────────────────

// TestGateReaderScopeAndClose covers the two API guards. A snapshot answers one
// session and one actor, and after Close it answers nothing.
func TestGateReaderScopeAndClose(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "gate-scope")
	other := feasibilityActor(t, store, "gate-scope-other")
	task := createHumanTestTask(t, store, "scope")
	seedAssignmentEpisode(t, store, task, "scope-owner", RoleOwnerResponsibility, actor, "scope-owner-start")
	gateClaim(t, store, actor, "scope-session")
	reader, err := NewGateReader(store, gateHarness, "scope-session")
	require.NoError(t, err)
	snapshot, err := reader.Snapshot(t.Context())
	require.NoError(t, err)

	_, err = snapshot.ResolveSession(ir.HarnessCodex, "scope-session")
	var scope *GateReaderScopeError
	require.ErrorAs(t, err, &scope)
	require.Contains(t, scope.Operation, "codex")
	_, err = snapshot.ResolveSession(gateHarness, "another-session")
	require.ErrorAs(t, err, &scope)
	_, err = snapshot.Authority(other)
	require.ErrorAs(t, err, &scope)
	require.Contains(t, scope.Operation, "authority")

	require.NoError(t, snapshot.Close())
	require.NoError(t, snapshot.Close(), "Close is idempotent")
	_, err = snapshot.ResolveSession(gateHarness, "scope-session")
	var closed *GateSnapshotClosedError
	require.ErrorAs(t, err, &closed)
	_, err = snapshot.Authority(actor)
	require.ErrorAs(t, err, &closed)
	for _, phrase := range []string{"closed snapshot", "Fix:"} {
		require.Contains(t, err.Error(), phrase)
	}
}

// TestReaderConstructionFaultKeepsItsKind proves a store the reader cannot use is
// refused as a scope fault, at construction, with its kind intact.
func TestReaderConstructionFaultKeepsItsKind(t *testing.T) {
	t.Parallel()
	_, err := NewGateReader(nil, gateHarness, "session")
	var scope *GateReaderScopeError
	require.ErrorAs(t, err, &scope)
	require.Contains(t, scope.Operation, "gate reader")
	var notCapability *GateReaderCapabilityError
	require.False(t, errors.As(err, &notCapability), "a construction fault must not change kind on the way out")

	_, err = NewGateReader(&gateForeignTracker{}, gateHarness, "session")
	require.ErrorAs(t, err, &scope)
	require.NotErrorIs(t, err, context.Canceled)

	// A tracker of the right type with no audit handle is refused the same way,
	// before any SQL.
	_, err = NewGateReader(&trackerImpl{}, gateHarness, "session")
	require.ErrorAs(t, err, &scope)

	// A nil context is refused by the same guard the reader uses everywhere.
	store, _ := openGateStore(t)
	reader, err := NewGateReader(store, gateHarness, "session")
	require.NoError(t, err)
	_, err = reader.Snapshot(nil) //nolint:staticcheck // the refusal is the subject
	require.ErrorAs(t, err, &scope)
}
