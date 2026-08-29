package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// ErrLifecycleAdapterUnsupported identifies a native lifecycle adapter that
// cannot be generated from a documented, version-bounded host contract.
var ErrLifecycleAdapterUnsupported = errors.New("native lifecycle adapter unsupported")

// EventSemantic is the only Pasture meaning a native lifecycle occurrence may
// have. A native event never carries Pasture review or publication authority.
type EventSemantic uint8

const (
	SemanticObservation EventSemantic = iota + 1
	SemanticGateConsultation
	SemanticExplicitHumanResponse
)

func (s EventSemantic) IsValid() bool {
	return s >= SemanticObservation && s <= SemanticExplicitHumanResponse
}

func (s EventSemantic) String() string {
	switch s {
	case SemanticObservation:
		return "observation"
	case SemanticGateConsultation:
		return "gate-consultation"
	case SemanticExplicitHumanResponse:
		return "explicit-human-response"
	default:
		return ""
	}
}

// HookSurface identifies the exact native transport the generated adapter must
// implement. It is intentionally closed; codegen cannot register a new surface
// or select one by string at runtime.
type HookSurface uint8

const (
	SurfaceClaudeCommandJSON HookSurface = iota + 1
	SurfaceCodexStrictCommandJSON
	SurfaceOpenCodeNamedOutput
	SurfaceOpenCodeCatchAllSSE
)

func (s HookSurface) IsValid() bool {
	return s >= SurfaceClaudeCommandJSON && s <= SurfaceOpenCodeCatchAllSSE
}

func (s HookSurface) String() string {
	switch s {
	case SurfaceClaudeCommandJSON:
		return "claude-command-json"
	case SurfaceCodexStrictCommandJSON:
		return "codex-strict-command-json"
	case SurfaceOpenCodeNamedOutput:
		return "opencode-named-output"
	case SurfaceOpenCodeCatchAllSSE:
		return "opencode-catch-all-sse"
	default:
		return ""
	}
}

// BlockingMode records whether the native host waits for and can act on this
// event's result. ConditionallyBlocking is used where the host excludes a
// documented matcher variant from blocking.
type BlockingMode uint8

const (
	NonBlocking BlockingMode = iota + 1
	Blocking
	ConditionallyBlocking
)

func (m BlockingMode) IsValid() bool { return m >= NonBlocking && m <= ConditionallyBlocking }

func (m BlockingMode) String() string {
	switch m {
	case NonBlocking:
		return "nonblocking"
	case Blocking:
		return "blocking"
	case ConditionallyBlocking:
		return "conditionally-blocking"
	default:
		return ""
	}
}

// MutationMode describes the only native payload mutation an adapter may
// return. Observation and explicit-human mappings always use MutationNone.
type MutationMode uint8

const (
	MutationNone MutationMode = iota + 1
	MutationInput
	MutationOutput
	MutationOutputObject
)

func (m MutationMode) IsValid() bool { return m >= MutationNone && m <= MutationOutputObject }

func (m MutationMode) String() string {
	switch m {
	case MutationNone:
		return "none"
	case MutationInput:
		return "input"
	case MutationOutput:
		return "output"
	case MutationOutputObject:
		return "output-object"
	default:
		return ""
	}
}

// HandlerOrder preserves native handler scheduling. It does not impose a
// Pasture ordering or merge rule on concurrent host handlers.
type HandlerOrder uint8

const (
	OrderConcurrentNative HandlerOrder = iota + 1
	OrderSequentialLoad
	OrderObservationStream
)

func (o HandlerOrder) IsValid() bool {
	return o >= OrderConcurrentNative && o <= OrderObservationStream
}

func (o HandlerOrder) String() string {
	switch o {
	case OrderConcurrentNative:
		return "concurrent-native"
	case OrderSequentialLoad:
		return "sequential-load-order"
	case OrderObservationStream:
		return "observation-stream"
	default:
		return ""
	}
}

