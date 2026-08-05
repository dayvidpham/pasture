package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// State is the closed progressive-activation state of one native event.
type State uint8

const (
	StateInvalid State = iota
	Enabled
	Withheld
)

func (s State) IsValid() bool { return s == Enabled || s == Withheld }

func (s State) String() string {
	switch s {
	case Enabled:
		return "enabled"
	case Withheld:
		return "withheld"
	default:
		return ""
	}
}

// WithheldReason explains why an event is not registered with its host.
type WithheldReason uint8

const (
	WithheldReasonInvalid WithheldReason = iota
	WithheldMissingFixture
	WithheldOutsideTargetSet
	WithheldUnverifiedBuild
	WithheldProductionProofMissing
	WithheldMissingRequestCorrelation
)

func (r WithheldReason) IsValid() bool {
	return r >= WithheldMissingFixture && r <= WithheldMissingRequestCorrelation
}

func (r WithheldReason) String() string {
	switch r {
	case WithheldMissingFixture:
		return "missing-fixture"
	case WithheldOutsideTargetSet:
		return "outside-target-set"
	case WithheldUnverifiedBuild:
		return "unverified-build"
	case WithheldProductionProofMissing:
		return "production-proof-missing"
	case WithheldMissingRequestCorrelation:
		return "missing-request-correlation"
	default:
		return ""
	}
}

// CaptureProof is a closed, event-bound proof that a reviewed native capture
// exists for one exact host contract. The zero value carries no proof.
type CaptureProof uint8

const (
	CaptureProofInvalid CaptureProof = iota
	CaptureProofSessionStart
	CaptureProofSessionEnd
	CaptureProofPreToolUse
	CaptureProofPostToolUse
	CaptureProofPostToolUseFailure
	CaptureProofPostToolBatch
	CaptureProofPreCompact
	CaptureProofPostCompact
	CaptureProofOpenCodeSessionCreated
	CaptureProofOpenCodeToolExecuteBefore
)

func (p CaptureProof) IsValid() bool {
	switch p {
	case CaptureProofSessionStart, CaptureProofSessionEnd, CaptureProofPreToolUse,
		CaptureProofPostToolUse, CaptureProofPostToolUseFailure, CaptureProofPostToolBatch,
		CaptureProofPreCompact, CaptureProofPostCompact,
		CaptureProofOpenCodeSessionCreated, CaptureProofOpenCodeToolExecuteBefore:
		return true
	default:
		return false
	}
}

func (p CaptureProof) Event() (model.ContractEventKind, bool) {
	switch p {
	case CaptureProofSessionStart:
		return registration.EventSessionStart, true
	case CaptureProofSessionEnd:
		return registration.EventSessionEnd, true
	case CaptureProofPreToolUse:
		return registration.EventPreToolUse, true
	case CaptureProofPostToolUse:
		return registration.EventPostToolUse, true
	case CaptureProofPostToolUseFailure:
		return registration.EventPostToolUseFailure, true
	case CaptureProofPostToolBatch:
		return registration.EventPostToolBatch, true
	case CaptureProofPreCompact:
		return registration.EventPreCompact, true
	case CaptureProofPostCompact:
		return registration.EventPostCompact, true
	case CaptureProofOpenCodeSessionCreated:
		return registration.EventOpenCodeSessionCreated, true
	case CaptureProofOpenCodeToolExecuteBefore:
		return registration.EventOpenCodeToolExecuteBefore, true
	default:
		return 0, false
	}
}

func (p CaptureProof) Name() string {
	switch p {
	case CaptureProofSessionStart:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofSessionEnd:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/session_end_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofPreToolUse:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofPostToolUse:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofPostToolUseFailure:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_failure_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofPostToolBatch:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_batch_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofPreCompact:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/pre_compact_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofPostCompact:
		return "internal/lifecycle/ingress/claude/testdata/fixtures/post_compact_2_1_222.json (Claude Code 2.1.222 authentic capture)"
	case CaptureProofOpenCodeSessionCreated:
		return "internal/lifecycle/ingress/opencode/testdata/fixtures/session_created_1_18_10.capture.json (OpenCode 1.18.10 authentic callback-object capture)"
	case CaptureProofOpenCodeToolExecuteBefore:
		return "internal/lifecycle/ingress/opencode/testdata/fixtures/tool_execute_before_1_18_10.capture.json (OpenCode 1.18.10 authentic callback-object capture)"
	default:
		return ""
	}
}

// ProductionProof is a closed, event-bound proof that the shipped production
// path has admitted one native event. The zero value carries no proof.
type ProductionProof uint8

const (
	ProductionProofInvalid ProductionProof = iota
	ProductionProofSessionStart
	ProductionProofSessionEnd
	ProductionProofPreToolUse
	ProductionProofPostToolUse
	ProductionProofPostToolUseFailure
	ProductionProofPostToolBatch
	ProductionProofPreCompact
	ProductionProofPostCompact
	ProductionProofOpenCodeSessionCreated
	ProductionProofOpenCodeToolExecuteBefore
)

