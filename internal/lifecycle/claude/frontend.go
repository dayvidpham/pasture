package claude

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const parseWhere = "Parsing a native Claude lifecycle payload (internal/lifecycle/claude/frontend.go in claude.Frontend.Parse)."

// Frontend translates Claude Code's native hook payloads into the Level-2
// lifecycle IR for the pinned Claude host contract.
//
// It holds the pinned contract and an index over that contract's own event
// catalogue, both resolved once by [New]. Building the index there rather than
// per call matters twice over: the pinned profile rebuilds its whole mapping
// table on every construction, and the index is the only place a native event
// SPELLING becomes a typed event value, so having exactly one of it makes that
// step auditable.
//
// The index is derived from the contract, never written here. A frontend that
// kept its own name table could resolve an event the contract no longer
// declares, which is the drift the pinned catalogue exists to prevent.
//
// It holds no per-invocation state, and neither field is written after [New]
// returns, so a Frontend is safe for concurrent use.
type Frontend struct {
	contract runtime.LifecycleContract[runtime.ClaudeLifecycleEvent]
	byName   map[string]runtime.ClaudeLifecycleEvent
}

// New returns the Claude Code lifecycle frontend bound to the pinned contract.
func New() Frontend {
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	events := contract.Events()
	byName := make(map[string]runtime.ClaudeLifecycleEvent, len(events))
	for _, event := range events {
		byName[event.NativeName()] = event
	}
	return Frontend{contract: contract, byName: byName}
}

// Harness returns the exact harness this frontend serves.
func (Frontend) Harness() ir.HarnessID { return ir.HarnessClaudeCode }

// Parse decodes one native Claude payload into the Level-2 lifecycle IR.
//
// The sequence is deliberately strict-first: nothing is extracted from a
// payload until the payload as a whole has been proven to be exactly one
// well-formed JSON object with no duplicate members and no field outside the
// pinned allowed set for the event that fired. A payload Pasture does not
// fully understand is rejected rather than partially interpreted.
//
//  1. bound and validate the bytes
//  2. digest the EXACT bytes received, before anything is decoded
//  3. strict decode: one JSON object, no duplicate members, no trailing value
//  4. resolve the event named by the payload against the pinned catalogue
//  5. require the caller's requested event, when given, to agree
//  6. bind the typed event to the pinned contract
//  7. reject any field outside the pinned allowed set, then extract ONLY the
//     correlation fields the pinned contract declares
//  8. hand the result to the verifier, which checks it against the contract
//
// Step 7 carries the unknown-field refusal that would otherwise belong with
// the strict decode in step 3. It cannot run there: which fields are
// admissible depends on which event fired AND on the correlation fields the
// pinned mapping declares for it, so the check needs the binding from step 6.
// Running it earlier would mean the frontend judging admissibility against a
// table it wrote itself.
//
// Parse is pure. It reads no store, no clock, and no environment, performs no
// effects, and opens nothing. That is what makes an IR-level test possible
// with no host and no database, and on error nothing has been read from, or
// written to, any store.
func (f Frontend) Parse(payload []byte, requested lifecycle.NativeEventName) (lifecycle.Event, error) {
	if !f.contract.IsValid() {
		return lifecycle.Event{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The Claude lifecycle frontend was used without being bound to the pinned contract.",
			Why:      "A zero Frontend value carries no contract and no event catalogue, so it has no reviewed description of what any Claude event means.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      "Construct the frontend with claude.New() rather than using a zero claude.Frontend value.",
		}
	}

	if err := boundPayload(payload); err != nil {
		return lifecycle.Event{}, err
	}

	// The digest covers the exact bytes the host sent, taken before any
	// decoding. Two deliveries carrying identical bytes therefore produce an
	// identical digest whatever the parse does with them, and two deliveries
	// carrying different bytes cannot collapse into one recorded fact — which
	// is the property replay detection is built on. Digesting the extracted
	// correlation instead would make one session doing the same thing twice
	// indistinguishable from a repeated delivery.
	digest := lifecycle.NewDigest(payload)

	fields, err := decodePayload(payload)
	if err != nil {
		return lifecycle.Event{}, err
	}

	declaredName, err := payloadEventName(fields)
	if err != nil {
		return lifecycle.Event{}, err
	}
	event, resolved := f.eventByNativeName(declaredName)
	if !resolved {
		return lifecycle.Event{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The payload reports Claude event %q, which the pinned contract for this host version does not describe.", declaredName),
			Why:      "Each pinned contract covers a closed catalogue of native events for one exact host version; this event is not in it, so Pasture has no reviewed description of what it means.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			// The catalogue is deliberately not enumerated here. Thirty event
			// names is a wall of text an operator has to read past to reach
			// the actual remedy, and the remedy is never "pick a different
			// event" — the host chose the event, not the operator.
			Fix: "Confirm the running Claude Code version matches the version the contract in internal/runtime/lifecycle_profiles.go is pinned to. If this event is genuinely new, pin a contract for the host version that declares it.",
		}
	}
	if requested != "" && string(requested) != declaredName {
		return lifecycle.Event{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The hook was invoked for Claude event %q but the payload reports %q.", requested, declaredName),
			Why:      "The hook registration and the payload must describe the same occurrence; a disagreement means the registration is stale or the payload was routed to the wrong hook.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      fmt.Sprintf("Register the Pasture hook under %q, or invoke it without naming an event so the payload decides.", declaredName),
		}
	}

	binding, err := lifecycle.BindEvent(f.contract, event)
	if err != nil {
		return lifecycle.Event{}, err
	}
	// The declared correlation fields come from the binding, i.e. from the
	// pinned contract itself. A frontend cannot consult a table it wrote.
	declared := binding.DeclaredIdentities()
	if err := rejectUnknownFields(fields, allowedFields(event, declared), declaredName); err != nil {
		return lifecycle.Event{}, err
	}

	identities, err := extractIdentities(fields, declared)
	if err != nil {
		return lifecycle.Event{}, err
	}

	// The event's meaning comes from the pinned contract by construction: the
	// verifier takes only the digest and the extracted correlation, and reads
	// everything else out of the binding. This is the constraint that retires
	// the previous design, whose adapter downgraded ConfigChange's failure
	// mode when config_source looked like a policy setting and suppressed Stop
	// when stop_hook_active was set — payload content deciding semantics, once
	// per harness. There is now no argument through which a frontend could
	// express such a decision.
	return binding.NewEvent(digest, identities)
}

