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
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	codexingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	opencodeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
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

type lifecycleDispatch struct {
	name        string
	manifest    registration.Manifest
	activations []activation.Entry
	parse       func([]byte, registration.Event, string) lifecycleCapture
	bind        func(model.ContractEventKind, []model.NativeBinding) (waist.L1, []waist.Identity, error)
}

func hookLifecycle(ctx context.Context, in HookLifecycleInput, open lifecycleStoreOpener) (backend.HostResponse, error) {
	if ctx == nil || in.Input == nil || in.Clock == nil || in.Operations == nil || open == nil {
		return backend.HostResponse{}, lifecycleError(pasterrors.CategoryValidation, "The lifecycle ingress boundary is incompletely wired.", "A context, stdin, clock, operation identity source, and store opener are required.", "Nothing was read or recorded.", "Invoke this path through the production lifecycle command.", nil)
	}
	dispatch, err := dispatchLifecycle(in.Harness)
	if err != nil {
		return backend.HostResponse{}, err
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
	// The committed per-harness manifest governs admission unless the caller
	// injects an activation configuration. Production callers leave in.Activations
	// nil, so Codex stays default-off until its committed activation lands; the
	// pre-activation production proof injects an enabled manifest to exercise this
	// exact durable path with no separate code path.
	activations := dispatch.activations
	if in.Activations != nil {
		activations = in.Activations
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
	if capture.disposition != model.CaptureValid {
		_, err = service.Receive(ctx, capture.delivery)
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
	derivation, err := middleend.Derive(l2)
	if err != nil {
		return backend.HostResponse{}, err
	}
	_, err = service.Receive(ctx, capture.delivery, derivation.Effects()...)
	if err != nil {
		return backend.HostResponse{}, err
	}
	return derivation.Response(), nil
}

func dispatchLifecycle(harness ir.HarnessID) (lifecycleDispatch, error) {
	switch harness {
	case ir.HarnessClaudeCode:
		activations, err := activation.ClaudeCode2_1_210()
		if err != nil {
			return lifecycleDispatch{}, fmt.Errorf("dispatch Claude lifecycle activation: %w", err)
		}
		return lifecycleDispatch{name: "Claude", manifest: registration.ClaudeCode2_1_210(), activations: activations, parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := claudeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		}, bind: claudefrontend.Bind}, nil
	case ir.HarnessOpenCode:
		activations, err := activation.OpenCode1_18_10()
		if err != nil {
			return lifecycleDispatch{}, fmt.Errorf("dispatch OpenCode lifecycle activation: %w", err)
		}
		return lifecycleDispatch{name: "OpenCode", manifest: registration.OpenCode1_18_10(), activations: activations, parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := opencodeingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		}, bind: opencodefrontend.Bind}, nil
	case ir.HarnessCodex:
		activations, err := activation.Codex0_146_0()
		if err != nil {
			return lifecycleDispatch{}, fmt.Errorf("dispatch Codex lifecycle activation: %w", err)
		}
		return lifecycleDispatch{name: "Codex", manifest: registration.Codex0_146_0(), activations: activations, parse: func(raw []byte, event registration.Event, version string) lifecycleCapture {
			capture := codexingress.Parse(raw, event, version, model.OccurrenceEnvelopeRef{})
			return lifecycleCapture{disposition: capture.Disposition, delivery: capture.Delivery}
		}, bind: codexfrontend.Bind}, nil
	default:
		return lifecycleDispatch{}, lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Harness %q is not supported by lifecycle ingress.", harness), "Lifecycle ingress has no static provider dispatch for this harness.", "The input was not read and no database was opened.", "Use a harness present in the generated lifecycle support report.", nil)
	}
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