func (p ProductionProof) IsValid() bool {
	switch p {
	case ProductionProofSessionStart, ProductionProofSessionEnd, ProductionProofPreToolUse,
		ProductionProofPostToolUse, ProductionProofPostToolUseFailure, ProductionProofPostToolBatch,
		ProductionProofPreCompact, ProductionProofPostCompact,
		ProductionProofOpenCodeSessionCreated, ProductionProofOpenCodeToolExecuteBefore:
		return true
	default:
		return false
	}
}

func (p ProductionProof) Event() (model.ContractEventKind, bool) {
	switch p {
	case ProductionProofSessionStart:
		return registration.EventSessionStart, true
	case ProductionProofSessionEnd:
		return registration.EventSessionEnd, true
	case ProductionProofPreToolUse:
		return registration.EventPreToolUse, true
	case ProductionProofPostToolUse:
		return registration.EventPostToolUse, true
	case ProductionProofPostToolUseFailure:
		return registration.EventPostToolUseFailure, true
	case ProductionProofPostToolBatch:
		return registration.EventPostToolBatch, true
	case ProductionProofPreCompact:
		return registration.EventPreCompact, true
	case ProductionProofPostCompact:
		return registration.EventPostCompact, true
	case ProductionProofOpenCodeSessionCreated:
		return registration.EventOpenCodeSessionCreated, true
	case ProductionProofOpenCodeToolExecuteBefore:
		return registration.EventOpenCodeToolExecuteBefore, true
	default:
		return 0, false
	}
}

func (p ProductionProof) Name() string {
	switch p {
	case ProductionProofSessionStart:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/SessionStart"
	case ProductionProofSessionEnd:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/SessionEnd"
	case ProductionProofPreToolUse:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PreToolUse"
	case ProductionProofPostToolUse:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolUse"
	case ProductionProofPostToolUseFailure:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolUseFailure"
	case ProductionProofPostToolBatch:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolBatch"
	case ProductionProofPreCompact:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PreCompact"
	case ProductionProofPostCompact:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostCompact"
	case ProductionProofOpenCodeSessionCreated:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/session.created"
	case ProductionProofOpenCodeToolExecuteBefore:
		return "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/tool.execute.before"
	default:
		return ""
	}
}

// Entry is one complete activation decision. Withheld entries always carry a
// reason and zero proofs; enabled entries carry both event-bound proofs and a
// zero reason.
type Entry struct {
	Event           model.ContractEventKind
	State           State
	Reason          WithheldReason
	CaptureProof    CaptureProof
	ProductionProof ProductionProof
}

func (e Entry) IsValid() bool {
	if e.Event == 0 {
		return false
	}
	switch e.State {
	case Enabled:
		captureEvent, captureOK := e.CaptureProof.Event()
		productionEvent, productionOK := e.ProductionProof.Event()
		return e.Reason == 0 && captureOK && productionOK &&
			captureEvent == e.Event && productionEvent == e.Event
	case Withheld:
		return e.Reason.IsValid() && e.CaptureProof == 0 && e.ProductionProof == 0
	default:
		return false
	}
}

func NewEnabled(event model.ContractEventKind, capture CaptureProof, production ProductionProof) (Entry, error) {
	if event == 0 {
		return Entry{}, fmt.Errorf("activation.NewEnabled: event kind is zero; a generated event ordinal is required before registration; select an event from the generated manifest")
	}
	if !capture.IsValid() {
		return Entry{}, fmt.Errorf("activation.NewEnabled: event %d has no recognized event-bound authentic capture proof; use a named proof produced for the requested generated event", event)
	}
	captureEvent, _ := capture.Event()
	if captureEvent != event {
		return Entry{}, fmt.Errorf("activation.NewEnabled: capture proof %d is bound to event %d, not requested event %d; use the proof for the same generated event", capture, captureEvent, event)
	}
	if !production.IsValid() {
		return Entry{}, fmt.Errorf("activation.NewEnabled: event %d has no recognized event-bound production proof; run the shipped production-path proof for the requested event", event)
	}
	productionEvent, _ := production.Event()
	if productionEvent != event {
		return Entry{}, fmt.Errorf("activation.NewEnabled: production proof %d is bound to event %d, not requested event %d; use the proof for the same generated event", production, productionEvent, event)
	}
	entry := Entry{Event: event, State: Enabled, CaptureProof: capture, ProductionProof: production}
	return entry, nil
}

func NewWithheld(event model.ContractEventKind, reason WithheldReason) (Entry, error) {
	if event == 0 || !reason.IsValid() {
		return Entry{}, fmt.Errorf("activation.NewWithheld: event %d or reason %q is invalid; support reporting requires a generated event ordinal and one declared typed withholding reason", event, reason.String())
	}
	return Entry{Event: event, State: Withheld, Reason: reason}, nil
}
