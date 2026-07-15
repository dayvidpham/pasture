package ir

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

type OperationKind string

const (
	OperationInvokeSkill              OperationKind = "invoke_skill"
	OperationDelegateAssignment       OperationKind = "delegate_assignment"
	OperationContinueAssignment       OperationKind = "continue_assignment"
	OperationSendAssignmentMessage    OperationKind = "send_assignment_message"
	OperationCollectAssignmentResults OperationKind = "collect_assignment_results"
	OperationStopAssignment           OperationKind = "stop_assignment"
	OperationRequestUserDecision      OperationKind = "request_user_decision"
)

var AllOperationKinds = []OperationKind{
	OperationInvokeSkill,
	OperationDelegateAssignment,
	OperationContinueAssignment,
	OperationSendAssignmentMessage,
	OperationCollectAssignmentResults,
	OperationStopAssignment,
	OperationRequestUserDecision,
}

var coreOperationIDs = map[OperationKind]SemanticOperationID{
	OperationInvokeSkill:              mustSemanticOperationID("pasture.orchestration.invoke-skill/v1"),
	OperationDelegateAssignment:       mustSemanticOperationID("pasture.orchestration.delegate-assignment/v1"),
	OperationContinueAssignment:       mustSemanticOperationID("pasture.orchestration.continue-assignment/v1"),
	OperationSendAssignmentMessage:    mustSemanticOperationID("pasture.orchestration.send-assignment-message/v1"),
	OperationCollectAssignmentResults: mustSemanticOperationID("pasture.orchestration.collect-assignment-results/v1"),
	OperationStopAssignment:           mustSemanticOperationID("pasture.orchestration.stop-assignment/v1"),
	OperationRequestUserDecision:      mustSemanticOperationID("pasture.orchestration.request-user-decision/v1"),
}

func mustSemanticOperationID(value string) SemanticOperationID {
	id, err := NewSemanticOperationID(value)
	if err != nil {
		panic(err)
	}
	return id
}

// CoreOperationID returns the canonical protocol identity for a closed
// orchestration variant.
func CoreOperationID(kind OperationKind) (SemanticOperationID, bool) {
	id, ok := coreOperationIDs[kind]
	return id, ok
}

type SemanticOperation interface {
	semanticOperation()
	operationKind() OperationKind
	operationID() SemanticOperationID
	validateOperation() error
	canonicalOperation() ([]byte, error)
}

// ResolvedOperand is immutable typed dataflow consumed by an operation.
type ResolvedOperand struct {
	key      string
	typeName string
	value    []byte
}

func ResolveOperand[T any](bindings RuntimeBindings, ref ValueRef[T], scope ScopeID) (ResolvedOperand, error) {
	value, err := ResolveRuntimeValue(bindings, ref, scope)
	if err != nil {
		return ResolvedOperand{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ResolvedOperand{}, operationError("typed operand cannot be encoded", "operation construction retains exact portable operand bytes", "bind a JSON-compatible typed value", err)
	}
	return ResolvedOperand{key: ref.key, typeName: typeOf[T]().String(), value: encoded}, nil
}

func (o ResolvedOperand) Key() string      { return o.key }
func (o ResolvedOperand) TypeName() string { return o.typeName }
func (o ResolvedOperand) Bytes() []byte    { return append([]byte(nil), o.value...) }
func (o ResolvedOperand) IsValid() bool {
	return o.key != "" && o.typeName != "" && json.Valid(o.value)
}

type operationBase struct {
	scope   ScopeID
	results []ResultSlotDeclaration
}

func newOperationBase(scope ScopeID, results []ResultSlotDeclaration) (operationBase, error) {
	if !scope.IsValid() {
		return operationBase{}, operationError("operation scope is zero or invalid", "every runtime invocation needs an explicit lexical scope", "construct a ScopeID before the operation", nil)
	}
	owned := append([]ResultSlotDeclaration(nil), results...)
	seen := make(map[string]struct{}, len(owned))
	for index, result := range owned {
		if !result.IsValid() {
			return operationBase{}, operationError(fmt.Sprintf("result declaration %d is invalid", index), "operations retain only typed constructor-produced result slots", "declare the result with DeclareResultSlot", nil)
		}
		if result.scope != scope {
			return operationBase{}, operationError(fmt.Sprintf("result %q belongs to scope %q, not operation scope %q", result.key, result.scope, scope), "an invocation cannot create a binding in an unrelated scope", "declare results in the operation scope", nil)
		}
		if _, duplicate := seen[result.key]; duplicate {
			return operationBase{}, operationError(fmt.Sprintf("result %q is declared twice", result.key), "one operation cannot capture two values under the same key", "use unique result keys", nil)
		}
		seen[result.key] = struct{}{}
	}
	return operationBase{scope: scope, results: owned}, nil
}

type InvokeSkill struct {
	base     operationBase
	skill    SkillID
	operands []ResolvedOperand
}

func (o InvokeSkill) Skill() SkillID { return o.skill }
func (o InvokeSkill) Operands() []ResolvedOperand {
	owned, _ := validateOperands(o.operands)
	return owned
}
func (o InvokeSkill) Scope() ScopeID { return o.base.scope }
func (o InvokeSkill) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), o.base.results...)
}

