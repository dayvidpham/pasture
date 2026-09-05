package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/acceptance/origin"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	codexfrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	opencodefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	codexingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	opencodeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

const hookLifecycleWhere = "Receiving a native lifecycle event (internal/handlers/hook_lifecycle.go in handlers.HookLifecycle)."

type HookLifecycleInput struct {
	DBPath      string
	Harness     ir.HarnessID
	Event       string
	HostVersion string
	Input       io.Reader
	Clock       receipt.Clock
	Operations  receipt.OperationIDSource
	// Barrier is called ONCE, after the durable receipt has been committed and
	// BEFORE the native continuation bytes are produced for the host. It names
	// the commit-to-emit boundary, which is where the commit-before-stdout
	// invariant lives. Production supplies PassThroughCommitBarrier, which does
	// nothing; a nil barrier means the same, so every other caller is
	// unaffected. See CommitBarrier.
	Barrier CommitBarrier
	// Activations optionally injects the activation configuration used by the
	// event gate, overriding the statically dispatched per-harness manifest.
	//
	// Production callers (the CLI) leave this nil so the committed per-harness
	// activation manifest governs admission. After acceptance review the
	// Codex dispatch enables the accepted SessionStart and PreToolUse events via
	// activation.Codex0_153_0(), exactly as the Claude and OpenCode cases do;
	// the two selected events are admitted and every other Codex event stays
	// withheld. This override remains available to exercise the same durable
	// handler path with an alternative manifest; there is no separate test-only
	// code path.
	Activations []activation.Entry
}

type lifecycleStoreOpener func(string) (protocol.TaskTracker, error)

// HookLifecycle preserves the accepted no-response Claude caller contract.
func HookLifecycle(ctx context.Context, in HookLifecycleInput) error {
	_, err := HookLifecycleResponse(ctx, in)
	return err
}

// HookLifecycleResponse records the lifecycle receipt before returning an
// optional response to the native host.
func HookLifecycleResponse(ctx context.Context, in HookLifecycleInput) (backend.HostResponse, error) {
	return hookLifecycle(ctx, in, tasks.OpenTaskTracker)
}

type lifecycleCapture struct {
	disposition model.CaptureDisposition
	delivery    receipt.Delivery
}

// lifecycleDispatch is the per-harness registry row. Its members fold the two
// former dispatch switches (the activation/parse/bind dispatch here and the
// native-response encode switch in nativeresponse) into one static map entry.
//
//   - activations is a lazy, fallible constructor: the generated proofs are
//     fallible, so the row stores the constructor func and hookLifecycle
//     resolves it at dispatch time, preserving the wrapped error text verbatim.
//   - encode is the per-target native emitter reached only through the registry
//     row, replacing the deleted nativeresponse.Encode harness switch.
type lifecycleDispatch struct {
	name        string
	manifest    registration.Manifest
	activations func() ([]activation.Entry, error)
	parse       func([]byte, registration.Event, string) lifecycleCapture
	// rawParse is the raw-ingestion classification row: the SAME
	// ingress Parse with an envelope pre-stamped with the raw origin and the
	// resulting delivery origin stamped raw. It is a sibling, not a second
	// pipeline: the raw hatch dispatches through the same row (same manifest,
	// same activation posture, same bind/NewEvent/Derive verifier) with
	// envelope produced for imports and migration.
	rawParse func([]byte, registration.Event, string) lifecycleCapture
	bind     func(model.ContractEventKind, []model.NativeBinding) (waist.L1, []waist.Identity, error)
	encode   func(backend.HostResponse) ([]byte, error)
	// refusesUndeclaredMembers says whether THIS harness's parser rejects a
	// payload carrying a member the registration does not declare.
	//
	// IT IS A ROW FIELD BECAUSE THE ANSWER DIFFERS PER PARSER AND THE ADVICE
	// NAMED THE HARNESS. Claude validates the member set against the allowed
	// fields and refuses an extra one; Codex and OpenCode decode into a struct,
	// so an added member is IGNORED and the event is recorded — measured, rc 0
	// with zero bytes on standard error. The refusal text told a Codex operator
	// BY NAME that added members are refused and that identity names must match
	// exactly, and both halves are false of that parser. The advice is derived
	// from this row rather than written once for whichever harness the author
	// had in mind.
	refusesUndeclaredMembers bool
	// matchesFieldNamesExactly says whether identity field names must match
	// the registration's spelling. Claude looks names up in a map, so they
	// must; the struct decoders accept a case-insensitive match, so SESSION_ID
	// and sessionid bind where the advice said they would not.
	matchesFieldNamesExactly bool
}

// frontendRegistry is the compile-time static dispatch map keyed by the closed
// set of string-typed ir.HarnessID constants. It has no init() registration, no
// reflection, and no string reverse lookup: it is a closed literal, the same
// construction the codegen harnessRegistry has always used. Adding a harness is
// adding one data row.
//
// Every parse closure passes an envelope with the origin carrier left at its
// zero value, so native commits stay byte-identical to the pre-origin path:
// the zero value is the documented default (the NATIVE sentinel
// authentic-capture) for pre-origin callers, and omitted from serialized output
// by the carrier's omitempty tag, satisfying the frozen golden native payload
// pins. The raw path is the first producer to populate the origin carrier:
// it stamps OriginRaw on the envelope it passes to the per-harness ingress
// parser and on the resulting delivery.
var frontendRegistry = map[ir.HarnessID]lifecycleDispatch{
	ir.HarnessClaudeCode: {
		name:        "Claude",
		manifest:    registration.ClaudeCode2_1_261(),
		activations: activation.ClaudeCode2_1_261,
		parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := claudeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		},
		rawParse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := claudeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return withRawOrigin(lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery})
		},
		bind:                     claudefrontend.Bind,
		encode:                   nativeresponse.CanonicalProceed,
		refusesUndeclaredMembers: true,
		matchesFieldNamesExactly: true,
	},
	ir.HarnessOpenCode: {
		name:        "OpenCode",
		manifest:    registration.OpenCode1_18_29(),
		activations: activation.OpenCode1_18_29,
		parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := opencodeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		},
		rawParse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := opencodeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return withRawOrigin(lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery})
		},
		bind:   opencodefrontend.Bind,
		encode: nativeresponse.CanonicalProceed,
		// This parser decodes into a struct, so an undeclared member is
		// IGNORED and a field name matches case-insensitively. Both schema
		// flags stay false, and the refusal text says only what holds here.
		refusesUndeclaredMembers: false,
		matchesFieldNamesExactly: false,
	},
	ir.HarnessCodex: {
		name:        "Codex",
		manifest:    registration.Codex0_153_0(),
		activations: activation.Codex0_153_0,
		parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := codexingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		},
		rawParse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := codexingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return withRawOrigin(lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery})
		},
		bind:   codexfrontend.Bind,
		encode: nativeresponse.CodexContinuation,
		// Same decoder shape as OpenCode: an added member is ignored and the
		// event is recorded. Measured on the built binary, rc 0 with zero bytes
		// on standard error.
		refusesUndeclaredMembers: false,
		matchesFieldNamesExactly: false,
	},
}

