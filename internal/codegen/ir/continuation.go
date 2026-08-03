package ir

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// MutationAuthority is the exact portable initiating authority retained across
// retry or fresh-agent continuation.
type MutationAuthority interface{ mutationAuthority() }

type assignmentAuthority struct{ assignment portable.AssignmentRef }
type bootstrapAuthority struct{ agent portable.AgentRef }

func (assignmentAuthority) mutationAuthority() {}
func (bootstrapAuthority) mutationAuthority()  {}

func InitiatingAssignment(assignment portable.AssignmentRef) (MutationAuthority, error) {
	if !assignment.IsValid() {
		return nil, continuationError("initiating assignment is zero or invalid", "a mutation must retain the exact logical assignment that created it", "construct the assignment with portable.NewAssignmentRef", nil)
	}
	return assignmentAuthority{assignment: assignment}, nil
}

func BootstrapActor(agent portable.AgentRef) (MutationAuthority, error) {
	if !agent.IsValid() {
		return nil, continuationError("bootstrap actor is zero or invalid", "a bootstrap mutation must retain one registered portable agent identity", "construct the agent with portable.NewAgentRef", nil)
	}
	return bootstrapAuthority{agent: agent}, nil
}

// AssignmentAuthority returns the initiating assignment when that is the
// retained authority.
func AssignmentAuthority(authority MutationAuthority) (portable.AssignmentRef, bool) {
	switch value := authority.(type) {
	case assignmentAuthority:
		return value.assignment, true
	case *assignmentAuthority:
		if value != nil {
			return value.assignment, true
		}
	}
	return portable.AssignmentRef{}, false
}

// BootstrapAuthority returns the initiating bootstrap agent when applicable.
func BootstrapAuthority(authority MutationAuthority) (portable.AgentRef, bool) {
	switch value := authority.(type) {
	case bootstrapAuthority:
		return value.agent, true
	case *bootstrapAuthority:
		if value != nil {
			return value.agent, true
		}
	}
	return portable.AgentRef{}, false
}

// ResolvedMutationRequest retains protocol meaning, request schema, and exact
// immutable canonical request bytes.
type ResolvedMutationRequest struct {
	operation SemanticOperationID
	schema    SchemaID
	canonical []byte
}

func NewResolvedMutationRequest(
	operation SemanticOperationID,
	schema SchemaID,
	request []byte,
) (ResolvedMutationRequest, error) {
	if !operation.IsValid() {
		return ResolvedMutationRequest{}, continuationError("resolved mutation has an invalid semantic operation", "mutation identity must not be confused with a runtime function or durable operation ID", "construct the operation ID with NewSemanticOperationID", nil)
	}
	if err := validateNamedID("mutation request schema", string(schema)); err != nil {
		return ResolvedMutationRequest{}, err
	}
	canonical, err := canonicalJSON(request)
	if err != nil {
		return ResolvedMutationRequest{}, continuationError("resolved mutation request is not exactly one JSON value", "a fresh agent must reconstruct byte-identical typed request bytes", "supply one valid JSON request value", err)
	}
	return ResolvedMutationRequest{operation: operation, schema: schema, canonical: canonical}, nil
}

func (r ResolvedMutationRequest) Operation() SemanticOperationID { return r.operation }
func (r ResolvedMutationRequest) Schema() SchemaID               { return r.schema }
func (r ResolvedMutationRequest) CanonicalBytes() []byte         { return append([]byte(nil), r.canonical...) }
func (r ResolvedMutationRequest) IsValid() bool {
	return r.operation.IsValid() && r.schema != "" && len(r.canonical) > 0 && json.Valid(r.canonical)
}

// canonicalJSON decodes exactly one JSON value and re-marshals it in Go's
// canonical (sorted-key, minimal) form. It rejects duplicate JSON object
// members at every nesting level first: encoding/json's own decode into `any`
// silently keeps the last occurrence of a repeated key, which would let a
// fresh agent reconstructing this mutation's request from canonical bytes
// disagree with whichever reader first saw the raw wire bytes about which
// duplicate value was authoritative.
func canonicalJSON(data []byte) ([]byte, error) {
	if err := rejectDuplicateJSONMembers(data); err != nil {
		if IsDuplicateJSONMember(err) {
			return nil, fmt.Errorf("mutation request has a duplicate JSON member: %w", err)
		}
		return nil, fmt.Errorf("mutation request is empty, truncated, or malformed JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("additional JSON value follows the first")
		}
		return nil, err
	}
	return json.Marshal(value)
}

