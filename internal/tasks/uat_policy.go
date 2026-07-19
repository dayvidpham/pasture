// Package tasks — Plan-UAT and Implementation-UAT typed payloads (#49).
//
// uat_policy.go defines the concrete, canonical UAT decision payloads #49 layers on the
// #43 base decision-ledger primitives. Two gates are modeled:
//
//   - Plan UAT: an accept / changes-requested / AFK-deferred verdict over a proposal
//     revision, recorded as one of three typed decision kinds.
//   - Implementation UAT: the single ImplUATPayload — the ONLY codec payload for the
//     implementation-UAT verdict, verbatim interactions, feedback, and carry-forward
//     resolutions. Actor attribution is never stored here: Decider and Recorder come
//     exclusively from the referenced DecisionLedgerEntry.Actor, so attribution cannot
//     disagree after restart.
//
// Every payload is a plain, deterministically-serializable value: no maps, stable field
// order, so its canonical JSON encoding is a pure function of the value and round-trips
// byte-identically through the #43 descriptor machinery.

package tasks

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/provenance"
)

// UATInteraction is one verbatim prompt/response exchange from a UAT session. Both halves
// are preserved exactly so a restart decode reproduces every interaction.
type UATInteraction struct {
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
}

func validateInteractions(field string, xs []UATInteraction) error {
	for i, x := range xs {
		if x.Prompt == "" {
			return uatErr(field, fmt.Sprintf("interaction %d has an empty prompt", i),
				"a verbatim UAT interaction preserves both the prompt and the response",
				"record the exact prompt text for every interaction")
		}
		if x.Response == "" {
			return uatErr(field, fmt.Sprintf("interaction %d has an empty response", i),
				"a verbatim UAT interaction preserves both the prompt and the response, so a recorded interaction must carry both halves",
				"record the exact response text for every interaction, or omit the interaction until a response is available")
		}
	}
	return nil
}

// UATFeedbackID identifies one UAT feedback item so a later gate can resolve or carry it.
type UATFeedbackID string

// UATFeedbackItem is one piece of UAT feedback. FixNow marks blocking (FIX-NOW) feedback,
// which forces a changes-requested verdict and makes an AFK deferral ineligible.
type UATFeedbackItem struct {
	ID     UATFeedbackID `json:"id"`
	Body   string        `json:"body"`
	FixNow bool          `json:"fixNow"`
}

func validateFeedback(field string, xs []UATFeedbackItem) error {
	seen := map[UATFeedbackID]bool{}
	for i, x := range xs {
		if x.ID == "" {
			return uatErr(field, fmt.Sprintf("feedback item %d has an empty id", i),
				"each feedback item carries a stable id so a later gate can resolve or carry it forward",
				"assign every feedback item a non-empty id")
		}
		if seen[x.ID] {
			return uatErr(field, fmt.Sprintf("feedback id %q appears more than once", x.ID),
				"feedback ids identify a single carry-forward target and must be unique",
				"give each feedback item a distinct id")
		}
		seen[x.ID] = true
	}
	return nil
}

// hasFixNowFeedback reports whether any feedback item is blocking (FIX-NOW).
func hasFixNowFeedback(xs []UATFeedbackItem) bool {
	for _, x := range xs {
		if x.FixNow {
			return true
		}
	}
	return false
}

// HeldUATQuestionID identifies one held (unanswered) UAT question.
type HeldUATQuestionID string

// HeldUATQuestion is a question held open during a UAT session. Stable gates only whether
// an AFK Plan deferral is ELIGIBLE to be recorded (at least one held question must be
// stable); it does not filter which held questions are carried forward — RequiredDecisions
// (uat_coverage.go) carries every held question from the deferred Plan decision forward
// regardless of Stable, matching issue #49's literal "every unresolved held question".
type HeldUATQuestion struct {
	ID       HeldUATQuestionID `json:"id"`
	Question string            `json:"question"`
	Stable   bool              `json:"stable"`
}

func validateHeldQuestions(field string, xs []HeldUATQuestion) error {
	seen := map[HeldUATQuestionID]bool{}
	for i, x := range xs {
		if x.ID == "" {
			return uatErr(field, fmt.Sprintf("held question %d has an empty id", i),
				"each held question carries a stable id so a later gate can resolve it",
				"assign every held question a non-empty id")
		}
		if seen[x.ID] {
			return uatErr(field, fmt.Sprintf("held question id %q appears more than once", x.ID),
				"held-question ids identify a single carry-forward target and must be unique",
				"give each held question a distinct id")
		}
		seen[x.ID] = true
	}
	return nil
}