// ReconciliationMode specifies who reconciles multiple native handler results.
// ReconcileNoAdapterMerge is load-bearing for Codex: generated adapters must
// preserve concurrency without inventing a Pasture merge lattice.
type ReconciliationMode uint8

const (
	ReconcileNone ReconciliationMode = iota + 1
	ReconcileHostNative
	ReconcileNoAdapterMerge
	ReconcileSequentialMutation
)

func (m ReconciliationMode) IsValid() bool {
	return m >= ReconcileNone && m <= ReconcileSequentialMutation
}

func (m ReconciliationMode) String() string {
	switch m {
	case ReconcileNone:
		return "none"
	case ReconcileHostNative:
		return "host-native"
	case ReconcileNoAdapterMerge:
		return "no-adapter-merge"
	case ReconcileSequentialMutation:
		return "sequential-mutation"
	default:
		return ""
	}
}

// FailureMode preserves native process or plugin failure behavior.
type FailureMode uint8

const (
	FailureReportAndContinue FailureMode = iota + 1
	FailureExitTwoBlocks
	FailureStrictHook
	FailureStrictExitTwoBlocks
	FailureThrowFailFast
	FailureObserveOnly
)

func (m FailureMode) IsValid() bool {
	return m >= FailureReportAndContinue && m <= FailureObserveOnly
}

// BlocksByExitCode reports whether the mode makes the host REFUSE the native
// operation because the pasture hook process exited with the blocking exit
// code. Only the two exit-code arms do that: Claude Code reads exit 2 as
// "block", and the Codex strict-output contract reads it the same way.
//
// The other four arms never turn a pasture process exit into a host refusal.
// report-and-continue and observe-only are non-blocking by definition.
// strict-hook-failure records a failed strict hook without blocking the
// operation. throw-fail-fast IS a blocking behavior, but the OpenCode plugin
// blocks by throwing inside the plugin chain, not by reading an exit code, so
// its blocking bytes are the plugin's own and are not decided here.
//
// This predicate is the gate of the failure-evidence rule: only a mode that
// blocks by exit code may claim a blocking exit, and only with evidence.
func (m FailureMode) BlocksByExitCode() bool {
	return m == FailureExitTwoBlocks || m == FailureStrictExitTwoBlocks
}

// FailureEvidence records WHERE the blocking behavior of a native failure mode
// was read from the host. A blocking exit code is a claim about somebody else's
// program, so it must cite its source.
//
// Source is a host documentation URL or a path committed in this repository. An
// empty Source means "no evidence", and a row with no evidence never carries a
// blocking exit code: it runs as report-and-continue until its harness supplies
// the citation.
type FailureEvidence struct {
	Source string
}

// IsPresent reports whether the evidence cites a source. Whitespace alone is
// not a citation, so it reads as no evidence and keeps the row non-blocking.
func (e FailureEvidence) IsPresent() bool { return strings.TrimSpace(e.Source) != "" }

// evidenceBoundFailure applies the failure-evidence rule to one row. A
// non-blocking row keeps its harness's non-blocking arm. A blocking row keeps
// its harness's blocking arm only while it cites evidence; without evidence it
// runs as report-and-continue, so an undocumented guess can never refuse a
// user's tool call or prompt.
func evidenceBoundFailure(
	blocking BlockingMode,
	evidence FailureEvidence,
	blockingArm FailureMode,
	nonBlockingArm FailureMode,
) FailureMode {
	if blocking == NonBlocking {
		return nonBlockingArm
	}
	if !evidence.IsPresent() {
		return FailureReportAndContinue
	}
	return blockingArm
}

func (m FailureMode) String() string {
	switch m {
	case FailureReportAndContinue:
		return "report-and-continue"
	case FailureExitTwoBlocks:
		return "exit-2-blocks"
	case FailureStrictHook:
		return "strict-hook-failure"
	case FailureStrictExitTwoBlocks:
		return "strict-output-exit-2-blocks"
	case FailureThrowFailFast:
		return "throw-fail-fast"
	case FailureObserveOnly:
		return "observe-only"
	default:
		return ""
	}
}