func NewInvokeSkill(skill SkillID, operands []ResolvedOperand, scope ScopeID, results ...ResultSlotDeclaration) (InvokeSkill, error) {
	if !skill.IsValid() {
		return InvokeSkill{}, operationError("skill identity is zero or invalid", "skill invocation must name portable semantic intent, not inferred prose", "construct the identity with NewSkillID", nil)
	}
	base, err := newOperationBase(scope, results)
	if err != nil {
		return InvokeSkill{}, err
	}
	owned, err := validateOperands(operands)
	if err != nil {
		return InvokeSkill{}, err
	}
	return InvokeSkill{base: base, skill: skill, operands: owned}, nil
}

func validateOperands(operands []ResolvedOperand) ([]ResolvedOperand, error) {
	owned := make([]ResolvedOperand, len(operands))
	seen := make(map[string]struct{}, len(operands))
	for index, operand := range operands {
		if !operand.IsValid() {
			return nil, operationError(fmt.Sprintf("operand %d is zero or invalid", index), "operation dataflow must be resolved before generation", "create every operand with ResolveOperand", nil)
		}
		if _, duplicate := seen[operand.key]; duplicate {
			return nil, operationError(fmt.Sprintf("operand %q is duplicated", operand.key), "one request field cannot resolve from two bindings", "supply each typed operand once", nil)
		}
		seen[operand.key] = struct{}{}
		operand.value = append([]byte(nil), operand.value...)
		owned[index] = operand
	}
	return owned, nil
}

type ScheduleKind string

const (
	ScheduleIndependent       ScheduleKind = "independent"
	ScheduleDependencyOrdered ScheduleKind = "dependency_ordered"
	ScheduleBoundedParallel   ScheduleKind = "bounded_parallel"
)

type Scheduling struct {
	kind        ScheduleKind
	maxParallel int
}

func Independent() Scheduling       { return Scheduling{kind: ScheduleIndependent} }
func DependencyOrdered() Scheduling { return Scheduling{kind: ScheduleDependencyOrdered} }
func BoundedParallel(maxParallel int) (Scheduling, error) {
	if maxParallel <= 0 {
		return Scheduling{}, operationError("bounded-parallel limit is not positive", "parallel scheduling must declare an enforceable bound", "supply maxParallel greater than zero", nil)
	}
	return Scheduling{kind: ScheduleBoundedParallel, maxParallel: maxParallel}, nil
}

func (s Scheduling) Kind() ScheduleKind { return s.kind }
func (s Scheduling) MaxParallel() int   { return s.maxParallel }
func (s Scheduling) IsValid() bool {
	switch s.kind {
	case ScheduleIndependent, ScheduleDependencyOrdered:
		return s.maxParallel == 0
	case ScheduleBoundedParallel:
		return s.maxParallel > 0
	default:
		return false
	}
}

type DelegateAssignment struct {
	base       operationBase
	contexts   []AssignmentContext
	scheduling Scheduling
}

