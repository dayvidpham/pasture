package handlers

// This file proves the gate read is WIRED, at the layer where the wiring is
// visible: the reader factory that hookLifecycle and HookLifecycleRaw are given,
// and the verdict that reaches the committed consultation.
//
// WHAT IT CAN AND CANNOT SEE, because that difference is why the proofs are
// split in two. This layer observes the Decision the handler computes, the
// durable consultation it commits, the typed error it returns and the pre-write
// sentinel that error carries. It CANNOT observe the command's standard error,
// the host's continuation bytes, a fault-record line, or fail-closed
// settlement: those are the command's, and the built-CLI subjects own them.
// Nothing here claims otherwise, and a fault asserted here is asserted as
// "nothing was written", never as "the host continued".
//
// THE FAKE READER PROVES THE SEAM, NOT THE READ. It supplies a claim, an
// authority or a fault, and each subject asserts which of those reached the
// policy and what the receipt then says. It says nothing about whether the real
// reader can read a real store: that production wiring is pinned by reading the
// source below and by the two committing surfaces driven over a real store at the
// end of this file, and it is driven end to end on the built binary by the CLI
// subjects, which are not these.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/gatepolicy"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

const (
	gateClaudeGateFixture         = "../lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_261.json"
	gateClaudeSessionStartFixture = "../lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json"
)

// ─── the fake reader ──────────────────────────────────────────────────────────

// gateFakeReader is one sealed answer handed to the handler instead of a store
// read. It records what it was opened with, because WHICH session the gate asked
// about is a question only the caller can get wrong, and a gate that looked up a
// different session still returns a decision.
type gateFakeReader struct {
	opened  []gateOpened
	snap    gateauthority.Snapshot
	fault   error
	reads   int
	refused bool
}

type gateOpened struct {
	harness ir.HarnessID
	session string
}

// gateFactory is the fake's own reader factory. The tracker it is handed is the
// REAL store the handler opened, and it is ignored on purpose: nothing here
// claims the real reader could have been built over it.
func (f *gateFakeReader) gateFactory(_ protocol.TaskTracker, harness ir.HarnessID, session string) (gateauthority.Reader, error) {
	f.opened = append(f.opened, gateOpened{harness: harness, session: session})
	return f, nil
}

func (f *gateFakeReader) Snapshot(context.Context) (gateauthority.Snapshot, error) {
	f.reads++
	if f.fault != nil {
		f.refused = true
		return nil, f.fault
	}
	return f.snap, nil
}

// gateFakeSnapshot is the sealed answer. Each accessor can be made to fail with
// the typed fault the real snapshot raises at that point, which is how the
// per-stage fault rows are driven without a store.
type gateFakeSnapshot struct {
	claim      gateauthority.SessionClaim
	authority  gateauthority.ActorAuthority
	resolved   []string
	askedAbout []provenance.ActorID
	closed     int

	resolveFault   error
	authorityFault error
	closeFault     error
}

func (s *gateFakeSnapshot) ResolveSession(harness ir.HarnessID, session string) (gateauthority.SessionClaim, error) {
	if s.resolveFault != nil {
		return gateauthority.SessionClaim{}, s.resolveFault
	}
	s.resolved = append(s.resolved, string(harness)+"/"+session)
	return s.claim, nil
}

func (s *gateFakeSnapshot) Authority(actor provenance.ActorID) (gateauthority.ActorAuthority, error) {
	if s.authorityFault != nil {
		return gateauthority.ActorAuthority{}, s.authorityFault
	}
	s.askedAbout = append(s.askedAbout, actor)
	return s.authority, nil
}

func (s *gateFakeSnapshot) Close() error {
	s.closed++
	return s.closeFault
}

// ─── fixtures, store and evidence helpers ────────────────────────────────────

func gateFixture(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the committed capture must be readable: %s", path)
	return raw
}

// GateSessionIdentity reads the session identity out of a committed capture, so
// a claim is written for the session the gate will actually be asked about
// instead of for a string a reader has to keep in step with the fixture by hand.
//
// IT IS EXPORTED BECAUSE IT IS THE ONE READER, AND THE CLAIM SEEDER BESIDE IT IS
// THE ONE SEEDER. This file is an internal test file of package handlers, so the
// black-box tests in hook_lifecycle_test.go reach both through the package they
// already import: a second copy in that other package could drift from this one,
// and the session identity is exactly the fact the gate must not look up wrong.
// Members may be dotted paths ("input.sessionID"); the first is returned.
func GateSessionIdentity(t *testing.T, raw []byte, members ...string) string {
	t.Helper()
	var payload any
	require.NoError(t, json.Unmarshal(raw, &payload))
	for _, member := range members {
		current := payload
		for _, part := range strings.Split(member, ".") {
			object, isObject := current.(map[string]any)
			require.True(t, isObject, "the capture must carry %q as an object member", member)
			current = object[part]
		}
		session, isString := current.(string)
		require.True(t, isString, "the capture must carry %q as a string session identity", member)
		require.NotEmpty(t, session, "the capture must carry its own session identity")
		return session
	}
	t.Fatal("a session member is required")
	return ""
}

