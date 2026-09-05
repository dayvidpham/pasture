package activation

//go:generate go run gen.go

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
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
	// WithheldNoReachableTrigger records that no known action makes the host
	// fire the event in a live session, so no authentic capture can exist. It
	// is a user decision: a row that carries it names the CLEARANCE.md where
	// the decision is recorded.
	WithheldNoReachableTrigger
	// WithheldUnclearablePayload records that the captured payload cannot be
	// made safe to commit by the substitution rules. It is a user decision: a
	// row that carries it names the CLEARANCE.md where the decision is recorded.
	WithheldUnclearablePayload
	// WithheldProviderHook records that the host reads the hook's own output
	// or exit code as an answer only the hook can give, so an observer row
	// would change host behaviour rather than only report on it (a Claude
	// worktree-create hook: standard output must carry the created worktree
	// path; a worktree-remove hook: exit 0 asserts the removal happened). It
	// is a user decision: a row that carries it names the CLEARANCE.md where
	// the decision is recorded.
	WithheldProviderHook
	// WithheldNotEmittedByHost records that the host declares the event name
	// or the hook key at the recorded version and never emits the event or
	// calls the key. This covers two kinds of declared-but-silent name: an
	// event name present only in generated SDK types with no publisher, or a
	// declared hook key nothing calls. A row may take this reason only on a
	// CITED absence of an emission site in the host source at the recorded
	// version — the file and symbol searched, with the search stated in the
	// recorded decision — never on the absence of a payload file, a
	// documentation page, or a struct; a directory listing alone can miss a
	// real emitter. It is a user decision: a row that carries it names the
	// CLEARANCE.md where the decision, including that cited search, is
	// recorded.
	WithheldNotEmittedByHost
	// WithheldEmittedOutsideTransport records that the host does emit the
	// event, but on a channel the harness transport does not observe, so no
	// capture can reach the hook however long a session runs. It is a user
	// decision: a row that carries it names the CLEARANCE.md where the
	// decision is recorded.
	WithheldEmittedOutsideTransport
	// WithheldTriggerNotExercised records that the host CAN fire the event,
	// and WE chose not to produce the condition that fires it. Its two limbs
	// are (a) a setting we will not impose on the capturing user's host
	// configuration, and (b) a condition we will not induce (for example a
	// real API failure). It is a user decision: a row that carries it names
	// the CLEARANCE.md where the decision, and which limb applies, is
	// recorded.
	//
	// It does NOT cover a row nobody knows how to fire: that case is
	// WithheldNoReachableTrigger, unchanged by this reason. It does NOT cover
	// a row that was captured under a disclosed non-default configuration:
	// that row is enabled, with the configuration stated beside its fixture.
	WithheldTriggerNotExercised
	// numWithheldReasons is the sentinel that closes the enum. A new arm goes
	// above it, and IsValid and AllWithheldReasons widen with it; String must
	// then name the arm or the enum-sync test turns red naming it.
	numWithheldReasons
)

func (r WithheldReason) IsValid() bool {
	return r >= WithheldMissingFixture && r < numWithheldReasons
}

// RequiresClearance reports whether the reason is a user decision that must
// be recorded in a CLEARANCE.md before a row may carry it.
func (r WithheldReason) RequiresClearance() bool {
	switch r {
	case WithheldNoReachableTrigger, WithheldUnclearablePayload,
		WithheldProviderHook, WithheldNotEmittedByHost, WithheldEmittedOutsideTransport,
		WithheldTriggerNotExercised:
		return true
	default:
		return false
	}
}

// AllWithheldReasons returns every valid arm in ordinal order. The population
// is derived from the sentinel, so an arm added above it is included without
// an edit here.
func AllWithheldReasons() []WithheldReason {
	out := make([]WithheldReason, 0, int(numWithheldReasons)-1)
	for r := WithheldMissingFixture; r < numWithheldReasons; r++ {
		out = append(out, r)
	}
	return out
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
	case WithheldNoReachableTrigger:
		return "no-reachable-trigger"
	case WithheldUnclearablePayload:
		return "unclearable-payload"
	case WithheldProviderHook:
		return "provider-hook"
	case WithheldNotEmittedByHost:
		return "not-emitted-by-host"
	case WithheldEmittedOutsideTransport:
		return "emitted-outside-transport"
	case WithheldTriggerNotExercised:
		return "trigger-not-exercised"
	default:
		return ""
	}
}