// CanonicalCommandDigest is a SHA-256 digest over canonical command bytes.
type CanonicalCommandDigest struct{ sum [sha256.Size]byte }

func DigestCanonicalCommand(command []byte) (CanonicalCommandDigest, error) {
	if len(command) == 0 {
		return CanonicalCommandDigest{}, continuationError("canonical command is empty", "a digest cannot stand in for a missing resolved command", "supply the complete canonical command bytes", nil)
	}
	return CanonicalCommandDigest{sum: sha256.Sum256(append([]byte(nil), command...))}, nil
}

func ParseCanonicalCommandDigest(value string) (CanonicalCommandDigest, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return CanonicalCommandDigest{}, continuationError("canonical command digest has no sha256 prefix", "digest algorithms must be explicit and version-stable", "use sha256:<64 lowercase hex characters>", nil)
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil || len(decoded) != sha256.Size {
		return CanonicalCommandDigest{}, continuationError("canonical command digest is malformed", "SHA-256 requires exactly 32 digest bytes", "use sha256:<64 lowercase hex characters>", err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], decoded)
	digest := CanonicalCommandDigest{sum: sum}
	if digest.String() != value {
		return CanonicalCommandDigest{}, continuationError("canonical command digest is not in canonical lowercase form", "one digest must have one stable textual identity", "use "+digest.String(), nil)
	}
	return digest, nil
}

func (d CanonicalCommandDigest) String() string { return "sha256:" + hex.EncodeToString(d.sum[:]) }
func (d CanonicalCommandDigest) IsValid() bool  { return d.sum != [sha256.Size]byte{} }
func (d CanonicalCommandDigest) Equal(other CanonicalCommandDigest) bool {
	return d.IsValid() && other.IsValid() && d.sum == other.sum
}

// MutationContinuation is the complete in-memory parent-owned continuation for
// one logical stateful invocation. It deliberately has no persistence codec.
type MutationContinuation struct {
	ref       portable.MutationRef
	authority MutationAuthority
	request   ResolvedMutationRequest
	command   CanonicalCommandDigest
	results   []ResultSlotDeclaration
	scope     LexicalScopeSnapshot
}

// MutationRefFactory mints a portable.MutationRef for one logical stateful
// invocation identity (seed). This package deliberately does not hardcode a
// minting strategy: a real orchestrator may choose any deterministic
// approach (e.g. a content digest of the resolved request and authority),
// while every consumer of a MutationRef relies on exactly one invariant —
// retrying the SAME logical invocation, or reconstructing it from a fresh
// agent with no memory of prior calls, must mint the exact same ref for the
// exact same seed; two distinct logical invocations must never collide.
// Injecting the factory (rather than calling portable.NewMutationRef
// directly) is what makes that invariant testable independent of whatever
// strategy production code eventually chooses.
type MutationRefFactory func(seed string) (portable.MutationRef, error)

// DeterministicMutationRefFactory is the default MutationRefFactory: the
// minted ref's value is exactly seed, so by portable.MutationRef's own
// value-equality semantics, equal seeds always mint an equal ref (continuity
// across retries and fresh-agent reconstruction) and distinct seeds mint
// distinct refs (distinct minting), with no additional strategy needed.
func DeterministicMutationRefFactory(seed string) (portable.MutationRef, error) {
	return portable.NewMutationRef(seed)
}