// gateStore opens a store the lifecycle handler accepts, with the persisted
// ingress identity the receipt writer requires.
func gateStore(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tracker.Create("file://lifecycle-gate-proof", "bootstrap", "initialize the persisted ingress identity",
		provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
	return dbPath
}

type gateClock struct{}

func (gateClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// gateOperations issues a fresh operation identity per call, because two receipts
// committed under one operation identity are one operation, not two.
type gateOperations struct{ sequence atomic.Int64 }

func (o *gateOperations) NewOperationID() (string, error) {
	return fmt.Sprintf("pasture.lifecycle.gate-proof.%d", o.sequence.Add(1)), nil
}

func gateService(t *testing.T, dbPath string) (receipt.Service, func()) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	service, err := tasks.NewLifecycleReceiptService(tracker, gateClock{}, &gateOperations{})
	require.NoError(t, err)
	return service, func() { require.NoError(t, tracker.Close()) }
}

// gateEvidenceKinds is every durable kind a lifecycle invocation can commit, so
// "nothing was written" is a statement about all of them and not about the one
// kind a subject happened to remember to read.
func gateEvidenceKinds() []provenance.EvidenceKind {
	return []provenance.EvidenceKind{
		"pasture.lifecycle.occurrence.v1",
		"pasture.lifecycle.interpreted.v2",
		receipt.CurrentConsultationEvidenceKind(),
	}
}

func gateQueryEvidence(t *testing.T, dbPath string, kinds []provenance.EvidenceKind) []provenance.EvidenceRow {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  kinds,
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	require.Nil(t, page.Next, "one page must hold every row the invocation committed")
	return page.Rows
}

// gateConsultation reads back the decision one invocation actually committed.
func gateConsultation(t *testing.T, dbPath string) backend.Decision {
	t.Helper()
	rows := gateQueryEvidence(t, dbPath, []provenance.EvidenceKind{receipt.CurrentConsultationEvidenceKind()})
	require.Len(t, rows, 1, "exactly one consultation must be committed")
	var stored struct {
		Decision backend.Decision `json:"decision"`
	}
	require.NoError(t, json.Unmarshal(rows[0].Payload, &stored))
	require.True(t, stored.Decision.IsValid(), "the committed consultation must carry a valid decision")
	return stored.Decision
}

func gateRequireNoReceipt(t *testing.T, dbPath string) {
	t.Helper()
	require.Empty(t, gateQueryEvidence(t, dbPath, gateEvidenceKinds()),
		"a fault raised before the durable write must leave no occurrence, no interpretation and no consultation behind")
}

func gateInput(t *testing.T, dbPath, event string, raw []byte) HookLifecycleInput {
	t.Helper()
	return HookLifecycleInput{
		DBPath:      dbPath,
		Harness:     ir.HarnessClaudeCode,
		Event:       event,
		HostVersion: registration.ClaudeCode2_1_261().Version,
		Input:       bytes.NewReader(raw),
		Clock:       gateClock{},
		Operations:  &gateOperations{},
	}
}

func gateActor(t *testing.T, wire string) provenance.ActorID {
	t.Helper()
	actor, err := provenance.ParseActorID(wire)
	require.NoError(t, err, "the test actor must be a canonical actor identifier: %s", wire)
	return actor
}

func gateTask(t *testing.T, wire string) provenance.TaskID {
	t.Helper()
	task, err := provenance.ParseTaskID(wire)
	require.NoError(t, err, "the test task must be a canonical task identifier: %s", wire)
	return task
}

// gateBoundSnapshot is a session whose claim resolved, holding the episodes the
// subject names. Known is true, so the policy answers on the ownership and not
// on an unregistered actor.
func gateBoundSnapshot(t *testing.T, wire string, episodes ...gateauthority.Episode) *gateFakeSnapshot {
	actor := gateActor(t, wire)
	return &gateFakeSnapshot{
		claim:     gateauthority.SessionClaim{Bound: true, Actor: actor, Known: true},
		authority: gateauthority.ActorAuthority{Actor: actor, Episodes: episodes},
	}
}

// gateOwnedEpisode is one owner episode in a phase the tool-use tables allow,
// which is what makes its holder allowed to use a tool.
func gateOwnedEpisode(t *testing.T, wire string, phase gateauthority.TaskPhase) gateauthority.Episode {
	return gateauthority.Episode{
		Assignment: provenance.AssignmentID(wire + "-assignment"),
		Task:       gateTask(t, "lifecycle-gate-task--0193f1c0-0000-7000-8000-0000000000a1"),
		Role:       gateauthority.RoleOwnerResponsibility,
		Phase:      phase,
	}
}

// ─── the production wiring, read from the source ──────────────────────────────

// TestTheLifecycleEntryPointsWireTheGateReader pins BOTH committing surfaces to
// the real reader, and pins the shape of the gate call so a second core cannot
// grow beside the first.
//
// IT IS A SOURCE READ BECAUSE NOTHING ELSE CAN OBSERVE IT. The reader's own
// BEHAVIOUR is observable; the FACT THAT PRODUCTION SUPPLIED IT is not. A test
// that injects a fake proves the seam works, and a build that shipped the seam
// unwired would pass every one of those tests while every live gate proceeded
// unevaluated — which is the exact state this change exists to end. So the pin
// reads the non-test sources and names the constructor, the opener, the parameter
// and the number of callers.
func TestTheLifecycleEntryPointsWireTheGateReader(t *testing.T) {
	t.Parallel()

	sources, err := filepath.Glob("*.go")
	require.NoError(t, err, "the handler package directory must be readable")

	nativeReaders, rawReaders := 0, 0
	coreCallers, gateCalls := 0, 0
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, parseErr, "every production source of this package must be readable: %s", name)
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction {
				continue
			}
			switch function.Name.Name {
			case "hookLifecycle":
				require.NotNil(t, function.Type.Params)
				require.Len(t, function.Type.Params.List, 4,
					"hookLifecycle must take the context, the input, the store opener and the reader factory, and nothing else")
				factory := function.Type.Params.List[3]
				require.Len(t, factory.Names, 1)
				require.Equal(t, "readers", factory.Names[0].Name)
				_, variadic := factory.Type.(*ast.Ellipsis)
				require.False(t, variadic,
					"a variadic decision seam is retired. A verdict comes from the reader, and a parameter a "+
						"caller may omit is a parameter every live gate will omit")
			case "evaluateGate":
				require.NotNil(t, function.Type.Params)
				require.Len(t, function.Type.Params.List, 7,
					"evaluateGate takes the context, the store, the reader factory, the dispatch row, the event, "+
						"the bindings and the harness — one input per fact it needs and no other way in")
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			callee := ""
			if identifier, isIdentifier := call.Fun.(*ast.Ident); isIdentifier {
				callee = identifier.Name
			} else if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
				callee = gateSourceOf(selector.Sel)
			}
			switch callee {
			case "hookLifecycle":
				coreCallers++
				require.Len(t, call.Args, 4, "%s calls the core with the wrong shape", name)
				require.Equal(t, "tasks.OpenTaskTracker", gateSourceOf(call.Args[2]),
					"the core must open the unified store itself, so the receipt and the gate read share one file")
				require.Equal(t, "tasks.NewGateReader", gateSourceOf(call.Args[3]),
					"PRODUCTION MUST SUPPLY THE REAL READER. A caller that passes anything else evaluates the gate "+
						"from something other than the store, and no ownership read backs the verdict it commits")
				nativeReaders++
			case "evaluateGate":
				gateCalls++
				require.Len(t, call.Args, 7, "%s calls evaluateGate with the wrong shape", name)
				supplied := gateSourceOf(call.Args[2])
				if name == "hook_lifecycle_raw.go" {
					require.Equal(t, "tasks.NewGateReader", supplied,
						"the raw surface is a second copy of the committing path, and the one thing it may not do "+
							"is evaluate the same payload by a weaker rule than the native path")
					rawReaders++
				} else {
					require.Equal(t, "readers", supplied,
						"the native path consults the reader through the factory it was given, so an in-process proof "+
							"can drive the gate without a second core")
				}
			}
			return true
		})
	}

	require.Equal(t, 1, nativeReaders,
		"exactly ONE production caller may reach the core. A second is a second gate path, and the two would be "+
			"free to disagree about when the gate runs")
	require.Equal(t, 1, rawReaders,
		"the raw committing path must run the same evaluateGate, or the two surfaces would judge one payload differently")
	require.Equal(t, 1, coreCallers, "hookLifecycle must have exactly one non-test caller")
	require.Equal(t, 2, gateCalls, "evaluateGate must be called from exactly two places: the native core and the raw commit")

	// THE FACTORY TYPE LIVES HERE, NOT IN THE AUTHORITY PACKAGE. That package
	// sits at the bottom of the import graph and must not name a Pasture store,
	// which is exactly what this signature does.
	authoritySources, err := filepath.Glob(filepath.Join("..", "lifecycle", "gateauthority", "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, authoritySources, "the authority package must exist for this pin to mean anything")
	for _, name := range authoritySources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(name)
		require.NoError(t, readErr)
		require.NotContains(t, string(source), "lifecycleReaderFactory",
			"%s declares a factory over the Pasture store, which points the authority package at the store it "+
				"exists to stay free of", name)
	}
}