// StopLoopPolicy records whether a Stop-family event carries host loop state.
// ConsultWhenInactive prevents a generated adapter from re-blocking the stop
// hook invocation that was itself triggered by an earlier block.
type StopLoopPolicy uint8

const (
	StopLoopNotApplicable StopLoopPolicy = iota + 1
	StopLoopConsultWhenInactive
)

func (p StopLoopPolicy) IsValid() bool {
	return p == StopLoopNotApplicable || p == StopLoopConsultWhenInactive
}

func (p StopLoopPolicy) String() string {
	switch p {
	case StopLoopNotApplicable:
		return "not-applicable"
	case StopLoopConsultWhenInactive:
		return "consult-when-inactive"
	default:
		return ""
	}
}

// NativeIdentityKind identifies correlation data owned by the native harness.
// These values are not Pasture actors, assignments, decisions, journal IDs,
// revisions, review evidence, or publication evidence.
type NativeIdentityKind uint8

const (
	IdentitySession NativeIdentityKind = iota + 1
	IdentityTurn
	IdentityRequest
	IdentityToolCall
	IdentityAgent
	IdentityMessage
)

func (k NativeIdentityKind) IsValid() bool { return k >= IdentitySession && k <= IdentityMessage }

func (k NativeIdentityKind) String() string {
	switch k {
	case IdentitySession:
		return "session"
	case IdentityTurn:
		return "turn"
	case IdentityRequest:
		return "request"
	case IdentityToolCall:
		return "tool-call"
	case IdentityAgent:
		return "agent"
	case IdentityMessage:
		return "message"
	default:
		return ""
	}
}

// NativeIdentityField is one native correlation field the generated adapter
// must forward byte-for-byte. It is opaque and has no public constructor, so a
// caller cannot add authority-bearing fields to a lifecycle mapping.
type NativeIdentityField struct {
	kind       NativeIdentityKind
	nativeName string
	required   bool
}

func nativeIdentity(kind NativeIdentityKind, nativeName string, required bool) NativeIdentityField {
	return NativeIdentityField{kind: kind, nativeName: nativeName, required: required}
}

func (f NativeIdentityField) Kind() NativeIdentityKind { return f.kind }
func (f NativeIdentityField) NativeName() string       { return f.nativeName }
func (f NativeIdentityField) Required() bool           { return f.required }
func (f NativeIdentityField) IsValid() bool {
	return f.kind.IsValid() && f.nativeName != "" && strings.TrimSpace(f.nativeName) == f.nativeName
}

// LifecycleEventMapping is immutable generation metadata for one native event.
// It deliberately contains no native payload and no Pasture command input. In
// particular, it cannot transport review/publication authority or manufacture a
// decision from the occurrence of a tool, permission, stop, catch-all, or SSE
// event.
type LifecycleEventMapping struct {
	nativeName     string
	semantic       EventSemantic
	surface        HookSurface
	blocking       BlockingMode
	identities     []NativeIdentityField
	unresolved     []NativeIdentityKind
	mutation       MutationMode
	order          HandlerOrder
	reconciliation ReconciliationMode
	failure        FailureMode
	evidence       FailureEvidence
	stopLoop       StopLoopPolicy
}