func hookLifecycle(ctx context.Context, in HookLifecycleInput, open lifecycleStoreOpener) (response backend.HostResponse, err error) {
	// durablePossible RECORDS THE ONE FACT A CALLER CANNOT OTHERWISE LEARN:
	// whether this invocation ever ATTEMPTED A WRITE, and so whether a row can
	// exist for it.
	//
	// It exists because the caller's DEFAULT had to become the weakest claim.
	// The stage table answered not-recorded for every error it did not
	// recognise, and that is a universal warrant this function cannot give:
	// the journal appender can fail AFTER the commit succeeds — its own text
	// says "the operation reported success" — and it carries no sentinel, so a
	// committed row was reported to the operator as "no occurrence was recorded
	// for it". With the default weakened, the precise answer has to come from
	// EVIDENCE, and this is the evidence.
	//
	// THE REGION IS RE-DERIVED, AND IT IS NOT WHERE THE FIRST VERSION PUT IT.
	// That version set the marker before open(in.DBPath) and called that "the
	// line the durable region begins". OPENING A STORE WRITES NO OCCURRENCE.
	// A --db under a read-only directory therefore reported record-unknown and
	// sent the operator to "the lifecycle occurrence journal" of a database
	// whose own cause says "unable to open database file" — advice that cannot
	// be followed, about a file that does not exist.
	//
	// A ROW CAN EXIST ONLY WHERE A WRITE WAS ATTEMPTED, and in this function
	// exactly two statements attempt one: the invalid-capture receipt, and the
	// shared delivery commit. Nothing else here writes — not the open, not the
	// service construction, not the gate. So the marker is set immediately
	// before each of those two, and TestTheDurableRegionBeginsAtItsWrites reads
	// this function and refuses any write not preceded by it: the producers of
	// this evidence are a population, and this slice's whole lesson is that an
	// unenumerated population is where the defect lives.
	durablePossible := false
	defer func() {
		if err != nil && !durablePossible {
			err = fmt.Errorf("%w: %w", ErrLifecycleBeforeDurableWrite, err)
		}
	}()

	if ctx == nil || in.Input == nil || in.Clock == nil || in.Operations == nil || open == nil {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, "The lifecycle ingress boundary is incompletely wired.", "A context, stdin, clock, operation identity source, and store opener are required.", "Nothing was read or recorded.", "Invoke this path through the production lifecycle command.", nil)
	}
	dispatch, err := dispatchLifecycle(in.Harness)
	if err != nil {
		return backend.HostResponse{}, err
	}
	// The activation catalog is a fallible generated proof; resolve it here at
	// dispatch time (the same point the former switch resolved it), preserving
	// the wrapped error text verbatim. Production callers leave in.Activations
	// nil so the committed per-harness manifest governs admission; the override
	// exercises the same durable path with an alternative manifest.
	activations, err := dispatch.activations()
	if err != nil {
		return backend.HostResponse{}, fmt.Errorf("dispatch %s lifecycle activation: %w", dispatch.name, err)
	}
	if in.Activations != nil {
		activations = in.Activations
	}
	if strings.TrimSpace(in.HostVersion) == "" {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, "The observed host version is missing.", "Every retained occurrence records which host version produced it, without using the value as an admission check.", "The input was not read and no database was opened.", "Pass the observed version through --host-version.", nil)
	}
	// The event is resolved by the one validating reverse lookup every ingress
	// path shares, so a name the registration does not declare is refused with
	// one text: the harness, the version and the name, spelled exactly.
	event, err := ingress.EventByNativeName(dispatch.manifest, in.Event)
	if err != nil {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, err.Error(), "The generated registration is the only authority for native event names.", "The input was not read and no database was opened.", "Invoke the hook with a native event name present in the support report.", nil)
	}
	state, found := activationFor(event.Kind, activations)
	if !found || state.State != activation.Enabled {
		reason := activation.WithheldMissingFixture
		if found {
			reason = state.Reason
		}
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("%s event %q is withheld (reason %s).", dispatch.name, event.NativeName, reason.String()), "Only events with authentic capture evidence and a passing production proof are admitted.", "The input was not read and no database was opened.", "Inspect the generated activation support report and enable the event only after its proof passes.", nil)
	}
	raw, err := io.ReadAll(io.LimitReader(in.Input, model.MaxNativePayloadBytes+1))
	if err != nil {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, "The native payload could not be read.", "Standard input failed during the bounded read.", "No database was opened.", "Retry with a readable complete payload.", err)
	}
	// A ZERO-LENGTH PAYLOAD HAS ITS OWN ARM, AND IT HAD NONE.
	//
	// The read succeeds, so the arm above does not fire; the length is under
	// the bound, so the arm below does not either. The empty slice then travels
	// all the way to the blob writer, where a nil body binds as NULL against a
	// NOT NULL column, and the operator is handed
	// "constraint failed: NOT NULL constraint failed: lifecycle_payload_blobs.body (1299)"
	// on all three harnesses — a storage fault naming a column, for a condition
	// that is neither storage nor a fault of the store.
	//
	// It is the nil-versus-empty class again: an absent value and a present
	// empty one are different facts, and a layer that cannot tell them apart
	// reports the wrong one. The condition is known HERE, where the bytes were
	// read and their count is in hand, so it is named here.
	if len(raw) == 0 {
		// WHAT CARRIES THE WHOLE SENTENCE, for the reason the sibling refusal
		// records: a StructuredError renders as "category: What" once WRAPPED,
		// and this error is always wrapped into the fault cause. Why and Fix
		// reach nobody unless something calls Report, and nothing on this path
		// does. They stay populated for a caller that renders the full block.
		const emptyWhy = "A lifecycle event is evaluated from the payload the host writes to standard input, " +
			"and this invocation received an empty stream rather than a malformed or partial one."
		const emptyImpact = "Nothing was parsed and no database was opened, so no occurrence exists for " +
			"this invocation."
		const emptyFix = "Check that the host is writing the event payload to the hook's standard input: a " +
			"hook wired without its input redirected, a wrapper that consumes stdin before pasture runs, " +
			"or a manual invocation with no payload piped in all arrive here."
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation,
			"The host sent no payload: standard input carried zero bytes. "+
				emptyWhy+" "+emptyImpact+" "+emptyFix,
			emptyWhy, emptyImpact, emptyFix, nil)
	}
	if len(raw) > model.MaxNativePayloadBytes {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("The native payload exceeds the %d-byte bound.", model.MaxNativePayloadBytes), "Ingress never truncates retained evidence.", "No database was opened.", "Reduce the host payload below the static bound.", nil)
	}
	capture := dispatch.parse(raw, event, in.HostVersion)
	tracker, err := open(in.DBPath)
	if err != nil {
		return backend.HostResponse{}, err
	}
	if tracker == nil {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryStorage, "The lifecycle store opener returned no store.", "A receipt cannot be appended without the unified tracker.", "Nothing was recorded.", "Use tasks.OpenTaskTracker.", nil)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptService(tracker, in.Clock, in.Operations)
	if err != nil {
		return backend.HostResponse{}, err
	}
	// The native path is the only producer of the invalid-capture receipt row:
	// it records the disposition evidence for a malformed host payload without
	// the derived effects. Raw ingestion refuses outright instead, so
	// this write class exists only at the native surface. Every durable write
	// presents the same origin-blind gate.Warrant as the shared commit tail.
	if capture.disposition != model.CaptureValid {
		deliveryIntent, refusal := gate.NewDeliveryIntent(capture.delivery.Contract, capture.delivery.Event)
		if refusal != nil {
			return backend.HostResponse{}, refusal
		}
		warrant, refusal := gate.Legalize(deliveryIntent)
		if refusal != nil {
			return backend.HostResponse{}, refusal
		}
		// A WRITE IS ATTEMPTED HERE. See durablePossible.
		durablePossible = true
		if _, err = service.Receive(ctx, warrant, capture.delivery); err != nil {
			return backend.HostResponse{}, err
		}
		// THE RECEIPT ROW IS DURABLE EVIDENCE, NOT A HOST ANSWER, AND THIS ARM
		// USED TO RETURN A NIL ERROR BESIDE IT.
		//
		// A nil error is how this handler says "evaluated". Both native
		// encoders answer nil for a zero HostResponse, so the command took its
		// SUCCESS path and called the exit authority with EMPTY continuation
		// bytes: exit 0, nothing on standard output, nothing on standard error,
		// no fault record, and no fail-closed consideration. AN EVENT THAT WAS
		// NEVER EVALUATED LEFT AS A DECISION.
		//
		// MEASURED ON A HEALTHY STORE: one renamed or empty identity field was
		// enough, on every harness. Driven under Bun against the plugin this
		// repository shipped before its empty-body belt existed — the
		// already-installed older reader that the continue bytes exist to
		// protect — the empty body made JSON.parse throw inside a gate callback
		// and the user's tool call stopped. No broken database, no old binary,
		// no diagnostic anywhere.
		//
		// Returning an error is the whole fix, and it is made HERE rather than
		// in a new guard because this is the authority that decides whether an
		// event was evaluated. The command routes every error through the fault
		// path, so this route now receives what every other unevaluated event
		// receives: this harness's continue bytes, a stderr diagnostic, a fault
		// record line, and the fail-closed opt-in.
		return backend.HostResponse{}, fmt.Errorf("%w: %w",
			ErrLifecycleDeliveryRefused, unbindableCaptureError(dispatch, event, in, capture))
	}
	// Valid captures converge on the shared delivery commit tail with the raw
	// surface, so the verification sequence cannot drift between
	// the two handlers.
	// A WRITE IS ATTEMPTED HERE. See durablePossible.
	durablePossible = true
	committed, err := deliveryCommit(ctx, service, dispatch, event, capture.delivery)
	if err != nil {
		return backend.HostResponse{}, err
	}
	response = committed
	return response, nil
}