// gateSourceOf renders an expression by re-printing it, so an assertion is about
// the source text and not about a spelling this file happened to choose.
func gateSourceOf(node ast.Node) string {
	var out bytes.Buffer
	if err := printer.Fprint(&out, token.NewFileSet(), node); err != nil {
		return fmt.Sprintf("unprintable(%T)", node)
	}
	return out.String()
}

// ─── the gate answers from the reader ─────────────────────────────────────────

// TestObservationNeverConsultsTheReader is the negative that keeps the gate read
// off the reporting rows. An observation asks nothing, so a read would produce
// facts nothing consumes — while holding a lease on a connection the host's next
// event needs.
func TestObservationNeverConsultsTheReader(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeSessionStartFixture)
	reader := &gateFakeReader{fault: errors.New("the reader must not be reached for an observation")}
	dbPath := gateStore(t)

	_, err := hookLifecycle(t.Context(), gateInput(t, dbPath, "SessionStart", raw), tasks.OpenTaskTracker, reader.gateFactory)

	require.NoError(t, err, "an observation is answered by the middle end and needs no reader")
	require.Empty(t, reader.opened, "the reader factory must not be called for an observation")
	require.Zero(t, reader.reads, "no snapshot may be taken for an observation")
	require.False(t, reader.refused)
	require.Len(t, gateQueryEvidence(t, dbPath, []provenance.EvidenceKind{receipt.CurrentConsultationEvidenceKind()}),
		0, "an observation commits no consultation, because no gate was consulted")
}

