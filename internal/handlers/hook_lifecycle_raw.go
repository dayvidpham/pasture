package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
)

const hookLifecycleRawWhere = "Ingesting a raw lifecycle event (internal/handlers/hook_lifecycle_raw.go in handlers.HookLifecycleRaw)."

// RawSchemaVersion is the wire-level schema identity of a raw lifecycle
// payload (URD R4.1, UAT-Q2). The decoder is pinned to the build: the
// identity selects the exact generated registration the payload conforms to,
// and an unknown value is a typed refusal (never a silent default decode).
//
// Values are the exact runtime contract spellings of the three registered,
// verified frontends (harness/version), mirroring what
// ir.NewRuntimeContractID(harness, manifest.Version) yields for each row. The
// closed set is pinned by tests against the generated registrations, so the
// enum cannot drift from the builds it names.
type RawSchemaVersion string

const (
	// RawSchemaClaudeCode2_1_261 is the wire identity of the Claude Code
	// 2.1.261 payload schema pinned in this build.
	RawSchemaClaudeCode2_1_261 RawSchemaVersion = "claude-code/2.1.261"
	// RawSchemaOpenCode1_18_29 is the wire identity of the OpenCode 1.18.29
	// payload schema pinned in this build.
	RawSchemaOpenCode1_18_29 RawSchemaVersion = "opencode/1.18.29"
	// RawSchemaCodex0_153_0 is the wire identity of the Codex 0.153.0 payload
	// schema pinned in this build.
	RawSchemaCodex0_153_0 RawSchemaVersion = "codex/0.153.0"
)

// IsValid reports whether the schema identity names a version pinned in this
// build.
func (v RawSchemaVersion) IsValid() bool {
	switch v {
	case RawSchemaClaudeCode2_1_261, RawSchemaOpenCode1_18_29, RawSchemaCodex0_153_0:
		return true
	default:
		return false
	}
}

// String returns the canonical wire spelling.
func (v RawSchemaVersion) String() string { return string(v) }

// ParseRawSchemaVersion validates a user-supplied --schema-version value
// against the closed set of wire identities pinned in this build. The
// returned error is actionable: it names the offending value and the set of
// identities this build decodes.
func ParseRawSchemaVersion(value string) (RawSchemaVersion, error) {
	candidate := RawSchemaVersion(strings.TrimSpace(value))
	if !candidate.IsValid() {
		return "", fmt.Errorf("wire schema %q is not known to this build of pasture; supply one of %q, %q, or %q",
			value, RawSchemaClaudeCode2_1_261, RawSchemaOpenCode1_18_29, RawSchemaCodex0_153_0)
	}
	return candidate, nil
}

// HookLifecycleRawInput mirrors HookLifecycleInput for the raw ingestion
// hatch: the same coordinates (db path, harness, event, host version, stdin)
// plus the explicitly typed wire-level schema identity. It is a fresh type so
// the raw surface can never be reduced accidentally by callers of the native
// entrypoint.
type HookLifecycleRawInput struct {
	DBPath        string
	Harness       ir.HarnessID
	Event         string
	HostVersion   string
	SchemaVersion RawSchemaVersion
	// DryRun runs the admission, verification, and L1→L2 derivation chain
	// and reports what would be committed without opening the store or
	// writing any durable receipt (UAT FIX-NOW SLICE-5). Invalid input
	// refuses identically to the committing path.
	DryRun bool
	Input  io.Reader
	// Clock and Operations are commit dependencies. The production CLI wires
	// them in both modes so one input contract governs commit and preview;
	// dry-run requires them for that contract parity but does not read them.
	Clock       receipt.Clock
	Operations  receipt.OperationIDSource
	Activations []activation.Entry
}

// rawSchemaVersionFor returns the wire schema identity pinned to the given
// harness by this build's generated registration. The identity is derived, not
// hand-maintained: it is ir.NewRuntimeContractID(harness, version) rendered
// canonically, so a build can never advertise a schema identity its own
// registrations do not decode.
func rawSchemaVersionFor(harness ir.HarnessID) RawSchemaVersion {
	var version string
	switch harness {
	case ir.HarnessClaudeCode:
		version = registration.ClaudeCode2_1_261().Version
	case ir.HarnessOpenCode:
		version = registration.OpenCode1_18_29().Version
	case ir.HarnessCodex:
		version = registration.Codex0_153_0().Version
	default:
		return ""
	}
	contract, err := ir.NewRuntimeContractID(harness, version)
	if err != nil {
		return ""
	}
	return RawSchemaVersion(contract.String())
}