func hasStableHeldQuestion(xs []HeldUATQuestion) bool {
	for _, x := range xs {
		if x.Stable {
			return true
		}
	}
	return false
}

// PlanUATVerdict is the closed set of Plan-UAT dispositions.
type PlanUATVerdict int

const (
	planUATVerdictInvalid PlanUATVerdict = iota
	// PlanUATAccepted ratifies the proposal revision.
	PlanUATAccepted
	// PlanUATChangesRequested returns the proposal for revision.
	PlanUATChangesRequested
	// PlanUATDeferredByAFK is an agent-attributed AFK deferral held for later ratification.
	PlanUATDeferredByAFK
)

func (v PlanUATVerdict) valid() bool {
	return v == PlanUATAccepted || v == PlanUATChangesRequested || v == PlanUATDeferredByAFK
}

func (v PlanUATVerdict) String() string {
	switch v {
	case PlanUATAccepted:
		return "accepted"
	case PlanUATChangesRequested:
		return "changes_requested"
	case PlanUATDeferredByAFK:
		return "deferred_by_afk"
	default:
		return fmt.Sprintf("PlanUATVerdict(%d)", int(v))
	}
}

// PlanAccepted is the typed payload of an accepted Plan-UAT decision.
type PlanAccepted struct {
	Snapshot     PlanUATSnapshot   `json:"snapshot"`
	Interactions []UATInteraction  `json:"interactions"`
	Feedback     []UATFeedbackItem `json:"feedback"`
}

func validatePlanAccepted(p PlanAccepted) error {
	if err := validatePlanSnapshot("PlanAccepted.Snapshot", p.Snapshot); err != nil {
		return err
	}
	if err := validateInteractions("PlanAccepted.Interactions", p.Interactions); err != nil {
		return err
	}
	if err := validateFeedback("PlanAccepted.Feedback", p.Feedback); err != nil {
		return err
	}
	if hasFixNowFeedback(p.Feedback) {
		return uatErr("PlanAccepted", "an accepted plan UAT carries FIX-NOW feedback",
			"FIX-NOW feedback forces a changes-requested verdict and cannot accompany an acceptance",
			"record the verdict as changes_requested when any feedback is FIX-NOW")
	}
	return nil
}

// PlanChangesRequested is the typed payload of a changes-requested Plan-UAT decision.
type PlanChangesRequested struct {
	Snapshot     PlanUATSnapshot   `json:"snapshot"`
	Interactions []UATInteraction  `json:"interactions"`
	Feedback     []UATFeedbackItem `json:"feedback"`
}

func validatePlanChangesRequested(p PlanChangesRequested) error {
	if err := validatePlanSnapshot("PlanChangesRequested.Snapshot", p.Snapshot); err != nil {
		return err
	}
	if err := validateInteractions("PlanChangesRequested.Interactions", p.Interactions); err != nil {
		return err
	}
	return validateFeedback("PlanChangesRequested.Feedback", p.Feedback)
}

// PlanDeferredByAFK is the typed payload of an AFK-deferred Plan-UAT decision. It records
// the exact AFK mode cursor entry in effect at deferral (ModeEntry): ratification later
// requires that exact entry to still be the latest mode entry with an AFK result.
type PlanDeferredByAFK struct {
	Snapshot      PlanUATSnapshot       `json:"snapshot"`
	Interactions  []UATInteraction      `json:"interactions"`
	Feedback      []UATFeedbackItem     `json:"feedback"`
	HeldQuestions []HeldUATQuestion     `json:"heldQuestions"`
	ModeEntry     DecisionLedgerEntryID `json:"modeEntry"`
}