// TestTheGateConsultsTheReaderWithTheBoundSession is the allow path, and the
// only place that proves WHICH session the gate asked about: the reader is opened
// with the session the capture carried and the harness the command was invoked
// for. A gate that looked up another session would still return a decision here,
// and would allow or deny on another session's ownership.
func TestTheGateConsultsTheReaderWithTheBoundSession(t *testing.T) {
	t.Parallel()
	const actor = "lifecycle-gate-allow--0193f1c0-0000-7000-8000-0000000000b1"
	raw := gateFixture(t, gateClaudeGateFixture)
	snapshot := gateBoundSnapshot(t, actor, gateOwnedEpisode(t, actor, gateauthority.PhaseImplPlan))
	reader := &gateFakeReader{snap: snapshot}
	dbPath := gateStore(t)

	_, err := hookLifecycle(t.Context(), gateInput(t, dbPath, "PreToolUse", raw), tasks.OpenTaskTracker, reader.gateFactory)

	require.NoError(t, err)
	require.Equal(t, []gateOpened{{harness: ir.HarnessClaudeCode, session: GateSessionIdentity(t, raw, "session_id")}}, reader.opened,
		"the gate must be opened for the session the capture carried, on the harness the command was invoked for")
	require.Equal(t, []string{string(ir.HarnessClaudeCode) + "/" + GateSessionIdentity(t, raw, "session_id")}, snapshot.resolved,
		"the snapshot must be asked about the same session it was opened for, on the same harness")
	require.Equal(t, []provenance.ActorID{gateActor(t, actor)}, snapshot.askedAbout,
		"the authority must be read for the actor the claim named and for no other")
	require.Equal(t, 1, snapshot.closed, "the snapshot holds a read lease, so it must be released on the committing path")
	committed := gateConsultation(t, dbPath)
	require.Equal(t, backend.DecisionProceed, committed.Kind())
	require.Equal(t, backend.ReasonLegal, committed.Reason(),
		"an owner in a phase that permits a tool use is allowed, and the receipt must say an evaluation RAN")
}

// TestTheGateDeniesABoundActorWithNoAssignment is the deny path, and the place
// that proves a DENIAL is an ordinary answer: no error, a committed receipt, and
// a host response that is not a continue.
func TestTheGateDeniesABoundActorWithNoAssignment(t *testing.T) {
	t.Parallel()
	const actor = "lifecycle-gate-deny--0193f1c0-0000-7000-8000-0000000000b2"
	raw := gateFixture(t, gateClaudeGateFixture)
	snapshot := gateBoundSnapshot(t, actor)
	reader := &gateFakeReader{snap: snapshot}
	dbPath := gateStore(t)

	response, err := hookLifecycle(t.Context(), gateInput(t, dbPath, "PreToolUse", raw), tasks.OpenTaskTracker, reader.gateFactory)

	require.NoError(t, err, "a denial is a decision, not a fault")
	require.True(t, response.IsValid(), "a denial reaches the host as a response")
	require.Equal(t, 1, snapshot.closed,
		"the snapshot holds a read lease, so it must be released on the denying path too")
	committed := gateConsultation(t, dbPath)
	require.Equal(t, backend.DecisionDeny, committed.Kind())
	require.Equal(t, backend.ReasonNoActiveAssignment, committed.Reason(),
		"the reason says WHY the actor may not act; a receipt that said only deny would send a maintainer back to the policy")
}

// TestAnUnboundSessionProceedsWithoutAReaderFault covers what a real deployment
// meets first: a session that never claimed an actor. It is not a fault, it is
// not a denial, and the receipt says which of the two it was.
func TestAnUnboundSessionProceedsWithoutAReaderFault(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeGateFixture)
	snapshot := &gateFakeSnapshot{claim: gateauthority.SessionClaim{Bound: false, Session: GateSessionIdentity(t, raw, "session_id")}}
	reader := &gateFakeReader{snap: snapshot}
	dbPath := gateStore(t)

	_, err := hookLifecycle(t.Context(), gateInput(t, dbPath, "PreToolUse", raw), tasks.OpenTaskTracker, reader.gateFactory)

	require.NoError(t, err)
	require.Empty(t, snapshot.askedAbout,
		"an unbound snapshot answers no actor, and asking it for one is the misuse the real snapshot refuses")
	require.Equal(t, 1, snapshot.closed)
	committed := gateConsultation(t, dbPath)
	require.Equal(t, backend.DecisionProceed, committed.Kind())
	require.Equal(t, backend.ReasonUnboundSession, committed.Reason(),
		"the receipt must distinguish ALLOWED from NEVER ASKED, or a reader cannot tell a working gate from one that read nothing")
}