// deliveryCommit is the ONE verification-and-commit sequence shared by the
// native and raw lifecycle surfaces — all flow into the same pipeline: deliveryWarrant (intent → legalize) → EnsureActiveMetamodel →
// deliveryDerive (typed bind → NewEvent → Derive) → Receive. The ordering is
// compatibility-sensitive: metamodel activation precedes derivation exactly
// as it did before dry-run existed.
func deliveryCommit(ctx context.Context, service receipt.Service, dispatch lifecycleDispatch, event registration.Event, delivery receipt.Delivery) (backend.HostResponse, error) {
	// THIS REFUSAL IS BEFORE ANY WRITE, AND IT IS PAST THE CALLER'S MARKER.
	//
	// The marker stands at the call to this function, because the metamodel
	// journalling below IS a write. The warrant step above it is not: it is a
	// gate refusal, and it left the caller answering "MAY OR MAY NOT exist"
	// while the same refusal on the invalid-capture arm answered not-recorded.
	// One refusal, two answers, decided by which arm reached it.
	//
	// It is wrapped HERE rather than by moving the caller's marker, because the
	// marker cannot sit inside a function the caller cannot see into, and
	// because the sentinel is a property of the REGION wherever a refusal is
	// raised — the same reason the harness refusal is wrapped where it is
	// raised rather than where it is caught.
	warrant, err := deliveryWarrant(delivery)
	if err != nil {
		return backend.HostResponse{}, fmt.Errorf("%w: %w", ErrLifecycleBeforeDurableWrite, err)
	}
	// On the valid-capture path, lazily journal the active metamodel BEFORE the
	// delivery receipt is written. The definition-activation operation commits
	// before the first delivery that references the coordinate, so a committed
	// interpreted.v2 record can never cite an unjournaled metamodel. It is
	// idempotent and race-safe (deterministic content-derived operation ID), so
	// steady state is one single lookup and zero writes.
	if _, err := receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		return backend.HostResponse{}, err
	}
	derivation, err := deliveryDerive(dispatch, event, delivery)
	if err != nil {
		return backend.HostResponse{}, err
	}
	if _, err := service.Receive(ctx, warrant, delivery, derivation.Effects()...); err != nil {
		return backend.HostResponse{}, err
	}
	return derivation.Response(), nil
}