// eventByNativeName resolves one native event spelling to its typed value in
// the pinned catalogue.
//
// The pinned contract deliberately offers no string lookup, so that the rest
// of Pasture cannot address an event by an arbitrary name. Resolving a source
// token into a typed value is precisely a frontend's job, and this is the one
// place it happens for Claude.
func (f Frontend) eventByNativeName(name string) (runtime.ClaudeLifecycleEvent, bool) {
	event, found := f.byName[name]
	return event, found
}

// boundPayload proves the raw bytes are worth decoding at all: non-empty,
// within the waist's size bound, and valid UTF-8.
//
// It runs before the digest so a hostile or malfunctioning host cannot make
// Pasture hash — or allocate for — an unbounded payload.
func boundPayload(payload []byte) error {
	switch {
	case len(payload) == 0:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The Claude hook sent an empty payload.",
			Why:      "A command hook passes its event payload on standard input; an empty stream means the hook fired without one, or the pipe was closed before anything was written.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      "Invoke the hook so Claude's payload reaches standard input, for example by registering it as a command hook rather than running it by hand.",
		}
	case len(payload) > lifecycle.MaxNativePayloadBytes:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The Claude hook payload is %d bytes, over the %d-byte limit.", len(payload), lifecycle.MaxNativePayloadBytes),
			Why:      "Payload size is bounded before decoding so a malformed or hostile host cannot make Pasture allocate without limit.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      "Send only the hook event payload; transcripts and tool output do not belong on this channel.",
		}
	case !utf8.Valid(payload):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The Claude hook payload is not valid UTF-8.",
			Why:      "Decoding malformed bytes would substitute replacement characters, so two different occurrences could end up with the same correlation values.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      "Send the payload as valid UTF-8.",
		}
	}
	return nil
}

// decodePayload proves the bytes are exactly one strict JSON object and
// returns its members. Duplicate members, trailing values, and non-object
// documents are all rejected here, before anything is read out of the payload.
//
// The decode target is a member map rather than a Go struct, and that choice
// is load-bearing in two directions.
//
// First, json.Decoder.DisallowUnknownFields cannot express what this frontend
// needs. It compares against ONE struct's fields, but which Claude fields are
// admissible depends on WHICH event fired: tool_name is an ordinary PreToolUse
// field and an unrecognised SessionStart field. A single struct wide enough to
// decode every pinned event would accept any event's fields on every event,
// which is weaker than what rejectUnknownFields does per event.
//
// Second, DisallowUnknownFields does not reject duplicate members at all —
// encoding/json silently applies "last member wins" for a repeated key. The
// shared strict decoder below walks the document and rejects duplicates
// outright, which is the guarantee that actually matters here: two readers of
// the same wire bytes must not be able to disagree about the effective value.
// It also applies DisallowUnknownFields to the target, but against a map that
// is a no-op, so it contributes nothing and nothing depends on it.
func decodePayload(payload []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := ir.StrictJSONWithPresence(payload, []string{hookEventNameField}, &fields); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The Claude hook payload is not one strict JSON object naming the event that fired.",
			Why:      fmt.Sprintf("The payload is malformed, repeats a field, omits %q, or has a second JSON value after the first.", hookEventNameField),
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      fmt.Sprintf("Send exactly one JSON object, with each field once and %q naming the event.", hookEventNameField),
			Cause:    err,
		}
	}
	// A non-object document (an array, a bare string, a null) cannot satisfy
	// the required-field check above, so it is already rejected by the time
	// execution reaches here and needs no separate branch.
	return fields, nil
}

