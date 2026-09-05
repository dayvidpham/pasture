package codegen

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// activation_report.go holds the shared committed-activation audit report shape.
//
// A harness that publishes an activation audit report emits one exhaustive
// report per pinned contract: every generated event appears exactly once,
// carrying either the withholding reason (Withheld) or the pair of event-bound
// capture and production proofs (Enabled), plus the two columns every row
// carries whatever its state: the response capability and the failure
// evidence. Emission is unconditional — the withheld dispositions are the
// audit payload, not a side effect of enablement.
//
// Three harnesses emit this shape:
//   - Claude Code at hooks/pasture-activation.json (emitClaudeHooks)
//   - Codex at .codex/pasture-codex-activation.json (codexManifestEmitter.Emit)
//   - OpenCode at .opencode/pasture-opencode-activation.json
//     (openCodeManifestEmitter.Emit)
//
// Every emitter builds its rows through activationSupportEntryFor, so the
// three reports cannot drift from one another in shape or in column meaning.
// There is deliberately no schema version tag; the pinned contract ID already
// identifies the exact shape.

// OpenCodeActivationReportPath is the committed OpenCode activation audit
// report. The OpenCode target manifest keeps its own activation array as
// target data; this file is the audit report in the shared shape.
const OpenCodeActivationReportPath = ".opencode/pasture-opencode-activation.json"

// activationSupportEntry is one generated event's line in a harness activation
// audit report. For an Enabled event, CaptureProof and ProductionProof name the
// event-bound proofs and Reason is empty; for a Withheld event, Reason names the
// typed withholding reason and both proof fields are empty, and a reason that
// records a user decision also names the committed CLEARANCE.md that holds it.
// The omitempty tags keep each disposition's irrelevant fields out of the
// committed JSON. ResponseCapability and FailureEvidence are NEVER omitted:
// their empty string is a value with a meaning (see activationColumnState),
// so a reader must be able to tell it from a missing key.
type activationSupportEntry struct {
	Event              string `json:"event"`
	State              string `json:"state"`
	Reason             string `json:"reason,omitempty"`
	Clearance          string `json:"clearance,omitempty"`
	CaptureProof       string `json:"captureProof,omitempty"`
	ProductionProof    string `json:"productionProof,omitempty"`
	ResponseCapability string `json:"responseCapability"`
	FailureEvidence    string `json:"failureEvidence"`
}

// activationSupportReport is the committed activation audit report for one
// harness contract: the harness identity, the pinned runtime contract ID, and
// one exhaustive entry per generated event in registration order.
type activationSupportReport struct {
	Harness  string                   `json:"harness"`
	Contract string                   `json:"contract"`
	Events   []activationSupportEntry `json:"events"`
}

// activationColumnState is the closed set of states a report column can be
// in. The three states render as three DISTINCT strings, because a reader
// that compares filled values across rows must never mistake a value that
// nobody has derived yet for a value that was derived as nothing.
type activationColumnState uint8

const (
	// activationColumnUnset: no derivation for this column exists yet in this
	// build. It renders the explicit token ActivationColumnUnset, never the
	// empty string.
	activationColumnUnset activationColumnState = iota + 1
	// activationColumnAbsent: the column's source is genuinely absent for this
	// row, so no future build will fill it. It renders the empty string.
	activationColumnAbsent
	// activationColumnValue: a derived, non-empty value.
	activationColumnValue
)

// ActivationColumnUnset is the token a report column carries while no
// derivation for it exists. It is a word and not the empty string on purpose:
// an empty string is reserved for a column whose source is genuinely absent.
const ActivationColumnUnset = "unset"

// renderActivationColumn renders one column state. A value state with an
// empty value is refused, because an empty value would collide with the
// absent state and the two mean different things.
func renderActivationColumn(state activationColumnState, value string) (string, error) {
	switch state {
	case activationColumnUnset:
		return ActivationColumnUnset, nil
	case activationColumnAbsent:
		return "", nil
	case activationColumnValue:
		if value == "" {
			return "", fmt.Errorf("codegen.renderActivationColumn: a derived column value is empty; an empty string is reserved for a genuinely absent source, so render the absent state instead or supply the value")
		}
		if value == ActivationColumnUnset {
			return "", fmt.Errorf("codegen.renderActivationColumn: a derived column value spells the unset token %q; a derivation cannot produce the token that means no derivation exists", ActivationColumnUnset)
		}
		return value, nil
	default:
		return "", fmt.Errorf("codegen.renderActivationColumn: column state %d is not one of unset, absent or value", state)
	}
}