func validatePlanDeferredByAFK(p PlanDeferredByAFK) error {
	if err := validatePlanSnapshot("PlanDeferredByAFK.Snapshot", p.Snapshot); err != nil {
		return err
	}
	if err := validateInteractions("PlanDeferredByAFK.Interactions", p.Interactions); err != nil {
		return err
	}
	if err := validateFeedback("PlanDeferredByAFK.Feedback", p.Feedback); err != nil {
		return err
	}
	if err := validateHeldQuestions("PlanDeferredByAFK.HeldQuestions", p.HeldQuestions); err != nil {
		return err
	}
	if p.ModeEntry == "" {
		return uatErr("PlanDeferredByAFK", "the deferral records no AFK mode entry",
			"an AFK deferral is anchored to the exact AFK mode cursor entry ratification later re-checks",
			"record the current AFK mode cursor entry id in ModeEntry")
	}
	if !hasStableHeldQuestion(p.HeldQuestions) {
		return uatErr("PlanDeferredByAFK", "the deferral carries no stable held question",
			"an AFK deferral defers real open questions, so at least one held question must be stable",
			"defer only with at least one stable held question, otherwise accept or request changes")
	}
	if hasFixNowFeedback(p.Feedback) {
		return uatErr("PlanDeferredByAFK", "the deferral carries FIX-NOW feedback",
			"FIX-NOW feedback forces a changes-requested verdict and cannot be deferred",
			"resolve FIX-NOW feedback with a changes-requested verdict rather than deferring")
	}
	return nil
}

func validatePlanSnapshot(field string, s PlanUATSnapshot) error {
	if s.ID == "" {
		return uatErr(field, "the plan-UAT snapshot id is empty",
			"a plan-UAT decision references its immutable snapshot id", "supply the plan-UAT snapshot id")
	}
	if s.UATTaskID == (provenance.TaskID{}) {
		return uatErr(field, "the snapshot names no UAT task",
			"a plan-UAT snapshot pins the exact task the UAT session ran under", "supply the UAT task id")
	}
	if s.Proposal == "" {
		return uatErr(field, "the snapshot names no proposal revision",
			"a plan-UAT snapshot pins the exact proposal revision under review", "supply the proposal revision")
	}
	if s.DecisionEntry == "" {
		return uatErr(field, "the snapshot names no decision-ledger entry",
			"a plan-UAT snapshot pins the exact decision-ledger entry the UAT verdict is recorded under", "supply the decision-ledger entry id")
	}
	if s.InputLedger == "" || s.OutputLedger == "" {
		return uatErr(field, "the snapshot names an empty input or output ledger revision",
			"a plan-UAT snapshot pins the exact input and post-command output ledger revisions",
			"supply both the input and output ledger revisions")
	}
	return nil
}

// PlanUATDecision is the higher-level command input a Plan-UAT gate receives. Its
// ReportedVerdict selects which of the three typed payloads is drafted; PolicySet.DraftPlanUAT
// validates the cross-field rules and lowers it to the matching canonical decision.
type PlanUATDecision struct {
	Snapshot        PlanUATSnapshot
	ReportedVerdict PlanUATVerdict
	Interactions    []UATInteraction
	Feedback        []UATFeedbackItem
	HeldQuestions   []HeldUATQuestion
	Mode            InteractionModeCursor
}

// ImplementationUATVerdict is the closed set of Implementation-UAT dispositions.
type ImplementationUATVerdict int

const (
	implUATVerdictInvalid ImplementationUATVerdict = iota
	// ImplUATAccepted accepts the integration candidate for landing.
	ImplUATAccepted
	// ImplUATChangesRequested returns the candidate for further work.
	ImplUATChangesRequested
)

func (v ImplementationUATVerdict) valid() bool {
	return v == ImplUATAccepted || v == ImplUATChangesRequested
}

func (v ImplementationUATVerdict) String() string {
	switch v {
	case ImplUATAccepted:
		return "accepted"
	case ImplUATChangesRequested:
		return "changes_requested"
	default:
		return fmt.Sprintf("ImplementationUATVerdict(%d)", int(v))
	}
}

// ResolutionKind is the closed set of carry-forward resolution dispositions. CONFIRM,
// DEFER, and REPLACE each resolve a target permanently; REPLACE additionally forces a
// changes-requested verdict (like FIX-NOW feedback) because it records that new work is
// required.
type ResolutionKind int

const (
	resolutionInvalid ResolutionKind = iota
	// ResolutionConfirm confirms the target as-is.
	ResolutionConfirm
	// ResolutionDefer resolves the target by deferring it to follow-up work.
	ResolutionDefer
	// ResolutionReplace resolves the target by superseding it, forcing changes-requested.
	ResolutionReplace
)

func (k ResolutionKind) valid() bool {
	return k == ResolutionConfirm || k == ResolutionDefer || k == ResolutionReplace
}

func (k ResolutionKind) String() string {
	switch k {
	case ResolutionConfirm:
		return "confirm"
	case ResolutionDefer:
		return "defer"
	case ResolutionReplace:
		return "replace"
	default:
		return fmt.Sprintf("ResolutionKind(%d)", int(k))
	}
}

