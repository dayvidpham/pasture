package codegen

// activation_report.go holds the shared committed-activation audit report shape.
//
// A harness that publishes an activation audit report emits one exhaustive
// report per pinned contract: every generated event appears exactly once,
// carrying either the withholding reason (Withheld) or the pair of event-bound
// capture and production proofs (Enabled). Emission is unconditional — the
// withheld dispositions are the audit payload, not a side effect of enablement.
//
// Two harnesses currently emit this shape:
//   - Claude Code at hooks/pasture-activation.json (emitClaudeHooks)
//   - Codex at .codex/pasture-codex-activation.json (codexManifestEmitter.Emit)
//
// These types are the SHARED row/report structs, reused verbatim across both
// emitters so the audit shape can never drift between harnesses. They are
// intentionally plain data with no builder or cross-harness derivation method:
// each emitter iterates its own pinned registration manifest and activation
// catalog and fills these structs inline (YAGNI — no shared builder, ratified).
// There is deliberately no schema version tag; the pinned contract ID already
// identifies the exact shape.

// activationSupportEntry is one generated event's line in a harness activation
// audit report. For an Enabled event, CaptureProof and ProductionProof name the
// event-bound proofs and Reason is empty; for a Withheld event, Reason names the
// typed withholding reason and both proof fields are empty. The omitempty tags
// keep each disposition's irrelevant fields out of the committed JSON.
type activationSupportEntry struct {
	Event           string `json:"event"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	CaptureProof    string `json:"captureProof,omitempty"`
	ProductionProof string `json:"productionProof,omitempty"`
}

// activationSupportReport is the committed activation audit report for one
// harness contract: the harness identity, the pinned runtime contract ID, and
// one exhaustive entry per generated event in registration order.
type activationSupportReport struct {
	Harness  string                   `json:"harness"`
	Contract string                   `json:"contract"`
	Events   []activationSupportEntry `json:"events"`
}