// ─── faults are not denials ───────────────────────────────────────────────────

// TestReaderFaultsNeverBecomeADenial drives every fault the handler can reach on
// the gate path, one row per typed carrier, and asserts the same three things for
// all of them: the typed identity survives the handler, the pre-write sentinel is
// present, and NOTHING was written.
//
// THE SENTINEL IS WHY THE SUBJECT EXISTS. Without it a fault falls to the
// command's weakest claim — "the row may or may not exist" — and an operator is
// sent to a journal that does not contain their invocation. With it, the
// not-recorded answer is evidence from the site that knows.
//
// A FAULT IS NOT A DENIAL, and this is where that could quietly stop being true.
// A handler that turned a read fault into a Proceed would keep the host working
// and would commit a receipt claiming the action was evaluated. So every row
// asserts an ERROR and an absent receipt; the mutation at the deciding site turns
// this subject red.
//
// THE GUARD DOES NOT TRUST ITS OWN ERROR. Each row also pins what the fake reader
// OBSERVED — opened once for the capture's session, asked for exactly one
// snapshot, and (where a snapshot was taken) its lease released. Those assertions
// are what make a fault row detectable for one reason at a time: an error with no
// read, a read that never took a snapshot, or a snapshot never closed each fail
// by name, so a nil error can never be mistaken for a scheduled miss and a
// swallowed fault can never pass silently.
func TestReaderFaultsNeverBecomeADenial(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeGateFixture)
	session := GateSessionIdentity(t, raw, "session_id")
	for _, row := range []struct {
		name  string
		build func() *gateFakeReader
		check func(t *testing.T, err error)
	}{
		{
			// A deadline that expires inside the read. This is the shape the
			// command's expiry arm produces against a store held by another
			// writer, and the cause has to stay reachable or an operator reads
			// "the store could not answer" instead of "you ran out of time".
			name: "deadline",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateReadError{
					Stage: tasks.GateReadStageOwnership, Harness: ir.HarnessClaudeCode, Session: session,
					Cause: context.DeadlineExceeded,
				}}
			},
			check: func(t *testing.T, err error) {
				var read *tasks.GateReadError
				require.ErrorAs(t, err, &read)
				require.Equal(t, tasks.GateReadStageOwnership, read.Stage)
				require.ErrorIs(t, err, context.DeadlineExceeded,
					"the cause must stay reachable, or a locked database cannot be told from a cancelled read")
			},
		},
		{
			name: "claim-read",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateReadError{
					Stage: tasks.GateReadStageClaim, Harness: ir.HarnessClaudeCode, Session: session,
					Cause: errors.New("the store refused the claim read"),
				}}
			},
			check: func(t *testing.T, err error) {
				var read *tasks.GateReadError
				require.ErrorAs(t, err, &read)
				require.Equal(t, tasks.GateReadStageClaim, read.Stage,
					"the stage names WHICH read failed; a claim fault and an ownership fault need different repairs")
			},
		},
		{
			name: "ownership-read",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateReadError{
					Stage: tasks.GateReadStageOwnership, Harness: ir.HarnessClaudeCode, Session: session,
					Cause: errors.New("the store refused the ownership read"),
				}}
			},
			check: func(t *testing.T, err error) {
				var read *tasks.GateReadError
				require.ErrorAs(t, err, &read)
				require.Equal(t, tasks.GateReadStageOwnership, read.Stage)
			},
		},
		{
			name: "capability",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateReaderCapabilityError{Missing: "provenance.ActorOwnershipQueryAPI"}}
			},
			check: func(t *testing.T, err error) {
				var capability *tasks.GateReaderCapabilityError
				require.ErrorAs(t, err, &capability)
				require.Equal(t, "provenance.ActorOwnershipQueryAPI", capability.Missing,
					"the missing capability is the only thing an operator can go and check")
			},
		},
		{
			name: "byte-limit",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateReadIntegrityError{
					Stage: provenance.ActorOwnershipStageMaterials,
					Cause: &provenance.ActorOwnershipLimitError{Stage: provenance.ActorOwnershipStageMaterials},
				}}
			},
			check: func(t *testing.T, err error) {
				var integrity *tasks.GateReadIntegrityError
				require.ErrorAs(t, err, &integrity)
				require.Equal(t, provenance.ActorOwnershipStageMaterials, integrity.Stage,
					"the stage is the STORE's answer about which rows are damaged, and it must survive the wrap")
			},
		},
		{
			name: "projection-mismatch",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateReadIntegrityError{
					Stage: provenance.ActorOwnershipStageOwnedTasks,
					Cause: &provenance.OwnerProjectionMismatchError{
						Task:  gateTask(t, "lifecycle-gate-task--0193f1c0-0000-7000-8000-0000000000a2"),
						Owner: gateActor(t, "lifecycle-gate-owner--0193f1c0-0000-7000-8000-0000000000a3"),
					},
				}}
			},
			check: func(t *testing.T, err error) {
				var integrity *tasks.GateReadIntegrityError
				require.ErrorAs(t, err, &integrity)
				require.Equal(t, provenance.ActorOwnershipStageOwnedTasks, integrity.Stage)
			},
		},
		{
			// A claim row that exists and cannot be attributed. It is never
			// downgraded to an unbound session, because "unbound" is a policy
			// answer and this is a store fault.
			name: "malformed-claim",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateClaimMalformedError{
					Harness: ir.HarnessClaudeCode, Session: session,
					Cause: errors.New("the stored actor is not a canonical actor identifier"),
				}}
			},
			check: func(t *testing.T, err error) {
				var malformed *tasks.GateClaimMalformedError
				require.ErrorAs(t, err, &malformed)
				require.Equal(t, session, malformed.Session)
			},
		},
		{
			name: "role-source",
			build: func() *gateFakeReader {
				return &gateFakeReader{fault: &tasks.GateRoleSourceError{
					Task:       gateTask(t, "lifecycle-gate-task--0193f1c0-0000-7000-8000-0000000000a4"),
					Assignment: provenance.AssignmentID("assignment-with-no-readable-role"),
					Reason:     "its role is written by no record the reader accepts",
				}}
			},
			check: func(t *testing.T, err error) {
				var role *tasks.GateRoleSourceError
				require.ErrorAs(t, err, &role)
				require.Equal(t, provenance.AssignmentID("assignment-with-no-readable-role"), role.Assignment,
					"the fault must name the assignment, or a maintainer has one unread episode out of many")
			},
		},
		{
			// The claim resolved and the SECOND accessor refused. This is the row
			// where a handler that ignored the authority error would commit a
			// decision it never made.
			name: "authority-read-fault",
			build: func() *gateFakeReader {
				return &gateFakeReader{snap: &gateFakeSnapshot{
					claim: gateauthority.SessionClaim{
						Bound: true, Known: true,
						Actor: gateActor(t, "lifecycle-gate-scope--0193f1c0-0000-7000-8000-0000000000b3"),
					},
					authorityFault: &tasks.GateReaderScopeError{Operation: "read an authority this snapshot does not hold"},
				}}
			},
			check: func(t *testing.T, err error) {
				var scope *tasks.GateReaderScopeError
				require.ErrorAs(t, err, &scope)
			},
		},
		{
			name: "closed-snapshot",
			build: func() *gateFakeReader {
				return &gateFakeReader{snap: &gateFakeSnapshot{
					claim: gateauthority.SessionClaim{
						Bound: true, Known: true,
						Actor: gateActor(t, "lifecycle-gate-closed--0193f1c0-0000-7000-8000-0000000000b4"),
					},
					resolveFault: &tasks.GateSnapshotClosedError{},
				}}
			},
			check: func(t *testing.T, err error) {
				var closed *tasks.GateSnapshotClosedError
				require.ErrorAs(t, err, &closed)
			},
		},
		{
			// Not a read fault: the policy cannot apply its tables to an episode
			// whose phase no build knows. It is still a fault, it still fails
			// open, and it is the row a "deny on anything unanswerable" reading
			// would silently turn into a blocked user.
			name: "unmapped-phase",
			build: func() *gateFakeReader {
				const actor = "lifecycle-gate-unmapped--0193f1c0-0000-7000-8000-0000000000b5"
				return &gateFakeReader{snap: gateBoundSnapshot(t, actor, gateOwnedEpisode(t, actor, gateauthority.TaskPhaseUnset))}
			},
			check: func(t *testing.T, err error) {
				var refusal *gatepolicy.Refusal
				require.ErrorAs(t, err, &refusal)
				require.Equal(t, gatepolicy.RefusalPhaseUnknown, refusal.Kind())
			},
		},
		{
			// The same class from the other side: a cell the tables do not
			// list. No valid event can produce one, and this is the fault a future
			// enum arm would meet before the tables caught up.
			name: "policy-unlisted",
			build: func() *gateFakeReader {
				const actor = "lifecycle-gate-unlisted--0193f1c0-0000-7000-8000-0000000000b6"
				episode := gateOwnedEpisode(t, actor, gateauthority.PhaseImplPlan)
				episode.Role = gateauthority.RoleUnset
				return &gateFakeReader{snap: gateBoundSnapshot(t, actor, episode)}
			},
			check: func(t *testing.T, err error) {
				var refusal *gatepolicy.Refusal
				require.ErrorAs(t, err, &refusal)
				require.Equal(t, gatepolicy.RefusalPolicyUnlisted, refusal.Kind())
			},
		},
	} {
		// THE ROWS RUN SERIALLY WITHIN THE SUBJECT, ON PURPOSE. Twelve parallel
		// subtests each open their own store and drive their own reader; under a
		// loaded runner that fan-out is what makes a single row's failure
		// ambiguous between "the handler swallowed a fault" and "the runner was
		// starved". The guard this subject carries is a runtime mutation detector
		// for the fault direction, so it must fail for exactly one reason and say
		// which. The package keeps its parallelism: the subject itself still runs
		// in parallel with its siblings.
		t.Run(row.name, func(t *testing.T) {
			reader := row.build()
			dbPath := gateStore(t)

			_, err := hookLifecycle(t.Context(), gateInput(t, dbPath, "PreToolUse", raw), tasks.OpenTaskTracker, reader.gateFactory)

			require.Error(t, err, "a read or evaluation fault is an error; it is never a decision the host may act on")
			require.ErrorIs(t, err, ErrLifecycleBeforeDurableWrite,
				"the fault was raised before any write was attempted, so the command may say no occurrence exists — "+
					"and it may say that on this sentinel's evidence alone")
			row.check(t, err)
			gateRequireNoReceipt(t, dbPath)

			// THE ERROR IS NOT TAKEN ON FAITH. Every row must show that the gate
			// was actually evaluated: the reader opened exactly once for the
			// capture's session, and it was asked for exactly one snapshot. A row
			// that returned an error without consulting the reader would prove
			// nothing about the seam this subject exists to guard, and a nil error
			// with no read would be indistinguishable from a swallowed fault.
			require.Equal(t, []gateOpened{{harness: ir.HarnessClaudeCode, session: session}}, reader.opened,
				"every fault row must open the reader exactly once, for the session the capture carried")
			require.Equal(t, 1, reader.reads,
				"the gate takes exactly one snapshot per invocation; a fault row that read zero or twice did not exercise the seam")
			// THE reader.refused FLAG IS DELIBERATELY NOT ASSERTED. On a row
			// that reached here it is fully derived: reads == 1 and the snapshot
			// type-asserts only when Snapshot returned one, and Snapshot sets
			// refused only on the path that returns no snapshot. Either polarity
			// would restate that derivation rather than constrain production, so
			// the flag is left as the fake's own bookkeeping. The facts that DO
			// constrain production are pinned above (opened, reads) and below
			// (closed, where a snapshot exists).
			if snapshot, tookSnapshot := reader.snap.(*gateFakeSnapshot); tookSnapshot {
				// A SNAPSHOT WAS TAKEN, SO ITS READ LEASE MUST BE RELEASED on the
				// fault return. Production closes it with one deferred Close; a
				// handler that closed only on the success path would leak the
				// lease on exactly the faults this subject drives, and no other
				// subject would name it.
				require.Equal(t, 1, snapshot.closed,
					"a snapshot that was taken holds a read lease, so it must be closed on the fault return")
			}
		})
	}
}