// HeldQuestionResolution resolves one carried-forward held question.
type HeldQuestionResolution struct {
	Target HeldUATQuestionID `json:"target"`
	Kind   ResolutionKind    `json:"kind"`
	Note   string            `json:"note"`
}

// DeferredFeedbackResolution resolves one carried-forward deferred Plan-feedback item.
type DeferredFeedbackResolution struct {
	Target UATFeedbackID  `json:"target"`
	Kind   ResolutionKind `json:"kind"`
	Note   string         `json:"note"`
}

// LedgerDecisionResolution resolves one material agent-judgment ledger entry.
type LedgerDecisionResolution struct {
	Target DecisionLedgerEntryID `json:"target"`
	Kind   ResolutionKind        `json:"kind"`
	Note   string                `json:"note"`
}

// ImplUATPayload is the single, canonical Implementation-UAT decision payload: the
// verdict, verbatim interactions, feedback, and the three carry-forward resolution
// domains. It stores no actor — attribution is read only from the referenced ledger
// entry. It is the ONLY representation of these fields, so no second copy can drift.
type ImplUATPayload struct {
	ReportedVerdict ImplementationUATVerdict     `json:"reportedVerdict"`
	Interactions    []UATInteraction             `json:"interactions"`
	Feedback        []UATFeedbackItem            `json:"feedback"`
	HeldAnswers     []HeldQuestionResolution     `json:"heldAnswers"`
	PlanFeedback    []DeferredFeedbackResolution `json:"planFeedback"`
	LedgerDecisions []LedgerDecisionResolution   `json:"ledgerDecisions"`
}

// forcesChangesRequested reports whether the payload's resolutions/feedback force a
// changes-requested verdict: any REPLACE resolution or any FIX-NOW feedback item.
func (p ImplUATPayload) forcesChangesRequested() bool {
	if hasFixNowFeedback(p.Feedback) {
		return true
	}
	for _, r := range p.HeldAnswers {
		if r.Kind == ResolutionReplace {
			return true
		}
	}
	for _, r := range p.PlanFeedback {
		if r.Kind == ResolutionReplace {
			return true
		}
	}
	for _, r := range p.LedgerDecisions {
		if r.Kind == ResolutionReplace {
			return true
		}
	}
	return false
}

func validateImplUATPayload(p ImplUATPayload) error {
	if !p.ReportedVerdict.valid() {
		return uatErr("ImplUATPayload.ReportedVerdict", fmt.Sprintf("the reported verdict %q is not accepted or changes_requested", p.ReportedVerdict),
			"an implementation-UAT decision is either accepted or changes_requested", "report accepted or changes_requested")
	}
	if err := validateInteractions("ImplUATPayload.Interactions", p.Interactions); err != nil {
		return err
	}
	if err := validateFeedback("ImplUATPayload.Feedback", p.Feedback); err != nil {
		return err
	}
	heldSeen := map[HeldUATQuestionID]bool{}
	for i, r := range p.HeldAnswers {
		if !r.Kind.valid() {
			return uatErr("ImplUATPayload.HeldAnswers", fmt.Sprintf("held-answer %d has resolution kind %q", i, r.Kind),
				"a resolution is confirm, defer, or replace", "resolve each held answer with confirm, defer, or replace")
		}
		if r.Target == "" {
			return uatErr("ImplUATPayload.HeldAnswers", fmt.Sprintf("held-answer %d has an empty target", i),
				"a held-answer resolution names the exact held-question target it resolves", "supply the held-question target id")
		}
		if heldSeen[r.Target] {
			return uatErr("ImplUATPayload.HeldAnswers", fmt.Sprintf("held-question target %q is resolved more than once", r.Target),
				"one target has at most one effective resolution", "resolve each held question exactly once")
		}
		heldSeen[r.Target] = true
	}
	fbSeen := map[UATFeedbackID]bool{}
	for i, r := range p.PlanFeedback {
		if !r.Kind.valid() {
			return uatErr("ImplUATPayload.PlanFeedback", fmt.Sprintf("plan-feedback %d has resolution kind %q", i, r.Kind),
				"a resolution is confirm, defer, or replace", "resolve each plan-feedback item with confirm, defer, or replace")
		}
		if r.Target == "" {
			return uatErr("ImplUATPayload.PlanFeedback", fmt.Sprintf("plan-feedback %d has an empty target", i),
				"a plan-feedback resolution names the exact deferred-feedback target it resolves", "supply the deferred-feedback target id")
		}
		if fbSeen[r.Target] {
			return uatErr("ImplUATPayload.PlanFeedback", fmt.Sprintf("deferred-feedback target %q is resolved more than once", r.Target),
				"one target has at most one effective resolution", "resolve each deferred-feedback item exactly once")
		}
		fbSeen[r.Target] = true
	}
	ledgerSeen := map[DecisionLedgerEntryID]bool{}
	for i, r := range p.LedgerDecisions {
		if !r.Kind.valid() {
			return uatErr("ImplUATPayload.LedgerDecisions", fmt.Sprintf("ledger-decision %d has resolution kind %q", i, r.Kind),
				"a resolution is confirm, defer, or replace", "resolve each ledger decision with confirm, defer, or replace")
		}
		if r.Target == "" {
			return uatErr("ImplUATPayload.LedgerDecisions", fmt.Sprintf("ledger-decision %d has an empty target", i),
				"a ledger-decision resolution names the exact material agent-judgment entry it resolves", "supply the ledger-entry target id")
		}
		if ledgerSeen[r.Target] {
			return uatErr("ImplUATPayload.LedgerDecisions", fmt.Sprintf("ledger-decision target %q is resolved more than once", r.Target),
				"one target has at most one effective resolution", "resolve each material agent-judgment entry exactly once")
		}
		ledgerSeen[r.Target] = true
	}
	if p.forcesChangesRequested() && p.ReportedVerdict != ImplUATChangesRequested {
		return uatErr("ImplUATPayload.ReportedVerdict", "an accepted verdict carries a REPLACE resolution or FIX-NOW feedback",
			"REPLACE resolutions and FIX-NOW feedback force a changes-requested verdict because they record new required work",
			"report changes_requested when any resolution is replace or any feedback is FIX-NOW")
	}
	return nil
}