// rawAcknowledge is deleted by design: the raw hatch emits the SAME native
// continuation bytes as the native path, and they come from ONE source of
// truth — the registry encoder seam (nativeresponse.CanonicalProceed /
// CodexContinuation) applied to the derivation the shared commit tail already
// produced. A second hardcoded spelling here would drift (it previously
// printed a proceed object for EVERY event, while native prints nothing for
// Claude/OpenCode observations and {} for Codex observations).

// HookLifecycleRaw admits a raw lifecycle payload through the SAME gate path
// as native (registry dispatch → activation gate → bounded read → parse →
// the shared deliveryCommit tail: bind → NewEvent → Derive → Receive),
// stamps the occurrence with the raw origin, and only on the nil-error path
// returns the native continuation bytes the harness would have read for the
// SAME event via the registry encoder seam (commit-before-stdout, mirroring
// HookLifecycleNative). It differs from native in exactly two ways:
//
//   - the wire schema identity is part of the ingress contract: unknown
//     values refuse with the pinned-identity diagnostic before ANY read, and
//     the identity must describe the selected harness's own registration;
//   - the UAT-Q1 "reject outright" posture: a raw capture that does not
//     classify as a valid capture refuses with a typed CategoryValidation
//     error instead of recording a CaptureDisposition evidence row.
//
// Every refusal — unknown harness, unknown or mismatched wire schema, missing
// host version, unknown event, withheld event, over-limit stdin, and
// malformed stdin — happens BEFORE the store opens, preserving the M1 §8
// property that an invalid invocation creates no database file (nor -wal/-
// shm sidecar). The store opens only after the capture classifies valid.
func HookLifecycleRaw(ctx context.Context, in HookLifecycleRawInput) ([]byte, error) {
	if ctx == nil || in.Input == nil || in.Clock == nil || in.Operations == nil {
		return nil, rawLifecycleError(pasterrors.CategoryValidation, "The raw lifecycle ingress boundary is incompletely wired.", "A context, stdin, clock, operation identity source, and store opener are required.", "Nothing was read or recorded.", "Invoke this path through the production raw lifecycle command.", nil)
	}
	if !in.SchemaVersion.IsValid() {
		if parsed, err := ParseRawSchemaVersion(string(in.SchemaVersion)); err != nil {
			return nil, rawLifecycleError(pasterrors.CategoryValidation, err.Error(), "The wire schema is the versioned decoder identity pinned to this build; only the closed set can be decoded.", "The input was not read and no database was opened.", "Pass one of the wire schema identities listed in the diagnostic.", nil)
		} else {
			in.SchemaVersion = parsed
		}
	}
	dispatch, err := dispatchLifecycle(in.Harness)
	if err != nil {
		return nil, err
	}
	if in.SchemaVersion != rawSchemaVersionFor(in.Harness) {
		return nil, rawLifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Wire schema %q does not describe the %s registration pinned to this build (%q).", in.SchemaVersion, dispatch.name, rawSchemaVersionFor(in.Harness)), "The wire identities are pinned one-to-one to each harness registration; a mismatched pairing cannot be decoded.", "The input was not read and no database was opened.", "Pass the wire schema that names the harness's own registration.", nil)
	}
	// The activation catalog is a fallible generated proof; resolve it here at
	// dispatch time (the same point the native flow resolves it), preserving
	// the wrapped error text verbatim.
	activations, err := dispatch.activations()
	if err != nil {
		return nil, fmt.Errorf("dispatch %s raw lifecycle activation: %w", dispatch.name, err)
	}
	if in.Activations != nil {
		activations = in.Activations
	}
	if strings.TrimSpace(in.HostVersion) == "" {
		return nil, rawLifecycleError(pasterrors.CategoryValidation, "The observed host version is missing.", "Every retained occurrence records which host version produced it, without using the value as an admission check.", "The input was not read and no database was opened.", "Pass the observed version through --host-version.", nil)
	}
	var event registration.Event
	for _, candidate := range dispatch.manifest.Events {
		if candidate.NativeName == in.Event {
			event = candidate
			break
		}
	}
	if event.Kind == 0 {
		return nil, rawLifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("Event %q is not in the generated %s registration.", in.Event, dispatch.name), "Ingress trusts the generated registration coordinate rather than an unparsed payload claim.", "The input was not read and no database was opened.", "Invoke one of the events present in the support report.", nil)
	}
	state, found := activationFor(event.Kind, activations)
	if !found || state.State != activation.Enabled {
		reason := activation.WithheldMissingFixture
		if found {
			reason = state.Reason
		}
		return nil, rawLifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("%s event %q is withheld (reason %s).", dispatch.name, event.NativeName, reason.String()), "Only events with authentic capture evidence and a passing production proof are admitted; raw ingestion admits the same closed set.", "The input was not read and no database was opened.", "Inspect the generated activation support report and enable the event only after its proof passes.", nil)
	}
	raw, err := io.ReadAll(io.LimitReader(in.Input, model.MaxNativePayloadBytes+1))
	if err != nil {
		return nil, rawLifecycleError(pasterrors.CategoryValidation, "The raw payload could not be read.", "Standard input failed during the bounded read, the raw handler never truncates retained bytes.", "No database was opened.", "Retry with a readable complete payload.", err)
	}
	if len(raw) > model.MaxNativePayloadBytes {
		return nil, rawLifecycleError(pasterrors.CategoryValidation, fmt.Sprintf("The raw payload exceeds the %d-byte bound.", model.MaxNativePayloadBytes), "The raw input gate never truncates retained evidence: the same 1 MiB bound the native ingress enforces.", "No database was opened.", "Reduce the raw payload below the static bound.", nil)
	}
	capture := dispatch.rawParse(raw, event, in.HostVersion)
	if capture.disposition != model.CaptureValid {
		return nil, rawDispositionRefusal(dispatch.name, capture.disposition, in.SchemaVersion)
	}
	// Dry-run: report what WOULD be committed without opening the store
	// (UAT FIX-NOW SLICE-5). The admission and classification chain above has
	// already run identically to the committing path — the L1→L2 derivation
	// tail (deliveryVerify: warrant → bind → NewEvent → Derive) is the same
	// pure sequence deliveryCommit runs, so the preview is exactly the
	// material a real commit would persist, minus any I/O. No database file
	// is created, opened, or written (M1 §8), and no receipt is issued.
	if in.DryRun {
		preview, previewErr := rawDryRunPreview(dispatch, event, in.HostVersion, in.SchemaVersion, capture.delivery)
		if previewErr != nil {
			return nil, previewErr
		}
		return preview, nil
	}
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return nil, err
	}
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptService(tracker, in.Clock, in.Operations)
	if err != nil {
		return nil, err
	}
	// The raw surface converges on the SAME verification-and-commit tail as
	// native (deliveryCommit): deliveryVerify (warrant → bind → NewEvent →
	// Derive, pure) → EnsureActiveMetamodel → Receive. There is no second
	// copy of the sequence here, so gate/metamodel/metadata parity cannot
	// drift.
	response, err := deliveryCommit(ctx, service, dispatch, event, capture.delivery)
	if err != nil {
		return nil, err
	}
	// The continuation bytes come from the registry encoder seam exactly as
	// for native: a gate consultation emits the canonical decision, a
	// Claude/OpenCode observation emits nothing, and a Codex observation emits
	// {} — per-event byte parity with native, with no raw-specific reply
	// shape.
	native, err := dispatch.encode(response)
	if err != nil {
		return nil, fmt.Errorf("%s lifecycle receipt committed but native continuation was not delivered (encode failed): %w", dispatch.name, err)
	}
	return native, nil
}