// deliveryVerify composes the same pure warrant and derivation helpers as the
// committing path without the interleaved metamodel/store operations. It is
// used only for dry-run; deliveryCommit keeps its compatibility-sensitive I/O
// ordering while sharing both pieces of verification logic.
func deliveryVerify(dispatch lifecycleDispatch, event registration.Event, delivery receipt.Delivery) (gate.Warrant, middleend.Derivation, error) {
	warrant, err := deliveryWarrant(delivery)
	if err != nil {
		return gate.Warrant{}, middleend.Derivation{}, err
	}
	derivation, err := deliveryDerive(dispatch, event, delivery)
	if err != nil {
		return gate.Warrant{}, middleend.Derivation{}, err
	}
	return warrant, derivation, nil
}

// deliveryWarrant performs the pure origin-blind write-gate verification.
func deliveryWarrant(delivery receipt.Delivery) (gate.Warrant, error) {
	deliveryIntent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	if refusal != nil {
		return gate.Warrant{}, refusal
	}
	warrant, refusal := gate.Legalize(deliveryIntent)
	if refusal != nil {
		return gate.Warrant{}, refusal
	}
	return warrant, nil
}

// deliveryDerive is the single pure L1→L2 verification and derivation path.
func deliveryDerive(dispatch lifecycleDispatch, event registration.Event, delivery receipt.Delivery) (middleend.Derivation, error) {
	l1, identities, err := dispatch.bind(event.Kind, delivery.Bindings)
	if err != nil {
		return middleend.Derivation{}, err
	}
	l2, err := l1.NewEvent(identities)
	if err != nil {
		return middleend.Derivation{}, err
	}
	derivation, err := middleend.Derive(l2, metamodel.Active())
	if err != nil {
		return middleend.Derivation{}, err
	}
	return derivation, nil
}

// withRawOrigin stamps the raw capture origin on BOTH the envelope carrier and
// the delivery carrier of an already-classified native capture — the whole raw
// stamping the shared pipeline applies. The raw parsers are the only
// producers that populate the origin carrier; the native path stays at the
// zero value so its golden payloads stay byte-identical.
func withRawOrigin(capture lifecycleCapture) lifecycleCapture {
	capture.delivery.Envelope.Origin = origin.OriginRaw
	capture.delivery.Origin = origin.OriginRaw
	return capture
}

// dispatchLifecycle resolves the static registry row for a harness. It is a
// pure map lookup: the only error is the unchanged unsupported-harness rejection
// (naming the harness), which is now the relocated home of the unknown-harness
// negative coverage formerly asserted in nativeresponse_test.go.
func dispatchLifecycle(harness ir.HarnessID) (lifecycleDispatch, error) {
	dispatch, ok := frontendRegistry[harness]
	if !ok {
		return lifecycleDispatch{}, lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Harness %q is not supported by lifecycle ingress.", harness), "Lifecycle ingress has no static provider dispatch for this harness.", "The input was not read and no database was opened.", "Use a harness present in the generated lifecycle support report.", nil)
	}
	return dispatch, nil
}

// CommitBarrier is the named boundary between "the lifecycle receipt is
// durably committed" and "the host is told". It is an interface rather than an
// implicit sequence point because that boundary carries the strongest guarantee
// of this command: nothing reaches the host's standard output until the
// evidence for it is persisted.
//
// Naming it buys three things a bare sequence point cannot. An operator reading
// the code sees where the guarantee is enforced instead of inferring it from
// statement order. A later change that needs to act at that point — flushing a
// capture sink, publishing a notification, waiting for a replica — has one
// place to attach rather than a new call inserted at a new place. And the
// guarantee becomes observable, so a test can hold the invocation exactly there
// and read the durable state on both sides, instead of racing a clock.
//
// AfterCommit is called at most ONCE per invocation, only on the path where the
// commit succeeded. An error it returns is a POST-COMMIT error: the occurrence
// exists and the host did not receive its continuation, which is the same class
// as a failed encode.
type CommitBarrier interface {
	AfterCommit(ctx context.Context, boundary CommitBoundary) error
}

// CommitBoundary names the invocation that crossed the boundary. It carries the
// host coordinates and nothing authority-bearing: a barrier observes that a
// commit happened, and never which decision it carried.
type CommitBoundary struct {
	Harness ir.HarnessID
	Event   string
}