// CaptureProof is a closed, event-bound proof that a reviewed native capture
// exists for one exact host contract. The zero value carries no proof.
//
// Its arms are DECLARED per harness in the three target files and GENERATED
// into proofs_{claude,codex,opencode}.gen.go, with one ordinal range per
// harness (Claude Code 1-99, Codex 100-199, OpenCode 200-299) so that the three
// harness files can gain arms independently without an ordinal collision. The
// declaration tables are the source of truth for what a proof MEANS (its event
// and its fixture); the generated constants are the names by which the rest of
// the tree refers to a proof.
type CaptureProof uint16

// ProductionProof is a closed, event-bound proof that the shipped production
// path has admitted one native event. The zero value carries no proof. It is
// declared and generated exactly as CaptureProof is.
type ProductionProof uint16

// captureProofDeclaration is one row of a harness capture-proof table. The
// generator reads ordinal and arm from source text; the package reads event
// and fixture at run time.
type captureProofDeclaration struct {
	ordinal CaptureProof
	arm     string
	event   model.ContractEventKind
	fixture string
}

// productionProofDeclaration is one row of a harness production-proof table.
type productionProofDeclaration struct {
	ordinal ProductionProof
	arm     string
	event   model.ContractEventKind
	test    string
}

// namedCaptureProof and namedProductionProof are the generated arm lists: the
// generated constant of each declared arm, by arm name.
type namedCaptureProof struct {
	name  string
	proof CaptureProof
}

type namedProductionProof struct {
	name  string
	proof ProductionProof
}

// harnessProofTables binds one harness to its declared tables and to the
// generated arm lists of its output file.
type harnessProofTables struct {
	harness             ir.HarnessID
	capture             []captureProofDeclaration
	production          []productionProofDeclaration
	generatedCapture    []namedCaptureProof
	generatedProduction []namedProductionProof
}

// proofTables lists the three harness files in one fixed order. A new harness
// file is added here in the change that reviews it.
func proofTables() []harnessProofTables {
	return []harnessProofTables{
		{ir.HarnessClaudeCode, claudeCaptureProofs[:], claudeProductionProofs[:], claudeGeneratedCaptureProofs, claudeGeneratedProductionProofs},
		{ir.HarnessCodex, codexCaptureProofs[:], codexProductionProofs[:], codexGeneratedCaptureProofs, codexGeneratedProductionProofs},
		{ir.HarnessOpenCode, openCodeCaptureProofs[:], openCodeProductionProofs[:], openCodeGeneratedCaptureProofs, openCodeGeneratedProductionProofs},
	}
}

// CaptureProofArm is one declared capture proof, as the tables declare it.
type CaptureProofArm struct {
	Harness ir.HarnessID
	Arm     string
	Proof   CaptureProof
	Event   model.ContractEventKind
	Fixture string
}

// ProductionProofArm is one declared production proof, as the tables declare it.
type ProductionProofArm struct {
	Harness ir.HarnessID
	Arm     string
	Proof   ProductionProof
	Event   model.ContractEventKind
	Test    string
}

// CaptureProofArms returns every declared capture proof of every harness, in
// harness order then declaration order.
func CaptureProofArms() []CaptureProofArm {
	var out []CaptureProofArm
	for _, table := range proofTables() {
		for _, d := range table.capture {
			out = append(out, CaptureProofArm{Harness: table.harness, Arm: d.arm, Proof: d.ordinal, Event: d.event, Fixture: d.fixture})
		}
	}
	return out
}

// ProductionProofArms returns every declared production proof of every
// harness, in harness order then declaration order.
func ProductionProofArms() []ProductionProofArm {
	var out []ProductionProofArm
	for _, table := range proofTables() {
		for _, d := range table.production {
			out = append(out, ProductionProofArm{Harness: table.harness, Arm: d.arm, Proof: d.ordinal, Event: d.event, Test: d.test})
		}
	}
	return out
}

// GeneratedCaptureProofs returns the generated constant of every capture proof
// arm by arm name, across the three generated files.
func GeneratedCaptureProofs() map[string]CaptureProof {
	out := map[string]CaptureProof{}
	for _, table := range proofTables() {
		for _, g := range table.generatedCapture {
			out[g.name] = g.proof
		}
	}
	return out
}

// GeneratedProductionProofs returns the generated constant of every production
// proof arm by arm name, across the three generated files.
func GeneratedProductionProofs() map[string]ProductionProof {
	out := map[string]ProductionProof{}
	for _, table := range proofTables() {
		for _, g := range table.generatedProduction {
			out[g.name] = g.proof
		}
	}
	return out
}

func captureDeclaration(p CaptureProof) (captureProofDeclaration, ir.HarnessID, bool) {
	if p == 0 {
		return captureProofDeclaration{}, "", false
	}
	for _, table := range proofTables() {
		for _, d := range table.capture {
			if d.ordinal == p {
				return d, table.harness, true
			}
		}
	}
	return captureProofDeclaration{}, "", false
}