func (m LifecycleEventMapping) NativeName() string                 { return m.nativeName }
func (m LifecycleEventMapping) Semantic() EventSemantic            { return m.semantic }
func (m LifecycleEventMapping) Surface() HookSurface               { return m.surface }
func (m LifecycleEventMapping) Blocking() BlockingMode             { return m.blocking }
func (m LifecycleEventMapping) Mutation() MutationMode             { return m.mutation }
func (m LifecycleEventMapping) Order() HandlerOrder                { return m.order }
func (m LifecycleEventMapping) Reconciliation() ReconciliationMode { return m.reconciliation }
func (m LifecycleEventMapping) Failure() FailureMode               { return m.failure }
func (m LifecycleEventMapping) Evidence() FailureEvidence          { return m.evidence }
func (m LifecycleEventMapping) StopLoop() StopLoopPolicy           { return m.stopLoop }
func (m LifecycleEventMapping) Identities() []NativeIdentityField {
	return append([]NativeIdentityField(nil), m.identities...)
}
func (m LifecycleEventMapping) UnresolvedIdentities() []NativeIdentityKind {
	return append([]NativeIdentityKind(nil), m.unresolved...)
}

func (m LifecycleEventMapping) validate(where string) error {
	if m.nativeName == "" || strings.TrimSpace(m.nativeName) != m.nativeName {
		return runtimeError(
			fmt.Sprintf("lifecycle event has an empty or padded native name %q", m.nativeName),
			"a generated hook requires one exact native event spelling",
			where, "the event cannot be emitted or matched deterministically",
			"use the exact native event name from the pinned host contract", nil,
		)
	}
	if !m.semantic.IsValid() || !m.surface.IsValid() || !m.blocking.IsValid() ||
		!m.mutation.IsValid() || !m.order.IsValid() || !m.reconciliation.IsValid() ||
		!m.failure.IsValid() || !m.stopLoop.IsValid() {
		return runtimeError(
			fmt.Sprintf("lifecycle event %q has an invalid semantic or native behavior enum", m.nativeName),
			"every generation decision must be represented by a closed typed value",
			where, "codegen could silently choose an unmodeled native behavior",
			"classify every lifecycle behavior with the declared runtime enum constants", nil,
		)
	}

	if m.evidence.IsPresent() && m.evidence.Source != strings.TrimSpace(m.evidence.Source) {
		return runtimeError(
			fmt.Sprintf("lifecycle event %q cites failure evidence %q with leading or trailing space", m.nativeName, m.evidence.Source),
			"an evidence citation is a host documentation URL or a repository path, and both are exact strings",
			where, "a reader checking the blocking claim could not resolve the citation",
			fmt.Sprintf("trim the FailureEvidence of the %q row to the exact URL or committed path", m.nativeName), nil,
		)
	}
	if m.failure.BlocksByExitCode() && !m.evidence.IsPresent() {
		return runtimeError(
			fmt.Sprintf("lifecycle event %q declares the blocking failure mode %q with no failure evidence", m.nativeName, m.failure),
			"a blocking exit code refuses the user's operation, so the claim that the host blocks must cite where it was read",
			where, "the generated adapter would refuse a prompt or a tool call on an undocumented guess",
			fmt.Sprintf("cite the host documentation URL or the committed capture path in the FailureEvidence of the %q row, or leave the row as report-and-continue until the citation exists", m.nativeName), nil,
		)
	}

	identityNames := make(map[string]struct{}, len(m.identities))
	identityKinds := make(map[NativeIdentityKind]struct{}, len(m.identities))
	hasRequiredRequestIdentity := false
	for _, identity := range m.identities {
		if !identity.IsValid() {
			return runtimeError(
				fmt.Sprintf("lifecycle event %q has an invalid native identity field", m.nativeName),
				"native correlation fields must have a typed kind and one exact wire name",
				where, "the adapter could not preserve request identity byte-for-byte",
				"use a constructor-owned native identity field", nil,
			)
		}
		if _, duplicate := identityNames[identity.nativeName]; duplicate {
			return runtimeError(
				fmt.Sprintf("lifecycle event %q repeats native identity field %q", m.nativeName, identity.nativeName),
				"one native identity must be forwarded exactly once",
				where, "the generated payload shape would be ambiguous",
				"remove the duplicate identity field", nil,
			)
		}
		identityNames[identity.nativeName] = struct{}{}
		identityKinds[identity.kind] = struct{}{}
		hasRequiredRequestIdentity = hasRequiredRequestIdentity ||
			(identity.kind == IdentityRequest && identity.Required())
	}

	unresolvedKinds := make(map[NativeIdentityKind]struct{}, len(m.unresolved))
	for _, kind := range m.unresolved {
		if !kind.IsValid() {
			return runtimeError(
				fmt.Sprintf("lifecycle event %q has invalid unresolved identity kind %d", m.nativeName, uint8(kind)),
				"an unresolved identity must use the same closed native identity vocabulary as resolved correlation",
				where, "the lifecycle mapping could publish an unknown correlation gap",
				"use a declared NativeIdentityKind", nil,
			)
		}
		if _, duplicate := unresolvedKinds[kind]; duplicate {
			return runtimeError(
				fmt.Sprintf("lifecycle event %q repeats unresolved identity kind %q", m.nativeName, kind),
				"one static correlation gap must be declared exactly once",
				where, "the lifecycle event would produce duplicate unresolved facts",
				"remove the duplicate unresolved identity kind", nil,
			)
		}
		if _, resolved := identityKinds[kind]; resolved {
			return runtimeError(
				fmt.Sprintf("lifecycle event %q declares identity kind %q as both resolved and unresolved", m.nativeName, kind),
				"a static lifecycle contract cannot both expose and lack stable correlation for the same identity kind",
				where, "consumers could not determine whether correlation exists",
				"keep the kind in either identities or unresolved identities, not both", nil,
			)
		}
		unresolvedKinds[kind] = struct{}{}
	}

	if m.semantic == SemanticObservation && m.mutation != MutationNone {
		return runtimeError(
			fmt.Sprintf("observation event %q declares native mutation %q", m.nativeName, m.mutation),
			"an observation records correlation only and cannot alter the native operation or a Pasture gate",
			where, "the generated adapter could change behavior from an observation path",
			"remove the mutation or classify a documented blocking event as gate consultation", nil,
		)
	}
	if m.semantic == SemanticGateConsultation && m.blocking == NonBlocking {
		return runtimeError(
			fmt.Sprintf("gate consultation event %q is nonblocking", m.nativeName),
			"a gate consultation can translate an existing Pasture gate only while the host is awaiting a result",
			where, "the adapter could report a gate result that the host cannot honor",
			"use an observation mapping for nonblocking events", nil,
		)
	}
	if m.semantic == SemanticExplicitHumanResponse {
		if !hasRequiredRequestIdentity {
			return runtimeError(
				fmt.Sprintf("explicit human response event %q has no required native request identity", m.nativeName),
				"a response may invoke a user gate only after byte-exact correlation to an existing Pasture-originated request",
				where, "an unrelated native occurrence could manufacture a user decision",
				"include a required native request identity or classify the event as observation", nil,
			)
		}
		if m.mutation != MutationNone {
			return runtimeError(
				fmt.Sprintf("explicit human response event %q declares native mutation %q", m.nativeName, m.mutation),
				"a reported response forwards verbatim input and does not rewrite the native request or result",
				where, "the generated adapter could alter the human response",
				"remove native mutation from explicit-human response mappings", nil,
			)
		}
	}
	if m.stopLoop == StopLoopConsultWhenInactive &&
		(m.semantic != SemanticGateConsultation || m.blocking == NonBlocking) {
		return runtimeError(
			fmt.Sprintf("lifecycle event %q applies a stop-loop guard outside a blocking gate consultation", m.nativeName),
			"stop-loop state exists to prevent a blocking Stop-family hook from re-blocking itself",
			where, "the generated adapter could loop or suppress an unrelated event",
			"apply the stop-loop policy only to blocking Stop and SubagentStop gate consultations", nil,
		)
	}

	switch m.surface {
	case SurfaceOpenCodeNamedOutput:
		if m.semantic != SemanticGateConsultation || m.blocking != Blocking ||
			m.mutation != MutationOutputObject || m.order != OrderSequentialLoad ||
			m.reconciliation != ReconcileSequentialMutation || m.failure != FailureThrowFailFast {
			return runtimeError(
				fmt.Sprintf("OpenCode named event %q does not preserve awaited sequential mutation and fail-fast behavior", m.nativeName),
				"OpenCode named handlers mutate an output object in deterministic plugin load order and stop the chain on throw",
				where, "generated handlers could reorder, lose, or continue after a failed gate result",
				"use blocking gate consultation, output-object mutation, sequential-load ordering, sequential reconciliation, and fail-fast failure", nil,
			)
		}
	case SurfaceOpenCodeCatchAllSSE:
		if m.semantic != SemanticObservation || m.blocking != NonBlocking ||
			m.mutation != MutationNone || m.order != OrderObservationStream ||
			m.reconciliation != ReconcileNone || m.failure != FailureObserveOnly {
			return runtimeError(
				fmt.Sprintf("OpenCode catch-all/SSE event %q is not observe-only", m.nativeName),
				"catch-all and SSE transports report native occurrences but do not participate in named blocking handler chains",
				where, "an observation stream could mutate or manufacture a gate decision",
				"use nonblocking observation-stream behavior with no mutation or reconciliation", nil,
			)
		}
	}
	return nil
}