// PassThroughCommitBarrier is the production barrier: it does nothing and
// cannot fail, so the boundary is named without costing an invocation anything.
type PassThroughCommitBarrier struct{}

// AfterCommit does nothing and returns nil.
func (PassThroughCommitBarrier) AfterCommit(context.Context, CommitBoundary) error { return nil }

// ErrLifecycleCommittedWithoutContinuation marks a fault that happened AFTER
// the durable receipt was committed and before the host received its
// continuation. It exists so the caller can tell the host the truth: for these
// faults the occurrence EXISTS, and a diagnostic claiming the event was never
// recorded would send a maintainer to look in the wrong place.
var ErrLifecycleCommittedWithoutContinuation = errors.New("the lifecycle receipt was committed but the host received no continuation")

// ErrLifecycleDeliveryRefused marks a delivery whose capture could not be
// bound. It is the sibling of the error above and exists for the same reason:
// the caller has to tell the host the truth about the durable state. For these
// faults the delivery row EXISTS — it carries the disposition that refused it
// and an empty interpreted set — while the event itself was NOT evaluated, so a
// diagnostic claiming nothing was recorded sends a maintainer to look in the
// wrong place, and one claiming the event was evaluated would be worse.
var ErrLifecycleDeliveryRefused = errors.New("the lifecycle delivery was recorded but its capture could not be bound")

// ErrLifecycleBeforeDurableWrite marks a fault raised before any WRITE was
// attempted, so no row can exist for the invocation.
//
// IT IS THE WRITE AND NOT THE OPEN, and this doc said the open. That was the
// first of three texts to define this sentinel that way and the only one left
// uncorrected — and it is the EXPORTED doc, so it is what `go doc` prints and
// the text the other two paraphrase. It had come to disagree with the error
// string on the line below it, and it was false for five producers, one of them
// added in the same round that corrected the other two. An open creates a file
// and no occurrence; refusals between the open and the first write are inside
// this sentinel's reach and outside any region the open would have described.
//
// IT IS THE EVIDENCE FOR A CLAIM THAT USED TO BE AN ASSUMPTION. Its caller's
// stage table answered "no occurrence was recorded" for every error it did not
// recognise, which is a promise about the unenumerated. The journal appender
// falsifies that promise: it can fail after the commit has succeeded, saying so
// in its own words, and it carries no sentinel of its own. The caller's default
// is now the WEAKEST claim, and this sentinel is what lets the ordinary
// pre-store refusals keep the precise one.
var ErrLifecycleBeforeDurableWrite = errors.New("the lifecycle fault happened before any durable write was attempted")

// HookLifecycleNative records the lifecycle receipt and, only after the durable
// commit has completed, returns the exact native continuation bytes the harness
// reads on standard output — the single dispatch surface the CLI invokes. The
// commit-before-stdout invariant is structural: the per-target encoder runs
// solely on the nil error path of HookLifecycleResponse, so native bytes never
// precede persisted evidence. An unsupported harness resolves no registry row
// and returns the unchanged unsupported-harness error with nil bytes, so nothing
// is written to stdout.
func HookLifecycleNative(ctx context.Context, in HookLifecycleInput) ([]byte, error) {
	// THIS REFUSAL NEVER REACHED hookLifecycle's WRAPPER, so it fell to the
	// caller's weakest-claim default and told an operator its occurrence "MAY
	// OR MAY NOT exist" and to read the record "beside the database" — on a run
	// where the filesystem shows no database was ever created, and where this
	// function's own doc and the error's own When field both say nothing was
	// opened. One code path away, the withheld-event refusal answered
	// correctly, because that one is raised inside the region the wrapper
	// covers.
	//
	// The wrapper is a property of the REGION, not of one function, so every
	// refusal raised before the first write attempt carries the sentinel
	// wherever it is raised.
	dispatch, err := dispatchLifecycle(in.Harness)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLifecycleBeforeDurableWrite, err)
	}
	response, err := HookLifecycleResponse(ctx, in)
	if err != nil {
		return nil, err
	}
	// The named commit-to-emit boundary. Everything above it is durable;
	// nothing below it has reached the host yet.
	barrier := in.Barrier
	if barrier == nil {
		barrier = PassThroughCommitBarrier{}
	}
	if err := barrier.AfterCommit(ctx, CommitBoundary{Harness: in.Harness, Event: in.Event}); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", dispatch.name, ErrLifecycleCommittedWithoutContinuation, err)
	}
	// The encode runs only AFTER the lifecycle receipt has been durably
	// committed by HookLifecycleResponse, so any failure here means the
	// receipt is persisted but the native continuation was not delivered to
	// the host. Wrap it so the operator knows the durable state is intact and
	// only the stdout continuation is missing. This branch is provably
	// unreachable today (both encoders return nil,nil on an invalid/absent
	// response: CanonicalProceed and CodexContinuation never fail on a valid
	// HostResponse); the guard exists as a post-commit audit guarantee so a
	// future encoder that can fail cannot silently drop the continuation.
	native, err := dispatch.encode(response)
	if err != nil {
		return nil, fmt.Errorf("%s lifecycle receipt committed but native continuation was not delivered (encode failed): %w: %w", dispatch.name, ErrLifecycleCommittedWithoutContinuation, err)
	}
	return native, nil
}

func activationFor(kind model.ContractEventKind, entries []activation.Entry) (activation.Entry, bool) {
	for _, entry := range entries {
		if entry.Event == kind {
			return entry, true
		}
	}
	return activation.Entry{}, false
}