// NewMutationContinuationFromFactory mints ref via factory(seed) and
// otherwise constructs exactly as NewMutationContinuation. A caller retrying
// the same logical invocation, or a fresh agent reconstructing it from
// persisted state, must supply the same seed to receive the same
// MutationRef; a different logical invocation must supply a different seed.
func NewMutationContinuationFromFactory(
	factory MutationRefFactory,
	seed string,
	authority MutationAuthority,
	request ResolvedMutationRequest,
	command CanonicalCommandDigest,
	results []ResultSlotDeclaration,
	scope LexicalScopeSnapshot,
) (MutationContinuation, error) {
	if factory == nil {
		return MutationContinuation{}, continuationError(
			"mutation invocation factory is nil",
			"minting a retry-stable reference requires an injected factory, not an ad hoc caller-chosen value",
			"supply DeterministicMutationRefFactory or a custom MutationRefFactory", nil,
		)
	}
	ref, err := factory(seed)
	if err != nil {
		return MutationContinuation{}, continuationError(
			"mutation invocation factory failed to mint a reference",
			"the injected factory must produce a valid portable.MutationRef for every seed it accepts",
			"correct the factory implementation or supply a valid seed", err,
		)
	}
	return NewMutationContinuation(ref, authority, request, command, results, scope)
}

func NewMutationContinuation(
	ref portable.MutationRef,
	authority MutationAuthority,
	request ResolvedMutationRequest,
	command CanonicalCommandDigest,
	results []ResultSlotDeclaration,
	scope LexicalScopeSnapshot,
) (MutationContinuation, error) {
	if !ref.IsValid() {
		return MutationContinuation{}, continuationError("mutation reference is zero or invalid", "one logical invocation needs one constructor-validated retry identity", "construct the reference with portable.NewMutationRef", nil)
	}
	if !validMutationAuthority(authority) {
		return MutationContinuation{}, continuationError("mutation authority is omitted or unknown", "authority is closed to an initiating assignment or registered bootstrap agent", "construct authority with InitiatingAssignment or BootstrapActor", nil)
	}
	if !request.IsValid() || !command.IsValid() || !scope.IsValid() {
		return MutationContinuation{}, continuationError("mutation request, command digest, or lexical snapshot is zero or invalid", "fresh-agent reconstruction requires all three complete values", "construct the resolved request, digest, and snapshot before the continuation", nil)
	}
	ownedResults := append([]ResultSlotDeclaration(nil), results...)
	seen := make(map[string]struct{}, len(ownedResults))
	for index, result := range ownedResults {
		if !result.IsValid() {
			return MutationContinuation{}, continuationError(fmt.Sprintf("result declaration %d is invalid", index), "continuations retain exact typed result contracts", "declare every result with DeclareResultSlot", nil)
		}
		if _, duplicate := seen[result.key]; duplicate {
			return MutationContinuation{}, continuationError(fmt.Sprintf("result declaration %q is duplicated", result.key), "one operation cannot bind the same result key twice", "give each result slot a unique BindingKey", nil)
		}
		seen[result.key] = struct{}{}
	}
	return MutationContinuation{
		ref: ref, authority: authority, request: request, command: command,
		results: ownedResults, scope: scope,
	}, nil
}

func validMutationAuthority(authority MutationAuthority) bool {
	if assignment, ok := AssignmentAuthority(authority); ok {
		return assignment.IsValid()
	}
	if agent, ok := BootstrapAuthority(authority); ok {
		return agent.IsValid()
	}
	return false
}

func (c MutationContinuation) Ref() portable.MutationRef        { return c.ref }
func (c MutationContinuation) Authority() MutationAuthority     { return c.authority }
func (c MutationContinuation) Request() ResolvedMutationRequest { return c.request }
func (c MutationContinuation) Command() CanonicalCommandDigest  { return c.command }
func (c MutationContinuation) Results() []ResultSlotDeclaration {
	return append([]ResultSlotDeclaration(nil), c.results...)
}
func (c MutationContinuation) Scope() LexicalScopeSnapshot { return c.scope }
func (c MutationContinuation) ReconstructRequest() []byte  { return c.request.CanonicalBytes() }
func (c MutationContinuation) IsValid() bool {
	return c.ref.IsValid() && validMutationAuthority(c.authority) && c.request.IsValid() && c.command.IsValid() && c.scope.IsValid()
}

// AssignmentContext is the complete logical continuity value restored for a
// resumed or fresh runtime agent.
type AssignmentContext struct {
	role        portable.RoleID
	assignment  portable.AssignmentRef
	task        portable.TaskRef
	worktree    WorktreeRef
	evidence    []EvidenceRef
	decisions   []DecisionRef
	outstanding []WorkItemRef
	mutations   []MutationContinuation
	bindings    RuntimeBindings
}