func (o DelegateAssignment) Contexts() []AssignmentContext {
	return append([]AssignmentContext(nil), o.contexts...)
}
func (o DelegateAssignment) Scheduling() Scheduling { return o.scheduling }
func (o DelegateAssignment) Scope() ScopeID         { return o.base.scope }
func (o DelegateAssignment) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), o.base.results...)
}

func NewDelegateAssignment(contexts []AssignmentContext, scheduling Scheduling, scope ScopeID, results ...ResultSlotDeclaration) (DelegateAssignment, error) {
	base, err := newOperationBase(scope, results)
	if err != nil {
		return DelegateAssignment{}, err
	}
	owned, err := validateContexts(contexts)
	if err != nil {
		return DelegateAssignment{}, err
	}
	if !scheduling.IsValid() {
		return DelegateAssignment{}, operationError("delegation scheduling is omitted or invalid", "parallelism must use explicit portable collection metadata", "use Independent, DependencyOrdered, or BoundedParallel", nil)
	}
	return DelegateAssignment{base: base, contexts: owned, scheduling: scheduling}, nil
}

func validateContexts(contexts []AssignmentContext) ([]AssignmentContext, error) {
	if len(contexts) == 0 {
		return nil, operationError("assignment collection is empty", "delegation and collection require at least one logical assignment", "supply one or more validated AssignmentContext values", nil)
	}
	owned := append([]AssignmentContext(nil), contexts...)
	seen := make(map[string]struct{}, len(owned))
	for index, context := range owned {
		if !context.assignment.IsValid() || !context.task.IsValid() || !context.role.IsValid() {
			return nil, operationError(fmt.Sprintf("assignment context %d is invalid", index), "delegation needs complete logical continuity", "construct every context with NewAssignmentContext", nil)
		}
		key := context.assignment.String()
		if _, duplicate := seen[key]; duplicate {
			return nil, operationError(fmt.Sprintf("assignment %q appears twice", key), "parallel collections cannot contain duplicate logical work", "deduplicate the assignment collection", nil)
		}
		seen[key] = struct{}{}
	}
	return owned, nil
}

type ContinueAssignment struct {
	base    operationBase
	context AssignmentContext
}

func (o ContinueAssignment) Context() AssignmentContext { return o.context }
func (o ContinueAssignment) Scope() ScopeID             { return o.base.scope }
func (o ContinueAssignment) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), o.base.results...)
}

func NewContinueAssignment(context AssignmentContext, scope ScopeID, results ...ResultSlotDeclaration) (ContinueAssignment, error) {
	base, err := newOperationBase(scope, results)
	if err != nil {
		return ContinueAssignment{}, err
	}
	if !context.assignment.IsValid() || !context.task.IsValid() || !context.role.IsValid() {
		return ContinueAssignment{}, operationError("continuation context is zero or invalid", "fresh-agent continuation requires complete logical identity and retained values", "construct the context with NewAssignmentContext", nil)
	}
	return ContinueAssignment{base: base, context: context}, nil
}

type SendAssignmentMessage struct {
	base       operationBase
	assignment protocol.AssignmentRef
	message    string
}

func (o SendAssignmentMessage) Assignment() protocol.AssignmentRef { return o.assignment }
func (o SendAssignmentMessage) Message() string                    { return o.message }
func (o SendAssignmentMessage) Scope() ScopeID                     { return o.base.scope }

func NewSendAssignmentMessage(assignment protocol.AssignmentRef, message string, scope ScopeID) (SendAssignmentMessage, error) {
	base, err := newOperationBase(scope, nil)
	if err != nil {
		return SendAssignmentMessage{}, err
	}
	if !assignment.IsValid() || !utf8.ValidString(message) || strings.TrimSpace(message) == "" {
		return SendAssignmentMessage{}, operationError("message assignment is invalid or message is empty/invalid UTF-8", "assignment messaging preserves exact portable content and destination", "supply a valid AssignmentRef and non-empty UTF-8 message", nil)
	}
	return SendAssignmentMessage{base: base, assignment: assignment, message: message}, nil
}

type CollectAssignmentResults struct {
	base        operationBase
	assignments []protocol.AssignmentRef
	scheduling  Scheduling
}