// captureDispositionAdvice is what an operator is told about ONE non-valid
// disposition: why the payload could not be bound, and what to do about THAT.
//
// THE FIX FOLLOWS THE DIAGNOSIS, PER DISPOSITION. Only the event-mismatch row
// had its own fix; every other disposition was told to check identity field
// NAMES and the host VERSION — and on a malformed, invalid-UTF-8 or
// duplicate-field payload NEITHER WAS EVER INSPECTED. Ingress stopped at the
// decode, so the message sent its reader to compare fields against a
// registration that was never consulted. A true sentence pointing at the wrong
// thing costs the reader the same hour as a false one.
type captureDispositionAdvice struct {
	// Reason is the clause that completes "... could not be bound: <reason>".
	Reason string
	// Fix is the instruction that follows from THAT reason.
	Fix string
	// NamesIdentities says whether the message may name the event's identities
	// AT ALL, and it deliberately no longer claims a RESULT for them.
	//
	// IT WAS ReachedIdentities, KEYED ON THE DISPOSITION, AND THE THREE CAUSES
	// OF ONE DISPOSITION DISAGREE ON EXACTLY THAT QUESTION. A payload carrying
	// every identity present, correctly named and usable is refused for an
	// unknown MEMBER, and the allowed-member loop returns BEFORE the identity
	// loop — so "none of the identities this event requires was bound" reported
	// the result of a step that did not run. That is not imprecision about
	// which cause fired; it is an inspection result invented for an inspection
	// that never happened.
	//
	// So where the causes disagree, the message NAMES the identities as context
	// and asserts nothing about them. Where a single cause is certain — the
	// decode failures — it names nothing, because nothing was looked at.
	NamesIdentities bool
}

// captureDispositionAdviceByDisposition covers every disposition a parser on
// this path can produce.
//
// CaptureTruncated AND CaptureOverLimit ARE ABSENT ON PURPOSE. They are
// declared in the model and NO parser produces either: the over-limit condition
// never reaches a capture at all, because the handler refuses a payload above
// the bound before it parses, with its own error. Carrying words for a
// disposition nothing produces is a sentence that cannot be true or false,
// which is a sentence nobody can check. If a parser ever produces one, the
// unlisted default below refuses to invent a reason for it and says so.
var captureDispositionAdviceByDisposition = map[model.CaptureDisposition]captureDispositionAdvice{
	// "NOT WELL-FORMED JSON" IS FALSE FOR HALF THIS ARM'S INPUTS. `[]`,
	// `"hello"` and `123` are all perfectly well-formed JSON and all three land
	// here, because what the parser needs is a JSON OBJECT and it got a value
	// of another kind. The sentence that is true of every input is the one
	// about the SHAPE, and it covers the genuinely malformed case too: a
	// fragment that does not parse is not an object either.
	model.CaptureMalformed: {
		Reason: "the payload is not a JSON object, so no field could be read from it",
		Fix: "Send a JSON OBJECT: a well-formed array, string or number is refused here too, because the " +
			"event's fields are read from an object's members. Nothing about the fields or the host version " +
			"was inspected on this route, because ingress stopped at the decode; capture the exact bytes the " +
			"host sent and check both that they parse AND that the top level is an object.",
		NamesIdentities: false,
	},
	model.CaptureInvalidUTF8: {
		Reason: "the payload is not valid UTF-8, so it was never decoded",
		Fix: "Send UTF-8. Nothing about the fields or the host version was inspected on this route, because ingress " +
			"stopped at the encoding check; the usual cause is a payload spliced together from bytes in another " +
			"encoding, or truncated mid-character.",
		NamesIdentities: false,
	},
	model.CaptureDuplicateField: {
		Reason: "the payload repeats a field, so which value was meant cannot be decided",
		Fix: "Send each field once. Pasture will not guess between two values for one field, and no field or version " +
			"check was reached on this route; look for a payload assembled by concatenation or merged from two sources.",
		NamesIdentities: false,
	},
	// ONE DISPOSITION, THREE CAUSES, AND THE SENTENCE NAMED ONLY ONE OF THEM.
	// A payload carrying EVERY identity field under the EXACT declared name is
	// refused when it also carries ONE MEMBER the registration does not allow,
	// and it was told its identity fields were missing or renamed — false of
	// that payload in both halves. Measured with a control: remove the extra
	// member and the same identity binds with zero bytes of stderr.
	//
	// A HOST ADDING A FIELD IS THE MOST ORDINARY THING THAT HAPPENS TO A
	// PAYLOAD OVER TIME, so this is the route most likely to be met and the one
	// most likely to cost its reader a day chasing field names that are
	// correct. The sentence now names every cause this disposition carries, so
	// it is true whichever fired. Telling them APART needs the classifier
	// split, which is a change to the model enum and to the parsers.
	model.CaptureUnsupportedSchema: {
		// Reason is COMPOSED PER HARNESS where it is assembled, because the
		// causes this disposition carries differ by parser: only a validating
		// parser can refuse an undeclared member. Offering that cause to a
		// struct decoder made ONE MESSAGE CONTRADICT ITSELF — the reason said
		// an added member may be why, and the remedy three sentences later said
		// added members are ignored. This entry holds the half that is true of
		// every parser; unsupportedSchemaReason adds the rest.
		Reason:          "",
		Fix:             "", // composed per call: it names the event and the host version.
		NamesIdentities: true,
	},
	// THREE CAUSES, ONE LINE, AND THE SENTENCE NAMED ONLY THE THIRD. The parser
	// returns this disposition when the event field is ABSENT, when it cannot
	// be read, and when it names a different event — and a payload carrying no
	// event at all was told it describes a different one. The cause does not
	// survive the classification, so the sentence covers all three rather than
	// guessing which fired; the same repair the identity clause took.
	model.CaptureEventMismatch: {
		Reason: "the payload does not report this event — the field is absent, unreadable, or names a " +
			"different event",
		Fix: "", // composed per call: it names the event and the host version.
		// The event claim is checked BEFORE the identities, so this one did not
		// reach them either: the payload decoded, and the refusal happened at
		// the coordinate rather than at a field.
		NamesIdentities: false,
	},
}

