package handlers

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
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
}

type lifecycleStoreOpener func(string) (protocol.TaskTracker, error)

func HookLifecycle(ctx context.Context, in HookLifecycleInput) error {
	activations, err := activation.ClaudeCode2_1_210()
	if err != nil {
		return err
	}
	return hookLifecycle(ctx, in, activations, tasks.OpenTaskTracker)
}

func hookLifecycle(ctx context.Context, in HookLifecycleInput, activations []activation.Entry, open lifecycleStoreOpener) error {
	if ctx == nil || in.Input == nil || in.Clock == nil || in.Operations == nil || open == nil {
		return lifecycleError(pasterrors.CategoryValidation, "The lifecycle ingress boundary is incompletely wired.", "A context, stdin, clock, operation identity source, and store opener are required.", "Nothing was read or recorded.", "Invoke this path through the production lifecycle command.", nil)
	}
	if in.Harness != ir.HarnessClaudeCode {
		return lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Harness %q is not supported by lifecycle ingress.", in.Harness), "The current generated registration serves only Claude Code.", "The input was not read and no database was opened.", "Use the generated Claude Code registration.", nil)
	}
	if strings.TrimSpace(in.HostVersion) == "" {
		return lifecycleError(pasterrors.CategoryValidation, "The observed host version is missing.", "Every retained occurrence records which host version produced it, without using the value as an admission check.", "The input was not read and no database was opened.", "Pass the observed version through --host-version.", nil)
	}
	manifest := registration.ClaudeCode2_1_210()
	var event registration.Event
	for _, candidate := range manifest.Events {
		if candidate.NativeName == in.Event {
			event = candidate
			break
		}
	}
	if event.Kind == 0 {
		return lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Event %q is not in the generated Claude registration.", in.Event), "Ingress trusts the generated registration coordinate rather than an unparsed payload claim.", "The input was not read and no database was opened.", "Invoke one of the events present in the support report.", nil)
	}
	state, found := activationFor(event.Kind, activations)
	if !found || state.State != activation.Enabled {
		reason := activation.WithheldMissingFixture
		if found {
			reason = state.Reason
		}
		return lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Claude event %q is withheld (reason %d).", event.NativeName, reason), "Only events with authentic capture evidence and a passing production proof are registered.", "The input was not read and no database was opened.", "Inspect the generated activation support report and enable the event only after its proof passes.", nil)
	}
	raw, err := io.ReadAll(io.LimitReader(in.Input, model.MaxNativePayloadBytes+1))
	if err != nil {
		return lifecycleError(pasterrors.CategoryValidation, "The native payload could not be read.", "Standard input failed during the bounded read.", "No database was opened.", "Retry with a readable complete payload.", err)
	}
	if len(raw) > model.MaxNativePayloadBytes {
		return lifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("The native payload exceeds the %d-byte bound.", model.MaxNativePayloadBytes), "Ingress never truncates retained evidence.", "No database was opened.", "Reduce the host payload below the static bound.", nil)
	}
	capture := claudeingress.Parse(raw, event, in.HostVersion, model.OccurrenceEnvelopeRef{})
	tracker, err := open(in.DBPath)
	if err != nil {
		return err
	}
	if tracker == nil {
		return lifecycleError(pasterrors.CategoryStorage, "The lifecycle store opener returned no store.", "A receipt cannot be appended without the unified tracker.", "Nothing was recorded.", "Use tasks.OpenTaskTracker.", nil)
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptService(tracker, in.Clock, in.Operations)
	if err != nil {
		return err
	}
	_, err = service.Receive(ctx, capture.Delivery)
	return err
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