func (o CollectAssignmentResults) Assignments() []protocol.AssignmentRef {
	return append([]protocol.AssignmentRef(nil), o.assignments...)
}
func (o CollectAssignmentResults) Scheduling() Scheduling { return o.scheduling }
func (o CollectAssignmentResults) Scope() ScopeID         { return o.base.scope }
func (o CollectAssignmentResults) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), o.base.results...)
}

func NewCollectAssignmentResults(assignments []protocol.AssignmentRef, scheduling Scheduling, scope ScopeID, results ...ResultSlotDeclaration) (CollectAssignmentResults, error) {
	base, err := newOperationBase(scope, results)
	if err != nil {
		return CollectAssignmentResults{}, err
	}
	owned, err := validateAssignmentRefs(assignments)
	if err != nil {
		return CollectAssignmentResults{}, err
	}
	if !scheduling.IsValid() {
		return CollectAssignmentResults{}, operationError("result-collection scheduling is omitted or invalid", "wait semantics require explicit collection metadata", "use Independent, DependencyOrdered, or BoundedParallel", nil)
	}
	return CollectAssignmentResults{base: base, assignments: owned, scheduling: scheduling}, nil
}

type StopAssignment struct {
	base        operationBase
	assignments []protocol.AssignmentRef
	reason      string
}

func (o StopAssignment) Assignments() []protocol.AssignmentRef {
	return append([]protocol.AssignmentRef(nil), o.assignments...)
}
func (o StopAssignment) Reason() string { return o.reason }
func (o StopAssignment) Scope() ScopeID { return o.base.scope }

func NewStopAssignment(assignments []protocol.AssignmentRef, reason string, scope ScopeID) (StopAssignment, error) {
	base, err := newOperationBase(scope, nil)
	if err != nil {
		return StopAssignment{}, err
	}
	owned, err := validateAssignmentRefs(assignments)
	if err != nil {
		return StopAssignment{}, err
	}
	if !utf8.ValidString(reason) || strings.TrimSpace(reason) == "" {
		return StopAssignment{}, operationError("stop reason is empty or invalid UTF-8", "stopping work requires actionable retained intent", "supply a non-empty UTF-8 reason", nil)
	}
	return StopAssignment{base: base, assignments: owned, reason: reason}, nil
}

func validateAssignmentRefs(assignments []protocol.AssignmentRef) ([]protocol.AssignmentRef, error) {
	if len(assignments) == 0 {
		return nil, operationError("assignment reference collection is empty", "collection operations need at least one logical destination", "supply one or more AssignmentRef values", nil)
	}
	owned := append([]protocol.AssignmentRef(nil), assignments...)
	seen := make(map[string]struct{}, len(owned))
	for index, assignment := range owned {
		if !assignment.IsValid() {
			return nil, operationError(fmt.Sprintf("assignment reference %d is invalid", index), "raw or zero identities cannot enter orchestration", "construct it with protocol.NewAssignmentRef", nil)
		}
		if _, duplicate := seen[assignment.String()]; duplicate {
			return nil, operationError(fmt.Sprintf("assignment %q is duplicated", assignment), "collection semantics operate once per logical assignment", "deduplicate the collection", nil)
		}
		seen[assignment.String()] = struct{}{}
	}
	return owned, nil
}

func (InvokeSkill) semanticOperation()              {}
func (DelegateAssignment) semanticOperation()       {}
func (ContinueAssignment) semanticOperation()       {}
func (SendAssignmentMessage) semanticOperation()    {}
func (CollectAssignmentResults) semanticOperation() {}
func (StopAssignment) semanticOperation()           {}
func (RequestUserDecision) semanticOperation()      {}

func (InvokeSkill) operationKind() OperationKind           { return OperationInvokeSkill }
func (DelegateAssignment) operationKind() OperationKind    { return OperationDelegateAssignment }
func (ContinueAssignment) operationKind() OperationKind    { return OperationContinueAssignment }
func (SendAssignmentMessage) operationKind() OperationKind { return OperationSendAssignmentMessage }
func (CollectAssignmentResults) operationKind() OperationKind {
	return OperationCollectAssignmentResults
}
func (StopAssignment) operationKind() OperationKind      { return OperationStopAssignment }
func (RequestUserDecision) operationKind() OperationKind { return OperationRequestUserDecision }