// TestReaderConstructionFaultKeepsItsKind covers the fault raised before any
// read at all: the reader could not be built over the store the hook opened. It
// is a wiring fault, and a handler that flattened it to a string would leave an
// operator with no scope to repair.
func TestReaderConstructionFaultKeepsItsKind(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeGateFixture)
	dbPath := gateStore(t)
	cause := &tasks.GateReaderScopeError{Operation: "open a gate reader over a store that is not the unified Pasture task store"}

	_, err := hookLifecycle(t.Context(), gateInput(t, dbPath, "PreToolUse", raw), tasks.OpenTaskTracker,
		func(protocol.TaskTracker, ir.HarnessID, string) (gateauthority.Reader, error) { return nil, cause })

	require.ErrorIs(t, err, cause)
	var scope *tasks.GateReaderScopeError
	require.ErrorAs(t, err, &scope)
	require.ErrorIs(t, err, ErrLifecycleBeforeDurableWrite)
	gateRequireNoReceipt(t, dbPath)
}

// TestCommitRefusesAGateWithoutAnEvaluatedDecision holds the seam that made
// every live gate proceed: a commit reached with no verdict.
//
// It calls the shared commit tail directly, because NO production path can reach
// this refusal — evaluateGate supplies a verdict for every gate — and a guard
// nothing can reach is worth nothing. What it protects is the NEXT change: one
// that adds a second commit call, or a branch that skips the evaluation, and
// finds this standing in its way.
func TestCommitRefusesAGateWithoutAnEvaluatedDecision(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeGateFixture)
	dbPath := gateStore(t)
	service, closeStore := gateService(t, dbPath)
	defer closeStore()
	dispatch, err := dispatchLifecycle(ir.HarnessClaudeCode)
	require.NoError(t, err)
	gateEvent, err := ingress.EventByNativeName(dispatch.manifest, "PreToolUse")
	require.NoError(t, err)
	gateCapture := dispatch.parse(raw, gateEvent, registration.ClaudeCode2_1_261().Version)

	_, err = deliveryCommit(t.Context(), service, dispatch, gateEvent, gateCapture.delivery, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "without an evaluated decision")
	require.Contains(t, err.Error(), "no receipt committed")
	require.ErrorIs(t, err, ErrLifecycleBeforeDurableWrite,
		"the refusal precedes the metamodel journal and the receipt, so this invocation recorded nothing")
	gateRequireNoReceipt(t, dbPath)

	// The same refusal is NOT raised for an observation, and the observation
	// commits. An observation is answered by the middle end, so demanding a
	// verdict there would refuse every reporting row on the host.
	observationRaw := gateFixture(t, gateClaudeSessionStartFixture)
	observationEvent, err := ingress.EventByNativeName(dispatch.manifest, "SessionStart")
	require.NoError(t, err)
	observation := dispatch.parse(observationRaw, observationEvent, registration.ClaudeCode2_1_261().Version)

	_, err = deliveryCommit(t.Context(), service, dispatch, observationEvent, observation.delivery, nil)

	require.NoError(t, err, "an observation commits without an evaluated decision")
	require.NotEmpty(t, gateQueryEvidence(t, dbPath, gateEvidenceKinds()))
}