func productionDeclaration(p ProductionProof) (productionProofDeclaration, ir.HarnessID, bool) {
	if p == 0 {
		return productionProofDeclaration{}, "", false
	}
	for _, table := range proofTables() {
		for _, d := range table.production {
			if d.ordinal == p {
				return d, table.harness, true
			}
		}
	}
	return productionProofDeclaration{}, "", false
}

func (p CaptureProof) IsValid() bool {
	_, _, ok := captureDeclaration(p)
	return ok
}

// Event returns the generated event the proof is bound to.
func (p CaptureProof) Event() (model.ContractEventKind, bool) {
	d, _, ok := captureDeclaration(p)
	if !ok {
		return 0, false
	}
	return d.event, true
}

// Name cites the committed fixture that is the proof, as "path (description)".
func (p CaptureProof) Name() string {
	d, _, ok := captureDeclaration(p)
	if !ok {
		return ""
	}
	return d.fixture
}

// Harness returns the harness whose target file declares the proof.
func (p CaptureProof) Harness() (ir.HarnessID, bool) {
	_, harness, ok := captureDeclaration(p)
	return harness, ok
}

func (p ProductionProof) IsValid() bool {
	_, _, ok := productionDeclaration(p)
	return ok
}

// Event returns the generated event the proof is bound to.
func (p ProductionProof) Event() (model.ContractEventKind, bool) {
	d, _, ok := productionDeclaration(p)
	if !ok {
		return 0, false
	}
	return d.event, true
}

// Name cites the production test that is the proof, as "file:Test/Subtest".
func (p ProductionProof) Name() string {
	d, _, ok := productionDeclaration(p)
	if !ok {
		return ""
	}
	return d.test
}

// Harness returns the harness whose target file declares the proof.
func (p ProductionProof) Harness() (ir.HarnessID, bool) {
	_, harness, ok := productionDeclaration(p)
	return harness, ok
}

// Entry is one complete activation decision. Withheld entries always carry a
// reason and zero proofs; enabled entries carry both event-bound proofs and a
// zero reason. A withheld entry whose reason is a user decision also carries
// the committed CLEARANCE.md path where that decision is recorded.
type Entry struct {
	Event           model.ContractEventKind
	State           State
	Reason          WithheldReason
	CaptureProof    CaptureProof
	ProductionProof ProductionProof
	Clearance       string
}

