package handlers

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	claudefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/claude"
	codexfrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	opencodefrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
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
	// Activations optionally injects the activation configuration used by the
	// event gate, overriding the statically dispatched per-harness manifest.
	//
	// Production callers (the CLI) leave this nil so the committed per-harness
	// activation manifest governs admission. After M3 Implementation UAT the
	// Codex dispatch enables the accepted SessionStart and PreToolUse events via
	// activation.Codex0_146_0(), exactly as the Claude and OpenCode cases do;
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
//     row (D4), replacing the deleted nativeresponse.Encode harness switch.
type lifecycleDispatch struct {
	name        string
	manifest    registration.Manifest
	activations func() ([]activation.Entry, error)
	parse       func([]byte, registration.Event, string) lifecycleCapture
	bind        func(model.ContractEventKind, []model.NativeBinding) (waist.L1, []waist.Identity, error)
	encode      func(backend.HostResponse) ([]byte, error)
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
// pins. The raw path (M4) is the first producer to populate the origin carrier:
// it stamps OriginRaw on the envelope it passes to the per-harness ingress
// parser and on the resulting delivery.
var frontendRegistry = map[ir.HarnessID]lifecycleDispatch{
	ir.HarnessClaudeCode: {
		name:        "Claude",
		manifest:    registration.ClaudeCode2_1_210(),
		activations: activation.ClaudeCode2_1_210,
		parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := claudeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		},
		bind:   claudefrontend.Bind,
		encode: nativeresponse.CanonicalProceed,
	},
	ir.HarnessOpenCode: {
		name:        "OpenCode",
		manifest:    registration.OpenCode1_18_10(),
		activations: activation.OpenCode1_18_10,
		parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := opencodeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		},
		bind:   opencodefrontend.Bind,
		encode: nativeresponse.CanonicalProceed,
	},
	ir.HarnessCodex: {
		name:        "Codex",
		manifest:    registration.Codex0_146_0(),
		activations: activation.Codex0_146_0,
		parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := codexingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		},
		bind:   codexfrontend.Bind,
		encode: nativeresponse.CodexContinuation,
	},
}

func hookLifecycle(ctx context.Context, in HookLifecycleInput, open lifecycleStoreOpener) (backend.HostResponse, error) {
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
	var event registration.Event
	for _, candidate := range dispatch.manifest.Events {
		if candidate.NativeName == in.Event {
			event = candidate
			break
		}
	}
	if event.Kind == 0 {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Event %q is not in the generated %s registration.", in.Event, dispatch.name), "Ingress trusts the generated registration coordinate rather than an unparsed payload claim.", "The input was not read and no database was opened.", "Invoke one of the events present in the support report.", nil)
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
	// Every durable lifecycle write presents a gate.Warrant. Deliveries are the
	// delivery-receipt write class; the gate is origin-blind, so one warrant
	// covers the valid and the invalid-capture receipt uniformly. The warrant is
	// pure (no I/O) and is built only on the admitted-dispatch path, so it never
	// affects the pre-store ordering that keeps an invalid invocation from
	// creating a database file (M1 §8).
	deliveryIntent, refusal := gate.NewDeliveryIntent(capture.delivery.Contract, capture.delivery.Event)
	if refusal != nil {
		return backend.HostResponse{}, refusal
	}
	warrant, refusal := gate.Legalize(deliveryIntent)
	if refusal != nil {
		return backend.HostResponse{}, refusal
	}
	if capture.disposition != model.CaptureValid {
		_, err = service.Receive(ctx, warrant, capture.delivery)
		return backend.HostResponse{}, err
	}
	// On the valid-capture path, lazily journal the active metamodel BEFORE the
	// delivery receipt is written. The definition-activation operation commits
	// before the first delivery that references the coordinate, so a committed
	// interpreted.v2 record can never cite an unjournaled metamodel. It is
	// idempotent and race-safe (deterministic content-derived operation ID), so
	// steady state is one bounded lookup and zero writes.
	if _, err = receipt.EnsureActiveMetamodel(ctx, service); err != nil {
		return backend.HostResponse{}, err
	}
	l1, identities, err := dispatch.bind(event.Kind, capture.delivery.Bindings)
	if err != nil {
		return backend.HostResponse{}, err
	}
	l2, err := l1.NewEvent(identities)
	if err != nil {
		return backend.HostResponse{}, err
	}
	derivation, err := middleend.Derive(l2, metamodel.Active())
	if err != nil {
		return backend.HostResponse{}, err
	}
	_, err = service.Receive(ctx, warrant, capture.delivery, derivation.Effects()...)
	if err != nil {
		return backend.HostResponse{}, err
	}
	return derivation.Response(), nil
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

// HookLifecycleNative records the lifecycle receipt and, only after the durable
// commit has completed, returns the exact native continuation bytes the harness
// reads on standard output — the single dispatch surface the CLI invokes. The
// commit-before-stdout invariant is structural: the per-target encoder runs
// solely on the nil error path of HookLifecycleResponse, so native bytes never
// precede persisted evidence. An unsupported harness resolves no registry row
// and returns the unchanged unsupported-harness error with nil bytes, so nothing
// is written to stdout.
func HookLifecycleNative(ctx context.Context, in HookLifecycleInput) ([]byte, error) {
	dispatch, err := dispatchLifecycle(in.Harness)
	if err != nil {
		return nil, err
	}
	response, err := HookLifecycleResponse(ctx, in)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("%s lifecycle receipt committed but native continuation was not delivered (encode failed): %w", dispatch.name, err)
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

func lifecycleError(category pasterrors.Category, what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: category, What: what, Why: why, Where: hookLifecycleWhere, Impact: impact, Fix: fix, Cause: cause}
}