// failureEvidenceColumn derives the evidence column of one row from the
// pinned lifecycle profile. A row the host contract declares NON-BLOCKING
// makes no blocking claim, so its evidence source is genuinely absent (the
// empty string). A blocking row renders its citation when the profile carries
// one and the unset token while its harness has not yet supplied it.
func failureEvidenceColumn(event registration.Event, policy runtime.LifecycleFailurePolicy) (string, error) {
	if event.Blocking == registration.NonBlocking {
		return renderActivationColumn(activationColumnAbsent, "")
	}
	if policy.Evidence.IsPresent() {
		return renderActivationColumn(activationColumnValue, policy.Evidence.Source)
	}
	return renderActivationColumn(activationColumnUnset, "")
}

// responseCapabilityColumn renders the capability column. No build derives a
// response capability yet, so every row carries the unset token; the
// derivation, when it exists, replaces this function's body and nothing else.
func responseCapabilityColumn() (string, error) {
	return renderActivationColumn(activationColumnUnset, "")
}

// activationSupportEntryFor renders one report row for one generated event of
// one harness. It is the single row builder of all three reports.
func activationSupportEntryFor(harness ir.HarnessID, event registration.Event, state activation.Entry) (activationSupportEntry, error) {
	if !state.IsValid() {
		return activationSupportEntry{}, fmt.Errorf("codegen.activationSupportEntryFor: activation entry for event %q is invalid; construct it with activation.NewEnabled, activation.NewWithheld or activation.NewWithheldByDecision", event.NativeName)
	}
	if state.Event != event.Kind {
		return activationSupportEntry{}, fmt.Errorf("codegen.activationSupportEntryFor: activation entry is for event %d, not generated event %q (%d); pair each report row with its own entry", state.Event, event.NativeName, event.Kind)
	}
	policy, ok := runtime.LookupLifecycleFailure(harness, event.NativeName)
	if !ok {
		return activationSupportEntry{}, fmt.Errorf("codegen.activationSupportEntryFor: generated event %q has no pinned lifecycle profile row for harness %q, so its failure evidence cannot be reported; align the registration and the runtime profile against the same pinned contract", event.NativeName, harness)
	}
	evidence, err := failureEvidenceColumn(event, policy)
	if err != nil {
		return activationSupportEntry{}, fmt.Errorf("codegen.activationSupportEntryFor: event %q: %w", event.NativeName, err)
	}
	capability, err := responseCapabilityColumn()
	if err != nil {
		return activationSupportEntry{}, fmt.Errorf("codegen.activationSupportEntryFor: event %q: %w", event.NativeName, err)
	}
	entry := activationSupportEntry{
		Event:              event.NativeName,
		State:              state.State.String(),
		Reason:             state.Reason.String(),
		Clearance:          state.Clearance,
		ResponseCapability: capability,
		FailureEvidence:    evidence,
	}
	if state.State == activation.Enabled {
		entry.CaptureProof = state.CaptureProof.Name()
		entry.ProductionProof = state.ProductionProof.Name()
	}
	return entry, nil
}

// buildActivationSupportReport renders one harness's exhaustive report from
// its generated registration manifest and its activation entries. Every
// generated event must have exactly one entry and every entry must belong to
// a generated event, so a drifted catalog fails generation rather than
// shipping a partial audit.
func buildActivationSupportReport(where string, manifest registration.Manifest, states []activation.Entry) (activationSupportReport, error) {
	stateByKind := make(map[model.ContractEventKind]activation.Entry, len(states))
	for _, state := range states {
		if !state.IsValid() {
			return activationSupportReport{}, fmt.Errorf("%s: activation entry for event %d is invalid; construct it with activation.NewEnabled, activation.NewWithheld or activation.NewWithheldByDecision", where, state.Event)
		}
		if _, duplicate := stateByKind[state.Event]; duplicate {
			return activationSupportReport{}, fmt.Errorf("%s: duplicate activation entry for event %d; provide exactly one decision per generated event", where, state.Event)
		}
		stateByKind[state.Event] = state
	}
	report := activationSupportReport{Harness: string(manifest.Harness), Contract: manifest.Contract.String()}
	for _, event := range manifest.Events {
		state, present := stateByKind[event.Kind]
		if !present {
			return activationSupportReport{}, fmt.Errorf("%s: generated event %q has no activation entry; add one exhaustive typed decision", where, event.NativeName)
		}
		entry, err := activationSupportEntryFor(manifest.Harness, event, state)
		if err != nil {
			return activationSupportReport{}, fmt.Errorf("%s: %w", where, err)
		}
		report.Events = append(report.Events, entry)
	}
	if len(stateByKind) != len(manifest.Events) {
		return activationSupportReport{}, fmt.Errorf("%s: activation has %d entries for %d generated events; remove non-manifest entries and provide one exact decision per event", where, len(stateByKind), len(manifest.Events))
	}
	return report, nil
}