func (e Entry) IsValid() bool {
	if e.Event == 0 {
		return false
	}
	switch e.State {
	case Enabled:
		captureEvent, captureOK := e.CaptureProof.Event()
		productionEvent, productionOK := e.ProductionProof.Event()
		return e.Reason == 0 && e.Clearance == "" && captureOK && productionOK &&
			captureEvent == e.Event && productionEvent == e.Event
	case Withheld:
		if !e.Reason.IsValid() || e.CaptureProof != 0 || e.ProductionProof != 0 {
			return false
		}
		if e.Reason.RequiresClearance() {
			return acceptance.ValidateClearancePath(e.Clearance) == nil
		}
		return e.Clearance == ""
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

// NewWithheld builds a withheld entry for a reason that needs no user
// decision. A reason that records a user decision is refused here, because
// the decision's CLEARANCE.md path is part of the entry; use
// NewWithheldByDecision for it.
func NewWithheld(event model.ContractEventKind, reason WithheldReason) (Entry, error) {
	if event == 0 || !reason.IsValid() {
		return Entry{}, fmt.Errorf("activation.NewWithheld: event %d or reason %q is invalid; support reporting requires a generated event ordinal and one declared typed withholding reason", event, reason.String())
	}
	if reason.RequiresClearance() {
		return Entry{}, fmt.Errorf("activation.NewWithheld: event %d reason %q records a user decision and requires the committed CLEARANCE.md path where that decision is recorded; build the entry with NewWithheldByDecision and the clearance path", event, reason.String())
	}
	return Entry{Event: event, State: Withheld, Reason: reason}, nil
}

// NewWithheldByDecision builds a withheld entry for a reason that records a
// user decision (no reachable trigger, unclearable payload, provider hook,
// not emitted by host, emitted outside transport, trigger not exercised). The
// clearance is the committed CLEARANCE.md path that holds the decision; it is
// validated by the same rule a capture sidecar's clearance is. For WithheldNotEmittedByHost
// specifically, the acceptance rule is stricter than an empty-path check: the
// recorded decision at that path must cite the search that found no emission
// site (the file and symbol searched, at the recorded host version), never
// only the absence of a payload file, a documentation page, or a struct; see
// the WithheldNotEmittedByHost doc comment.
func NewWithheldByDecision(event model.ContractEventKind, reason WithheldReason, clearance string) (Entry, error) {
	if event == 0 || !reason.IsValid() {
		return Entry{}, fmt.Errorf("activation.NewWithheldByDecision: event %d or reason %q is invalid; support reporting requires a generated event ordinal and one declared typed withholding reason", event, reason.String())
	}
	if !reason.RequiresClearance() {
		return Entry{}, fmt.Errorf("activation.NewWithheldByDecision: event %d reason %q is not a user decision and carries no clearance path; build the entry with NewWithheld", event, reason.String())
	}
	if err := acceptance.ValidateClearancePath(clearance); err != nil {
		return Entry{}, fmt.Errorf("activation.NewWithheldByDecision: event %d reason %q needs the committed CLEARANCE.md path that records the user decision: %w", event, reason.String(), err)
	}
	return Entry{Event: event, State: Withheld, Reason: reason, Clearance: clearance}, nil
}

// targetEventDeclaration is one row of a harness target table. It uses
// generated event ordinals rather than native-name strings or a second map.
// An enabled row carries both proofs; a withheld row carries its reason, and
// a reason that records a user decision also carries the committed
// CLEARANCE.md path where that decision is recorded.
type targetEventDeclaration struct {
	event           model.ContractEventKind
	captureProof    CaptureProof
	productionProof ProductionProof
	withheldReason  WithheldReason
	clearance       string
}

// targetEvents returns the typed target subset of one table in declaration
// order, as a slice independent of the static table.
func targetEvents(declarations []targetEventDeclaration) []model.ContractEventKind {
	out := make([]model.ContractEventKind, len(declarations))
	for index, declaration := range declarations {
		out[index] = declaration.event
	}
	return out
}

func targetDeclaration(declarations []targetEventDeclaration, event model.ContractEventKind) (targetEventDeclaration, bool) {
	for _, declaration := range declarations {
		if declaration.event == event {
			return declaration, true
		}
	}
	return targetEventDeclaration{}, false
}

// deriveManifest builds a fresh exhaustive activation manifest for one harness
// from its generated registration events and its static target table. It
// performs no filesystem access. Every generated event gets exactly one entry:
// a non-target event is withheld outside the target set; a target row with
// proofs is enabled; a target row without proofs is withheld for its declared
// reason, or missing-fixture when it declares none. A reason that records a
// user decision must name its CLEARANCE.md, and a row that names a clearance
// for a reason that is not a decision is refused, so the report can never
// claim a decision that was not made or hide one that was.
func deriveManifest(where string, events []registration.Event, declarations []targetEventDeclaration) ([]Entry, error) {
	out := make([]Entry, 0, len(events))
	for _, event := range events {
		declaration, target := targetDeclaration(declarations, event.Kind)
		if !target {
			entry, err := NewWithheld(event.Kind, WithheldOutsideTargetSet)
			if err != nil {
				return nil, fmt.Errorf("%s: withhold generated event %q: %w", where, event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		if declaration.captureProof != 0 || declaration.productionProof != 0 {
			if declaration.withheldReason != 0 {
				return nil, fmt.Errorf("%s: target event %q has both proofs and withholding reason %q; choose one static activation state", where, event.NativeName, declaration.withheldReason.String())
			}
			if declaration.clearance != "" {
				return nil, fmt.Errorf("%s: target event %q is enabled by proofs and also names clearance %q; a clearance path belongs only to a withheld reason that records a user decision", where, event.NativeName, declaration.clearance)
			}
			entry, err := NewEnabled(event.Kind, declaration.captureProof, declaration.productionProof)
			if err != nil {
				return nil, fmt.Errorf("%s: enable generated event %q: %w", where, event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		reason := declaration.withheldReason
		if reason == 0 {
			reason = WithheldMissingFixture
		}
		var entry Entry
		var err error
		switch {
		case reason.RequiresClearance():
			entry, err = NewWithheldByDecision(event.Kind, reason, declaration.clearance)
			if err != nil {
				return nil, fmt.Errorf("%s: target event %q withheld as %q records a user decision and must name the committed CLEARANCE.md that holds it: %w", where, event.NativeName, reason.String(), err)
			}
		case declaration.clearance != "":
			return nil, fmt.Errorf("%s: target event %q withheld as %q names clearance %q, but that reason is not a user decision; remove the clearance or use a decision reason", where, event.NativeName, reason.String(), declaration.clearance)
		default:
			entry, err = NewWithheld(event.Kind, reason)
			if err != nil {
				return nil, fmt.Errorf("%s: withhold target event %q: %w", where, event.NativeName, err)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}