// IntegrationCandidateSetID identifies the exact integration candidate set an
// Implementation-UAT decision ranges over.
type IntegrationCandidateSetID string

// ImplementationUATDecision is the command-level Implementation-UAT record: the UAT task,
// integration candidate set, the decision ledger entry it will append, the exact input
// and post-command output ledger revisions, the deferred Plan decision it carries forward
// from, the single canonical payload, and the accepted coverage digest land recomputes.
type ImplementationUATDecision struct {
	UATTaskID      provenance.TaskID
	IntegrationSet IntegrationCandidateSetID
	DecisionEntry  DecisionLedgerEntryID
	InputLedger    DocumentRevisionID
	OutputLedger   DocumentRevisionID
	PlanDecision   PlanUATDecisionID
	Payload        ImplUATPayload
	Coverage       CoverageDigest
}

// SetInteractionModeCommand is the command-level input of `pasture task epoch
// interaction-mode set`. It retains the exact originating protocol-reported user decision;
// the live handler (pending-seed) calls the originating request's DecodeReportedResult on
// one bounded JSON value before any mutation. It accepts no actor/decider/trust/file
// alternative: the Recorder derives from the active assignment and the Decider from the
// epoch root's registered UserActorID.
type SetInteractionModeCommand struct {
	Epoch          EpochRootID
	Desired        InteractionMode
	ExpectedLedger DocumentRevisionID
	Report         ir.ReportedUserDecision
}

// Validate rejects a malformed set-interaction-mode command shape. It does NOT consume the
// report (the live handler decodes it against its originating request); it checks the
// command's own required fields.
func (c SetInteractionModeCommand) Validate() error {
	if c.Epoch == "" {
		return uatErr("SetInteractionModeCommand.Epoch", "the epoch root is empty",
			"a mode change targets a specific protected epoch root", "supply the epoch root id")
	}
	if !c.Desired.valid() {
		return uatErr("SetInteractionModeCommand.Desired", fmt.Sprintf("the desired mode %q is not normal or afk", c.Desired),
			"a mode change sets the mode to normal or afk", "set Desired to normal or afk")
	}
	if c.ExpectedLedger == "" {
		return uatErr("SetInteractionModeCommand.ExpectedLedger", "the expected ledger revision is empty",
			"a mode change is a compare-and-swap against the exact expected current ledger revision",
			"supply the expected current ledger revision")
	}
	return nil
}

func uatErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected a UAT policy value: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("UAT policy (internal/tasks/uat_policy.go, %s).", where),
		Impact:   "The UAT policy value was not constructed or accepted; nothing was persisted.",
		Fix:      fix + ".",
	}
}