// rawDryRunPreview renders what a real raw ingestion WOULD commit, without
// opening the store or issuing a receipt (UAT FIX-NOW SLICE-5). It reuses the
// exact pure L1→L2 derivation tail the committing path runs (deliveryVerify),
// so the preview is byte-faithful to the commit's binding material and its
// canonical continuation — minus any durable write.
func rawDryRunPreview(dispatch lifecycleDispatch, event registration.Event, hostVersion string, schema RawSchemaVersion, delivery receipt.Delivery) ([]byte, error) {
	_, derivation, err := deliveryVerify(dispatch, event, delivery)
	if err != nil {
		return nil, err
	}
	continuation, err := dispatch.encode(derivation.Response())
	if err != nil {
		return nil, fmt.Errorf("%s dry-run continuation could not be rendered (encode failed): %w", dispatch.name, err)
	}
	effects, err := receipt.CanonicalizeLifecycleEffects(derivation.Effects())
	if err != nil {
		return nil, rawLifecycleError(
			pasterrors.CategoryValidation,
			"The dry-run derivation could not cross the canonical journal boundary.",
			"Preview effects pass through the same canonicalization and consultation rebinding as a real receipt; the derived effect shape was incompatible.",
			"No database was opened and no receipt was written.",
			"Report the incompatible derived effect and retry only after correcting the lifecycle implementation.",
			err,
		)
	}
	effectViews := make([]rawDryRunEffectView, 0, len(effects))
	for _, effect := range effects {
		effectViews = append(effectViews, rawDryRunEffectView{
			Sort:          effect.Sort.String(),
			ResultSlot:    string(effect.ResultSlot),
			EvidenceKind:  string(effect.EvidenceKind),
			ContentDigest: hex.EncodeToString(effect.ContentDigest),
			Payload:       append(json.RawMessage(nil), effect.Payload...),
		})
	}
	preview := rawDryRunView{
		DryRun:        true,
		Harness:       dispatch.name,
		Event:         event.NativeName,
		HostVersion:   hostVersion,
		SchemaVersion: schema.String(),
		Origin:        string(delivery.Origin),
		Contract:      delivery.Contract.String(),
		Effects:       effectViews,
		Continuation:  string(continuation),
	}
	out, err := json.MarshalIndent(preview, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("raw dry-run preview could not be rendered: %w", err)
	}
	return append(out, '\n'), nil
}