func (o InvokeSkill) operationID() SemanticOperationID { return coreOperationIDs[o.operationKind()] }
func (o DelegateAssignment) operationID() SemanticOperationID {
	return coreOperationIDs[o.operationKind()]
}
func (o ContinueAssignment) operationID() SemanticOperationID {
	return coreOperationIDs[o.operationKind()]
}
func (o SendAssignmentMessage) operationID() SemanticOperationID {
	return coreOperationIDs[o.operationKind()]
}
func (o CollectAssignmentResults) operationID() SemanticOperationID {
	return coreOperationIDs[o.operationKind()]
}
func (o StopAssignment) operationID() SemanticOperationID { return coreOperationIDs[o.operationKind()] }
func (o RequestUserDecision) operationID() SemanticOperationID {
	return coreOperationIDs[o.operationKind()]
}

func (o InvokeSkill) validateOperation() error {
	_, err := NewInvokeSkill(o.skill, o.operands, o.base.scope, o.base.results...)
	return err
}
func (o DelegateAssignment) validateOperation() error {
	_, err := NewDelegateAssignment(o.contexts, o.scheduling, o.base.scope, o.base.results...)
	return err
}
func (o ContinueAssignment) validateOperation() error {
	_, err := NewContinueAssignment(o.context, o.base.scope, o.base.results...)
	return err
}
func (o SendAssignmentMessage) validateOperation() error {
	_, err := NewSendAssignmentMessage(o.assignment, o.message, o.base.scope)
	return err
}
func (o CollectAssignmentResults) validateOperation() error {
	_, err := NewCollectAssignmentResults(o.assignments, o.scheduling, o.base.scope, o.base.results...)
	return err
}
func (o StopAssignment) validateOperation() error {
	_, err := NewStopAssignment(o.assignments, o.reason, o.base.scope)
	return err
}
func (o RequestUserDecision) validateOperation() error {
	_, err := validatedRequest(o)
	return err
}

func canonicalSemanticOperation(operation SemanticOperation) ([]byte, error) {
	if operation == nil {
		return nil, operationError("semantic operation is nil", "the orchestration sum is closed and nil has no meaning", "construct one supported operation variant", nil)
	}
	if err := operation.validateOperation(); err != nil {
		return nil, err
	}
	payload, err := operation.canonicalOperation()
	if err != nil {
		return nil, operationError("semantic operation could not be canonicalized", "target lowering consumes stable protocol meaning", "correct the operation payload", err)
	}
	return payload, nil
}

func normalizeSemanticOperation(operation SemanticOperation) (SemanticOperation, error) {
	if operation == nil {
		return nil, operationError("semantic operation is nil", "the orchestration sum is closed and nil has no meaning", "construct one supported operation variant", nil)
	}
	switch value := operation.(type) {
	case RequestUserDecision:
		validated, err := validatedRequest(value)
		if err != nil {
			return nil, err
		}
		return validated, nil
	case *RequestUserDecision:
		if value == nil {
			return nil, operationError("user-decision operation is nil", "nil has no closed operation meaning", "supply a RequestUserDecision value", nil)
		}
		validated, err := validatedRequest(*value)
		if err != nil {
			return nil, err
		}
		return validated, nil
	default:
		if err := operation.validateOperation(); err != nil {
			return nil, err
		}
		return operation, nil
	}
}

// SemanticOperationKind returns the closed protocol variant identity.
func SemanticOperationKind(operation SemanticOperation) (OperationKind, error) {
	normalized, err := normalizeSemanticOperation(operation)
	if err != nil {
		return "", err
	}
	return normalized.operationKind(), nil
}

// SemanticOperationIdentity returns the protocol identity, never a native
// harness function name or durable-store operation ID.
func SemanticOperationIdentity(operation SemanticOperation) (SemanticOperationID, error) {
	normalized, err := normalizeSemanticOperation(operation)
	if err != nil {
		return SemanticOperationID{}, err
	}
	return normalized.operationID(), nil
}