// ─── both committing surfaces, over the real reader ───────────────────────────

// TestBothCommittingSurfacesRunTheSameGate drives the NATIVE and the RAW commit
// over a REAL store with a REAL claim, and requires the same denial from both.
//
// The fake above proves the seam. This proves the wiring where a verdict becomes
// durable, and it is the reason the raw surface may not be treated as a bypass:
// raw ingestion enters the same gate, so an import cannot smuggle a payload past
// a decision the host would have faced.
func TestBothCommittingSurfacesRunTheSameGate(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeGateFixture)
	session := GateSessionIdentity(t, raw, "session_id")

	t.Run("native", func(t *testing.T) {
		t.Parallel()
		dbPath := gateStore(t)
		ClaimGateSession(t, dbPath, ir.HarnessClaudeCode, session)

		outcome, err := HookLifecycleNative(t.Context(), gateInput(t, dbPath, "PreToolUse", raw))

		require.NoError(t, err, "a denial is a committed decision, not a fault")
		require.Equal(t, hostexit.ExitBlock, outcome.Exit,
			"a bound actor with no assignment is refused on the Claude row, whose evidence cites an exit-code refusal")
		require.Equal(t, backend.ReasonNoActiveAssignment.Message(), outcome.Stderr)
		require.Empty(t, outcome.Stdout, "a refusal emits no continuation bytes")
		committed := gateConsultation(t, dbPath)
		require.Equal(t, backend.DecisionDeny, committed.Kind())
		require.Equal(t, backend.ReasonNoActiveAssignment, committed.Reason())
	})

	t.Run("raw", func(t *testing.T) {
		t.Parallel()
		dbPath := gateStore(t)
		ClaimGateSession(t, dbPath, ir.HarnessClaudeCode, session)

		result, err := HookLifecycleRaw(t.Context(), HookLifecycleRawInput{
			DBPath:        dbPath,
			Harness:       ir.HarnessClaudeCode,
			Event:         "PreToolUse",
			HostVersion:   registration.ClaudeCode2_1_261().Version,
			SchemaVersion: RawSchemaClaudeCode2_1_261,
			Input:         bytes.NewReader(raw),
			Clock:         gateClock{},
			Operations:    &gateOperations{},
		})

		require.NoError(t, err)
		outcome, committedResult := result.Outcome()
		require.True(t, committedResult, "a raw commit publishes the host Outcome and nothing else")
		require.Equal(t, hostexit.ExitBlock, outcome.Exit)
		require.Equal(t, backend.ReasonNoActiveAssignment.Message(), outcome.Stderr)
		committed := gateConsultation(t, dbPath)
		require.Equal(t, backend.DecisionDeny, committed.Kind())
		require.Equal(t, backend.ReasonNoActiveAssignment, committed.Reason())
	})

	t.Run("unbound-native", func(t *testing.T) {
		t.Parallel()
		dbPath := gateStore(t)

		outcome, err := HookLifecycleNative(t.Context(), gateInput(t, dbPath, "PreToolUse", raw))

		require.NoError(t, err)
		require.Equal(t, hostexit.ExitContinue, outcome.Exit, "an unclaimed session fails open: the action continues")
		require.Empty(t, outcome.Stdout)
		committed := gateConsultation(t, dbPath)
		require.Equal(t, backend.DecisionProceed, committed.Kind())
		require.Equal(t, backend.ReasonUnboundSession, committed.Reason())
	})
}