// LifecycleContract is an immutable, exact-version native event table. E is a
// harness-specific uint enum, so callers cannot look up events by arbitrary
// string. There is no public constructor or registration API; the only values
// are the three reviewed static profiles in lifecycle_profiles.go.
type LifecycleContract[E comparable] struct {
	id          ir.RuntimeContractID
	versions    VersionConstraint
	events      []E
	mappings    map[E]LifecycleEventMapping
	constructed bool
}

func (c LifecycleContract[E]) ID() ir.RuntimeContractID { return c.id }
func (c LifecycleContract[E]) Harness() ir.HarnessID    { return c.id.Harness() }
func (c LifecycleContract[E]) Versions() VersionConstraint {
	return c.versions
}
func (c LifecycleContract[E]) Supports(version HostVersion) bool {
	return c.constructed && c.versions.Allows(version)
}
func (c LifecycleContract[E]) IsValid() bool { return c.constructed }
func (c LifecycleContract[E]) Events() []E {
	return append([]E(nil), c.events...)
}

// Mapping returns immutable generation metadata for one typed native event.
// Missing or zero event values fail actionably; there is deliberately no string
// lookup counterpart.
func (c LifecycleContract[E]) Mapping(event E) (LifecycleEventMapping, error) {
	if !c.constructed {
		return LifecycleEventMapping{}, runtimeError(
			"lifecycle lookup used a zero contract",
			"a lookup needs one of the static version-bounded lifecycle profiles",
			"LifecycleContract.Mapping", "no native event mapping can be generated",
			"use ClaudeCode2_1_210Lifecycle, Codex0_146_0Lifecycle, or OpenCode1_18_10Lifecycle", nil,
		)
	}
	mapping, found := c.mappings[event]
	if !found {
		return LifecycleEventMapping{}, runtimeError(
			fmt.Sprintf("typed lifecycle event value %v is not bound by contract %q", event, c.id),
			"a pinned lifecycle profile covers only its closed harness-specific event enum",
			"LifecycleContract.Mapping", "the event cannot be generated or silently treated as another native event",
			"use a value returned by the matching harness event enumeration", nil,
		)
	}
	mapping.identities = append([]NativeIdentityField(nil), mapping.identities...)
	mapping.unresolved = append([]NativeIdentityKind(nil), mapping.unresolved...)
	return mapping, nil
}