// CanonicalSemanticOperation returns a defensive copy of the neutral operation
// codec for validators, goldens, and semantic-instruction lowerings.
func CanonicalSemanticOperation(operation SemanticOperation) ([]byte, error) {
	encoded, err := canonicalSemanticOperation(operation)
	return append([]byte(nil), encoded...), err
}

type operationWire struct {
	Kind    OperationKind    `json:"kind"`
	ID      string           `json:"id"`
	Scope   string           `json:"scope,omitempty"`
	Payload any              `json:"payload"`
	Results []resultSlotWire `json:"results"`
}

type resultSlotWire struct {
	Key    string `json:"key"`
	Scope  string `json:"scope"`
	Schema string `json:"schema"`
	Type   string `json:"type"`
}

func resultWires(results []ResultSlotDeclaration) []resultSlotWire {
	out := make([]resultSlotWire, len(results))
	for index, result := range results {
		out[index] = resultSlotWire{Key: result.key, Scope: result.scope.String(), Schema: string(result.schema), Type: result.typeName}
	}
	return out
}

func marshalOperation(kind OperationKind, scope ScopeID, payload any, results []ResultSlotDeclaration) ([]byte, error) {
	return json.Marshal(operationWire{
		Kind: kind, ID: coreOperationIDs[kind].String(), Scope: scope.String(),
		Payload: payload, Results: resultWires(results),
	})
}

func (o InvokeSkill) canonicalOperation() ([]byte, error) {
	type operandWire struct {
		Key   string          `json:"key"`
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	operands := make([]operandWire, len(o.operands))
	for index, operand := range o.operands {
		operands[index] = operandWire{Key: operand.key, Type: operand.typeName, Value: operand.value}
	}
	return marshalOperation(o.operationKind(), o.base.scope, struct {
		Skill    string        `json:"skill"`
		Operands []operandWire `json:"operands"`
	}{Skill: o.skill.String(), Operands: operands}, o.base.results)
}

func (o DelegateAssignment) canonicalOperation() ([]byte, error) {
	assignments := make([]string, len(o.contexts))
	for index, context := range o.contexts {
		assignments[index] = context.assignment.String()
	}
	return marshalOperation(o.operationKind(), o.base.scope, map[string]any{
		"assignments": assignments, "scheduling": o.scheduling.kind, "max_parallel": o.scheduling.maxParallel,
	}, o.base.results)
}

func (o ContinueAssignment) canonicalOperation() ([]byte, error) {
	return marshalOperation(o.operationKind(), o.base.scope, map[string]any{
		"assignment": o.context.assignment.String(), "task": o.context.task.String(), "role": o.context.role.String(),
	}, o.base.results)
}

func (o SendAssignmentMessage) canonicalOperation() ([]byte, error) {
	return marshalOperation(o.operationKind(), o.base.scope, map[string]any{"assignment": o.assignment.String(), "message": o.message}, nil)
}

func (o CollectAssignmentResults) canonicalOperation() ([]byte, error) {
	assignments := make([]string, len(o.assignments))
	for index, assignment := range o.assignments {
		assignments[index] = assignment.String()
	}
	return marshalOperation(o.operationKind(), o.base.scope, map[string]any{
		"assignments": assignments, "scheduling": o.scheduling.kind, "max_parallel": o.scheduling.maxParallel,
	}, o.base.results)
}

func (o StopAssignment) canonicalOperation() ([]byte, error) {
	assignments := make([]string, len(o.assignments))
	for index, assignment := range o.assignments {
		assignments[index] = assignment.String()
	}
	return marshalOperation(o.operationKind(), o.base.scope, map[string]any{"assignments": assignments, "reason": o.reason}, nil)
}

func (o RequestUserDecision) canonicalOperation() ([]byte, error) {
	request, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	return marshalOperation(o.operationKind(), ScopeID{}, json.RawMessage(request), nil)
}

func operationError(what, why, fix string, cause error) error {
	return diagnostic(
		what, why, "semantic operation construction", "operation validation",
		"generation stops before any target invocation", fix, cause,
	)
}