// CaptureDispositionReasons returns every reason text a disposition can render,
// across every harness this build dispatches on.
//
// IT IS A SET AND NOT A STRING because one disposition's reason is COMPOSED
// FROM THE PARSER that refused: a validating parser can offer an undeclared
// member as a cause and a struct decoder cannot. A test asking "is this
// disposition driven" must ask against what it can actually say, or a
// disposition whose reason is composed looks undriven.
func CaptureDispositionReasons(disposition model.CaptureDisposition) []string {
	advice, known := captureDispositionAdviceByDisposition[disposition]
	if !known {
		return nil
	}
	if advice.Reason != "" {
		return []string{advice.Reason}
	}
	seen := map[string]bool{}
	reasons := []string{}
	for _, dispatch := range frontendRegistry {
		reason := unsupportedSchemaReason(dispatch)
		if !seen[reason] {
			seen[reason] = true
			reasons = append(reasons, reason)
		}
	}
	sort.Strings(reasons)
	return reasons
}

// LifecycleHarnessCoordinate names one harness this build dispatches on, with a
// coordinate that reaches its ingress.
type LifecycleHarnessCoordinate struct {
	Harness     string
	Event       string
	HostVersion string
}

// LifecycleHarnessCoordinates derives the harness set from the SAME registry the
// command dispatches on, so a test over "every harness" grows with the product
// instead of with somebody's memory.
//
// IT RETURNS AN ERROR WHEN THE SET IS SMALLER THAN THE REGISTRY. A harness whose
// activation proofs fail, or which admits no event, used to be dropped through
// a silent `continue`; a caller ranging over the result then ran one subtest
// fewer and stayed green, which is a derived population that shrank with no
// reader noticing. The dropped harness is named instead, because the caller
// cannot see the registry and has no other way to learn that its "every
// harness" is one short.
func LifecycleHarnessCoordinates() ([]LifecycleHarnessCoordinate, error) {
	// AN ADMITTED EVENT, NOT THE FIRST ONE LISTED. The activation posture is
	// resolved from the same generated proofs the command resolves it from,
	// because a WITHHELD event refuses before the payload is ever read — a
	// coordinate chosen without asking would drive a different arm on some
	// harnesses and quietly prove nothing about this one.
	coordinates := []LifecycleHarnessCoordinate{}
	dropped := []string{}
	for harness, dispatch := range frontendRegistry {
		activations, err := dispatch.activations()
		if err != nil {
			dropped = append(dropped, fmt.Sprintf("%s: its activation proofs did not resolve (%v)", harness, err))
			continue
		}
		admitted := false
		for _, entry := range dispatch.manifest.Entries() {
			state, found := activationFor(entry.Kind, activations)
			if !found || state.State != activation.Enabled {
				continue
			}
			coordinates = append(coordinates, LifecycleHarnessCoordinate{
				Harness:     string(harness),
				Event:       entry.NativeName,
				HostVersion: dispatch.manifest.Version,
			})
			admitted = true
			break
		}
		if !admitted {
			dropped = append(dropped, fmt.Sprintf("%s: its %s registration admits no event", harness, dispatch.manifest.Version))
		}
	}
	sort.Strings(dropped)
	if len(dropped) != 0 {
		return nil, fmt.Errorf("LifecycleHarnessCoordinates (internal/handlers/hook_lifecycle.go) could not give every "+
			"dispatched harness a coordinate while deriving the harness set: %s. A harness with no coordinate "+
			"cannot be driven, so a caller that ranges over this set would run one subtest fewer and prove "+
			"nothing about it; restore that harness's activation proofs, or enable at least one of its events, "+
			"before relying on the set", strings.Join(dropped, "; "))
	}
	sort.Slice(coordinates, func(i, j int) bool { return coordinates[i].Harness < coordinates[j].Harness })
	return coordinates, nil
}

// CaptureDispositionAdvice exposes the disposition advice table so a test can
// require every disposition this build states advice for to be DRIVEN on a real
// invocation. It returns a copy; nothing may edit the table through it.
func CaptureDispositionAdvice() map[model.CaptureDisposition]captureDispositionAdvice {
	copied := make(map[model.CaptureDisposition]captureDispositionAdvice, len(captureDispositionAdviceByDisposition))
	for disposition, advice := range captureDispositionAdviceByDisposition {
		copied[disposition] = advice
	}
	return copied
}

// unsupportedSchemaReason names the causes THIS harness's parser can produce.
//
// THE DIAGNOSIS HALF WAS LEFT BEHIND WHEN THE REMEDY HALF WAS FIXED. The remedy
// learned that a struct decoder IGNORES an undeclared member; the shared reason
// went on offering that cause to every harness, so one rendered message told a
// Codex operator both that an added member may be why their payload was refused
// and that added members are ignored. A message that contradicts itself costs
// its reader more than one that is merely incomplete.
//
// Both facts are on the dispatch row this already receives.
func unsupportedSchemaReason(dispatch lifecycleDispatch) string {
	naming := "an identity field is missing or unusable"
	if dispatch.matchesFieldNamesExactly {
		naming = "an identity field is missing, renamed or unusable"
	}
	if !dispatch.refusesUndeclaredMembers {
		return "the payload does not match the shape this event's registration declares: " + naming
	}
	return "the payload does not match the shape this event's registration declares: either " +
		naming + ", or the payload carries a member the registration does not allow"
}