// payloadEventName reads the payload's own claim about which event fired.
func payloadEventName(fields map[string]json.RawMessage) (string, error) {
	name, err := stringField(fields, hookEventNameField)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The Claude hook payload has an empty %q.", hookEventNameField),
			Why:      "The event name selects the reviewed description of what the occurrence means; without it there is nothing to look up.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      "Send the event name Claude reports for this hook, for example \"SessionStart\".",
		}
	}
	return name, nil
}

// rejectUnknownFields refuses any payload carrying a field outside the pinned
// allowed set for the event that fired.
//
// This is what stops the waist widening by accident. A host that starts
// sending new payload detail fails loudly here, and someone decides — with a
// review — whether the new field is meaningless (add it to the allowed set) or
// meaningful (pin a new contract). It never acquires meaning by default.
func rejectUnknownFields(fields map[string]json.RawMessage, allowed []string, eventName string) error {
	permitted := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		permitted[field] = struct{}{}
	}
	unknown := make([]string, 0)
	for field := range fields {
		if _, ok := permitted[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	// Sorted, and reported together rather than one per attempt: a host
	// version drift usually adds several fields at once, and an operator
	// should see its whole shape in one run.
	slices.Sort(unknown)
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("The Claude %s payload carries unrecognised field(s): %s.", eventName, strings.Join(unknown, ", ")),
		Why:      "Pasture accepts only the payload shape its pinned contract describes for this host version; silently ignoring an unrecognised field would let host behaviour change without anyone noticing.",
		Where:    parseWhere,
		Impact:   "The native event was not translated and nothing was recorded.",
		Fix: fmt.Sprintf(
			"Confirm the running Claude version matches the pinned contract. The recognised fields for %s are: %s.",
			eventName, strings.Join(allowed, ", "),
		),
	}
}

// extractIdentities reads ONLY the correlation fields the pinned contract
// declares for this event. Every other field in the payload — including
// transcript_path, prompts, and tool output — is left untouched.
//
// The kind attached to each value is the pinned contract's own, never one this
// package chose. That is not a formality: the waist correlates by KIND, so a
// value extracted under the wrong kind would correlate the occurrence with
// unrelated facts inside IR the verifier has already blessed.
//
// An absent field is simply not extracted, whether the contract marks it
// required or not. Deciding that a required field is missing is the verifier's
// judgement, not this package's: it owns the declared field set, its refusal
// already names the harness, the event and the field, and a second check here
// would be a duplicate whose removal changed nothing except which of two
// near-identical messages an operator saw.
func extractIdentities(
	fields map[string]json.RawMessage,
	declared []runtime.NativeIdentityField,
) ([]lifecycle.Identity, error) {
	identities := make([]lifecycle.Identity, 0, len(declared))
	for _, field := range declared {
		raw, present := fields[field.NativeName()]
		if !present {
			continue
		}
		value, err := decodeString(raw, field.NativeName())
		if err != nil {
			return nil, err
		}
		identity, err := lifecycle.NewIdentity(field.Kind(), field.NativeName(), value)
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

// stringField reads one required string member from a decoded payload.
func stringField(fields map[string]json.RawMessage, name string) (string, error) {
	raw, present := fields[name]
	if !present {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The Claude hook payload has no %q field.", name),
			Why:      "This field is required on every Claude command-hook payload for the pinned host version.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      fmt.Sprintf("Include %q in the payload.", name),
		}
	}
	return decodeString(raw, name)
}

// decodeString requires one payload member to be a JSON string. Numbers,
// objects, and nulls are rejected rather than coerced: a correlation value
// that changed shape between host versions must be noticed, not stringified.
func decodeString(raw json.RawMessage, name string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The Claude hook payload field %q is not a JSON string.", name),
			Why:      "Correlation values are compared byte-for-byte; converting another JSON type into text would invent a spelling the host never sent.",
			Where:    parseWhere,
			Impact:   "The native event was not translated and nothing was recorded.",
			Fix:      fmt.Sprintf("Send %q as a JSON string.", name),
			Cause:    err,
		}
	}
	return value, nil
}

// Compile-time proof that this frontend satisfies the waist's contract. The
// assertion lives here, on the implementation side, because the IR package
// must never import a frontend.
var _ lifecycle.Frontend = Frontend{}