func NewAssignmentContext(
	role portable.RoleID,
	assignment portable.AssignmentRef,
	task portable.TaskRef,
	worktree WorktreeRef,
	evidence []EvidenceRef,
	decisions []DecisionRef,
	outstanding []WorkItemRef,
	mutations []MutationContinuation,
	bindings RuntimeBindings,
) (AssignmentContext, error) {
	if !role.IsValid() || !assignment.IsValid() || !task.IsValid() || !worktree.IsValid() {
		return AssignmentContext{}, continuationError("assignment context has an invalid role, assignment, task, or worktree", "logical continuity requires all four portable domains and they are not interchangeable", "construct each portable value with its matching constructor", nil)
	}
	if err := validateRefSlice("evidence", len(evidence), func(index int) bool { return evidence[index].IsValid() }); err != nil {
		return AssignmentContext{}, err
	}
	if err := validateRefSlice("decision", len(decisions), func(index int) bool { return decisions[index].IsValid() }); err != nil {
		return AssignmentContext{}, err
	}
	if err := validateRefSlice("outstanding work", len(outstanding), func(index int) bool { return outstanding[index].IsValid() }); err != nil {
		return AssignmentContext{}, err
	}
	ownedMutations := append([]MutationContinuation(nil), mutations...)
	seenMutation := make(map[string]struct{}, len(ownedMutations))
	for index, mutation := range ownedMutations {
		if !mutation.IsValid() {
			return AssignmentContext{}, continuationError(fmt.Sprintf("mutation continuation %d is invalid", index), "a child replacement needs every in-flight invocation complete", "construct each continuation with NewMutationContinuation", nil)
		}
		key := mutation.Ref().String()
		if _, duplicate := seenMutation[key]; duplicate {
			return AssignmentContext{}, continuationError(fmt.Sprintf("mutation reference %q is duplicated", key), "one logical invocation may appear only once in a context", "retain one continuation per MutationRef", nil)
		}
		seenMutation[key] = struct{}{}
	}
	return AssignmentContext{
		role: role, assignment: assignment, task: task, worktree: worktree,
		evidence: append([]EvidenceRef(nil), evidence...), decisions: append([]DecisionRef(nil), decisions...),
		outstanding: append([]WorkItemRef(nil), outstanding...), mutations: ownedMutations,
		bindings: cloneBindings(bindings),
	}, nil
}

func validateRefSlice(domain string, length int, valid func(int) bool) error {
	for index := 0; index < length; index++ {
		if !valid(index) {
			return continuationError(fmt.Sprintf("%s reference %d is zero or invalid", domain, index), "logical continuity cannot retain forged references", "construct every reference with its matching constructor", nil)
		}
	}
	return nil
}

func (c AssignmentContext) Role() portable.RoleID              { return c.role }
func (c AssignmentContext) Assignment() portable.AssignmentRef { return c.assignment }
func (c AssignmentContext) Task() portable.TaskRef             { return c.task }
func (c AssignmentContext) Worktree() WorktreeRef              { return c.worktree }
func (c AssignmentContext) Evidence() []EvidenceRef            { return append([]EvidenceRef(nil), c.evidence...) }
func (c AssignmentContext) Decisions() []DecisionRef {
	return append([]DecisionRef(nil), c.decisions...)
}
func (c AssignmentContext) Outstanding() []WorkItemRef {
	return append([]WorkItemRef(nil), c.outstanding...)
}
func (c AssignmentContext) Mutations() []MutationContinuation {
	return append([]MutationContinuation(nil), c.mutations...)
}
func (c AssignmentContext) Bindings() RuntimeBindings { return cloneBindings(c.bindings) }
func (c AssignmentContext) IsValid() bool {
	return c.role.IsValid() && c.assignment.IsValid() && c.task.IsValid() && c.worktree.IsValid()
}

func continuationError(what, why, fix string, cause error) error {
	return diagnostic(
		what, why, "logical mutation continuation", "continuation validation",
		"retry or fresh-agent continuation is rejected before a stateful attempt", fix, cause,
	)
}