// unbindableCaptureError says that an event WAS NOT EVALUATED, and says which
// correlation identities could not be bound.
//
// IT NAMES THE IDENTITIES FROM THE GENERATED REGISTRATION and never from a list
// written here, so the message cannot drift from the contract it describes: the
// required identities of the event are what ingress had to bind, and whatever
// is missing from the bindings the parser DID recover is what it could not.
// A host that renames or drops a correlation field between versions lands here
// by construction, which is why the message points at the host version too.
func unbindableCaptureError(
	dispatch lifecycleDispatch,
	event registration.Event,
	in HookLifecycleInput,
	capture lifecycleCapture,
) error {
	// THE LIST IS EVERY IDENTITY THE EVENT REQUIRES, AND IT NEVER DISCRIMINATES.
	// It once filtered by which bindings survived, which read as a claim that
	// the message names the fields that actually failed. IT DOES NOT AND CANNOT:
	// every ingress parser returns NIL BINDINGS on a non-valid capture, so the
	// filter removed nothing on any input a user can produce, and a payload
	// missing one identity rendered a message byte-identical to one missing all
	// of them. Dropping the filter changed no output anywhere.
	//
	// So the sentence says what is true — these are the identities the event
	// REQUIRES, one of which is unsatisfied — rather than implying a
	// discrimination the data cannot support. Narrowing it to the field that
	// actually failed would need the parsers to report which one, and they do
	// not; that is a change to ingress, not a rewording here.
	required := []string{}
	for _, identity := range event.IdentityFields() {
		if identity.Required {
			required = append(required, identity.Binding.String())
		}
	}

	advice, named := captureDispositionAdviceByDisposition[capture.disposition]
	reason := advice.Reason
	if capture.disposition == model.CaptureUnsupportedSchema {
		reason = unsupportedSchemaReason(dispatch)
	}
	if !named {
		reason = "the payload could not be classified, and this build states no reason for that classification"
	}

	// THE CLAUSE NAMES THE IDENTITIES AND ASSERTS NOTHING ABOUT THEM. It used
	// to say they "was not bound", which is an inspection RESULT — and on the
	// unknown-member cause the inspection never ran, because the allowed-member
	// loop returns before the identity loop. Naming them is still worth doing:
	// the reader needs to know which fields the event requires in order to
	// check them. Claiming they failed is not.
	missing := ""
	switch {
	case !advice.NamesIdentities:
		missing = ""
	case len(required) == 1:
		missing = fmt.Sprintf("; the identity this event requires is %s", required[0])
	case len(required) > 1:
		missing = fmt.Sprintf("; the identities this event requires are %s", strings.Join(required, ", "))
	case len(event.IdentityFields()) == 0:
		missing = "; this event declares no identities, so the payload itself could not be classified"
	}

	why := reason + missing
	// THIS CLAUSE SAID "No occurrence was recorded for this event" AND THAT WAS
	// FALSE. Measured on the built binary: the delivery IS written to the
	// lifecycle occurrence journal, and `hook lifecycle list` shows it with an
	// empty interpreted set. What is absent is anything DERIVED from it, not
	// the row itself.
	//
	// IT NO LONGER SAYS WHERE THE ROW IS, and that is deliberate. The generic
	// fault diagnostic this message is wrapped in now derives that from the
	// fault stage and says it once. A second copy here would be the same
	// sentence maintained in two places, which is how the clause it replaced
	// came to be false in the first place. This clause says only what the
	// generic one cannot know: that nothing was derived and no gate was
	// consulted, and what the row carries.
	impact := "Nothing was derived from this delivery and no gate was consulted, so the event had no part in the " +
		"host's answer; the row carries the disposition that refused it and an empty interpreted set."
	// THE FIX FOLLOWS THE DIAGNOSIS, and the two dispositions whose fix has to
	// NAME the event and the host version compose it here; the rest carry their
	// own, because theirs do not depend on this invocation.
	fix := advice.Fix
	switch {
	case !named:
		fix = fmt.Sprintf("Report this: a payload for %q was refused with a classification this build has no words "+
			"for, so pasture cannot say what to change.", event.NativeName)
	case capture.disposition == model.CaptureEventMismatch:
		fix = fmt.Sprintf("Invoke the hook with the event the payload actually describes, or send the payload for %q: "+
			"the event named on the command line is authoritative, and a payload that declares a different one is "+
			"refused rather than reinterpreted. Check too that the host version on the command line (%q) is the "+
			"version the host actually runs, because the event a host reports can change between versions.",
			event.NativeName, in.HostVersion)
	case capture.disposition == model.CaptureUnsupportedSchema:
		// THE ADVICE FOLLOWS WHAT THIS PARSER ENFORCES, and it used to name the
		// harness while describing another one's rules. Both facts are on the
		// dispatch row this call already receives, so no new plumbing and no
		// classifier change is needed to stop telling a Codex operator that
		// added members are refused and that field names must match exactly —
		// neither of which is true of the parser that refused them.
		naming := "must be present and carry a usable value"
		if dispatch.matchesFieldNamesExactly {
			naming = "must be present under the name it expects, spelled exactly, and carry a usable value"
		}
		members := "Members this build does not declare are IGNORED by this harness's parser, so an added " +
			"field is not what refused this payload."
		if dispatch.refusesUndeclaredMembers {
			members = "AND the payload must carry no member the registration does not declare — on this " +
				"harness a host that ADDS a field is refused just as one that renames or drops a " +
				"correlation field is."
		}
		fix = fmt.Sprintf("Compare the payload with this build's %s registration for %q: every identity field "+
			"it declares %s. %s Check too that the host version on the command line (%q) is the version the "+
			"host actually runs.",
			dispatch.name, event.NativeName, naming, members, in.HostVersion)
	}

	// WHAT CARRIES THE WHOLE SENTENCE, and that is not redundancy. A
	// StructuredError renders as "category: What" once it is WRAPPED, and this
	// error is always wrapped — the command folds it into the fault cause. Only
	// Report prints Why and Fix, and nothing on this path calls Report. So an
	// operator reads What and nothing else, and the identity that could not be
	// bound has to be in it or it does not reach them. Why, Impact and Fix stay
	// populated for any caller that does render the full block.
	return lifecycleError(
		pasterrors.CategoryValidation,
		fmt.Sprintf("The %s payload for event %q at host version %q could not be bound, so the event WAS NOT EVALUATED: "+
			"%s. %s %s",
			dispatch.name, event.NativeName, in.HostVersion, why, impact, fix),
		why, impact, fix, nil)
}

func lifecycleError(category pasterrors.Category, what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: category, What: what, Why: why, Where: hookLifecycleWhere, Impact: impact, Fix: fix, Cause: cause}
}