// ClaimGateSession writes the claim the gate will read, through the same writer
// the session-start event uses. The actor is REGISTERED and owns nothing, which
// is the state a real deployment reaches before a slice is assigned to it.
//
// IT IS THE ONE SEEDER OF A GATE-PROOF CLAIM. It is exported for the same reason
// GateSessionIdentity is: the black-box subjects in hook_lifecycle_test.go drive
// the exported entry points and need the same real claim, and a second copy of
// this writer's invocation could seed a different claim shape than the gate is
// proven against.
func ClaimGateSession(t *testing.T, dbPath string, harness ir.HarnessID, session string) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	agent, err := tracker.RegisterHumanAgent("lifecycle-gate-proof", "gate-owner", "gate-owner@example.invalid")
	require.NoError(t, err)
	require.NoError(t, tasks.RecordLifecycleSessionClaim(
		context.Background(), tracker, harness, sessionStartEventKind(harness),
		[]model.NativeBinding{{Kind: model.BindingSession, Value: session}},
		tasks.ActorClaim(agent.ID.String()), gateClock{},
	))
}

func sessionStartEventKind(harness ir.HarnessID) model.ContractEventKind {
	switch harness {
	case ir.HarnessCodex:
		return registration.EventCodexSessionStart
	case ir.HarnessOpenCode:
		return registration.EventOpenCodeSessionCreated
	default:
		return registration.EventSessionStart
	}
}
