package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

const (
	RequestUserDecisionSchema  = "pasture.request-user-decision/v1"
	ReportedUserDecisionSchema = "pasture.reported-user-decision/v1"
)

// PromptStimulus is the complete material presented to the user.
type PromptStimulus struct {
	Question        string `json:"question"`
	DefinitionShown string `json:"definition_shown"`
	CommandShown    string `json:"command_shown"`
}

// DecisionOption is one option in presentation order.
type DecisionOption struct {
	ID          OptionID `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
}

type UserDecisionPrompt interface{ userDecisionPrompt() }

type SelectOnePrompt struct {
	Stimulus PromptStimulus
	Options  []DecisionOption
}

type SelectManyPrompt struct {
	Stimulus      PromptStimulus
	Options       []DecisionOption
	MinSelections int
	MaxSelections int
}

type FreeTextPrompt struct{ Stimulus PromptStimulus }

func (SelectOnePrompt) userDecisionPrompt()  {}
func (SelectManyPrompt) userDecisionPrompt() {}
func (FreeTextPrompt) userDecisionPrompt()   {}

type UserDecisionResult interface{ userDecisionResult() }

type SelectOneResult struct {
	Selected       OptionID
	VerbatimAnswer string
}

type SelectManyResult struct {
	Selected       []OptionID
	VerbatimAnswer string
}

type FreeTextResult struct{ Text string }

func (SelectOneResult) userDecisionResult()  {}
func (SelectManyResult) userDecisionResult() {}
func (FreeTextResult) userDecisionResult()   {}

// RequestUserDecision is the neutral request a runtime binding presents. Its
// lexical scope and any declared result slot are constructor-owned (set only
// by NewRequestUserDecision) rather than public fields: a decision that will
// bind its answer into RuntimeBindings must use the exact same scope/result
// validation every other SemanticOperation uses, including out-of-scope
// result rejection.
type RequestUserDecision struct {
	base            operationBase
	Schema          string
	RequestID       UserDecisionRequestID
	Harness         HarnessID
	RuntimeContract RuntimeContractID
	Epoch           portable.TaskRef
	GateTask        portable.TaskRef
	Purpose         DecisionPurpose
	Prompt          UserDecisionPrompt
}

// ReportedUserDecision is protocol-reported evidence. It intentionally carries
// no actor, recorder, trust, authentication, or mutation-authority field. Its
// base (scope and declared result slot) is never part of the wire form —
// DecodeReportedResult copies it from the originating, already-validated
// request, so a caller always knows exactly where a reported answer belongs
// in the parent's lexical bindings.
type ReportedUserDecision struct {
	base            operationBase
	Schema          string
	RequestID       UserDecisionRequestID
	Harness         HarnessID
	RuntimeContract RuntimeContractID
	Epoch           portable.TaskRef
	GateTask        portable.TaskRef
	Purpose         DecisionPurpose
	Prompt          UserDecisionPrompt
	Result          UserDecisionResult
}

// Scope returns the lexical scope this decision request was constructed in.
func (q RequestUserDecision) Scope() ScopeID { return q.base.scope }

// Results returns a defensive copy of the result slot(s) this decision
// request declared for capturing its reported answer.
func (q RequestUserDecision) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), q.base.results...)
}

// Scope returns the lexical scope copied from the originating request.
func (r ReportedUserDecision) Scope() ScopeID { return r.base.scope }

// Results returns a defensive copy of the result slot(s) copied from the
// originating request.
func (r ReportedUserDecision) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), r.base.results...)
}

// CanonicalDecisionBytes is the exact canonical JSON of one report validated
// through its originating request. It is opaque and constructor-owned: the
// only way to produce a non-zero value is a successful
// RequestUserDecision.DecodeReportedResult call, so possessing one is proof
// the bytes passed every report/request identity and prompt/result
// invariant.
type CanonicalDecisionBytes struct{ value []byte }

func newCanonicalDecisionBytes(data []byte) CanonicalDecisionBytes {
	return CanonicalDecisionBytes{value: append([]byte(nil), data...)}
}

func (b CanonicalDecisionBytes) Bytes() []byte { return append([]byte(nil), b.value...) }
func (b CanonicalDecisionBytes) IsValid() bool { return len(b.value) > 0 && json.Valid(b.value) }

// NewRequestUserDecision constructs and validates a user-decision request.
// scope and results follow the same lexical-binding rules as every other
// SemanticOperation (see newOperationBase): results must be declared in
// scope, and a caller with no result to capture can pass no results.
func NewRequestUserDecision(
	requestID UserDecisionRequestID,
	harness HarnessID,
	runtimeContract RuntimeContractID,
	epoch portable.TaskRef,
	gateTask portable.TaskRef,
	purpose DecisionPurpose,
	prompt UserDecisionPrompt,
	scope ScopeID,
	results ...ResultSlotDeclaration,
) (RequestUserDecision, error) {
	base, err := newOperationBase(scope, results)
	if err != nil {
		return RequestUserDecision{}, err
	}
	request := RequestUserDecision{
		base: base, Schema: RequestUserDecisionSchema, RequestID: requestID, Harness: harness,
		RuntimeContract: runtimeContract, Epoch: epoch, GateTask: gateTask,
		Purpose: purpose, Prompt: prompt,
	}
	validated, err := validatedRequest(request)
	if err != nil {
		return RequestUserDecision{}, err
	}
	return validated, nil
}

func validatedRequest(request RequestUserDecision) (RequestUserDecision, error) {
	if request.Schema != RequestUserDecisionSchema {
		return RequestUserDecision{}, decisionError(
			fmt.Sprintf("request schema %q is not supported", request.Schema),
			"schema identity must be exact before a prompt is presented",
			"set Schema to "+RequestUserDecisionSchema, nil,
		)
	}
	base, err := newOperationBase(request.base.scope, request.base.results)
	if err != nil {
		return RequestUserDecision{}, err
	}
	if err := validateDecisionIdentity(
		request.RequestID, request.Harness, request.RuntimeContract,
		request.Epoch, request.GateTask, request.Purpose,
	); err != nil {
		return RequestUserDecision{}, err
	}
	prompt, err := cloneAndValidatePrompt(request.Prompt)
	if err != nil {
		return RequestUserDecision{}, err
	}
	request.base = base
	request.Prompt = prompt
	return request, nil
}

func validateDecisionIdentity(
	requestID UserDecisionRequestID,
	harness HarnessID,
	runtimeContract RuntimeContractID,
	epoch portable.TaskRef,
	gateTask portable.TaskRef,
	purpose DecisionPurpose,
) error {
	if err := validateNamedID("user-decision request identity", string(requestID)); err != nil {
		return err
	}
	if !harness.IsValid() {
		return decisionError(
			fmt.Sprintf("harness %q is not enabled", harness),
			"a decision report must bind one known harness contract",
			"use an enabled HarnessID", nil,
		)
	}
	if !runtimeContract.IsValid() {
		return decisionError(
			fmt.Sprintf("runtime contract %q is zero or invalid", runtimeContract),
			"a decision report must bind one constructor-validated version-bound contract",
			"construct the contract with NewRuntimeContractID", nil,
		)
	}
	if runtimeContract.Harness() != harness {
		return decisionError(
			fmt.Sprintf("runtime contract %q is bound to harness %q, not the declared harness %q", runtimeContract, runtimeContract.Harness(), harness),
			"a decision's harness and runtime contract must name the exact same reviewed profile; accepting a cross-harness contract would let a report claim compatibility with a harness it never validated against",
			"construct the contract with NewRuntimeContractID(harness, ...) using the exact same harness", nil,
		)
	}
	if !epoch.IsValid() || !gateTask.IsValid() {
		return decisionError(
			"epoch or gate task reference is zero or invalid",
			"the report must bind the exact originating portable tasks",
			"construct both references with portable.NewTaskRef", nil,
		)
	}
	if err := validateNamedID("decision purpose", string(purpose)); err != nil {
		return err
	}
	return nil
}

func cloneAndValidatePrompt(prompt UserDecisionPrompt) (UserDecisionPrompt, error) {
	switch value := prompt.(type) {
	case SelectOnePrompt:
		options, err := cloneAndValidateOptions(value.Options)
		if err != nil {
			return nil, err
		}
		if err := validateStimulus(value.Stimulus); err != nil {
			return nil, err
		}
		return SelectOnePrompt{Stimulus: value.Stimulus, Options: options}, nil
	case *SelectOnePrompt:
		if value == nil {
			return nil, decisionError("select-one prompt is nil", "nil is not a closed prompt variant", "supply a SelectOnePrompt value", nil)
		}
		return cloneAndValidatePrompt(*value)
	case SelectManyPrompt:
		options, err := cloneAndValidateOptions(value.Options)
		if err != nil {
			return nil, err
		}
		if err := validateStimulus(value.Stimulus); err != nil {
			return nil, err
		}
		if value.MinSelections < 0 || value.MaxSelections < value.MinSelections || value.MaxSelections > len(options) {
			return nil, decisionError(
				fmt.Sprintf("select-many bounds [%d,%d] are invalid for %d options", value.MinSelections, value.MaxSelections, len(options)),
				"cardinality must be explicit, nonnegative, ordered, and achievable",
				"set 0 <= MinSelections <= MaxSelections <= len(Options)", nil,
			)
		}
		return SelectManyPrompt{
			Stimulus: value.Stimulus, Options: options,
			MinSelections: value.MinSelections, MaxSelections: value.MaxSelections,
		}, nil
	case *SelectManyPrompt:
		if value == nil {
			return nil, decisionError("select-many prompt is nil", "nil is not a closed prompt variant", "supply a SelectManyPrompt value", nil)
		}
		return cloneAndValidatePrompt(*value)
	case FreeTextPrompt:
		if err := validateStimulus(value.Stimulus); err != nil {
			return nil, err
		}
		return value, nil
	case *FreeTextPrompt:
		if value == nil {
			return nil, decisionError("free-text prompt is nil", "nil is not a closed prompt variant", "supply a FreeTextPrompt value", nil)
		}
		return cloneAndValidatePrompt(*value)
	default:
		return nil, decisionError(
			fmt.Sprintf("prompt variant %T is omitted or unknown", prompt),
			"the prompt sum is closed to SelectOnePrompt, SelectManyPrompt, and FreeTextPrompt",
			"use exactly one supported prompt variant", nil,
		)
	}
}

func validateStimulus(stimulus PromptStimulus) error {
	if !utf8.ValidString(stimulus.Question) || !utf8.ValidString(stimulus.DefinitionShown) || !utf8.ValidString(stimulus.CommandShown) {
		return decisionError(
			"prompt stimulus contains invalid UTF-8", "stimulus must be preserved exactly across JSON and runtime presentation",
			"replace invalid byte sequences with intentional Unicode text", nil,
		)
	}
	if strings.TrimSpace(stimulus.Question) == "" {
		return decisionError(
			"prompt question is empty", "a user decision requires an explicit question",
			"provide a non-whitespace Question", nil,
		)
	}
	return nil
}

func cloneAndValidateOptions(options []DecisionOption) ([]DecisionOption, error) {
	if len(options) == 0 {
		return nil, decisionError(
			"selection prompt has no options", "selection modes require a finite presented option set",
			"provide at least one uniquely identified option", nil,
		)
	}
	owned := append([]DecisionOption(nil), options...)
	seen := make(map[OptionID]struct{}, len(owned))
	for index, option := range owned {
		if err := validateNamedID("decision option identity", string(option.ID)); err != nil {
			return nil, decisionError(fmt.Sprintf("option %d has an invalid identity", index), "every presented option needs a stable identity", "replace the option ID", err)
		}
		if !utf8.ValidString(option.Label) || !utf8.ValidString(option.Description) || strings.TrimSpace(option.Label) == "" {
			return nil, decisionError(
				fmt.Sprintf("option %q has an empty/invalid label or invalid description", option.ID),
				"presented option text must be exact valid UTF-8",
				"provide a non-empty label and valid UTF-8 description", nil,
			)
		}
		if _, duplicate := seen[option.ID]; duplicate {
			return nil, decisionError(
				fmt.Sprintf("option identity %q is duplicated", option.ID),
				"a reported selection must resolve to exactly one presented option",
				"give every option a unique OptionID", nil,
			)
		}
		seen[option.ID] = struct{}{}
	}
	return owned, nil
}

// DecodeReportedResult strictly decodes and validates one report through its
// originating request. There is intentionally no free report decoder.
func (q RequestUserDecision) DecodeReportedResult(
	r io.Reader,
	maxBytes int64,
) (ReportedUserDecision, CanonicalDecisionBytes, error) {
	request, err := validatedRequest(q)
	if err != nil {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, err
	}
	if r == nil || maxBytes <= 0 {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
			"reported-result reader is nil or maxBytes is not positive",
			"bounded decoding needs both a source and an explicit positive limit",
			"supply a reader and a positive byte bound", nil,
		)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
			"reported-result bytes could not be read", "the complete bounded JSON value is required for exact validation",
			"retry with a readable result source", err,
		)
	}
	if int64(len(data)) > maxBytes {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
			fmt.Sprintf("reported-result size exceeds %d bytes", maxBytes), "unbounded user-controlled JSON can exhaust the runtime",
			"reduce the result below the configured bound", nil,
		)
	}
	if !utf8.Valid(data) {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
			"reported-result JSON is not valid UTF-8", "exact stimulus and verbatim answers cannot use replacement decoding",
			"encode the complete report as valid UTF-8 JSON", nil,
		)
	}
	if err := rejectDuplicateJSONMembers(data); err != nil {
		if IsDuplicateJSONMember(err) {
			return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
				"reported-result JSON has a duplicate member",
				"a duplicate key lets different readers of the same bytes disagree on the effective value",
				"encode each field exactly once", err,
			)
		}
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
			"reported-result JSON is empty, truncated, or malformed",
			"the evidence codec requires one complete, syntactically valid JSON value before it can be validated",
			"supply the complete, syntactically valid report JSON", err,
		)
	}
	report, err := decodeReportedWire(data)
	if err != nil {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, err
	}
	if err := compareReportIdentity(request, report); err != nil {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, err
	}
	result, err := cloneAndValidateResult(request.Prompt, report.Result)
	if err != nil {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, err
	}
	report.base = request.base
	report.Prompt = request.Prompt
	report.Result = result
	canonical, err := json.Marshal(report)
	if err != nil {
		return ReportedUserDecision{}, CanonicalDecisionBytes{}, decisionError(
			"validated report could not be canonicalized", "canonical evidence bytes are required by downstream commands",
			"correct the report codec implementation", err,
		)
	}
	return report, newCanonicalDecisionBytes(canonical), nil
}

func cloneAndValidateResult(prompt UserDecisionPrompt, result UserDecisionResult) (UserDecisionResult, error) {
	switch expected := prompt.(type) {
	case SelectOnePrompt:
		value, ok := result.(SelectOneResult)
		if !ok {
			if pointer, pointerOK := result.(*SelectOneResult); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
		}
		if !ok {
			return nil, wrongDecisionResult("select_one", result)
		}
		if !knownOption(expected.Options, value.Selected) {
			return nil, decisionError(fmt.Sprintf("selected option %q was not presented", value.Selected), "select-one must identify exactly one known option", "select one presented OptionID", nil)
		}
		if !utf8.ValidString(value.VerbatimAnswer) {
			return nil, decisionError("select-one verbatim answer is invalid UTF-8", "verbatim evidence must round-trip exactly", "supply valid UTF-8", nil)
		}
		return value, nil
	case SelectManyPrompt:
		value, ok := result.(SelectManyResult)
		if !ok {
			if pointer, pointerOK := result.(*SelectManyResult); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
		}
		if !ok {
			return nil, wrongDecisionResult("select_many", result)
		}
		selected := append([]OptionID(nil), value.Selected...)
		seen := make(map[OptionID]struct{}, len(selected))
		for _, id := range selected {
			if _, duplicate := seen[id]; duplicate {
				return nil, decisionError(fmt.Sprintf("selected option %q is duplicated", id), "duplicates must be rejected before canonical sorting", "select each OptionID at most once", nil)
			}
			if !knownOption(expected.Options, id) {
				return nil, decisionError(fmt.Sprintf("selected option %q was not presented", id), "select-many can contain only known option identities", "select only presented OptionIDs", nil)
			}
			seen[id] = struct{}{}
		}
		if len(selected) < expected.MinSelections || len(selected) > expected.MaxSelections {
			return nil, decisionError(
				fmt.Sprintf("selected %d options outside required range [%d,%d]", len(selected), expected.MinSelections, expected.MaxSelections),
				"the result must satisfy the originating prompt's explicit cardinality",
				"adjust the selected set to the declared range", nil,
			)
		}
		if len(selected) == 0 && expected.MinSelections != 0 {
			return nil, decisionError("empty select-many result is not permitted", "only an explicit zero minimum permits no selections", "select at least the declared minimum", nil)
		}
		if !utf8.ValidString(value.VerbatimAnswer) {
			return nil, decisionError("select-many verbatim answer is invalid UTF-8", "verbatim evidence must round-trip exactly", "supply valid UTF-8", nil)
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i] < selected[j] })
		return SelectManyResult{Selected: selected, VerbatimAnswer: value.VerbatimAnswer}, nil
	case FreeTextPrompt:
		value, ok := result.(FreeTextResult)
		if !ok {
			if pointer, pointerOK := result.(*FreeTextResult); pointerOK && pointer != nil {
				value, ok = *pointer, true
			}
		}
		if !ok {
			return nil, wrongDecisionResult("free_text", result)
		}
		if !utf8.ValidString(value.Text) {
			return nil, decisionError("free-text result is invalid UTF-8", "text evidence must round-trip exactly", "supply valid UTF-8", nil)
		}
		return value, nil
	default:
		return nil, decisionError("originating prompt is invalid", "result validation requires a closed validated prompt", "construct the request with NewRequestUserDecision", nil)
	}
}

func knownOption(options []DecisionOption, selected OptionID) bool {
	for _, option := range options {
		if option.ID == selected {
			return true
		}
	}
	return false
}

func wrongDecisionResult(mode string, result UserDecisionResult) error {
	return decisionError(
		fmt.Sprintf("result variant %T does not match %s prompt", result, mode),
		"the closed result mode must match the originating closed prompt mode",
		"return exactly the matching result variant", nil,
	)
}

func compareReportIdentity(request RequestUserDecision, report ReportedUserDecision) error {
	if report.Schema != ReportedUserDecisionSchema ||
		report.RequestID != request.RequestID ||
		report.Harness != request.Harness ||
		report.RuntimeContract != request.RuntimeContract ||
		report.Epoch != request.Epoch ||
		report.GateTask != request.GateTask ||
		report.Purpose != request.Purpose {
		return decisionError(
			"reported-result identity does not exactly match the originating request",
			"schema, request, harness, runtime contract, epoch, gate, and purpose are all binding evidence",
			"copy every identity field from the originating request and use the reported-result schema", nil,
		)
	}
	if !promptsEqual(request.Prompt, report.Prompt) {
		return decisionError(
			"reported prompt does not exactly match the originating prompt",
			"question, shown definition/command, option order/text, and bounds are evidence",
			"repeat the complete originating prompt without reordering or normalization", nil,
		)
	}
	return nil
}

func promptsEqual(left, right UserDecisionPrompt) bool {
	leftBytes, leftErr := marshalPrompt(left)
	rightBytes, rightErr := marshalPrompt(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func decisionError(what, why, fix string, cause error) error {
	return diagnostic(
		what, why, "RequestUserDecision.DecodeReportedResult", "user-decision validation",
		"the reported user decision is rejected before any command or mutation", fix, cause,
	)
}

type promptMode string

const (
	promptSelectOne  promptMode = "select_one"
	promptSelectMany promptMode = "select_many"
	promptFreeText   promptMode = "free_text"
)

type promptEnvelope struct {
	Mode promptMode      `json:"mode"`
	Data json.RawMessage `json:"data"`
}

type selectOnePromptWire struct {
	Stimulus PromptStimulus   `json:"stimulus"`
	Options  []DecisionOption `json:"options"`
}

type selectManyPromptWire struct {
	Stimulus      PromptStimulus   `json:"stimulus"`
	Options       []DecisionOption `json:"options"`
	MinSelections int              `json:"min_selections"`
	MaxSelections int              `json:"max_selections"`
}

type freeTextPromptWire struct {
	Stimulus PromptStimulus `json:"stimulus"`
}

type resultEnvelope struct {
	Mode promptMode      `json:"mode"`
	Data json.RawMessage `json:"data"`
}

type selectOneResultWire struct {
	Selected       OptionID `json:"selected"`
	VerbatimAnswer string   `json:"verbatim_answer"`
}

type selectManyResultWire struct {
	Selected       []OptionID `json:"selected"`
	VerbatimAnswer string     `json:"verbatim_answer"`
}

type freeTextResultWire struct {
	Text string `json:"text"`
}

type requestWire struct {
	Schema          string                `json:"schema"`
	RequestID       UserDecisionRequestID `json:"request_id"`
	Harness         HarnessID             `json:"harness"`
	RuntimeContract RuntimeContractID     `json:"runtime_contract"`
	Epoch           portable.TaskRef      `json:"epoch"`
	GateTask        portable.TaskRef      `json:"gate_task"`
	Purpose         DecisionPurpose       `json:"purpose"`
	Prompt          json.RawMessage       `json:"prompt"`
}

type reportWire struct {
	Schema          string                `json:"schema"`
	RequestID       UserDecisionRequestID `json:"request_id"`
	Harness         HarnessID             `json:"harness"`
	RuntimeContract RuntimeContractID     `json:"runtime_contract"`
	Epoch           portable.TaskRef      `json:"epoch"`
	GateTask        portable.TaskRef      `json:"gate_task"`
	Purpose         DecisionPurpose       `json:"purpose"`
	Prompt          json.RawMessage       `json:"prompt"`
	Result          json.RawMessage       `json:"result"`
}

// Every required-field list below is the exhaustive omission matrix for its
// wire form: StrictJSONWithPresence rejects a decode when any of these JSON
// members is missing, even though its Go zero value (empty string, 0, nil
// slice) could otherwise look like a deliberately supplied value — an
// omitted "min_selections" and an explicit "min_selections": 0 must not be
// indistinguishable, since 0 is a legitimate declared minimum.
var (
	envelopeRequiredFields             = []string{"mode", "data"}
	selectOnePromptWireRequiredFields  = []string{"stimulus", "options"}
	selectManyPromptWireRequiredFields = []string{"stimulus", "options", "min_selections", "max_selections"}
	freeTextPromptWireRequiredFields   = []string{"stimulus"}
	selectOneResultWireRequiredFields  = []string{"selected", "verbatim_answer"}
	selectManyResultWireRequiredFields = []string{"selected", "verbatim_answer"}
	freeTextResultWireRequiredFields   = []string{"text"}
	reportWireRequiredFields           = []string{
		"schema", "request_id", "harness", "runtime_contract", "epoch", "gate_task", "purpose", "prompt", "result",
	}
	promptStimulusRequiredFields = []string{"question", "definition_shown", "command_shown"}
	decisionOptionRequiredFields = []string{"id", "label", "description"}
)

// promptStimulusWire is PromptStimulus's underlying type without its methods,
// used to decode through StrictJSONWithPresence without recursing back into
// PromptStimulus.UnmarshalJSON.
type promptStimulusWire PromptStimulus

// UnmarshalJSON makes PromptStimulus itself presence-aware: it is nested
// inside every prompt variant's data object, so without its own decoder an
// omitted "definition_shown" or "command_shown" would silently decode as "",
// indistinguishable from a caller who explicitly supplied an empty string.
func (s *PromptStimulus) UnmarshalJSON(data []byte) error {
	var wire promptStimulusWire
	if err := StrictJSONWithPresence(data, promptStimulusRequiredFields, &wire); err != nil {
		return decisionError(
			"prompt stimulus JSON is malformed or omits a required field",
			"question, definition_shown, and command_shown must all be explicitly present, even when a value is legitimately empty",
			"encode question, definition_shown, and command_shown exactly once each", err,
		)
	}
	*s = PromptStimulus(wire)
	return nil
}

// decisionOptionWire is DecisionOption's underlying type without its
// methods, used to decode through StrictJSONWithPresence without recursing
// back into DecisionOption.UnmarshalJSON.
type decisionOptionWire DecisionOption

// UnmarshalJSON makes DecisionOption itself presence-aware: it is nested
// inside every selection prompt's options array, so without its own decoder
// an omitted "description" would silently decode as "", indistinguishable
// from a caller who explicitly supplied an empty description — the same gap
// PromptStimulus.UnmarshalJSON closes for the stimulus object.
func (o *DecisionOption) UnmarshalJSON(data []byte) error {
	var wire decisionOptionWire
	if err := StrictJSONWithPresence(data, decisionOptionRequiredFields, &wire); err != nil {
		return decisionError(
			"decision option JSON is malformed or omits a required field",
			"id, label, and description must all be explicitly present, even when a value is legitimately empty",
			"encode id, label, and description exactly once each", err,
		)
	}
	*o = DecisionOption(wire)
	return nil
}

func (q RequestUserDecision) MarshalJSON() ([]byte, error) {
	request, err := validatedRequest(q)
	if err != nil {
		return nil, err
	}
	prompt, err := marshalPrompt(request.Prompt)
	if err != nil {
		return nil, err
	}
	return json.Marshal(requestWire{
		Schema: request.Schema, RequestID: request.RequestID, Harness: request.Harness,
		RuntimeContract: request.RuntimeContract, Epoch: request.Epoch, GateTask: request.GateTask,
		Purpose: request.Purpose, Prompt: prompt,
	})
}

func (r ReportedUserDecision) MarshalJSON() ([]byte, error) {
	if r.Schema != ReportedUserDecisionSchema {
		return nil, decisionError(
			fmt.Sprintf("reported schema %q is not supported", r.Schema),
			"canonical report bytes require the exact versioned schema",
			"set Schema to "+ReportedUserDecisionSchema, nil,
		)
	}
	if err := validateDecisionIdentity(
		r.RequestID, r.Harness, r.RuntimeContract, r.Epoch, r.GateTask, r.Purpose,
	); err != nil {
		return nil, err
	}
	prompt, err := cloneAndValidatePrompt(r.Prompt)
	if err != nil {
		return nil, err
	}
	result, err := cloneAndValidateResult(prompt, r.Result)
	if err != nil {
		return nil, err
	}
	promptBytes, _ := marshalPrompt(prompt)
	resultBytes, _ := marshalResult(result)
	return json.Marshal(reportWire{
		Schema: r.Schema, RequestID: r.RequestID, Harness: r.Harness,
		RuntimeContract: r.RuntimeContract, Epoch: r.Epoch, GateTask: r.GateTask,
		Purpose: r.Purpose, Prompt: promptBytes, Result: resultBytes,
	})
}

func marshalPrompt(prompt UserDecisionPrompt) ([]byte, error) {
	switch value := prompt.(type) {
	case SelectOnePrompt:
		data, err := json.Marshal(selectOnePromptWire{Stimulus: value.Stimulus, Options: value.Options})
		if err != nil {
			return nil, err
		}
		return json.Marshal(promptEnvelope{Mode: promptSelectOne, Data: data})
	case SelectManyPrompt:
		data, err := json.Marshal(selectManyPromptWire{
			Stimulus: value.Stimulus, Options: value.Options,
			MinSelections: value.MinSelections, MaxSelections: value.MaxSelections,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(promptEnvelope{Mode: promptSelectMany, Data: data})
	case FreeTextPrompt:
		data, err := json.Marshal(freeTextPromptWire{Stimulus: value.Stimulus})
		if err != nil {
			return nil, err
		}
		return json.Marshal(promptEnvelope{Mode: promptFreeText, Data: data})
	default:
		return nil, fmt.Errorf("unknown prompt variant %T", prompt)
	}
}

func marshalResult(result UserDecisionResult) ([]byte, error) {
	switch value := result.(type) {
	case SelectOneResult:
		data, err := json.Marshal(selectOneResultWire(value))
		if err != nil {
			return nil, err
		}
		return json.Marshal(resultEnvelope{Mode: promptSelectOne, Data: data})
	case SelectManyResult:
		data, err := json.Marshal(selectManyResultWire(value))
		if err != nil {
			return nil, err
		}
		return json.Marshal(resultEnvelope{Mode: promptSelectMany, Data: data})
	case FreeTextResult:
		data, err := json.Marshal(freeTextResultWire(value))
		if err != nil {
			return nil, err
		}
		return json.Marshal(resultEnvelope{Mode: promptFreeText, Data: data})
	default:
		return nil, fmt.Errorf("unknown result variant %T", result)
	}
}

func decodeReportedWire(data []byte) (ReportedUserDecision, error) {
	var wire reportWire
	if err := StrictJSONWithPresence(data, reportWireRequiredFields, &wire); err != nil {
		return ReportedUserDecision{}, decisionError(
			"reported-result JSON is malformed, has unknown fields, omits a required field, or has trailing content",
			"the evidence codec accepts exactly one closed versioned value with every field explicitly present, "+
				"so an attacker cannot use JSON's zero-value defaulting to smuggle a missing identity field past validation",
			"encode one report with every documented field present exactly once", err,
		)
	}
	prompt, err := decodePrompt(wire.Prompt)
	if err != nil {
		return ReportedUserDecision{}, err
	}
	result, err := decodeResult(wire.Result)
	if err != nil {
		return ReportedUserDecision{}, err
	}
	return ReportedUserDecision{
		Schema: wire.Schema, RequestID: wire.RequestID, Harness: wire.Harness,
		RuntimeContract: wire.RuntimeContract, Epoch: wire.Epoch, GateTask: wire.GateTask,
		Purpose: wire.Purpose, Prompt: prompt, Result: result,
	}, nil
}

func decodePrompt(data []byte) (UserDecisionPrompt, error) {
	var envelope promptEnvelope
	if err := StrictJSONWithPresence(data, envelopeRequiredFields, &envelope); err != nil {
		return nil, decisionError("prompt envelope is malformed or omits mode/data", "prompt mode and data are a closed sum and both must be explicitly present", "encode one supported prompt variant", err)
	}
	switch envelope.Mode {
	case promptSelectOne:
		var wire selectOnePromptWire
		if err := StrictJSONWithPresence(envelope.Data, selectOnePromptWireRequiredFields, &wire); err != nil {
			return nil, decisionError("select-one prompt data is malformed or omits a required field", "variant data cannot contain fields from another mode, and every field of this mode must be present", "encode exactly stimulus and options", err)
		}
		return cloneAndValidatePrompt(SelectOnePrompt(wire))
	case promptSelectMany:
		var wire selectManyPromptWire
		if err := StrictJSONWithPresence(envelope.Data, selectManyPromptWireRequiredFields, &wire); err != nil {
			return nil, decisionError("select-many prompt data is malformed or omits a required field", "variant data must include explicit bounds, and an omitted min_selections/max_selections is not the same as an explicit 0", "encode stimulus, options, min_selections, and max_selections", err)
		}
		return cloneAndValidatePrompt(SelectManyPrompt(wire))
	case promptFreeText:
		var wire freeTextPromptWire
		if err := StrictJSONWithPresence(envelope.Data, freeTextPromptWireRequiredFields, &wire); err != nil {
			return nil, decisionError("free-text prompt data is malformed or omits a required field", "variant data cannot contain option fields", "encode exactly one stimulus", err)
		}
		return cloneAndValidatePrompt(FreeTextPrompt(wire))
	default:
		return nil, decisionError(fmt.Sprintf("unknown prompt mode %q", envelope.Mode), "the prompt sum is closed", "use select_one, select_many, or free_text", nil)
	}
}

func decodeResult(data []byte) (UserDecisionResult, error) {
	var envelope resultEnvelope
	if err := StrictJSONWithPresence(data, envelopeRequiredFields, &envelope); err != nil {
		return nil, decisionError("result envelope is malformed or omits mode/data", "result mode and data are a closed sum and both must be explicitly present", "encode one supported result variant", err)
	}
	switch envelope.Mode {
	case promptSelectOne:
		var wire selectOneResultWire
		if err := StrictJSONWithPresence(envelope.Data, selectOneResultWireRequiredFields, &wire); err != nil {
			return nil, decisionError("select-one result data is malformed or omits a required field", "variant data cannot contain fields from another mode", "encode selected and verbatim_answer", err)
		}
		return SelectOneResult(wire), nil
	case promptSelectMany:
		var wire selectManyResultWire
		if err := StrictJSONWithPresence(envelope.Data, selectManyResultWireRequiredFields, &wire); err != nil {
			return nil, decisionError("select-many result data is malformed or omits a required field", "variant data cannot contain free-text fields", "encode selected and verbatim_answer", err)
		}
		return SelectManyResult(wire), nil
	case promptFreeText:
		var wire freeTextResultWire
		if err := StrictJSONWithPresence(envelope.Data, freeTextResultWireRequiredFields, &wire); err != nil {
			return nil, decisionError("free-text result data is malformed or omits a required field", "variant data cannot contain option IDs", "encode exactly text", err)
		}
		return FreeTextResult(wire), nil
	default:
		return nil, decisionError(fmt.Sprintf("unknown result mode %q", envelope.Mode), "the result sum is closed", "use select_one, select_many, or free_text", nil)
	}
}