func mustLifecycleContract[E comparable](
	base RuntimeContract,
	events []E,
	mappings map[E]LifecycleEventMapping,
) LifecycleContract[E] {
	contract, err := newLifecycleContract(base, events, mappings)
	if err != nil {
		panic(err)
	}
	return contract
}

func newLifecycleContract[E comparable](
	base RuntimeContract,
	events []E,
	mappings map[E]LifecycleEventMapping,
) (LifecycleContract[E], error) {
	const where = "newLifecycleContract"
	if !base.IsValid() || !base.Versions().IsValid() {
		return LifecycleContract[E]{}, runtimeError(
			"lifecycle contract has a zero or invalid base runtime contract",
			"lifecycle and operation profiles share one reviewed harness identity and host-version bound",
			where, "the lifecycle profile could drift from the runtime selected by codegen",
			"build the lifecycle profile from a pinned RuntimeContract", nil,
		)
	}
	if len(events) == 0 || len(events) != len(mappings) {
		return LifecycleContract[E]{}, runtimeError(
			fmt.Sprintf("lifecycle contract %q has %d typed events and %d mappings", base.ID(), len(events), len(mappings)),
			"every event in the closed pinned catalog must map exactly once",
			where, "codegen could omit an event or use an undeclared mapping",
			"provide one mapping for every typed event and no extras", nil,
		)
	}

	ownedEvents := append([]E(nil), events...)
	ownedMappings := make(map[E]LifecycleEventMapping, len(mappings))
	seenEvents := make(map[E]struct{}, len(events))
	seenNativeNames := make(map[string]struct{}, len(events))
	for index, event := range events {
		if _, duplicate := seenEvents[event]; duplicate {
			return LifecycleContract[E]{}, runtimeError(
				fmt.Sprintf("lifecycle contract %q repeats typed event value %v at index %d", base.ID(), event, index),
				"the canonical event order must enumerate each event once",
				where, "generated output ordering and lookup coverage would be ambiguous",
				"remove the duplicate event value", nil,
			)
		}
		seenEvents[event] = struct{}{}
		mapping, found := mappings[event]
		if !found {
			return LifecycleContract[E]{}, runtimeError(
				fmt.Sprintf("lifecycle contract %q has no mapping for typed event value %v", base.ID(), event),
				"every event in the closed pinned catalog must map exactly once",
				where, "codegen could omit a native lifecycle event",
				"add the missing static event mapping", nil,
			)
		}
		if err := mapping.validate(where); err != nil {
			return LifecycleContract[E]{}, err
		}
		if _, duplicate := seenNativeNames[mapping.nativeName]; duplicate {
			return LifecycleContract[E]{}, runtimeError(
				fmt.Sprintf("lifecycle contract %q maps native event name %q twice", base.ID(), mapping.nativeName),
				"one native event spelling must identify one typed event",
				where, "generated dispatch would be ambiguous",
				"bind each exact native event name once", nil,
			)
		}
		seenNativeNames[mapping.nativeName] = struct{}{}
		mapping.identities = append([]NativeIdentityField(nil), mapping.identities...)
		mapping.unresolved = append([]NativeIdentityKind(nil), mapping.unresolved...)
		ownedMappings[event] = mapping
	}
	return LifecycleContract[E]{
		id:          base.ID(),
		versions:    base.Versions(),
		events:      ownedEvents,
		mappings:    ownedMappings,
		constructed: true,
	}, nil
}

// AntigravityLifecycleContract returns the actionable unsupported result for
// Antigravity generation. No public event catalog or wire contract is available,
// so returning an error is safer than reserving guessed event names or payloads.
func AntigravityLifecycleContract() error {
	return runtimeError(
		"Antigravity native lifecycle adapter is unsupported",
		"no recoverable public Antigravity hook catalog, payload schema, or wire contract is available to version-bound",
		"AntigravityLifecycleContract", "codegen cannot produce an authentic Antigravity adapter without inventing native semantics",
		"do not generate the adapter; first provide a public versioned hook contract and authentic loader fixtures, then add a reviewed static profile",
		ErrLifecycleAdapterUnsupported,
	)
}