// rawDryRunView is the stable JSON shape of the dry-run preview.
type rawDryRunView struct {
	DryRun        bool                  `json:"dryRun"`
	Harness       string                `json:"harness"`
	Event         string                `json:"event"`
	HostVersion   string                `json:"hostVersion"`
	SchemaVersion string                `json:"schemaVersion"`
	Origin        string                `json:"origin"`
	Contract      string                `json:"contract"`
	Effects       []rawDryRunEffectView `json:"effects"`
	Continuation  string                `json:"continuation"`
}

// rawDryRunEffectView is the stable evidence representation passed to the
// durable receipt service on a real commit. Payload stays as JSON rather than
// base64 so operators can inspect the exact interpreted/consultation material.
type rawDryRunEffectView struct {
	Sort          string          `json:"sort"`
	ResultSlot    string          `json:"resultSlot"`
	EvidenceKind  string          `json:"evidenceKind"`
	ContentDigest string          `json:"contentDigest"`
	Payload       json.RawMessage `json:"payload"`
}

// rawDispositionRefusal renders the typed refusal for a raw capture that did
// NOT classify valid (UAT-Q1 "reject outright"). Each disposition names its
// own diagnosis so the operator can act on the exact failure class.
func rawDispositionRefusal(name string, disposition model.CaptureDisposition, schema RawSchemaVersion) error {
	var what string
	switch disposition {
	case model.CaptureMalformed:
		what = fmt.Sprintf("The raw payload is not a JSON object the %s classifier can capture for wire identity %q.", name, schema)
	case model.CaptureInvalidUTF8:
		what = fmt.Sprintf("The raw payload is not valid UTF-8 for the %s classifier (wire identity %q).", name, schema)
	case model.CaptureDuplicateField:
		what = fmt.Sprintf("The raw payload repeats an object member; the %s classifier rejects duplicate fields (wire identity %q).", name, schema)
	case model.CaptureUnsupportedSchema:
		what = fmt.Sprintf("The raw payload references fields the %s registration does not support (wire identity %q).", name, schema)
	case model.CaptureEventMismatch:
		what = fmt.Sprintf("The raw payload's own event identity does not match the invoked %s event (wire identity %q).", name, schema)
	default:
		what = fmt.Sprintf("The raw payload was not captured as a valid event by the %s classifier (wire identity %q).", name, schema)
	}
	return rawLifecycleError(pasterrors.CategoryValidation, what, "Raw ingestion takes the reject-outright posture: a capture that does not classify valid is refused, never recorded as an evidence row.", "The input was not read and no database was opened.", "Submit a raw payload that classifies as a valid capture of the pinned wire schema.", nil)
}

// rawLifecycleError is the raw-surface error constructor; it carries the raw
// ingress location in the Where field so diagnostics name the exact file.
func rawLifecycleError(category pasterrors.Category, what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: category, What: what, Why: why, Where: hookLifecycleRawWhere, Impact: impact, Fix: fix, Cause: cause}
}
